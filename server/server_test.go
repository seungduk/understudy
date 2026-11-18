package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seungduk/understudy/script"
)

var t0 = time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)

func mustScript(t *testing.T, src string) *script.Script {
	t.Helper()
	s, err := script.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type rig struct {
	t   *testing.T
	s   *Server
	srv *httptest.Server
}

func start(t *testing.T, src string, o Options) *rig {
	t.Helper()
	if src != "" {
		o.Script = mustScript(t, src)
	}
	s := New(o)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &rig{t: t, s: s, srv: srv}
}

// conv builds a Claude-Code-like request body: messages alternate user and
// assistant; each assistant turn calls one tool and the next user message answers it.
type turn struct {
	tool    string
	isError bool
	result  string
}

func body(stream bool, tools []string, system string, turns ...turn) string {
	msgs := []any{
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "do the task"}}},
		map[string]any{"role": "system", "content": []any{map[string]any{"type": "text", "text": "# Environment\n - Primary working directory: /work/dir\n"}}},
	}
	for i, tr := range turns {
		id := fmt.Sprintf("toolu_%d", i)
		msgs = append(msgs, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": id, "name": tr.tool, "input": map[string]any{}}}})
		msgs = append(msgs, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "is_error": tr.isError, "content": tr.result}}})
	}
	tl := []any{}
	for _, n := range tools {
		tl = append(tl, map[string]any{"name": n, "input_schema": map[string]any{"type": "object"}})
	}
	b, _ := json.Marshal(map[string]any{"model": "claude-test", "stream": stream, "max_tokens": 100, "system": system, "tools": tl, "messages": msgs})
	return string(b)
}

var allTools = []string{"Bash", "Edit", "Read", "Write"}

type resp struct {
	status int
	header http.Header
	body   string
	events []sse
}

type sse struct {
	name string
	data map[string]any
}

func (h *rig) post(path, b string, hdr map[string]string) resp {
	h.t.Helper()
	req, _ := http.NewRequest("POST", h.srv.URL+path, strings.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", "dummy-secret")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer r.Body.Close()
	raw, _ := io.ReadAll(r.Body)
	out := resp{status: r.StatusCode, header: r.Header, body: string(raw)}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "text/event-stream") {
		sc := bufio.NewScanner(strings.NewReader(string(raw)))
		var name string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				name = line[7:]
			case strings.HasPrefix(line, "data: "):
				var d map[string]any
				if err := json.Unmarshal([]byte(line[6:]), &d); err != nil {
					h.t.Fatalf("bad data %q", line)
				}
				out.events = append(out.events, sse{name, d})
			}
		}
	}
	return out
}

func (h *rig) msg(b string, hdr map[string]string) resp {
	return h.post("/v1/messages?beta=true", b, hdr)
}

// get sends a GET as Claude Code does (with anthropic-version); getPlain sends none.
func (h *rig) get(path string) resp {
	h.t.Helper()
	return h.getWith(path, map[string]string{"Anthropic-Version": "2023-06-01"})
}

func (h *rig) getPlain(path string) resp {
	h.t.Helper()
	return h.getWith(path, nil)
}

func (h *rig) getWith(path string, hdr map[string]string) resp {
	h.t.Helper()
	req, _ := http.NewRequest("GET", h.srv.URL+path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer r.Body.Close()
	raw, _ := io.ReadAll(r.Body)
	return resp{status: r.StatusCode, header: r.Header, body: string(raw)}
}

// text joins the text deltas; tools lists the tool_use blocks with their joined input.
func (r resp) text() string {
	var b strings.Builder
	for _, e := range r.events {
		if e.name == "content_block_delta" {
			if d := e.data["delta"].(map[string]any); d["type"] == "text_delta" {
				b.WriteString(d["text"].(string))
			}
		}
	}
	return b.String()
}

func (r resp) tools() []string {
	var out []string
	var cur strings.Builder
	name := ""
	for _, e := range r.events {
		switch e.name {
		case "content_block_start":
			if cb := e.data["content_block"].(map[string]any); cb["type"] == "tool_use" {
				name = cb["name"].(string)
				cur.Reset()
			}
		case "content_block_delta":
			if d := e.data["delta"].(map[string]any); d["type"] == "input_json_delta" {
				cur.WriteString(d["partial_json"].(string))
			}
		case "content_block_stop":
			if name != "" {
				out = append(out, name+" "+cur.String())
				name = ""
			}
		}
	}
	return out
}

func (r resp) names() string {
	var n []string
	for _, e := range r.events {
		n = append(n, e.name)
	}
	return strings.Join(n, ",")
}

const loop = `schema: understudy/v1
sessions:
  default:
    steps:
      - name: read
        reply: {tool_use: {thinking: first read, text: Reading., calls: [{tool: Read, input: {file_path: "{{cwd}}/a.txt"}}]}}
      - name: edit
        reply: {tool_use: {calls: [{tool: Edit, input: {file_path: "{{cwd}}/a.txt", old_string: old, new_string: new}}]}}
      - name: test
        reply: {tool_use: {calls: [{tool: Bash, input: {command: go test ./..., description: run tests}}]}}
      - name: judge
        cases:
          - when: {tool: Bash, exit_code: 0}
            reply: {message: {text: Tests pass.}}
          - when: {tool: Bash, result: error}
            next: edit
            reply: {message: {text: Tests fail.}}
`

func TestToolLoop(t *testing.T) {
	h := start(t, loop, Options{})
	r := h.msg(body(true, allTools, ""), nil)
	if r.status != 200 || r.names() != "message_start,content_block_start,ping,content_block_delta,content_block_delta,content_block_stop,content_block_start,content_block_delta,content_block_stop,content_block_start,content_block_delta,content_block_delta,content_block_delta,content_block_stop,message_delta,message_stop" {
		t.Fatalf("%d %s %s", r.status, r.names(), r.body)
	}
	if got := r.tools(); len(got) != 1 || got[0] != `Read {"file_path":"/work/dir/a.txt"}` {
		t.Fatalf("%q", got)
	}
	if r.header.Get("Request-Id") == "" {
		t.Error("no request id")
	}
	r = h.msg(body(true, allTools, "", turn{"Read", false, "1\told"}), nil)
	if got := r.tools(); got[0] != `Edit {"file_path":"/work/dir/a.txt","new_string":"new","old_string":"old"}` {
		t.Fatalf("%q", got)
	}
	r = h.msg(body(true, allTools, "", turn{"Read", false, ""}, turn{"Edit", false, "updated"}, turn{"Bash", false, "ok"}), nil)
	if r.text() != "Tests pass." {
		t.Fatalf("%q", r.text())
	}
	// A failing run goes back to edit.
	r = h.msg(body(true, allTools, "", turn{"Read", false, ""}, turn{"Edit", false, ""}, turn{"Bash", true, "Exit code 1\nFAIL"}), nil)
	if r.text() != "Tests fail." {
		t.Fatalf("%q", r.text())
	}
	r = h.msg(body(true, allTools, "", turn{"Read", false, ""}, turn{"Edit", false, ""}, turn{"Bash", true, "Exit code 1"}, turn{"x", false, ""}), nil)
	if got := r.tools(); len(got) != 1 || !strings.HasPrefix(got[0], "Edit ") {
		t.Fatalf("jump %q", got)
	}
	log := h.s.Requests()
	if len(log) != 5 {
		t.Fatalf("%d", len(log))
	}
	e := log[3]
	if e.Session != "default" || e.SessionSource != "default" || e.Turn != 3 || e.Step != "judge" || e.Branch != "case 1" || e.Attempt != 1 || e.Reply != KindMessage || e.Outcome != Served || e.Status != 200 || !e.Stream || e.CWD != "/work/dir" || e.ToolsOffered != 4 || len(e.Trace) != 5 {
		t.Fatalf("%+v", e)
	}
	if _, ok := e.Headers["x-api-key"]; ok {
		t.Fatal("credential logged")
	}
	if e.Headers["content-type"] != "application/json" {
		t.Fatalf("headers %v", e.Headers)
	}
}

func TestAttemptsAndErrors(t *testing.T) {
	src := `schema: understudy/v1
sessions:
  default:
    steps:
      - name: first
        attempts:
          - http_error: {error: overloaded, retry_after_seconds: 2, retry: always}
          - http_error: {error: rate_limit, message: slow down, retry: never}
          - stream_error: {error: overloaded, partial_text: "Half an"}
          - message: {text: finally, thinking: hmm, stop: max_tokens}
`
	h := start(t, src, Options{})
	r := h.msg(body(true, allTools, ""), nil)
	if r.status != 529 || r.header.Get("Retry-After") != "2" || r.header.Get("X-Should-Retry") != "true" || !strings.Contains(r.body, `"type":"overloaded_error"`) || !strings.Contains(r.body, `"request_id":"req_understudy_000001"`) {
		t.Fatalf("1: %d %v %s", r.status, r.header, r.body)
	}
	r = h.msg(body(true, allTools, ""), nil)
	if r.status != 429 || r.header.Get("X-Should-Retry") != "false" || !strings.Contains(r.body, `"message":"slow down"`) {
		t.Fatalf("2: %d %s", r.status, r.body)
	}
	r = h.msg(body(true, allTools, ""), nil)
	if r.status != 200 || r.names() != "message_start,content_block_start,ping,content_block_delta,error" || r.text() != "Half an" {
		t.Fatalf("3: %s", r.names())
	}
	if e := r.events[len(r.events)-1].data["error"].(map[string]any); e["type"] != "overloaded_error" {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ { // the last attempt repeats
		r = h.msg(body(true, allTools, ""), nil)
		if r.text() != "finally" || !strings.Contains(r.events[len(r.events)-2].name, "message_delta") {
			t.Fatalf("4: %s", r.names())
		}
		if sr := r.events[len(r.events)-2].data["delta"].(map[string]any)["stop_reason"]; sr != "max_tokens" {
			t.Fatal(sr)
		}
	}
	got := []int{}
	for _, e := range h.s.Requests() {
		got = append(got, e.Attempt)
	}
	if fmt.Sprint(got) != "[1 2 3 4 5]" {
		t.Fatal(got)
	}
	// Non-streamed: a stream_error becomes the same HTTP error; a message is a JSON body.
	h.s.Reset()
	h.msg(body(false, allTools, ""), nil)
	h.msg(body(false, allTools, ""), nil)
	r = h.msg(body(false, allTools, ""), nil)
	if r.status != 529 || !strings.Contains(r.body, "overloaded_error") {
		t.Fatalf("non-stream stream_error %d %s", r.status, r.body)
	}
	r = h.msg(body(false, allTools, ""), nil)
	var m struct {
		Type    string
		Content []map[string]any
	}
	if err := json.Unmarshal([]byte(r.body), &m); err != nil || m.Type != "message" || m.Content[1]["text"] != "finally" || m.Content[0]["type"] != "thinking" {
		t.Fatalf("non-stream %s", r.body)
	}
}

func TestLimit(t *testing.T) {
	src := `schema: understudy/v1
sessions:
  default: {steps: [{name: a, reply: {limit: {window: seven_day, resets_in_seconds: 3600}}}]}
  five: {steps: [{name: a, reply: {limit: {window: five_hour, resets_in_seconds: 60}}}]}
`
	h := start(t, src, Options{Clock: VirtualClock, Start: t0})
	r := h.msg(body(true, allTools, ""), nil)
	want := map[string]string{
		"X-Should-Retry": "false", "Retry-After": "3600",
		"Anthropic-Ratelimit-Unified-Status": "rejected", "Anthropic-Ratelimit-Unified-Representative-Claim": "seven_day",
		"Anthropic-Ratelimit-Unified-Reset": fmt.Sprint(t0.Unix() + 3600), "Anthropic-Ratelimit-Unified-7d-Status": "rejected",
		"Anthropic-Ratelimit-Unified-7d-Reset": fmt.Sprint(t0.Unix() + 3600), "Anthropic-Ratelimit-Unified-7d-Utilization": "1.0",
	}
	for k, v := range want {
		if r.header.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, r.header.Get(k), v)
		}
	}
	if r.status != 429 || !strings.Contains(r.body, "rate_limit_error") {
		t.Fatal(r.body)
	}
	r = h.msg(body(true, allTools, ""), map[string]string{"X-Understudy-Session": "five"})
	if r.header.Get("Anthropic-Ratelimit-Unified-5h-Status") != "rejected" || r.header.Get("Anthropic-Ratelimit-Unified-Representative-Claim") != "five_hour" {
		t.Fatal(r.header)
	}
}

func TestRefusals(t *testing.T) {
	h := start(t, "", Options{})
	check := func(r resp, status int, outcome Outcome, has string) {
		t.Helper()
		if r.status != status || r.header.Get("X-Should-Retry") != "false" || !strings.Contains(r.body, has) {
			t.Fatalf("%d %v %s", r.status, r.header, r.body)
		}
		log := h.s.Requests()
		if e := log[len(log)-1]; e.Outcome != outcome || e.Error == "" {
			t.Fatalf("%+v", e)
		}
	}
	check(h.msg(body(true, allTools, ""), nil), 400, NoScript, "no script is loaded")
	h.s.Load(mustScript(t, loop))
	check(h.msg(`{"model":"m"}`, nil), 400, BadRequest, "messages")
	check(h.msg(strings.Repeat(" ", MaxBody+1), nil), 413, BadRequest, "request_too_large")
	check(h.msg(body(true, allTools, ""), map[string]string{"X-Understudy-Session": "nope"}), 400, NoScript, `has no session \"nope\"`)
	check(h.msg(body(true, allTools, ""), map[string]string{"X-Understudy-Session": "bad key"}), 400, NoScript, "not a valid key")
	check(h.msg(body(true, allTools, "[understudy-session: ]"), nil), 400, NoScript, "not a valid key")
	// The tool is not offered: strict divergence.
	check(h.msg(body(true, []string{"Bash"}, ""), nil), 400, Diverged, "does not offer (it offers: Bash)")
	// After the last step.
	check(h.msg(body(true, allTools, "", turn{"Read", false, ""}, turn{"Edit", false, ""}, turn{"Bash", false, ""}, turn{"x", false, ""}), nil), 400, Diverged, "has ended")
	// No case matches.
	check(h.msg(body(true, allTools, "", turn{"Read", false, ""}, turn{"Edit", false, ""}, turn{"Write", false, ""}), nil), 400, Diverged, "no case matched")
}

func TestLenientAndCWD(t *testing.T) {
	src := strings.Replace(loop, "schema: understudy/v1\n", "schema: understudy/v1\ndivergence: lenient\n", 1)
	h := start(t, src, Options{})
	r := h.msg(body(true, []string{"Bash"}, ""), nil)
	if r.status != 200 || len(r.tools()) != 1 {
		t.Fatal(r.body)
	}
	if e := h.s.Requests()[0]; !strings.Contains(e.Divergence, "does not offer") || e.Outcome != Served {
		t.Fatalf("%+v", e)
	}
	// No working directory in the request.
	b := `{"model":"m","stream":true,"tools":[{"name":"Read"}],"messages":[{"role":"user","content":"x"}]}`
	r = h.msg(b, nil)
	if got := r.tools(); got[0] != `Read {"file_path":"/a.txt"}` {
		t.Fatal(got)
	}
	if e := h.s.Requests()[1]; !strings.Contains(e.Divergence, "{{cwd}}") {
		t.Fatalf("%+v", e)
	}
	h.s.Load(mustScript(t, loop))
	if r := h.msg(b, nil); r.status != 400 || !strings.Contains(r.body, "working directory") {
		t.Fatal(r.body)
	}
	v, used := substitute([]any{"{{cwd}}", map[string]any{"a": "x{{cwd}}"}, 3}, "/w")
	if !used || fmt.Sprint(v) != "[/w map[a:x/w] 3]" {
		t.Fatal(v)
	}
}

func TestStallAndControl(t *testing.T) {
	src := `schema: understudy/v1
sessions:
  default:
    steps:
      - name: held
        reply: {message: {text: released}, stall: {gate: review}}
  timed:
    steps:
      - name: slow
        reply: {message: {text: later}, stall: {seconds: 600, at: mid_stream}, pace: {first_event_ms: 1000, between_events_ms: 10}}
`
	h := start(t, src, Options{Clock: VirtualClock, Start: t0})
	done := make(chan resp)
	go func() { done <- h.msg(body(true, allTools, ""), nil) }()
	for h.s.Gates() == nil || len(h.s.Gates()) == 0 || h.s.Gates()[0].Waiting != 1 {
		time.Sleep(time.Millisecond)
	}
	if e := h.s.Requests()[0]; e.Outcome != Pending {
		t.Fatal(e.Outcome)
	}
	g := h.get(ControlPrefix + "gates")
	if !strings.Contains(g.body, `"name":"review"`) || !strings.Contains(g.body, `"waiting":1`) {
		t.Fatal(g.body)
	}
	r := h.post(ControlPrefix+"gates/review/release", "", nil)
	if r.status != 200 || !strings.Contains(r.body, `"waiting":1`) {
		t.Fatal(r.body)
	}
	if out := <-done; out.text() != "released" {
		t.Fatal(out.body)
	}
	if r := h.post(ControlPrefix+"gates/Bad!/release", "", nil); r.status != 400 {
		t.Fatal(r.body)
	}
	// Timed stall and pacing on the virtual clock, moved through the control API.
	go func() { done <- h.msg(body(true, allTools, ""), map[string]string{"X-Understudy-Session": "timed"}) }()
	steps := 0
wait:
	for {
		select {
		case out := <-done:
			if out.text() != "later" {
				t.Fatal(out.body)
			}
			break wait
		default:
		}
		if h.s.virtual.Sleepers() == 0 {
			time.Sleep(time.Millisecond)
			continue
		}
		steps++
		if r := h.post(ControlPrefix+"clock/advance", `{"ms":600000}`, nil); r.status != 200 {
			t.Fatal(r.body)
		}
	}
	// first_event_ms, the stall and one sleep between each pair of events.
	if steps < 3 || !h.s.Now().After(t0.Add(20*time.Minute)) {
		t.Fatal(steps, h.s.Now())
	}
	if c := h.get(ControlPrefix + "clock"); !strings.Contains(c.body, `"mode":"virtual"`) {
		t.Fatal(c.body)
	}
	for _, bad := range []string{``, `{"ms":-1}`, `{"sec":1}`} {
		if r := h.post(ControlPrefix+"clock/advance", bad, nil); r.status != 400 {
			t.Fatal(bad, r.body)
		}
	}
	real := start(t, src, Options{})
	if r := real.post(ControlPrefix+"clock/advance", `{"ms":1}`, nil); r.status != 409 {
		t.Fatal(r.body)
	}
	if c := real.get(ControlPrefix + "clock"); !strings.Contains(c.body, `"mode":"real"`) {
		t.Fatal(c.body)
	}
	if real.s.Now().IsZero() {
		t.Fatal("now")
	}
}

func TestControlScriptAndLog(t *testing.T) {
	h := start(t, "", Options{})
	if r := h.get(ControlPrefix + "health"); !strings.Contains(r.body, `"script_loaded":false`) {
		t.Fatal(r.body)
	}
	if r := h.get(ControlPrefix + "script"); r.status != 404 {
		t.Fatal(r.body)
	}
	req, _ := http.NewRequest("PUT", h.srv.URL+ControlPrefix+"script", strings.NewReader("schema: understudy/v1\nsessions: {default: {steps: [{name: a, replly: {}}]}}"))
	r0, _ := http.DefaultClient.Do(req)
	b, _ := io.ReadAll(r0.Body)
	r0.Body.Close()
	if r0.StatusCode != 400 || !strings.Contains(string(b), `"rule":"unknown_field"`) || !strings.Contains(string(b), "did you mean reply?") {
		t.Fatal(string(b))
	}
	req, _ = http.NewRequest("PUT", h.srv.URL+ControlPrefix+"script", strings.NewReader(loop))
	r0, _ = http.DefaultClient.Do(req)
	b, _ = io.ReadAll(r0.Body)
	r0.Body.Close()
	if r0.StatusCode != 200 || !strings.Contains(string(b), `"sessions":["default"]`) {
		t.Fatal(string(b))
	}
	if r := h.get(ControlPrefix + "script"); r.status != 200 || !strings.Contains(r.body, "name: judge") {
		t.Fatal(r.body)
	}
	if _, err := script.Parse([]byte(h.get(ControlPrefix + "script").body)); err != nil {
		t.Fatalf("GET script does not parse again: %v", err)
	}
	h.msg(body(true, allTools, ""), nil)
	h.msg(body(true, allTools, ""), map[string]string{"X-Understudy-Session": "other"})
	if r := h.get(ControlPrefix + "requests?session=default"); strings.Count(r.body, `"seq"`) != 1 {
		t.Fatal(r.body)
	}
	if r := h.get(ControlPrefix + "requests"); strings.Count(r.body, `"seq"`) != 2 {
		t.Fatal(r.body)
	}
	if r := h.post(ControlPrefix+"reset", "", nil); r.status != 200 || len(h.s.Requests()) != 0 {
		t.Fatal(r.body)
	}
	// Reset also forgets attempts: the first answer comes again.
	if r := h.msg(body(true, allTools, ""), nil); len(r.tools()) != 1 || h.s.Requests()[0].Attempt != 1 {
		t.Fatal(r.body)
	}
	if r := h.get(ControlPrefix + "nope"); r.status != 404 || !strings.Contains(r.body, "fix") {
		t.Fatal(r.body)
	}
	if h.s.Script() == nil || len(h.s.SessionRequests("default")) != 1 {
		t.Fatal("accessors")
	}
}

func TestOtherEndpoints(t *testing.T) {
	h := start(t, loop, Options{})
	if r := h.get("/v1/models"); !strings.Contains(r.body, `"id":"`+DefaultModel+`"`) {
		t.Fatal(r.body)
	}
	h.msg(body(true, allTools, ""), nil)
	if r := h.get("/v1/models"); !strings.Contains(r.body, `"id":"claude-test"`) || !strings.Contains(r.body, `"has_more":false`) {
		t.Fatal(r.body)
	}
	if r := h.get("/v1/models/claude-x"); !strings.Contains(r.body, `"id":"claude-x"`) {
		t.Fatal(r.body)
	}
	if r := h.post("/v1/messages/count_tokens", body(false, nil, ""), nil); r.status != 200 || !strings.Contains(r.body, `"input_tokens":`) {
		t.Fatal(r.body)
	}
	if r := h.post("/v1/messages/count_tokens", `{}`, nil); r.status != 400 {
		t.Fatal(r.body)
	}
	req, _ := http.NewRequest("HEAD", h.srv.URL+"/api/hello", nil)
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 200 {
		t.Fatal(err)
	}
	if r := h.get("/v1/files"); r.status != 404 || !strings.Contains(r.body, "not_found_error") {
		t.Fatal(r.body)
	}
	// Without an Anthropic header, the shared model list is OpenAI's.
	if r := h.getPlain("/v1/models"); !strings.Contains(r.body, `"object":"list"`) || !strings.Contains(r.body, `"id":"claude-test"`) {
		t.Fatal(r.body)
	}
	if r := h.getPlain("/v1beta/models"); !strings.Contains(r.body, `"name":"models/claude-test"`) {
		t.Fatal(r.body)
	}
	other := 0
	for _, e := range h.s.Requests() {
		if e.Outcome == Other {
			other++
		}
	}
	if other != 8 { // the bad count_tokens body is a bad_request
		t.Fatal(other)
	}
}

// Many sessions at once, each through the whole loop, with client retries:
// every session gets exactly its own answers.
func TestConcurrentSessions(t *testing.T) {
	var src strings.Builder
	src.WriteString("schema: understudy/v1\nsessions:\n")
	const n = 40
	for i := 0; i < n; i++ {
		fmt.Fprintf(&src, "  t%d/writer:\n    steps:\n      - name: a\n        attempts: [{http_error: {error: overloaded}}, {tool_use: {calls: [{tool: Bash, input: {command: echo %d}}]}}]\n      - name: b\n        reply: {message: {text: done %d}}\n", i, i, i)
	}
	h := start(t, src.String(), Options{})
	var wg sync.WaitGroup
	errs := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			hd := map[string]string{"X-Understudy-Session": fmt.Sprintf("t%d/writer", i)}
			if r := h.msg(body(true, allTools, ""), hd); r.status != 529 {
				errs <- fmt.Sprintf("%d: first %d", i, r.status)
				return
			}
			r := h.msg(body(true, allTools, ""), hd)
			if got := r.tools(); len(got) != 1 || got[0] != fmt.Sprintf(`Bash {"command":"echo %d"}`, i) {
				errs <- fmt.Sprintf("%d: %q", i, got)
				return
			}
			r = h.msg(body(true, allTools, "", turn{"Bash", false, "x"}), hd)
			if r.text() != fmt.Sprintf("done %d", i) {
				errs <- fmt.Sprintf("%d: %q", i, r.text())
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	if len(h.s.Requests()) != 3*n {
		t.Fatal(len(h.s.Requests()))
	}
}

func TestIDsDeterministic(t *testing.T) {
	ids := func() []string {
		h := start(t, loop, Options{})
		r := h.msg(body(true, allTools, ""), nil)
		var out []string
		for _, e := range r.events {
			if e.name == "message_start" {
				out = append(out, e.data["message"].(map[string]any)["id"].(string))
			}
			if e.name == "content_block_start" {
				cb := e.data["content_block"].(map[string]any)
				if id, ok := cb["id"]; ok {
					out = append(out, id.(string))
				}
			}
			if e.name == "content_block_delta" {
				if d := e.data["delta"].(map[string]any); d["type"] == "signature_delta" {
					out = append(out, d["signature"].(string))
				}
			}
		}
		return out
	}
	a, b := ids(), ids()
	if len(a) != 3 || fmt.Sprint(a) != fmt.Sprint(b) || !strings.HasPrefix(a[0], "msg_") || !strings.HasPrefix(a[1], "sig_") || !strings.HasPrefix(a[2], "toolu_") {
		t.Fatal(a, b)
	}
}
