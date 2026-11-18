package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/seungduk/understudy/internal/reqlog"
	"github.com/seungduk/understudy/script"
)

func TestLogKeepsTheConversation(t *testing.T) {
	h := start(t, loop, Options{})
	h.msg(body(true, allTools, ""), nil)
	h.msg(body(true, allTools, "", turn{"Read", false, "1\told"}), nil)
	h.msg(body(true, allTools, "", turn{"Read", false, "1\told"}, turn{"Edit", false, "ok"}, turn{"Write", true, "Exit code 2\nno"}), nil)
	es := h.s.Requests()
	if es[0].Prompt != "do the task" || es[1].Prompt != "" {
		t.Fatalf("prompts %q %q", es[0].Prompt, es[1].Prompt)
	}
	a := es[0].Answer
	if a == nil || a.Kind != KindToolUse || a.Thinking != "first read" || a.Text != "Reading." || a.Stop != "tool_use" ||
		len(a.Calls) != 1 || a.Calls[0].Tool != "Read" || string(a.Calls[0].Input) != `{"file_path":"/work/dir/a.txt"}` {
		t.Fatalf("answer %+v", a)
	}
	if es[0].Expected != nil || es[0].Actual != nil {
		t.Fatalf("a fitting request has no expected/actual: %+v", es[0])
	}
	// Turn 3 is judge with a Write result: no case matches (both test Bash), strict divergence.
	d := es[2]
	if d.Outcome != Diverged || d.Answer != nil || len(d.Expected) != 2 || !strings.HasPrefix(d.Expected[0], `step "judge" case 0 when`) ||
		len(d.Actual) != 1 || d.Actual[0] != `result 0 (Write): error: "Exit code 2\nno"` {
		t.Fatalf("divergence %+v", d)
	}
}

func TestAnswerOfEveryKind(t *testing.T) {
	h := start(t, `schema: understudy/v1
sessions:
  m: {steps: [{name: a, reply: {message: {text: hi, thinking: hm, stop: max_tokens}, pace: {first_event_ms: 1, between_events_ms: 1}}}]}
  e: {steps: [{name: a, reply: {http_error: {error: overloaded, message: busy, retry: never}}}]}
  d: {steps: [{name: a, reply: {http_error: {error: api_error}}}]}
  s: {steps: [{name: a, reply: {stream_error: {error: overloaded, partial_text: par}}}]}
  l: {steps: [{name: a, reply: {limit: {window: five_hour, resets_in_seconds: 60}}}]}
  t: {steps: [{name: a, reply: {tool_use: {calls: [{tool: Nope, input: {}}]}}}]}
`, Options{})
	for _, k := range []string{"m", "e", "d", "s", "l", "t"} {
		h.msg(body(true, allTools, ""), map[string]string{"X-Understudy-Session": k})
	}
	es := h.s.Requests()
	want := []reqlog.Answer{
		{Kind: KindMessage, Text: "hi", Thinking: "hm", Stop: "max_tokens", Faults: []string{"pace: first event after 1 ms, 1 ms between events"}},
		{Kind: KindHTTPError, Error: "overloaded", Message: "busy", Retry: "never"},
		{Kind: KindHTTPError, Error: "api_error", Retry: "default"},
		{Kind: KindStreamError, Error: "overloaded", Text: "par"},
		{Kind: KindLimit, Window: "five_hour", ResetsInSeconds: 60},
	}
	for i, w := range want {
		g, _ := json.Marshal(es[i].Answer)
		x, _ := json.Marshal(w)
		if string(g) != string(x) {
			t.Errorf("%s: answer %s, want %s", es[i].Session, g, x)
		}
	}
	if es[5].Outcome != Diverged || es[5].Answer != nil || es[5].Expected[0] != "the client offers the tool(s) Nope" || es[5].Actual[0] != "the client offers: Bash, Edit, Read, Write" {
		t.Fatalf("not offered %+v", es[5])
	}
	if !strings.Contains(h.get(ControlPrefix+"sessions").body, `"key":"e","state":"done"`) {
		t.Fatal(h.get(ControlPrefix + "sessions").body)
	}
}

func TestHeldWhileStalledAndSessionStates(t *testing.T) {
	h := start(t, `schema: understudy/v1
sessions:
  g:
    steps:
      - {name: a, reply: {tool_use: {calls: [{tool: Read, input: {file_path: /x}}]}}}
      - {name: b, reply: {message: {text: done}, stall: {gate: review}}}
  never: {steps: [{name: a, reply: {message: {text: x}}}]}
`, Options{})
	hdr := map[string]string{"X-Understudy-Session": "g"}
	h.msg(body(true, allTools, ""), hdr)
	states := func() map[string]SessionStatus {
		m := map[string]SessionStatus{}
		for _, s := range h.s.Sessions() {
			m[s.Key] = s
		}
		return m
	}
	if s := states(); s["g"].State != StateWaiting || s["never"].State != StateIdle || !s["never"].InScript || s["never"].ScriptSteps != 1 || s["g"].Requests != 1 {
		t.Fatalf("%+v", s)
	}
	done := make(chan resp)
	go func() { done <- h.msg(body(true, allTools, "", turn{"Read", false, "x"}), hdr) }()
	for h.s.Requests()[len(h.s.Requests())-1].Held == nil {
		time.Sleep(time.Millisecond)
	}
	e := h.s.Requests()[1]
	if e.Held.Gate != "review" || e.Held.Since == "" || e.Outcome != Pending {
		t.Fatalf("%+v", e)
	}
	if s := states()["g"]; s.State != StateStalled || s.Gate != "review" || s.Step != "b" || s.Turn != 1 {
		t.Fatalf("%+v", s)
	}
	h.s.Release("review")
	<-done
	if e := h.s.Requests()[1]; e.Held != nil || e.Outcome != Served {
		t.Fatalf("%+v", e)
	}
	if s := states()["g"]; s.State != StateDone || s.Gate != "" {
		t.Fatalf("%+v", s)
	}
	h.msg(body(true, allTools, "", turn{"Read", false, "x"}, turn{"Read", false, "y"}), hdr)
	if s := states()["g"]; s.State != StateDiverged || s.Divergences != 1 || s.Requests != 3 || !strings.Contains(s.Divergence, "has ended") {
		t.Fatalf("%+v", s)
	}
}

func TestStateOfEveryOutcome(t *testing.T) {
	ans := func(kind, retry string) *reqlog.Answer { return &reqlog.Answer{Kind: kind, Retry: retry} }
	cases := []struct {
		e    Entry
		want SessionState
	}{
		{Entry{Outcome: Pending}, StateStreaming},
		{Entry{Outcome: Pending, Held: &reqlog.Hold{Seconds: 3}}, StateStalled},
		{Entry{Outcome: Diverged}, StateDiverged},
		{Entry{Outcome: NoScript}, StateDiverged},
		{Entry{Outcome: BadRequest}, StateDiverged},
		{Entry{Outcome: Served, Answer: ans(KindMessage, "")}, StateDone},
		{Entry{Outcome: Served, Answer: ans(KindLimit, "")}, StateDone},
		{Entry{Outcome: Served, Answer: ans(KindHTTPError, "never")}, StateDone},
		{Entry{Outcome: Served, Answer: ans(KindHTTPError, "default")}, StateWaiting},
		{Entry{Outcome: Served, Answer: ans(KindHTTPError, "always")}, StateWaiting},
		{Entry{Outcome: Served, Answer: ans(KindToolUse, "")}, StateWaiting},
		{Entry{Outcome: Served, Answer: ans(KindStreamError, "")}, StateWaiting},
		{Entry{Outcome: Served}, StateWaiting},
		{Entry{Outcome: CutClose}, StateWaiting},
		{Entry{Outcome: CutReset}, StateWaiting},
		{Entry{Outcome: ClientGone}, StateWaiting},
	}
	for _, c := range cases {
		if got := stateOf(c.e); got != c.want {
			t.Errorf("%+v: %s, want %s", c.e, got, c.want)
		}
	}
	// Other endpoints and requests with no session are not sessions.
	if ss := SessionsOf([]Entry{{Outcome: Other, Session: "x"}, {Outcome: BadRequest}}, nil); len(ss) != 0 {
		t.Fatalf("%+v", ss)
	}
}

func TestEventsStream(t *testing.T) {
	h := start(t, loop, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", h.srv.URL+ControlPrefix+"events", nil)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatal(ct)
	}
	sc := bufio.NewScanner(r.Body)
	next := func() uint64 {
		for sc.Scan() {
			if l := sc.Text(); strings.HasPrefix(l, "data: ") {
				var d struct{ Version uint64 }
				if err := json.Unmarshal([]byte(l[6:]), &d); err != nil {
					t.Fatal(l)
				}
				return d.Version
			}
		}
		t.Fatal("stream ended", sc.Err())
		return 0
	}
	v0 := next()
	h.msg(body(true, allTools, ""), nil) // a burst of log changes: one event
	v1 := next()
	if v1 <= v0 || v1 != h.s.Version() {
		t.Fatalf("v0 %d v1 %d now %d", v0, v1, h.s.Version())
	}
	h.s.Release("g")
	if v2 := next(); v2 <= v1 {
		t.Fatal(v2)
	}
	h.s.Load(mustScript(t, loop))
	h.s.Reset()
	if v3 := next(); v3 != h.s.Version() {
		t.Fatal(v3)
	}
}

func TestValidateAndScriptInfo(t *testing.T) {
	h := start(t, "", Options{})
	if b := h.get(ControlPrefix + "script/info").body; strings.TrimSpace(b) != `{"loaded":false}` {
		t.Fatal(b)
	}
	r := h.post(ControlPrefix+"validate", strings.Replace(loop, "reply:", "replly:", 1), nil)
	var v Validation
	if r.status != 200 || json.Unmarshal([]byte(r.body), &v) != nil || v.OK || v.Problems[1].Rule != script.RuleUnknownField || v.Problems[1].Line != 6 || len(v.Sessions) != 0 {
		t.Fatal(r.status, r.body)
	}
	if r = h.post(ControlPrefix+"validate", loop, nil); !strings.Contains(r.body, `"ok":true,"sessions":["default"],"problems":[]`) {
		t.Fatal(r.body)
	}
	if v := Validate([]byte("")); v.OK || v.Problems[0].Rule != script.RuleRequired {
		t.Fatalf("%+v", v)
	}
	if h.s.Script() != nil {
		t.Fatal("validate loaded the script")
	}
	src := "# my comment\n" + loop
	req, _ := http.NewRequest("PUT", h.srv.URL+ControlPrefix+"script", strings.NewReader(src))
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 200 {
		t.Fatal(err, resp)
	}
	if b := h.get(ControlPrefix + "script").body; b != src {
		t.Fatalf("GET script gives the text as loaded, comments too: %q", b)
	}
	info := h.get(ControlPrefix + "script/info").body
	if !strings.Contains(info, `"divergence":"strict","loaded":true`) || !strings.Contains(info, `{"name":"judge","line":12}`) {
		t.Fatal(info)
	}
	h.s.Load(script.MustCompile(h.s.Script().File()))
	if b := h.get(ControlPrefix + "script").body; !strings.HasPrefix(b, "# understudy script") {
		t.Fatalf("a script compiled in Go is written out: %q", b)
	}
}

func TestActualOfAndCutText(t *testing.T) {
	long := strings.Repeat("é", 200) // 400 bytes
	got := actualOf([]script.Result{{Tool: "Bash", IsError: true, Text: "Exit code 1\nFAIL"}, {Tool: "Bash", Text: " ok "}, {Text: long}}, 2)
	want := []string{
		`result 0 (Bash): error, exit code 1: "Exit code 1\nFAIL"`,
		`result 1 (Bash): ok, exit code 0: "ok"`,
		`result 2 (unknown tool): ok: "` + strings.Repeat("é", 150) + `..."`,
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("%q", got)
	}
	if g := actualOf(nil, 0); g[0] != "the first request of the session: no tool results" {
		t.Fatal(g)
	}
	if g := actualOf(nil, 3); g[0] != "turn 3 carries no tool results" {
		t.Fatal(g)
	}
	if cutText("abc", 5) != "abc" || cutText("aé", 2) != "a" || cutText("aé", 3) != "aé" {
		t.Fatal("cutText")
	}
}

func TestFaultWords(t *testing.T) {
	r := script.Reply{Message: &script.Message{Text: "x"}, Stall: &script.Stall{Gate: "g", At: script.MidStream}, Cut: &script.Cut{AfterEvents: 3, How: script.Reset}}
	_, a, _ := render(r, renderCtx{stream: true})
	if strings.Join(a.Faults, "|") != "stall mid-stream until gate g is released|cut (reset) after 3 event(s)" {
		t.Fatalf("%q", a.Faults)
	}
	r = script.Reply{Message: &script.Message{Text: "x"}, Stall: &script.Stall{Seconds: 5}}
	if _, a, _ = render(r, renderCtx{stream: true}); strings.Join(a.Faults, "|") != "stall before the response for 5 s" {
		t.Fatalf("%q", a.Faults)
	}
}
