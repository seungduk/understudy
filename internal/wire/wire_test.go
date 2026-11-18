package wire

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/quick"
	"unicode/utf8"
)

func TestErrorKindsTable(t *testing.T) {
	want := map[string][3]any{
		"invalid_request":   {400, "invalid_request_error", true},
		"authentication":    {401, "authentication_error", true},
		"billing":           {402, "billing_error", true},
		"permission":        {403, "permission_error", true},
		"not_found":         {404, "not_found_error", true},
		"request_too_large": {413, "request_too_large", true},
		"rate_limit":        {429, "rate_limit_error", true},
		"api_error":         {500, "api_error", true},
		"timeout":           {504, "timeout_error", true},
		"overloaded":        {529, "overloaded_error", true},
	}
	if len(ErrorKinds) != len(want) {
		t.Fatalf("kinds %d, want %d", len(ErrorKinds), len(want))
	}
	for _, k := range ErrorKinds {
		w, ok := want[k.Name()]
		if !ok {
			t.Fatalf("unexpected kind %q", k.Name())
		}
		if k.Status() != w[0] || k.Type() != w[1] || k.DefaultMessage() == "" {
			t.Errorf("%s: %d %s", k.Name(), k.Status(), k.Type())
		}
		back, ok := ParseErrorKind(k.Name())
		if !ok || back != k {
			t.Errorf("round trip %s", k.Name())
		}
		var env struct {
			Type      string
			Error     struct{ Type, Message string }
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal(ErrorBody(k, "", "req_1"), &env); err != nil {
			t.Fatal(err)
		}
		if env.Type != "error" || env.Error.Type != k.Type() || env.Error.Message != k.DefaultMessage() || env.RequestID != "req_1" {
			t.Errorf("body %+v", env)
		}
		if !strings.Contains(string(ErrorEventData(k, "boom")), `"message":"boom"`) || strings.Contains(string(ErrorEventData(k, "x")), "request_id") {
			t.Errorf("event data %s", ErrorEventData(k, "boom"))
		}
	}
	if _, ok := ParseErrorKind("overload"); ok {
		t.Error("unknown name parsed")
	}
	if len(ErrorKindNames()) != len(ErrorKinds) {
		t.Error("names")
	}
	defer func() {
		if recover() == nil {
			t.Error("no panic on unknown kind")
		}
	}()
	ErrorKind(99).Name()
}

func TestSplitProperties(t *testing.T) {
	check := func(s string, m uint8) bool {
		max := int(m%40) + 1
		for _, f := range []func(string, int) []string{SplitText, SplitBytes} {
			parts := f(s, max)
			if strings.Join(parts, "") != s {
				return false
			}
			for _, p := range parts {
				if p == "" || !utf8.ValidString(p) && utf8.ValidString(s) {
					return false
				}
				if len(p) > max && utf8.RuneCountInString(p) != 1 {
					return false
				}
			}
		}
		return true
	}
	if err := quick.Check(check, &quick.Config{MaxCount: 3000}); err != nil {
		t.Fatal(err)
	}
	if got := SplitText("hello big world", 10); strings.Join(got, "|") != "hello big |world" {
		t.Errorf("word split %q", got)
	}
	if got := SplitText("", 5); len(got) != 0 {
		t.Errorf("empty %q", got)
	}
	if got := SplitBytes("한국어", 4); strings.Join(got, "|") != "한|국|어" {
		t.Errorf("rune split %q", got)
	}
	if got := SplitBytes("한", 1); strings.Join(got, "|") != "한" {
		t.Errorf("long rune %q", got)
	}
}

func names(evs []Event) string {
	var n []string
	for _, e := range evs {
		var d map[string]any
		if err := json.Unmarshal(e.Data, &d); err != nil {
			panic(err)
		}
		if d["type"] != e.Name {
			panic("type mismatch " + e.Name)
		}
		s := e.Name
		if e.Name == "content_block_delta" {
			s = d["delta"].(map[string]any)["type"].(string)
		}
		n = append(n, s)
	}
	return strings.Join(n, ",")
}

func TestStreamEventsSequence(t *testing.T) {
	m := Meta{ID: "msg_1", Model: "m"}
	evs := StreamEvents(m, []OutBlock{
		{Kind: OutThinking, Text: "think", Signature: "sig"},
		{Kind: OutText, Text: "hi"},
		{Kind: OutToolUse, ID: "toolu_1", Name: "Bash", Input: json.RawMessage(`{"command":"echo 12345678901234567890"}`)},
		{Kind: OutToolUse, ID: "toolu_2", Name: "X", Input: json.RawMessage(`{}`)},
	}, ToolUse, Usage{Input: 7, Output: 9})
	want := "message_start,content_block_start,ping,thinking_delta,signature_delta,content_block_stop," +
		"content_block_start,text_delta,content_block_stop," +
		"content_block_start,input_json_delta,input_json_delta,input_json_delta,input_json_delta,content_block_stop," +
		"content_block_start,input_json_delta,content_block_stop,message_delta,message_stop"
	if got := names(evs); got != want {
		t.Fatalf("sequence\n got %s\nwant %s", got, want)
	}
	var start struct {
		Message struct {
			ID, Type, Role, Model string
			Content               []any
			StopReason            *string `json:"stop_reason"`
			Usage                 map[string]any
		}
	}
	json.Unmarshal(evs[0].Data, &start)
	if start.Message.ID != "msg_1" || start.Message.Role != "assistant" || start.Message.Type != "message" || start.Message.Content == nil || start.Message.StopReason != nil {
		t.Errorf("message_start %s", evs[0].Data)
	}
	for _, k := range []string{"input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "output_tokens", "service_tier"} {
		if _, ok := start.Message.Usage[k]; !ok {
			t.Errorf("usage lacks %s", k)
		}
	}
	// The tool input fragments join to the input.
	var frag strings.Builder
	for _, e := range evs[9:15] {
		var d struct {
			Delta struct {
				PartialJSON string `json:"partial_json"`
			}
		}
		json.Unmarshal(e.Data, &d)
		frag.WriteString(d.Delta.PartialJSON)
	}
	if frag.String() != `{"command":"echo 12345678901234567890"}` {
		t.Errorf("fragments %q", frag.String())
	}
	if !strings.Contains(string(evs[len(evs)-2].Data), `"stop_reason":"tool_use"`) || !strings.Contains(string(evs[len(evs)-2].Data), `"output_tokens":9`) {
		t.Errorf("message_delta %s", evs[len(evs)-2].Data)
	}
	if string(evs[0].Encode()[:21]) != "event: message_start\n" || !strings.HasSuffix(string(evs[0].Encode()), "\n\n") {
		t.Errorf("encode %q", evs[0].Encode())
	}
	if got := names(StreamEvents(m, nil, EndTurn, Usage{})); got != "message_start,ping,message_delta,message_stop" {
		t.Errorf("empty %s", got)
	}
	for _, s := range []StopReason{EndTurn, ToolUse, MaxTokens} {
		StreamEvents(m, nil, s, Usage{})
	}
}

func TestErrorEvents(t *testing.T) {
	m := Meta{ID: "msg_1", Model: "m"}
	if got := names(ErrorEvents(m, Usage{}, "", Overloaded, "")); got != "message_start,ping,error" {
		t.Errorf("plain %s", got)
	}
	if got := names(ErrorEvents(m, Usage{}, "part", APIError, "x")); got != "message_start,content_block_start,ping,text_delta,error" {
		t.Errorf("partial %s", got)
	}
}

func TestMessageJSON(t *testing.T) {
	b := MessageJSON(Meta{ID: "msg_1", Model: "m"}, []OutBlock{{Kind: OutThinking, Text: "t", Signature: "s"}, {Kind: OutText, Text: "hi"},
		{Kind: OutToolUse, ID: "toolu_1", Name: "Read", Input: json.RawMessage(`{"file_path":"/a"}`)}}, ToolUse, Usage{Input: 1, Output: 2})
	want := `{"content":[{"signature":"s","thinking":"t","type":"thinking"},{"text":"hi","type":"text"},{"id":"toolu_1","input":{"file_path":"/a"},"name":"Read","type":"tool_use"}],"id":"msg_1","model":"m","role":"assistant","stop_reason":"tool_use","stop_sequence":null,"type":"message","usage":{"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"input_tokens":1,"output_tokens":2,"service_tier":"standard"}}`
	if string(b) != want {
		t.Errorf("got %s", b)
	}
}

func TestParseRequest(t *testing.T) {
	body := `{"model":"m","stream":true,"max_tokens":5,"system":[{"type":"text","text":"a"},{"type":"text","text":"b"}],
	"tools":[{"name":"Bash"},{"name":"Read"}],
	"messages":[{"role":"user","content":"hi"},{"role":"system","content":[{"type":"text","text":"env"}]},
	{"role":"assistant","content":[{"type":"thinking","thinking":"t","signature":"s"},{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}]},
	{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","is_error":true,"content":[{"type":"text","text":"Exit code 1"},{"type":"image"}]},{"type":"image"}]},
	{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_2","content":"plain"}]}]}`
	r, err := ParseRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if r.Model != "m" || !r.Stream || r.MaxTokens != 5 || r.System != "a\nb" || strings.Join(r.ToolNames, ",") != "Bash,Read" || len(r.Messages) != 5 {
		t.Fatalf("%+v", r)
	}
	if r.Messages[1].Role != SystemRole || r.Messages[2].Role != Assistant || r.Messages[2].Blocks[1].ToolName != "Bash" || r.Messages[2].Blocks[0].Kind != ThinkingBlock {
		t.Errorf("roles %+v", r.Messages)
	}
	tr := r.Messages[3].Blocks[0]
	if tr.Kind != ToolResultBlock || !tr.IsError || tr.Text != "Exit code 1" || tr.ToolUseID != "toolu_1" || r.Messages[3].Blocks[1].Kind != OtherBlock {
		t.Errorf("tool_result %+v", r.Messages[3])
	}
	if r.Messages[4].Blocks[0].Text != "plain" {
		t.Errorf("string result %+v", r.Messages[4])
	}
	for _, bad := range []string{`nope`, `{"messages":[{"role":"user","content":"x"}]}`, `{"model":"m"}`,
		`{"model":"m","messages":[{"role":"tool","content":"x"}]}`, `{"model":"m","messages":[{"role":"user","content":5}]}`,
		`{"model":"m","system":5,"messages":[{"role":"user","content":"x"}]}`,
		`{"model":"m","messages":[{"role":"user","content":[{"type":"tool_result","content":5}]}]}`} {
		if _, err := ParseRequest([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	if r, err := ParseRequest([]byte(`{"model":"m","messages":[{"role":"user","content":null}]}`)); err != nil || len(r.Messages[0].Blocks) != 0 {
		t.Errorf("null content %v", err)
	}
	if EstimateTokens(0) != 1 || EstimateTokens(8) != 2 || EstimateTokens(9) != 3 {
		t.Error("estimate")
	}
}
