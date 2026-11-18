package openaichat

import (
	"encoding/json"
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

func TestContract(t *testing.T) {
	codectest.Run(t, New(), codectest.Options{
		CallID:       func(id string) string { return "call_" + id },
		Freeform:     true,
		NoSignature:  true,
		LimitWindows: []core.Window{core.FiveHour, core.SevenDay, core.Daily, core.PerMinute},
	})
}

func TestDecode(t *testing.T) {
	body := `{"model":"m","stream":true,"messages":[
	{"role":"system","content":"sys a"},{"role":"developer","content":[{"type":"text","text":"sys b"}]},
	{"role":"user","content":[{"type":"text","text":"go"},{"type":"image_url"}]},
	{"role":"assistant","content":null,"reasoning_content":"think","tool_calls":[
	  {"id":"call_1","type":"function","function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}},
	  {"id":"call_2","type":"function","function":{"name":"bad","arguments":"not json"}},
	  {"id":"call_3","type":"custom","custom":{"name":"apply_patch","input":"*** Begin Patch"}}]},
	{"role":"tool","tool_call_id":"call_1","content":"a.txt"},{"role":"tool","tool_call_id":"call_2","content":[{"type":"text","text":"x"}]},
	{"role":"system","content":"later"},
	{"role":"assistant","content":"done"}],
	"tools":[{"type":"function","function":{"name":"bash"}},{"type":"custom","custom":{"name":"apply_patch"}}]}`
	d, err := New().Decode(httptest.NewRequest("POST", "/v1/chat/completions", nil), []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	r := d.Request
	if d.Op != protocol.Generate || r.Model != "m" || !r.Stream || r.System != "sys a\nsys b" ||
		!reflect.DeepEqual(r.Tools, []core.Tool{{Name: "bash"}, {Name: "apply_patch", Kind: core.Freeform}}) {
		t.Fatalf("%+v", r)
	}
	want := []core.Turn{
		{Role: core.User, Parts: []core.Part{{Kind: core.Text, Text: "go"}}},
		{Role: core.Assistant, Parts: []core.Part{{Kind: core.Thinking, Text: "think"},
			{Kind: core.ToolCall, CallID: "call_1", Tool: "bash", Input: json.RawMessage(`{"command":"ls"}`)},
			{Kind: core.ToolCall, CallID: "call_2", Tool: "bad", Input: json.RawMessage(`"not json"`)},
			{Kind: core.ToolCall, CallID: "call_3", Tool: "apply_patch", ToolKind: core.Freeform, Text: "*** Begin Patch"}}},
		{Role: core.User, Parts: []core.Part{{Kind: core.ToolResult, CallID: "call_1", Text: "a.txt"}, {Kind: core.ToolResult, CallID: "call_2", Text: "x"}}},
		{Role: core.System, Parts: []core.Part{{Kind: core.Text, Text: "later"}}},
		{Role: core.Assistant, Parts: []core.Part{{Kind: core.Text, Text: "done"}}},
	}
	if !reflect.DeepEqual(r.Turns, want) {
		t.Errorf("turns\n got %+v\nwant %+v", r.Turns, want)
	}
	for _, bad := range []string{`nope`, `{"messages":[{"role":"user","content":"x"}]}`, `{"model":"m"}`,
		`{"model":"m","messages":[{"role":"robot","content":"x"}]}`, `{"model":"m","messages":[{"role":"user","content":5}]}`} {
		if _, err := New().Decode(httptest.NewRequest("POST", "/chat/completions", nil), []byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	for path, op := range map[string]protocol.Op{"/v1/models": protocol.ListModels, "/models": protocol.ListModels, "/v1/models/x": protocol.GetModel} {
		if d, _ := New().Decode(httptest.NewRequest("GET", path, nil), nil); d.Op != op {
			t.Errorf("%s %v", path, d.Op)
		}
	}
	if !New().Accepts(httptest.NewRequest("GET", "/v1/models", nil)) || New().ID() != protocol.OpenAIChat || len(New().Routes()) != 6 {
		t.Error("routes")
	}
}

func TestStreamShape(t *testing.T) {
	r := core.Reply{Thinking: "hm", Text: "hi", Stop: core.ToolUse, Calls: []core.Call{{ID: "1", Tool: "bash", Input: json.RawMessage(`{"command":"echo 12345678901234567890"}`)}}}
	e := New().Encode(protocol.Meta{Seed: "s", Model: "m", Stream: true, Usage: core.Usage{Input: 3, Output: 4}, Now: time.Unix(5, 0)}, protocol.Out{Kind: protocol.OutReply, Reply: r})
	var kinds []string
	for _, f := range e.Frames {
		d := sse.Parse(f)[0].Data
		if d == "[DONE]" {
			kinds = append(kinds, "DONE")
			continue
		}
		var c struct {
			Object  string
			Choices []struct {
				Delta        map[string]any
				FinishReason *string `json:"finish_reason"`
			}
			Usage map[string]int
		}
		json.Unmarshal([]byte(d), &c)
		if c.Object != "chat.completion.chunk" {
			t.Fatal(d)
		}
		ch := c.Choices[0]
		switch {
		case ch.FinishReason != nil:
			kinds = append(kinds, "finish:"+*ch.FinishReason)
			if c.Usage["total_tokens"] != 7 {
				t.Error("usage", d)
			}
		case ch.Delta["role"] != nil:
			kinds = append(kinds, "role")
		case ch.Delta["reasoning_content"] != nil:
			kinds = append(kinds, "reasoning")
		case ch.Delta["content"] != nil:
			kinds = append(kinds, "content")
		case ch.Delta["tool_calls"] != nil:
			tc := ch.Delta["tool_calls"].([]any)[0].(map[string]any)
			if tc["id"] != nil {
				kinds = append(kinds, "call:"+tc["id"].(string))
			} else {
				kinds = append(kinds, "args")
			}
		}
	}
	if got := strings.Join(kinds, ","); got != "role,reasoning,content,call:call_1,args,args,args,finish:tool_calls,DONE" {
		t.Error(got)
	}
	// The in-stream error is OpenAI's data: {"error": ...} with no [DONE].
	e = New().Encode(protocol.Meta{Stream: true}, protocol.Out{Kind: protocol.OutStreamError, Error: protocol.ErrorSpec{Kind: core.Overloaded}})
	last := string(e.Frames[len(e.Frames)-1])
	if !strings.Contains(last, `"code":"server_is_overloaded"`) || strings.Contains(last, "[DONE]") {
		t.Error(last)
	}
}
