package gemini

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/seungduk/understudy/core"
	"github.com/seungduk/understudy/internal/protocol/codectest"
	"github.com/seungduk/understudy/internal/protocol/sse"
	"github.com/seungduk/understudy/protocol"
)

func TestContract(t *testing.T) {
	codectest.Run(t, New(), codectest.Options{
		CallID: func(string) string { return "" }, // generateContent calls carry no id
		// Google gives both the same status and body.
		SameAs:       map[core.ErrorKind]core.ErrorKind{core.RequestTooLarge: core.InvalidRequest},
		LimitWindows: []core.Window{core.FiveHour, core.SevenDay, core.Daily, core.PerMinute},
	})
}

func req(method, path string) *http.Request { return httptest.NewRequest(method, path, nil) }

func TestDecode(t *testing.T) {
	body := `{"systemInstruction":{"parts":[{"text":"sys"},{"text":"more"}]},
	"tools":[{"functionDeclarations":[{"name":"run_shell_command"},{"name":"read_file"}]},{"googleSearch":{}}],
	"contents":[
	{"role":"user","parts":[{"text":"go"},{"inlineData":{"mimeType":"image/png"}}]},
	{"role":"model","parts":[{"text":"plan","thought":true},{"text":"ok"},{"functionCall":{"name":"run_shell_command","args":{"command":"ls"}},"thoughtSignature":"c2ln"},{"functionCall":{"id":"x","name":"read_file"}}]},
	{"role":"model","parts":[{"functionResponse":{"id":"x","name":"run_shell_command","response":{"output":"Exit Code: 1"}}}]},
	{"role":"user","parts":[{"functionResponse":{"name":"read_file","response":{"error":"File not found"}}},{"functionResponse":{"name":"a","response":{"result":"r"}}},{"functionResponse":{"name":"b","response":{"other":1}}},{"functionResponse":{"name":"c","response":"plain"}}]}]}`
	d, err := New().Decode(req("POST", "/v1beta/models/gemini-x:streamGenerateContent?alt=sse"), []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	r := d.Request
	if d.Op != protocol.Generate || r.Model != "gemini-x" || !r.Stream || r.System != "sys\nmore" || strings.Join(r.ToolNames(), ",") != "run_shell_command,read_file" {
		t.Fatalf("%+v", r)
	}
	want := []core.Turn{
		{Role: core.User, Parts: []core.Part{{Kind: core.Text, Text: "go"}, {Kind: core.Other}}},
		{Role: core.Assistant, Parts: []core.Part{{Kind: core.Thinking, Text: "plan"}, {Kind: core.Text, Text: "ok"},
			{Kind: core.ToolCall, Tool: "run_shell_command", Input: json.RawMessage(`{"command":"ls"}`)},
			{Kind: core.ToolCall, CallID: "x", Tool: "read_file", Input: json.RawMessage(`{}`)}}},
		{Role: core.User, Parts: []core.Part{{Kind: core.ToolResult, CallID: "x", Tool: "run_shell_command", Text: "Exit Code: 1"}}},
		{Role: core.User, Parts: []core.Part{{Kind: core.ToolResult, Tool: "read_file", Text: "File not found", Mark: core.MarkedError},
			{Kind: core.ToolResult, Tool: "a", Text: "r"}, {Kind: core.ToolResult, Tool: "b", Text: `{"other":1}`}, {Kind: core.ToolResult, Tool: "c", Text: `"plain"`}}},
	}
	if !reflect.DeepEqual(r.Turns, want) {
		t.Errorf("turns\n got %+v\nwant %+v", r.Turns, want)
	}
	if d, err := New().Decode(req("POST", "/v1beta/models/m:generateContent"), []byte(`{"contents":[{"parts":[{"text":"x"}]}]}`)); err != nil || d.Request.Stream || d.Request.Turns[0].Role != core.User {
		t.Error(err, d)
	}
	if d, _ := New().Decode(req("POST", "/v1beta/models/m:countTokens"), []byte(`{"contents":[{"parts":[{"text":"x"}]}]}`)); d.Op != protocol.CountTokens {
		t.Error(d.Op)
	}
	if d, _ := New().Decode(req("GET", "/v1beta/models"), nil); d.Op != protocol.ListModels {
		t.Error(d.Op)
	}
	if d, _ := New().Decode(req("GET", "/v1beta/models/m"), nil); d.Op != protocol.GetModel || d.Model != "m" {
		t.Error(d)
	}
	for path, b := range map[string]string{
		"/v1beta/models/m:generateContent":   `nope`,
		"/v1beta/models/m:streamGenerate":    `{"contents":[{"parts":[{"text":"x"}]}]}`,
		"/v1beta/models/m":                   `{"contents":[{"parts":[{"text":"x"}]}]}`,
		"/v1beta/models/m:generateContent?x": `{"contents":[]}`,
		"/v1beta/models/n:generateContent":   `{"contents":[{"role":"robot","parts":[]}]}`,
	} {
		if _, err := New().Decode(req("POST", path), []byte(b)); err == nil {
			t.Errorf("accepted %s %s", path, b)
		}
	}
}

func TestStreamShape(t *testing.T) {
	r := core.Reply{Thinking: "plan", ThinkingSignature: "sig", Text: "hi", Stop: core.ToolUse, Calls: []core.Call{
		{ID: "1", Tool: "a", Input: json.RawMessage(`{"x":1}`)}, {ID: "2", Tool: "b", Kind: core.Freeform, Raw: "raw"}}}
	e := New().Encode(protocol.Meta{Seed: "s", Model: "m", Stream: true}, protocol.Out{Kind: protocol.OutReply, Reply: r})
	if len(e.Frames) != 3 {
		t.Fatalf("%d frames", len(e.Frames))
	}
	last := sse.Parse(e.Frames[2])[0].Data
	for _, s := range []string{`"finishReason":"STOP"`, `"thoughtSignature":"c2ln"`, `"args":{"input":"raw"}`, `"functionCall":{"args":{"x":1},"name":"a"}`} {
		if !strings.Contains(last, s) {
			t.Errorf("%s lacks %s", last, s)
		}
	}
	if !strings.Contains(sse.Parse(e.Frames[0])[0].Data, `"thought":true`) {
		t.Error(string(e.Frames[0]))
	}
	e = New().Encode(protocol.Meta{Model: "m"}, protocol.Out{Kind: protocol.OutLimit, Limit: protocol.LimitSpec{Window: core.Daily}})
	for _, s := range []string{`"status":"RESOURCE_EXHAUSTED"`, `GenerateRequestsPerDayPerProjectPerModel`, `"retryDelay":"0s"`, `Please retry in 0s.`} {
		if !strings.Contains(string(e.Body), s) {
			t.Errorf("limit lacks %s: %s", s, e.Body)
		}
	}
	if !New().Accepts(nil) || New().ID() != protocol.Gemini || len(New().Routes()) != 3 {
		t.Error("routes")
	}
}
