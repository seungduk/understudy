//go:build e2e

// Package e2e drives the real Claude Code CLI (claude -p --output-format
// stream-json) against an in-process understudy server. Run it with
//
//	go test -tags e2e -v ./e2e
//
// Tests skip when the claude CLI is not installed. Only dummy credentials are used.
package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seungduk/understudy/clienv"
	"github.com/seungduk/understudy/record"
	"github.com/seungduk/understudy/script"
	"github.com/seungduk/understudy/server"
	us "github.com/seungduk/understudy/understudytest"
)

func TestMain(m *testing.M) {
	if bin, err := us.ClaudePath(); err == nil {
		out, _ := exec.Command(bin, "--version").Output()
		os.Stdout.WriteString("e2e: claude CLI " + strings.TrimSpace(string(out)) + " at " + bin + "\n")
	}
	os.Exit(m.Run())
}

func messages(f *us.Fake, session string) []server.Entry {
	var out []server.Entry
	for _, e := range f.Messages() {
		if session == "" || e.Session == session {
			out = append(out, e)
		}
	}
	return out
}

func steps(es []server.Entry) string {
	var s []string
	for _, e := range es {
		s = append(s, e.Step+"/"+string(e.Outcome))
	}
	return strings.Join(s, " ")
}

func TestText(t *testing.T) {
	f := us.Start(t, us.YAML(`schema: understudy/v1
sessions:
  t1/planner:
    steps:
      - name: answer
        reply: {message: {thinking: "Short answer.", text: "PLAN: one step."}}
`))
	o := us.Claude(t, f, us.Run{Dir: t.TempDir(), Session: "t1/planner", Prompt: "plan it"})
	if o.Exit != 0 || o.IsError || o.Result != "PLAN: one step." {
		t.Fatalf("%+v", o)
	}
	es := messages(f, "t1/planner")
	if len(es) != 1 || es[0].SessionSource != "header" || !strings.Contains(es[0].Path, "beta=true") || !es[0].Stream {
		t.Fatalf("%+v", es)
	}
	t.Logf("claude %s: text ok", o.Version)
}

const toolLoop = `schema: understudy/v1
sessions:
  t1/writer:
    steps:
      - name: read
        reply:
          tool_use:
            thinking: I read the file before editing it.
            text: Reading greeting.txt.
            calls: [{tool: Read, input: {file_path: "{{cwd}}/greeting.txt"}}]
      - name: edit
        expect: {tool: Read, result: ok, contains: hello old world}
        reply:
          tool_use:
            calls: [{tool: Edit, input: {file_path: "{{cwd}}/greeting.txt", old_string: old, new_string: new}}]
      - name: test
        expect: {tool: Edit, result: ok}
        reply:
          tool_use:
            calls: [{tool: Bash, input: {command: "grep -q 'hello new world' greeting.txt", description: Check the edit}}]
      - name: judge
        cases:
          - when: {tool: Bash, exit_code: 0}
            reply: {message: {text: "DONE: greeting updated"}}
          - when: {tool: Bash, result: error}
            reply: {message: {text: "blocked: the check failed"}}
`

func TestToolLoopEditsRealFile(t *testing.T) {
	f := us.Start(t, us.YAML(toolLoop))
	dir := us.TempDir(t, map[string]string{"greeting.txt": "hello old world\n"})
	o := us.Claude(t, f, us.Run{Dir: dir, Session: "t1/writer", Prompt: "update the greeting"})
	b, _ := os.ReadFile(filepath.Join(dir, "greeting.txt"))
	if o.Exit != 0 || o.Result != "DONE: greeting updated" || string(b) != "hello new world\n" {
		t.Fatalf("file %q, outcome %+v\nlog: %s", b, o, steps(f.Messages()))
	}
	es := messages(f, "t1/writer")
	if steps(es) != "read/served edit/served test/served judge/served" || es[3].Branch != "case 0" {
		t.Fatalf("%s %+v", steps(es), es[3])
	}
	t.Logf("claude %s: tool loop ok, file edited", o.Version)
}

func TestBranchOnFailingTests(t *testing.T) {
	f := us.Start(t, us.YAML(`schema: understudy/v1
sessions:
  t1/writer:
    steps:
      - name: test
        reply: {tool_use: {calls: [{tool: Bash, input: {command: "test -f fixed || { echo FAIL; exit 1; }", description: Run tests}}]}}
      - name: judge
        cases:
          - when: {tool: Bash, exit_code: 0}
            reply: {message: {text: "DONE"}}
          - when: {tool: Bash, exit_code: 1, contains: FAIL}
            next: test
            reply: {tool_use: {calls: [{tool: Write, input: {file_path: "{{cwd}}/fixed", content: "yes\n"}}]}}
`))
	dir := t.TempDir()
	o := us.Claude(t, f, us.Run{Dir: dir, Session: "t1/writer", Prompt: "make tests pass"})
	if o.Exit != 0 || o.Result != "DONE" {
		t.Fatalf("%+v\n%s", o, steps(f.Messages()))
	}
	if got := steps(f.Messages()); got != "test/served judge/served test/served judge/served" {
		t.Fatal(got)
	}
	if _, err := os.Stat(filepath.Join(dir, "fixed")); err != nil {
		t.Fatal(err)
	}
}

func TestRetryAfter529(t *testing.T) {
	f := us.Start(t, us.YAML(`schema: understudy/v1
sessions:
  s:
    steps:
      - name: a
        attempts:
          - http_error: {error: overloaded}
          - http_error: {error: api_error}
          - message: {text: "after retries"}
`))
	o := us.Claude(t, f, us.Run{Dir: t.TempDir(), Session: "s", Prompt: "x"})
	if o.Exit != 0 || o.Result != "after retries" || o.Count("system", "api_retry") != 2 {
		t.Fatalf("%+v", o)
	}
	if es := messages(f, "s"); len(es) != 3 || es[0].Status != 529 || es[1].Status != 500 || es[2].Attempt != 3 {
		t.Fatalf("%s", steps(es))
	}
}

func TestInStreamErrorRetried(t *testing.T) {
	f := us.Start(t, us.YAML(`schema: understudy/v1
sessions:
  s:
    steps:
      - name: a
        attempts:
          - stream_error: {error: overloaded, partial_text: "I will"}
          - message: {text: "second try"}
`))
	o := us.Claude(t, f, us.Run{Dir: t.TempDir(), Session: "s", Prompt: "x"})
	if o.Exit != 0 || o.Result != "second try" || len(messages(f, "s")) != 2 {
		t.Fatalf("%+v %s", o, steps(f.Messages()))
	}
}

func TestDisconnectAndResetRetried(t *testing.T) {
	f := us.Start(t, us.YAML(`schema: understudy/v1
sessions:
  s:
    steps:
      - name: a
        attempts:
          - {message: {text: never seen whole}, cut: {after_events: 4, how: close}}
          - {message: {text: never seen}, cut: {after_events: 0, how: reset}}
          - {message: {text: never seen}, cut: {after_events: 3, how: reset}}
          - message: {text: "after cuts"}
`))
	o := us.Claude(t, f, us.Run{Dir: t.TempDir(), Session: "s", Prompt: "x"})
	es := messages(f, "s")
	t.Logf("claude %s: cuts -> exit %d result %q, %d requests: %s", o.Version, o.Exit, o.Result, len(es), steps(es))
	if o.Exit != 0 || o.Result != "after cuts" || len(es) != 4 {
		t.Fatalf("%+v", o)
	}
	if es[0].Outcome != server.CutClose || es[1].Outcome != server.CutReset || es[2].Outcome != server.CutReset {
		t.Fatal(steps(es))
	}
}

func TestStallUntilReleased(t *testing.T) {
	f := us.Start(t, us.YAML(`schema: understudy/v1
sessions:
  t1/reviewer:
    steps:
      - name: verdict
        reply: {message: {text: "CAUSE: WORK"}, stall: {gate: review, at: mid_stream}}
`))
	done := make(chan us.Outcome, 1)
	go func() { done <- us.Claude(t, f, us.Run{Dir: t.TempDir(), Session: "t1/reviewer", Prompt: "review"}) }()
	f.WaitGate("review", 1, time.Minute)
	select {
	case o := <-done:
		t.Fatalf("finished while stalled: %+v", o)
	case <-time.After(3 * time.Second):
	}
	if e := messages(f, "t1/reviewer"); e[0].Outcome != server.Pending {
		t.Fatal(e[0].Outcome)
	}
	if n := f.Release("review"); n != 1 {
		t.Fatal(n)
	}
	o := <-done
	if o.Exit != 0 || o.Result != "CAUSE: WORK" {
		t.Fatalf("%+v", o)
	}
}

func TestWeeklyLimitMessage(t *testing.T) {
	f := us.Start(t, us.YAML(`schema: understudy/v1
sessions:
  week: {steps: [{name: a, reply: {limit: {window: seven_day, resets_in_seconds: 7200}}}]}
  five: {steps: [{name: a, reply: {limit: {window: five_hour, resets_in_seconds: 600}}}]}
`))
	for key, want := range map[string]string{"week": "You've hit your weekly limit", "five": "You've hit your 5-hour limit"} {
		o := us.Claude(t, f, us.Run{Dir: t.TempDir(), Session: key, Prompt: "x", Auth: clienv.Gateway})
		if o.Exit != 1 || !o.IsError || !strings.HasPrefix(o.Result, want+" · resets ") {
			t.Fatalf("%s: %+v", key, o)
		}
		if n := len(messages(f, key)); n != 1 {
			t.Fatalf("%s: %d requests (the limit must not be retried)", key, n)
		}
		t.Logf("claude %s: %s", o.Version, o.Result)
	}
	// With an API key the same 429 is a plain rate limit.
	o := us.Claude(t, f, us.Run{Dir: t.TempDir(), Session: "week", Prompt: "x", Auth: clienv.APIKey})
	if o.Exit != 1 || !strings.HasPrefix(o.Result, "API Error: Request rejected (429)") {
		t.Fatalf("%+v", o)
	}
	t.Logf("claude %s with an API key: %s", o.Version, o.Result)
}

func TestSessionFromSystemPromptMarker(t *testing.T) {
	f := us.Start(t, us.YAML(`schema: understudy/v1
sessions:
  t7/merger: {steps: [{name: a, reply: {message: {text: "merged"}}}]}
`))
	o := us.Claude(t, f, us.Run{Dir: t.TempDir(), Prompt: "merge", Args: []string{"--append-system-prompt", "[understudy-session: t7/merger]"}})
	if o.Result != "merged" {
		t.Fatalf("%+v", o)
	}
	if e := messages(f, "t7/merger"); len(e) != 1 || e[0].SessionSource != "system_prompt" {
		t.Fatalf("%+v", e)
	}
}

func TestStrictDivergenceStops(t *testing.T) {
	f := us.Start(t, us.YAML(`schema: understudy/v1
sessions:
  s:
    steps:
      - name: run
        reply: {tool_use: {calls: [{tool: Bash, input: {command: "exit 4", description: fail}}]}}
      - name: after
        expect: {exit_code: 0}
        reply: {message: {text: "should not be reached"}}
`))
	o := us.Claude(t, f, us.Run{Dir: t.TempDir(), Session: "s", Prompt: "x"})
	es := messages(f, "s")
	t.Logf("claude %s: strict divergence -> exit %d result %q", o.Version, o.Exit, o.Result)
	// Claude Code retries a 400 five times even with x-should-retry: false (measured on 2.1.325).
	if o.Exit != 1 || !strings.Contains(o.Result, "does not fit the script") || len(es) < 2 || es[0].Outcome != server.Served {
		t.Fatalf("%+v\n%s", o.Result, steps(es))
	}
	for _, e := range es[1:] {
		if e.Outcome != server.Diverged || e.Step != "after" {
			t.Fatalf("%s", steps(es))
		}
	}
	t.Logf("claude %s: %d requests at the diverging turn", o.Version, len(es)-1)
}

// Record a real run from its stream-json transcript, then replay the recording
// against the CLI: in a copy of the same folder it passes; in a folder where the
// edit cannot apply, strict replay stops at the first divergence.
func TestRecordAndReplay(t *testing.T) {
	f := us.Start(t, us.YAML(toolLoop))
	dir := us.TempDir(t, map[string]string{"greeting.txt": "hello old world\n"})
	bin, _ := us.ClaudePath()
	_ = bin
	o := us.Claude(t, f, us.Run{Dir: dir, Session: "t1/writer", Prompt: "update the greeting"})
	if o.Result != "DONE: greeting updated" {
		t.Fatalf("%+v", o)
	}
	var transcript bytes.Buffer
	for _, ev := range o.Events {
		b, _ := jsonMarshal(ev)
		transcript.Write(append(b, '\n'))
	}
	file, rep, err := record.FromTranscript(&transcript, record.Meta{SessionKey: "replay", Task: "t1", Seat: "writer", RecordedAt: time.Now()})
	if err != nil || rep.Steps != 4 {
		t.Fatalf("%v %+v", err, rep)
	}
	rec, err := script.Compile(file)
	if err != nil {
		t.Fatal(err)
	}
	r := us.Start(t, us.Script(rec))
	again := us.TempDir(t, map[string]string{"greeting.txt": "hello old world\n"})
	o = us.Claude(t, r, us.Run{Dir: again, Session: "replay", Prompt: "update the greeting"})
	b, _ := os.ReadFile(filepath.Join(again, "greeting.txt"))
	if o.Exit != 0 || o.Result != "DONE: greeting updated" || string(b) != "hello new world\n" {
		t.Fatalf("replay: %+v %q", o, b)
	}
	other := us.TempDir(t, map[string]string{"greeting.txt": "something else\n"})
	r.Reset()
	o = us.Claude(t, r, us.Run{Dir: other, Session: "replay", Prompt: "update the greeting"})
	es := messages(r, "replay")
	last := es[len(es)-1]
	t.Logf("claude %s: diverged replay -> %q at step %s: %s", o.Version, o.Result, last.Step, last.Divergence)
	if o.Exit != 1 || last.Outcome != server.Diverged || last.Step != "turn-3" {
		t.Fatalf("%+v\n%s", o, steps(es))
	}
}
