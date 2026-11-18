package responses

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
		LimitWindows: []core.Window{core.FiveHour, core.SevenDay, core.Daily, core.PerMinute},
	})
}

func TestDecode(t *testing.T) {
	body := `{"model":"gpt","stream":true,"instructions":"be brief","input":[
	{"type":"message","role":"developer","content":[{"type":"input_text","text":"dev"}]},
	{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context><cwd>/w</cwd></environment_context>"}]},
	{"role":"user","content":"do it"},
	{"type":"reasoning","summary":[{"type":"summary_text","text":"plan"}],"encrypted_content":"x"},
	{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]},
	{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"ls\"}","call_id":"call_1"},
	{"type":"custom_tool_call","name":"apply_patch","input":"*** Begin Patch","call_id":"call_2"},
	{"type":"function_call_output","call_id":"call_1","output":"Process exited with code 0"},
	{"type":"custom_tool_call_output","call_id":"call_2","output":[{"type":"input_text","text":"Exit code: 0"}]},
	{"type":"web_search_call"},
	{"type":"message","role":"developer","content":"later"}],
	"tools":[{"type":"function","name":"exec_command"},{"type":"custom","name":"apply_patch"},{"type":"web_search"}]}`
	d, err := New().Decode(httptest.NewRequest("POST", "/v1/responses", nil), []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	r := d.Request
	if r.System != "be brief\ndev" || !r.Stream || !reflect.DeepEqual(r.Tools, []core.Tool{{Name: "exec_command"}, {Name: "apply_patch", Kind: core.Freeform}, {Name: "web_search"}}) {
		t.Fatalf("%+v", r)
	}
	want := []core.Turn{
		{Role: core.User, Parts: []core.Part{{Kind: core.Text, Text: "<environment_context><cwd>/w</cwd></environment_context>"}}},
		{Role: core.User, Parts: []core.Part{{Kind: core.Text, Text: "do it"}}},
		{Role: core.Assistant, Parts: []core.Part{{Kind: core.Thinking, Text: "plan"}, {Kind: core.Text, Text: "ok"},
			{Kind: core.ToolCall, CallID: "call_1", Tool: "exec_command", Input: json.RawMessage(`{"cmd":"ls"}`)},
			{Kind: core.ToolCall, CallID: "call_2", Tool: "apply_patch", ToolKind: core.Freeform, Text: "*** Begin Patch"}}},
		{Role: core.User, Parts: []core.Part{{Kind: core.ToolResult, CallID: "call_1", Text: "Process exited with code 0"}, {Kind: core.ToolResult, CallID: "call_2", Text: "Exit code: 0"}}},
		{Role: core.User, Parts: []core.Part{{Kind: core.Other}}},
		{Role: core.System, Parts: []core.Part{{Kind: core.Text, Text: "later"}}},
	}
	if !reflect.DeepEqual(r.Turns, want) {
		t.Errorf("turns\n got %+v\nwant %+v", r.Turns, want)
	}
	if d, err := New().Decode(httptest.NewRequest("POST", "/responses", nil), []byte(`{"model":"m","input":"hi"}`)); err != nil || d.Request.Turns[0].Parts[0].Text != "hi" {
		t.Error(err, d)
	}
	for _, bad := range []string{`nope`, `{"input":"x"}`, `{"model":"m"}`, `{"model":"m","input":5}`,
		`{"model":"m","input":[{"type":"message","role":"robot","content":"x"}]}`, `{"model":"m","input":[{"type":"message","role":"user","content":5}]}`,
		`{"model":"m","input":[{"type":"function_call_output","output":5}]}`} {
		if _, err := New().Decode(httptest.NewRequest("POST", "/v1/responses", nil), []byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	codex := httptest.NewRequest("GET", "/v1/models", nil)
	codex.Header.Set("Originator", "codex_exec")
	if !New().Accepts(codex) || New().Accepts(httptest.NewRequest("GET", "/v1/models", nil)) {
		t.Error("accepts")
	}
	if d, _ := New().Decode(httptest.NewRequest("GET", "/v1/models/x", nil), nil); d.Op != protocol.GetModel || d.Model != "x" {
		t.Error(d)
	}
}

// The event sequence is the one Codex consumes, numbered from 0.
func TestStreamSequence(t *testing.T) {
	r := core.Reply{Thinking: "plan", ThinkingSignature: "enc", Text: "hi", Stop: core.ToolUse, Calls: []core.Call{
		{ID: "1", Tool: "exec_command", Input: json.RawMessage(`{"cmd":"ls"}`)}, {ID: "2", Tool: "apply_patch", Kind: core.Freeform, Raw: "patch"}}}
	e := New().Encode(protocol.Meta{Seed: "s", Model: "m", Stream: true, Now: time.Unix(5, 0)}, protocol.Out{Kind: protocol.OutReply, Reply: r})
	var names []string
	for i, f := range e.Frames {
		fr := sse.Parse(f)[0]
		var d struct {
			Type string
			Seq  int `json:"sequence_number"`
		}
		json.Unmarshal([]byte(fr.Data), &d)
		if d.Type != fr.Event || d.Seq != i {
			t.Fatalf("frame %d: %s", i, f)
		}
		names = append(names, strings.TrimPrefix(fr.Event, "response."))
	}
	want := "created,in_progress," +
		"output_item.added,reasoning_summary_part.added,reasoning_summary_text.delta,reasoning_summary_text.done,reasoning_summary_part.done,output_item.done," +
		"output_item.added,content_part.added,output_text.delta,output_text.done,content_part.done,output_item.done," +
		"output_item.added,function_call_arguments.delta,function_call_arguments.done,output_item.done," +
		"output_item.added,custom_tool_call_input.delta,custom_tool_call_input.done,output_item.done,completed"
	if got := strings.Join(names, ","); got != want {
		t.Errorf("sequence\n got %s\nwant %s", got, want)
	}
	last := sse.Parse(e.Frames[len(e.Frames)-1])[0].Data
	if !strings.Contains(last, `"encrypted_content":"enc"`) || !strings.Contains(last, `"total_tokens"`) {
		t.Error(last)
	}
	e = New().Encode(protocol.Meta{Stream: true}, protocol.Out{Kind: protocol.OutReply, Reply: core.Reply{Text: "x", Stop: core.MaxTokens}})
	if l := string(e.Frames[len(e.Frames)-1]); !strings.Contains(l, "response.incomplete") || !strings.Contains(l, "max_output_tokens") {
		t.Error(l)
	}
	e = New().Encode(protocol.Meta{Stream: true}, protocol.Out{Kind: protocol.OutStreamError, Error: protocol.ErrorSpec{Kind: core.RateLimit}})
	if l := string(e.Frames[len(e.Frames)-1]); !strings.Contains(l, "response.failed") || !strings.Contains(l, `"code":"rate_limit_exceeded"`) {
		t.Error(l)
	}
}
