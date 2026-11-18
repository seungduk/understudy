package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	ssep "github.com/seungduk/understudy/internal/protocol/sse"
)

// One neutral script, served to a harness over each protocol it speaks: the
// actions become that harness's tools, and the results are read by its profile.
const neutral = `schema: understudy/v2
sessions:
  s:
    steps:
      - name: test
        reply: {tool_use: {text: "Testing.", calls: [{shell: {command: "go test ./..."}}]}}
      - name: judge
        cases:
          - when: {action: shell, exit_code: 0}
            reply: {message: {text: "DONE"}}
          - when: {action: shell, result: error}
            next: test
            reply: {tool_use: {calls: [{edit: {path: "{{cwd}}/a.go", old: "bug", new: "fix"}}]}}
`

func (h *rig) raw(method, path, b string, hdr map[string]string) resp {
	h.t.Helper()
	req, _ := http.NewRequest(method, h.srv.URL+path, strings.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer r.Body.Close()
	out, _ := io.ReadAll(r.Body)
	return resp{status: r.StatusCode, header: r.Header, body: string(out)}
}

// calls finds the tool calls of a response in any protocol, as "name input" lines.
func calls(body string) []string {
	var out []string
	for _, line := range ssep.DataLines([]byte(body)) {
		var v map[string]any
		if json.Unmarshal([]byte(line), &v) != nil {
			continue
		}
		walk(v, &out)
	}
	if len(out) == 0 {
		var v map[string]any
		if json.Unmarshal([]byte(body), &v) == nil {
			walk(v, &out)
		}
	}
	return out
}

func walk(v any, out *[]string) {
	switch x := v.(type) {
	case map[string]any:
		if fc, ok := x["functionCall"].(map[string]any); ok { // gemini
			b, _ := json.Marshal(fc["args"])
			*out = append(*out, fmt.Sprintf("%v %s", fc["name"], b))
			return
		}
		if x["type"] == "response.output_item.done" { // responses
			it := x["item"].(map[string]any)
			switch it["type"] {
			case "function_call":
				*out = append(*out, fmt.Sprintf("%v %v", it["name"], it["arguments"]))
			case "custom_tool_call":
				*out = append(*out, fmt.Sprintf("%v %q", it["name"], it["input"]))
			}
			return
		}
		if fn, ok := x["function"].(map[string]any); ok && fn["name"] != nil && x["id"] != nil { // chat (first delta has the name)
			*out = append(*out, fmt.Sprintf("%v", fn["name"]))
			return
		}
		for _, e := range x {
			walk(e, out)
		}
	case []any:
		for _, e := range x {
			walk(e, out)
		}
	}
}

func TestNeutralScriptOverEveryProtocol(t *testing.T) {
	h := start(t, neutral, Options{})
	hdr := map[string]string{"X-Understudy-Session": "s"}
	// Codex over Responses: shell is exec_command; a failing result goes back to edit as an apply_patch.
	codexTools := `"tools":[{"type":"function","name":"exec_command"},{"type":"custom","name":"apply_patch"}]`
	env := `{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context><cwd>/w</cwd></environment_context>"}]}`
	r := h.raw("POST", "/v1/responses", `{"model":"gpt","stream":true,`+codexTools+`,"input":[`+env+`]}`, hdr)
	if got := strings.Join(calls(r.body), "|"); r.status != 200 || got != `exec_command {"cmd":"go test ./...","yield_time_ms":30000}` {
		t.Fatalf("%d %s\n%s", r.status, got, r.body)
	}
	turn := func(out string) string {
		return `{"type":"function_call","name":"exec_command","arguments":"{}","call_id":"c1"},{"type":"function_call_output","call_id":"c1","output":` + out + `}`
	}
	r = h.raw("POST", "/v1/responses", `{"model":"gpt","stream":true,`+codexTools+`,"input":[`+env+`,`+turn(`"Process exited with code 1\nOutput:\nFAIL"`)+`]}`, hdr)
	if got := strings.Join(calls(r.body), "|"); got != `apply_patch "*** Begin Patch\n*** Update File: /w/a.go\n@@\n-bug\n+fix\n*** End Patch\n"` {
		t.Fatalf("%s\n%s", got, r.body)
	}
	r = h.raw("POST", "/v1/responses", `{"model":"gpt","stream":false,`+codexTools+`,"input":[`+env+`,`+turn(`"Process exited with code 0\nOutput:\nok"`)+`]}`, hdr)
	if !strings.Contains(r.body, `"text":"DONE"`) {
		t.Fatal(r.body)
	}
	// pi over Gemini: shell is bash; its own exit code line is read.
	piTools := `"tools":[{"functionDeclarations":[{"name":"read"},{"name":"bash"},{"name":"edit"},{"name":"write"}]}]`
	sys := `"systemInstruction":{"parts":[{"text":"Current working directory: /p"}]}`
	g := func(results string) string {
		c := `{"role":"user","parts":[{"text":"go"}]}`
		if results != "" {
			c += `,{"role":"model","parts":[{"functionCall":{"name":"bash","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"name":"bash","response":{"output":` + results + `}}}]}`
		}
		return `{` + sys + `,` + piTools + `,"contents":[` + c + `]}`
	}
	r = h.raw("POST", "/v1beta/models/fake:streamGenerateContent?alt=sse", g(""), hdr)
	if got := strings.Join(calls(r.body), "|"); got != `bash {"command":"go test ./..."}` {
		t.Fatalf("%s\n%s", got, r.body)
	}
	r = h.raw("POST", "/v1beta/models/fake:generateContent", g(`"FAIL\n\nCommand exited with code 1"`), hdr)
	if got := strings.Join(calls(r.body), "|"); got != `edit {"edits":[{"newText":"fix","oldText":"bug"}],"path":"/p/a.go"}` {
		t.Fatalf("%s\n%s", got, r.body)
	}
	// opencode over chat completions: shell is bash, and the title request is answered aside.
	ocTools := `"tools":[{"type":"function","function":{"name":"bash"}},{"type":"function","function":{"name":"read"}},{"type":"function","function":{"name":"edit"}},{"type":"function","function":{"name":"write"}},{"type":"function","function":{"name":"todowrite"}}]`
	r = h.raw("POST", "/v1/chat/completions", `{"model":"m","stream":true,"messages":[{"role":"system","content":"You are a title generator. You output ONLY a thread title."},{"role":"user","content":"go"}]}`, hdr)
	if !strings.Contains(r.body, `"content":"Scripted session"`) {
		t.Fatal(r.body)
	}
	r = h.raw("POST", "/chat/completions", `{"model":"m","stream":true,`+ocTools+`,"messages":[{"role":"system","content":"Working directory: /o"},{"role":"user","content":"go"}]}`, hdr)
	if got := strings.Join(calls(r.body), "|"); got != "bash" {
		t.Fatalf("%s\n%s", got, r.body)
	}
	log := h.s.SessionRequests("s")
	var got []string
	for _, e := range log {
		got = append(got, fmt.Sprintf("%s/%s/%s/%s", e.Protocol, e.Harness, e.Step+e.Aux, e.Branch))
	}
	want := "openai-responses/codex/test/reply openai-responses/codex/judge/case 1 openai-responses/codex/judge/case 0 " +
		"gemini/pi/test/reply gemini/pi/judge/case 1 openai-chat/generic/title/ openai-chat/opencode/test/reply"
	if strings.Join(got, " ") != want {
		t.Errorf("log\n got %s\nwant %s", strings.Join(got, " "), want)
	}
}

// The harness can be named by the script or a header instead of detected; an
// action the harness has no tool for, or a harness that does not offer the
// tool, is a divergence; protocol errors come back in the protocol's own form.
func TestHarnessChoiceAndProtocolErrors(t *testing.T) {
	h := start(t, `schema: understudy/v2
sessions:
  named:
    harness: gemini-cli
    steps:
      - name: a
        reply: {tool_use: {calls: [{read: {path: /x}}]}}
  header:
    steps:
      - name: a
        reply: {tool_use: {calls: [{read: {path: /x}}]}}
  limit:
    steps:
      - name: a
        reply: {limit: {window: daily, resets_in_seconds: 60}}
`, Options{})
	tools := `"tools":[{"functionDeclarations":[{"name":"read_file"},{"name":"read"}]}]`
	body := `{` + tools + `,"contents":[{"parts":[{"text":"go"}]}]}`
	r := h.raw("POST", "/v1beta/models/m:generateContent", body, map[string]string{"X-Understudy-Session": "named"})
	if got := strings.Join(calls(r.body), "|"); got != `read_file {"file_path":"/x"}` {
		t.Fatalf("%s %s", got, r.body)
	}
	r = h.raw("POST", "/v1beta/models/m:generateContent", body, map[string]string{"X-Understudy-Session": "header", "X-Understudy-Harness": "pi"})
	if got := strings.Join(calls(r.body), "|"); got != `read {"path":"/x"}` {
		t.Fatalf("%s %s", got, r.body)
	}
	// No profile fits a request that offers no tools: the read action has no tool.
	r = h.raw("POST", "/v1beta/models/m:generateContent", `{"contents":[{"parts":[{"text":"go"}]}]}`, map[string]string{"X-Understudy-Session": "header"})
	if r.status != 400 || !strings.Contains(r.body, `"status":"INVALID_ARGUMENT"`) || !strings.Contains(r.body, "no tool for the read action") {
		t.Fatal(r.status, r.body)
	}
	r = h.raw("POST", "/v1/chat/completions", `{"model":"m","messages":[{"role":"user","content":"x"}]}`, map[string]string{"X-Understudy-Session": "limit"})
	if r.status != 429 || !strings.Contains(r.body, `"type":"usage_limit_reached"`) || r.header.Get("X-Codex-Primary-Window-Minutes") != "1440" {
		t.Fatal(r.status, r.header, r.body)
	}
	r = h.raw("POST", "/v1beta/models/m:generateContent", `{"contents":[{"parts":[{"text":"x"}]}]}`, map[string]string{"X-Understudy-Session": "limit"})
	if r.status != 429 || !strings.Contains(r.body, "GenerateRequestsPerDayPerProjectPerModel") {
		t.Fatal(r.status, r.body)
	}
	r = h.raw("POST", "/v1/responses", `{"model":"m","input":"x"}`, map[string]string{"X-Understudy-Session": "nope"})
	if r.status != 400 || !strings.Contains(r.body, `"type":"invalid_request_error"`) || r.header.Get("X-Should-Retry") != "false" {
		t.Fatal(r.status, r.body)
	}
	r = h.raw("POST", "/v1/responses", `{"input":"x"}`, nil)
	if r.status != 400 || !strings.Contains(r.body, "model") {
		t.Fatal(r.status, r.body)
	}
	r = h.raw("POST", "/v1beta/models/m:explode", `{}`, nil)
	if r.status != 400 || !strings.Contains(r.body, "INVALID_ARGUMENT") {
		t.Fatal(r.status, r.body)
	}
	for _, e := range h.s.Requests()[:2] {
		if e.Harness != map[string]string{"named": "gemini-cli", "header": "pi"}[e.Session] {
			t.Errorf("%+v", e)
		}
	}
}
