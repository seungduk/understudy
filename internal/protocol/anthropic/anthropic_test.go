package anthropic

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/seungduk/understudy/core"
	"github.com/seungduk/understudy/internal/protocol/codectest"
	"github.com/seungduk/understudy/internal/protocol/sse"
	"github.com/seungduk/understudy/protocol"
)

func TestErrorKindsTable(t *testing.T) {
	want := map[string][2]any{
		"invalid_request":   {400, "invalid_request_error"},
		"authentication":    {401, "authentication_error"},
		"billing":           {402, "billing_error"},
		"permission":        {403, "permission_error"},
		"not_found":         {404, "not_found_error"},
		"request_too_large": {413, "request_too_large"},
		"rate_limit":        {429, "rate_limit_error"},
		"api_error":         {500, "api_error"},
		"timeout":           {504, "timeout_error"},
		"overloaded":        {529, "overloaded_error"},
	}
	c := New()
	for _, k := range core.ErrorKinds {
		w := want[k.Name()]
		if Status(k) != w[0] || Type(k) != w[1] {
			t.Errorf("%s: %d %s", k.Name(), Status(k), Type(k))
		}
		var env struct {
			Type      string
			Error     struct{ Type, Message string }
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal(ErrorBody(k, "", "req_1"), &env); err != nil {
			t.Fatal(err)
		}
		if env.Type != "error" || env.Error.Type != Type(k) || env.Error.Message == "" || env.RequestID != "req_1" {
			t.Errorf("body %+v", env)
		}
		if !strings.Contains(string(errorEventData(k, "boom")), `"message":"boom"`) || strings.Contains(string(errorEventData(k, "x")), "request_id") {
			t.Errorf("event data %s", errorEventData(k, "boom"))
		}
		// Every retry hint and the retry delay.
		for _, rt := range []core.Retry{core.RetryDefault, core.RetryNever, core.RetryAlways} {
			e := c.Encode(protocol.Meta{RequestID: "req_1"}, protocol.Out{Kind: protocol.OutError, Error: protocol.ErrorSpec{Kind: k, Retry: rt, RetryAfter: 0, HasRetryAfter: true}})
			wantRetry := map[core.Retry]string{core.RetryDefault: "", core.RetryNever: "false", core.RetryAlways: "true"}[rt]
			if e.Status != Status(k) || e.Header.Get("X-Should-Retry") != wantRetry || e.Header.Get("Retry-After") != "0" || e.Header.Get("Request-Id") != "req_1" {
				t.Errorf("%s %d: %d %v", k.Name(), rt, e.Status, e.Header)
			}
			o, ok := c.Observe(protocol.Generate, e.Status, e.Header, e.Body)
			if !ok || o.Kind != protocol.SawError || o.Error != k {
				t.Errorf("observe %s: %+v", k.Name(), o)
			}
		}
	}
	if len(want) != len(core.ErrorKinds) {
		t.Fatal("kinds")
	}
	if e := c.Encode(protocol.Meta{}, protocol.Out{Kind: protocol.OutError, Error: protocol.ErrorSpec{Kind: core.Overloaded}}); e.Header.Get("Retry-After") != "" {
		t.Error("retry-after without HasRetryAfter")
	}
	defer func() {
		if recover() == nil {
			t.Error("no panic on unknown kind")
		}
	}()
	Status(core.ErrorKind(99))
}

func names(frames [][]byte) string {
	var n []string
	for _, f := range frames {
		fs := sse.Parse(f)
		if len(fs) != 1 {
			panic("one frame")
		}
		var d map[string]any
		if err := json.Unmarshal([]byte(fs[0].Data), &d); err != nil {
			panic(err)
		}
		if d["type"] != fs[0].Event {
			panic("type mismatch " + fs[0].Event)
		}
		s := fs[0].Event
		if s == "content_block_delta" {
			s = d["delta"].(map[string]any)["type"].(string)
		}
		n = append(n, s)
	}
	return strings.Join(n, ",")
}

func enc(r core.Reply, stream bool) protocol.Encoded {
	return New().Encode(protocol.Meta{Seed: "s", Model: "m", Stream: stream, Usage: core.Usage{Input: 7, Output: 9}}, protocol.Out{Kind: protocol.OutReply, Reply: r})
}

func TestStreamEventsSequence(t *testing.T) {
	r := core.Reply{Thinking: "think", ThinkingSignature: "sig", Text: "hi", Stop: core.ToolUse, Calls: []core.Call{
		{ID: "1", Tool: "Bash", Input: json.RawMessage(`{"command":"echo 12345678901234567890"}`)},
		{ID: "2", Tool: "X", Input: json.RawMessage(`{}`)},
	}}
	e := enc(r, true)
	want := "message_start,content_block_start,ping,thinking_delta,signature_delta,content_block_stop," +
		"content_block_start,text_delta,content_block_stop," +
		"content_block_start,input_json_delta,input_json_delta,input_json_delta,input_json_delta,content_block_stop," +
		"content_block_start,input_json_delta,content_block_stop,message_delta,message_stop"
	if got := names(e.Frames); got != want {
		t.Fatalf("sequence\n got %s\nwant %s", got, want)
	}
	start := sse.Parse(e.Frames[0])[0].Data
	var ms struct {
		Message struct {
			ID, Type, Role, Model string
			Content               []any
			StopReason            *string `json:"stop_reason"`
			Usage                 map[string]any
		}
	}
	json.Unmarshal([]byte(start), &ms)
	if !strings.HasPrefix(ms.Message.ID, "msg_") || ms.Message.Role != "assistant" || ms.Message.Content == nil || ms.Message.StopReason != nil {
		t.Errorf("message_start %s", start)
	}
	for _, k := range []string{"input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "output_tokens", "service_tier"} {
		if _, ok := ms.Message.Usage[k]; !ok {
			t.Errorf("usage lacks %s", k)
		}
	}
	md := sse.Parse(e.Frames[len(e.Frames)-2])[0].Data
	if !strings.Contains(md, `"stop_reason":"tool_use"`) || !strings.Contains(md, `"output_tokens":9`) {
		t.Errorf("message_delta %s", md)
	}
	if !strings.HasPrefix(string(e.Frames[0]), "event: message_start\ndata: ") || !strings.HasSuffix(string(e.Frames[0]), "\n\n") {
		t.Errorf("frame %q", e.Frames[0])
	}
	if !strings.Contains(string(e.Frames[9]), `"id":"toolu_1"`) {
		t.Errorf("tool id %s", e.Frames[9])
	}
	if got := names(frames(streamEvents("m", "m", nil, core.EndTurn, core.Usage{}))); got != "message_start,ping,message_delta,message_stop" {
		t.Errorf("empty %s", got)
	}
}

func TestStreamErrorEvents(t *testing.T) {
	c := New()
	m := protocol.Meta{Seed: "s", Model: "m", Stream: true}
	e := c.Encode(m, protocol.Out{Kind: protocol.OutStreamError, Error: protocol.ErrorSpec{Kind: core.Overloaded}})
	if got := names(e.Frames); got != "message_start,ping,error" {
		t.Errorf("plain %s", got)
	}
	e = c.Encode(m, protocol.Out{Kind: protocol.OutStreamError, Error: protocol.ErrorSpec{Kind: core.APIError, Message: "x"}, Partial: "part"})
	if got := names(e.Frames); got != "message_start,content_block_start,ping,text_delta,error" {
		t.Errorf("partial %s", got)
	}
	o, ok := c.Observe(protocol.Generate, 200, http.Header{"Content-Type": {"text/event-stream"}}, join(e.Frames))
	if !ok || o.Kind != protocol.SawStreamError || o.Error != core.APIError || o.Message != "x" || o.Reply.Text != "part" {
		t.Errorf("observe %+v", o)
	}
	m.Stream = false
	if e := c.Encode(m, protocol.Out{Kind: protocol.OutStreamError, Error: protocol.ErrorSpec{Kind: core.Overloaded}}); e.Status != 529 || e.Frames != nil {
		t.Errorf("non-streamed %+v", e)
	}
}

func join(fs [][]byte) []byte {
	var b []byte
	for _, f := range fs {
		b = append(b, f...)
	}
	return b
}

func TestMessageJSON(t *testing.T) {
	e := enc(core.Reply{Thinking: "t", ThinkingSignature: "s", Text: "hi", Stop: core.ToolUse,
		Calls: []core.Call{{ID: "1", Tool: "Read", Input: json.RawMessage(`{"file_path":"/a"}`)}}}, false)
	want := `{"content":[{"signature":"s","thinking":"t","type":"thinking"},{"text":"hi","type":"text"},{"id":"toolu_1","input":{"file_path":"/a"},"name":"Read","type":"tool_use"}],"id":"` + core.DeriveID("msg_", "s") + `","model":"m","role":"assistant","stop_reason":"tool_use","stop_sequence":null,"type":"message","usage":{"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"input_tokens":7,"output_tokens":9,"service_tier":"standard"}}`
	if string(e.Body) != want || e.Status != 200 || e.Header.Get("Content-Type") != "application/json" {
		t.Errorf("got %s", e.Body)
	}
}

// Every reply shape, streamed and not, reads back as the reply it was.
func TestEncodeObserveRoundTrip(t *testing.T) {
	c := New()
	replies := []core.Reply{
		{Text: "hello world, a longer text than one chunk", Stop: core.EndTurn},
		{Text: "cut", Stop: core.MaxTokens},
		{Thinking: "plan", ThinkingSignature: "sig_x", Text: "done", Stop: core.EndTurn},
		{Stop: core.ToolUse, Calls: []core.Call{{ID: "a", Tool: "Bash", Input: json.RawMessage(`{"command":"go test ./...","description":"Run"}`)}}},
		{Thinking: "t", ThinkingSignature: "s", Text: "two calls", Stop: core.ToolUse, Calls: []core.Call{
			{ID: "a", Tool: "Read", Input: json.RawMessage(`{"file_path":"/x/한국어.txt"}`)}, {ID: "b", Tool: "Y", Input: json.RawMessage(`{}`)}}},
	}
	for i, r := range replies {
		for _, stream := range []bool{true, false} {
			e := enc(r, stream)
			body := e.Body
			if stream {
				body = join(e.Frames)
			}
			h := e.Header.Clone()
			if stream {
				h = http.Header{"Content-Type": {"text/event-stream"}}
			}
			o, ok := c.Observe(protocol.Generate, e.Status, h, body)
			want := r
			want.Calls = nil
			for _, cl := range r.Calls {
				cl.ID = "toolu_" + cl.ID
				want.Calls = append(want.Calls, cl)
			}
			if !ok || o.Kind != protocol.SawReply || !reflect.DeepEqual(o.Reply, want) {
				t.Errorf("%d stream=%v:\n got %+v\nwant %+v", i, stream, o.Reply, want)
			}
		}
	}
	// A freeform call has no Messages form: it goes as {"input": text}.
	e := enc(core.Reply{Stop: core.ToolUse, Calls: []core.Call{{ID: "f", Tool: "apply_patch", Kind: core.Freeform, Raw: "*** Begin Patch"}}}, false)
	if !strings.Contains(string(e.Body), `"input":{"input":"*** Begin Patch"}`) {
		t.Error(string(e.Body))
	}
	if _, ok := c.Observe(protocol.Generate, 200, http.Header{}, []byte("nope")); ok {
		t.Error("bad json observed")
	}
	bad := "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"name\":\"X\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"partial_json\":\"{bad\"}}\n\n"
	if _, ok := c.Observe(protocol.Generate, 200, http.Header{"Content-Type": {"text/event-stream"}}, []byte(bad)); ok {
		t.Error("bad input observed")
	}
	if _, ok := c.Observe(protocol.CountTokens, 200, http.Header{}, []byte(`{}`)); ok {
		t.Error("count_tokens observed")
	}
	if _, ok := c.Observe(protocol.Generate, 418, http.Header{}, []byte(`{}`)); ok {
		t.Error("unknown status observed")
	}
}

func TestLimits(t *testing.T) {
	c := New()
	now := time.Unix(1000, 0)
	for _, w := range core.Windows {
		e := c.Encode(protocol.Meta{RequestID: "r"}, protocol.Out{Kind: protocol.OutLimit, Limit: protocol.LimitSpec{Window: w, Now: now, Resets: now.Add(time.Hour)}})
		if e.Status != 429 || e.Header.Get("Retry-After") != "3600" || !strings.Contains(string(e.Body), "rate_limit_error") || limitMessage(w) == "" {
			t.Errorf("%s: %d %v %s", w.Name(), e.Status, e.Header, e.Body)
		}
		o, ok := c.Observe(protocol.Generate, e.Status, e.Header, e.Body)
		switch w {
		case core.PerMinute, core.Daily:
			if e.Header.Get("Anthropic-Ratelimit-Unified-Status") != "" || !ok || o.Kind != protocol.SawError || o.Error != core.RateLimit {
				t.Errorf("%s: %v %+v", w.Name(), e.Header, o)
			}
			if (w == core.Daily) != (e.Header.Get("X-Should-Retry") == "false") {
				t.Errorf("%s retry %v", w.Name(), e.Header)
			}
		default:
			win := "7d"
			if w == core.FiveHour {
				win = "5h"
			}
			if e.Header.Get("Anthropic-Ratelimit-Unified-Representative-Claim") != w.Name() || e.Header.Get("Anthropic-Ratelimit-Unified-"+win+"-Reset") != "4600" ||
				e.Header.Get("X-Should-Retry") != "false" || e.Header.Get("Anthropic-Ratelimit-Unified-"+win+"-Utilization") != "1.0" {
				t.Errorf("%s: %v", w.Name(), e.Header)
			}
			if !ok || o.Kind != protocol.SawLimit || o.Window != w || o.ResetsIn != time.Hour {
				t.Errorf("observe %s: %+v", w.Name(), o)
			}
		}
	}
	h := http.Header{}
	h.Set("Anthropic-Ratelimit-Unified-Status", "rejected")
	if o, _ := c.Observe(protocol.Generate, 429, h, nil); o.Window != core.SevenDay {
		t.Error("unknown claim", o)
	}
}

func TestDecode(t *testing.T) {
	c := New()
	body := `{"model":"m","stream":true,"max_tokens":5,"system":[{"type":"text","text":"a"},{"type":"text","text":"b"}],
	"tools":[{"name":"Bash"},{"name":"Read"}],
	"messages":[{"role":"user","content":"hi"},{"role":"system","content":[{"type":"text","text":"env"}]},
	{"role":"assistant","content":[{"type":"thinking","thinking":"t","signature":"s"},{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}]},
	{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","is_error":true,"content":[{"type":"text","text":"Exit code 1"},{"type":"image"}]},{"type":"image"}]},
	{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_2","content":"plain"}]}]}`
	d, err := c.Decode(httptest.NewRequest("POST", "/v1/messages?beta=true", nil), []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	r := d.Request
	if d.Op != protocol.Generate || r.Model != "m" || !r.Stream || r.System != "a\nb" || strings.Join(r.ToolNames(), ",") != "Bash,Read" || len(r.Turns) != 5 {
		t.Fatalf("%+v", r)
	}
	want := []core.Turn{
		{Role: core.User, Parts: []core.Part{{Kind: core.Text, Text: "hi"}}},
		{Role: core.System, Parts: []core.Part{{Kind: core.Text, Text: "env"}}},
		{Role: core.Assistant, Parts: []core.Part{{Kind: core.Thinking, Text: "t"}, {Kind: core.ToolCall, CallID: "toolu_1", Tool: "Bash", Input: json.RawMessage(`{"command":"ls"}`)}}},
		{Role: core.User, Parts: []core.Part{{Kind: core.ToolResult, CallID: "toolu_1", Mark: core.MarkedError, Text: "Exit code 1"}, {Kind: core.Other}}},
		{Role: core.User, Parts: []core.Part{{Kind: core.ToolResult, CallID: "toolu_2", Mark: core.MarkedOK, Text: "plain"}}},
	}
	if !reflect.DeepEqual(r.Turns, want) {
		t.Errorf("turns\n got %+v\nwant %+v", r.Turns, want)
	}
	for _, bad := range []string{`nope`, `{"messages":[{"role":"user","content":"x"}]}`, `{"model":"m"}`,
		`{"model":"m","messages":[{"role":"tool","content":"x"}]}`, `{"model":"m","messages":[{"role":"user","content":5}]}`,
		`{"model":"m","system":5,"messages":[{"role":"user","content":"x"}]}`,
		`{"model":"m","messages":[{"role":"user","content":[{"type":"tool_result","content":5}]}]}`} {
		if _, err := c.Decode(httptest.NewRequest("POST", "/v1/messages", nil), []byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	if d, err := c.Decode(httptest.NewRequest("POST", "/v1/messages", nil), []byte(`{"model":"m","messages":[{"role":"user","content":null}]}`)); err != nil || len(d.Request.Turns[0].Parts) != 0 {
		t.Errorf("null content %v", err)
	}
	ops := map[string]protocol.Op{"/api/hello": protocol.Probe, "/v1/models": protocol.ListModels, "/v1/models/x": protocol.GetModel,
		"/anthropic/v1/models": protocol.ListModels}
	for p, op := range ops {
		if d, _ := c.Decode(httptest.NewRequest("GET", p, nil), nil); d.Op != op {
			t.Errorf("%s: %v", p, d.Op)
		}
	}
	if d, _ := c.Decode(httptest.NewRequest("POST", "/anthropic/v1/messages/count_tokens", nil), []byte(body)); d.Op != protocol.CountTokens {
		t.Error("count_tokens")
	}
	if d, _ := c.Decode(httptest.NewRequest("GET", "/v1/models/claude-x", nil), nil); d.Model != "claude-x" {
		t.Error(d.Model)
	}
}

func TestRoutesAndOtherOutcomes(t *testing.T) {
	c := New()
	if c.ID() != protocol.Anthropic || len(c.Routes()) != 9 {
		t.Fatal(c.Routes())
	}
	r := httptest.NewRequest("GET", "/v1/models", nil)
	if c.Accepts(r) {
		t.Error("plain request accepted")
	}
	r.Header.Set("Anthropic-Version", "2023-06-01")
	if !c.Accepts(r) || !c.Accepts(httptest.NewRequest("GET", "/anthropic/v1/models", nil)) {
		t.Error("anthropic request refused")
	}
	if e := c.Encode(protocol.Meta{}, protocol.Out{Kind: protocol.OutTokens, Tokens: 5}); string(e.Body) != `{"input_tokens":5}` {
		t.Error(string(e.Body))
	}
	e := c.Encode(protocol.Meta{Op: protocol.ListModels}, protocol.Out{Kind: protocol.OutModels, Models: []string{"a", "b"}})
	if !strings.Contains(string(e.Body), `"first_id":"a","has_more":false,"last_id":"b"`) {
		t.Error(string(e.Body))
	}
	if e := c.Encode(protocol.Meta{Op: protocol.GetModel}, protocol.Out{Kind: protocol.OutModels, Models: []string{"x"}}); !strings.Contains(string(e.Body), `"type":"model"`) {
		t.Error(string(e.Body))
	}
	if e := c.Encode(protocol.Meta{}, protocol.Out{Kind: protocol.OutProbe}); e.Status != 200 || len(e.Body) != 0 {
		t.Error(e)
	}
	defer func() {
		if recover() == nil {
			t.Error("no panic on unknown OutKind")
		}
	}()
	c.Encode(protocol.Meta{}, protocol.Out{})
}

func TestContract(t *testing.T) {
	codectest.Run(t, New(), codectest.Options{
		CallID:       func(id string) string { return "toolu_" + id },
		LimitWindows: []core.Window{core.FiveHour, core.SevenDay, core.SevenDayOpus, core.SevenDaySonnet},
	})
}
