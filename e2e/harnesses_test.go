//go:build e2e

package e2e

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/seungduk/understudy/harness"
	"github.com/seungduk/understudy/protocol"
	"github.com/seungduk/understudy/record"
	"github.com/seungduk/understudy/script"
	"github.com/seungduk/understudy/server"
	us "github.com/seungduk/understudy/understudytest"
)

// A harness run of the suite: which harness over which protocol, and what it
// was measured to do with a usage limit.
type subject struct {
	name     string
	id       harness.ID
	proto    protocol.ID
	model    string // "" is the profile's model for proto
	limit    string // what the harness prints for a seven_day limit; "" when it waits instead
	limitMax int    // the most requests it sends for one limit (it retries up to this)
}

// subjects are the non-Claude harnesses; Claude Code has its own suite (e2e_test.go).
var subjects = []subject{
	{"codex", harness.Codex, protocol.OpenAIResponses, "", "You’ve hit your usage limit", 1},
	{"opencode", harness.OpenCode, protocol.OpenAIChat, "", "", 2},
	{"opencode-anthropic", harness.OpenCode, protocol.Anthropic, "", "", 2},
	{"opencode-deepseek", harness.OpenCode, protocol.OpenAIChat, "deepseek/deepseek-chat", "", 2},
	{"pi", harness.Pi, protocol.OpenAIChat, "", "429 The usage limit has been reached", 4},
	{"pi-responses", harness.Pi, protocol.OpenAIResponses, "", "429 The usage limit has been reached", 4},
	{"pi-anthropic", harness.Pi, protocol.Anthropic, "", "You have reached your weekly usage limit", 4},
	{"pi-gemini", harness.Pi, protocol.Gemini, "", "You exceeded your current quota", 4},
	{"gemini-cli", harness.GeminiCLI, protocol.Gemini, "", "You exceeded your current quota", 1},
	{"antigravity", harness.Antigravity, protocol.Gemini, "", "RESOURCE_EXHAUSTED", 1},
}

func only(t *testing.T) []subject {
	want := os.Getenv("UNDERSTUDY_HARNESS")
	var out []subject
	for _, s := range subjects {
		if want == "" || strings.Contains(","+want+",", ","+s.name+",") || strings.Contains(","+want+",", ","+string(s.id)+",") {
			out = append(out, s)
		}
	}
	return out
}

func (s subject) run(t *testing.T, f *us.Fake, dir, session, prompt string) us.HarnessOutcome {
	t.Helper()
	o := us.Harness(t, f, s.id, us.HarnessRun{Dir: dir, Session: session, Prompt: prompt, Protocol: s.proto, Model: s.model, Timeout: 3 * time.Minute})
	if o.TimedOut {
		t.Fatalf("%s timed out\nstdout: %s\nstderr: %s", s.name, tail(o.Stdout, 2000), tail(o.Stderr, 2000))
	}
	return o
}

func scripted(t *testing.T, f *us.Fake, session string) []server.Entry {
	var out []server.Entry
	for _, e := range f.Messages() {
		if e.Session == session && e.Aux == "" {
			out = append(out, e)
		}
	}
	return out
}

func tail(s string, n int) string {
	if len(s) > n {
		return "..." + s[len(s)-n:]
	}
	return s
}

func expectLog(t *testing.T, s subject, es []server.Entry, want string) {
	t.Helper()
	var got []string
	for _, e := range es {
		got = append(got, e.Step+"/"+string(e.Outcome))
		if e.Protocol != s.proto.Name() || e.Harness != string(s.id) || e.Divergence != "" {
			t.Errorf("request %d: protocol %s harness %s divergence %q", e.Seq, e.Protocol, e.Harness, e.Divergence)
		}
	}
	if strings.Join(got, " ") != want {
		t.Fatalf("log %s, want %s", strings.Join(got, " "), want)
	}
}

// The neutral tool loop: read, edit, check, judge. Edits are whole lines so
// Codex's apply_patch can make them too.
const harnessLoop = `schema: understudy/v2
sessions:
  t1/writer:
    steps:
      - name: read
        reply: {tool_use: {thinking: "Read before editing.", text: "Reading greeting.txt.", calls: [{read: {path: "{{cwd}}/greeting.txt"}}]}}
      - name: edit
        expect: {action: read, result: ok, contains: hello old world}
        reply: {tool_use: {calls: [{edit: {path: "{{cwd}}/greeting.txt", old: "hello old world", new: "hello new world"}}]}}
      - name: check
        expect: {action: edit, result: ok}
        reply: {tool_use: {calls: [{shell: {command: "grep -q 'hello new world' greeting.txt && echo CHECK-PASS"}}]}}
      - name: judge
        cases:
          - when: {action: shell, contains: CHECK-PASS}
            reply: {message: {text: "DONE: greeting updated"}}
        otherwise:
          reply: {message: {text: "blocked: the check failed"}}
`

func TestHarnessText(t *testing.T) {
	for _, s := range only(t) {
		t.Run(s.name, func(t *testing.T) {
			f := us.Start(t, us.YAML(`schema: understudy/v2
sessions:
  t1/planner:
    steps:
      - name: answer
        reply: {message: {thinking: "Short answer.", text: "PLAN-ONE-STEP"}}
`))
			o := s.run(t, f, t.TempDir(), "t1/planner", "plan it")
			if o.Exit != 0 || !strings.Contains(o.Stdout, "PLAN-ONE-STEP") {
				t.Fatalf("exit %d\nstdout: %s\nstderr: %s", o.Exit, tail(o.Stdout, 2000), tail(o.Stderr, 2000))
			}
			expectLog(t, s, scripted(t, f, "t1/planner"), "answer/served")
			t.Logf("%s %s over %s: text ok", s.id, o.Version, s.proto.Name())
		})
	}
}

func TestHarnessToolLoopEditsRealFile(t *testing.T) {
	for _, s := range only(t) {
		t.Run(s.name, func(t *testing.T) {
			f := us.Start(t, us.YAML(harnessLoop))
			dir := us.TempDir(t, map[string]string{"greeting.txt": "hello old world\n"})
			o := s.run(t, f, dir, "t1/writer", "update the greeting")
			b, _ := os.ReadFile(filepath.Join(dir, "greeting.txt"))
			if o.Exit != 0 || !strings.Contains(o.Stdout, "DONE: greeting updated") || string(b) != "hello new world\n" {
				t.Fatalf("file %q exit %d\nstdout: %s\nstderr: %s", b, o.Exit, tail(o.Stdout, 2000), tail(o.Stderr, 2000))
			}
			expectLog(t, s, scripted(t, f, "t1/writer"), "read/served edit/served check/served judge/served")
			t.Logf("%s %s over %s: tool loop ok, file edited", s.id, o.Version, s.proto.Name())
		})
	}
}

func TestHarnessBranchOnFailingTest(t *testing.T) {
	for _, s := range only(t) {
		t.Run(s.name, func(t *testing.T) {
			f := us.Start(t, us.YAML(`schema: understudy/v2
sessions:
  t1/writer:
    steps:
      - name: test
        reply: {tool_use: {calls: [{shell: {command: "test -f fixed && echo TESTS-PASS || { echo TESTS-FAIL; exit 1; }"}}]}}
      - name: judge
        cases:
          - when: {action: shell, contains: TESTS-PASS}
            reply: {message: {text: "DONE: tests pass"}}
          - when: {action: shell, contains: TESTS-FAIL}
            next: test
            reply: {tool_use: {calls: [{write: {path: "{{cwd}}/fixed", content: "yes\n"}}]}}
`))
			dir := t.TempDir()
			o := s.run(t, f, dir, "t1/writer", "make the tests pass")
			if o.Exit != 0 || !strings.Contains(o.Stdout, "DONE: tests pass") {
				t.Fatalf("exit %d\nstdout: %s\nstderr: %s", o.Exit, tail(o.Stdout, 2000), tail(o.Stderr, 2000))
			}
			if _, err := os.Stat(filepath.Join(dir, "fixed")); err != nil {
				t.Fatal(err)
			}
			es := scripted(t, f, "t1/writer")
			expectLog(t, s, es, "test/served judge/served test/served judge/served")
			if es[1].Branch != "case 1" || es[3].Branch != "case 0" {
				t.Fatalf("branches %s %s", es[1].Branch, es[3].Branch)
			}
			// Where the harness shows exit codes, the failing run is an error with code 1.
			pr, _ := harness.Lookup(string(s.id))
			if r := pr.Read(es[1].History[1][0].Tool, es[1].History[1][0].Text, 0); pr.ID != harness.OpenCode && (!r.HasExit || r.Exit != 1) {
				t.Errorf("exit code not read: %+v from %q", r, es[1].History[1][0].Text)
			}
			t.Logf("%s %s over %s: branch on a failing test ok", s.id, o.Version, s.proto.Name())
		})
	}
}

func TestHarnessRetryAfterServerError(t *testing.T) {
	for _, s := range only(t) {
		t.Run(s.name, func(t *testing.T) {
			f := us.Start(t, us.YAML(`schema: understudy/v2
sessions:
  t1/writer:
    steps:
      - name: first
        attempts:
          - http_error: {error: overloaded}
          - message: {text: "RECOVERED-AFTER-RETRY"}
`))
			o := s.run(t, f, t.TempDir(), "t1/writer", "say hi")
			es := scripted(t, f, "t1/writer")
			if !strings.Contains(o.Stdout, "RECOVERED-AFTER-RETRY") || len(es) != 2 || es[0].Status < 500 || es[1].Attempt != 2 {
				t.Fatalf("exit %d, %d requests\nstdout: %s\nstderr: %s", o.Exit, len(es), tail(o.Stdout, 2000), tail(o.Stderr, 2000))
			}
			t.Logf("%s %s over %s: retried after %d, then answered", s.id, o.Version, s.proto.Name(), es[0].Status)
		})
	}
}

func TestHarnessLimitMessage(t *testing.T) {
	for _, s := range only(t) {
		t.Run(s.name, func(t *testing.T) {
			if s.limit == "" {
				// The harness waits out retry-after and asks again (measured: it waited
				// past 150 s for a two-hour reset), so a short reset is used.
				f := us.Start(t, us.YAML(`schema: understudy/v2
sessions:
  t3/writer:
    steps:
      - name: first
        attempts:
          - limit: {window: seven_day, resets_in_seconds: 3}
          - message: {text: "AFTER-THE-RESET"}
`))
				start := time.Now()
				o := s.run(t, f, t.TempDir(), "t3/writer", "say hi")
				es := scripted(t, f, "t3/writer")
				if !strings.Contains(o.Stdout, "AFTER-THE-RESET") || len(es) != 2 || time.Since(start) < 3*time.Second {
					t.Fatalf("exit %d, %d requests in %s\nstdout: %s\nstderr: %s", o.Exit, len(es), time.Since(start), tail(o.Stdout, 2000), tail(o.Stderr, 2000))
				}
				t.Logf("%s %s over %s: waited out the limit's retry-after, then asked again", s.id, o.Version, s.proto.Name())
				return
			}
			f := us.Start(t, us.YAML(`schema: understudy/v2
sessions:
  t3/writer:
    steps:
      - name: first
        reply: {limit: {window: seven_day, resets_in_seconds: 7200}}
`))
			o := s.run(t, f, t.TempDir(), "t3/writer", "say hi")
			es := scripted(t, f, "t3/writer")
			if out := o.Stdout + o.Stderr; !strings.Contains(out, s.limit) || len(es) == 0 || len(es) > s.limitMax {
				t.Fatalf("exit %d, %d requests, want %q\nstdout: %s\nstderr: %s", o.Exit, len(es), s.limit, tail(o.Stdout, 2000), tail(o.Stderr, 2000))
			}
			t.Logf("%s %s over %s: limit shown as %q after %d request(s), exit %d", s.id, o.Version, s.proto.Name(), s.limit, len(es), o.Exit)
		})
	}
}

func TestHarnessStallRelease(t *testing.T) {
	for _, s := range only(t) {
		t.Run(s.name, func(t *testing.T) {
			f := us.Start(t, us.YAML(`schema: understudy/v2
sessions:
  t4/reviewer:
    steps:
      - name: verdict
        reply: {message: {text: "VERDICT-WORK"}, stall: {gate: reviewer-gate}}
`))
			var o us.HarnessOutcome
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				o = us.Harness(t, f, s.id, us.HarnessRun{Dir: t.TempDir(), Session: "t4/reviewer", Prompt: "review it", Protocol: s.proto, Model: s.model, Timeout: 3 * time.Minute})
			}()
			f.WaitGate("reviewer-gate", 1, 2*time.Minute)
			time.Sleep(2 * time.Second) // held: the harness waits, nothing answers
			if n := f.Release("reviewer-gate"); n != 1 {
				t.Fatalf("released %d", n)
			}
			wg.Wait()
			if o.Exit != 0 || !strings.Contains(o.Stdout, "VERDICT-WORK") {
				t.Fatalf("exit %d\nstdout: %s\nstderr: %s", o.Exit, tail(o.Stdout, 2000), tail(o.Stderr, 2000))
			}
			t.Logf("%s %s over %s: held at the gate, then answered", s.id, o.Version, s.proto.Name())
		})
	}
}

// Record a run through the recording proxy (the upstream is another scripted
// server, never a real API), then replay the recorded script: the harness edits
// the file the same way, and a replay whose file differs diverges.
func TestHarnessRecordReplay(t *testing.T) {
	for _, s := range only(t) {
		t.Run(s.name, func(t *testing.T) {
			up := us.Start(t, us.YAML(harnessLoop))
			p, err := record.NewProxy(up.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			px := httptest.NewServer(p)
			t.Cleanup(px.Close)
			viaProxy := &us.Fake{Server: up.Server, URL: px.URL}
			dir := us.TempDir(t, map[string]string{"greeting.txt": "hello old world\n"})
			o := s.run(t, viaProxy, dir, "t1/writer", "update the greeting")
			if o.Exit != 0 || !strings.Contains(o.Stdout, "DONE: greeting updated") {
				t.Fatalf("recording run: exit %d\nstdout: %s\nstderr: %s", o.Exit, tail(o.Stdout, 2000), tail(o.Stderr, 2000))
			}
			file := p.File(record.Meta{Run: "r1", Task: "t1", Seat: "writer"})
			sc, err := script.Compile(file)
			if err != nil {
				b, _ := script.Marshal(file)
				t.Fatalf("%v\n%s", err, b)
			}
			if ses := file.Sessions["t1/writer"]; len(ses.Steps) != 4 || (s.id != harness.ClaudeCode && ses.Harness != string(s.id)) {
				t.Fatalf("%+v", file.Sessions)
			}
			replay := us.Start(t, us.Script(sc))
			dir2 := us.TempDir(t, map[string]string{"greeting.txt": "hello old world\n"})
			o = s.run(t, replay, dir2, "t1/writer", "update the greeting")
			b, _ := os.ReadFile(filepath.Join(dir2, "greeting.txt"))
			if o.Exit != 0 || !strings.Contains(o.Stdout, "DONE: greeting updated") || string(b) != "hello new world\n" {
				t.Fatalf("replay: file %q exit %d\nstdout: %s\nstderr: %s", b, o.Exit, tail(o.Stdout, 2000), tail(o.Stderr, 2000))
			}
			for _, e := range scripted(t, replay, "t1/writer") {
				if e.Outcome != server.Served || e.Divergence != "" {
					t.Fatalf("replay request %d: %s %q", e.Seq, e.Outcome, e.Divergence)
				}
			}
			t.Logf("%s %s over %s: recorded %d steps through the proxy and replayed them", s.id, o.Version, s.proto.Name(), len(file.Sessions["t1/writer"].Steps))
		})
	}
}
