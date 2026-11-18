package understudytest

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seungduk/understudy/script"
)

const held = `schema: understudy/v1
sessions:
  default: {steps: [{name: a, reply: {message: {text: hi}, stall: {gate: g}}}]}
`

func TestStartWaitRelease(t *testing.T) {
	f := Start(t, YAML(held), VirtualClock(time.Unix(100, 0)))
	if !f.Now().Equal(time.Unix(100, 0)) {
		t.Fatal(f.Now())
	}
	done := make(chan int)
	go func() {
		r, err := http.Post(f.URL+"/v1/messages", "application/json", strings.NewReader(`{"model":"m","stream":true,"messages":[{"role":"user","content":"x"}]}`))
		if err != nil {
			done <- 0
			return
		}
		_, _ = io.Copy(io.Discard, r.Body) // closing early would log the request as client_gone
		r.Body.Close()
		done <- r.StatusCode
	}()
	f.WaitGate("g", 1, 5*time.Second)
	f.WaitRequests(1, time.Second)
	f.Release("g")
	if s := <-done; s != 200 {
		t.Fatal(s)
	}
	http.Get(f.URL + "/v1/models")
	if len(f.Messages()) != 1 || len(f.Requests()) != 2 {
		t.Fatal(f.Requests())
	}
}

func TestOptions(t *testing.T) {
	dir := TempDir(t, map[string]string{"s.yaml": held, "a.txt": "x"})
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "x" {
		t.Fatal("TempDir")
	}
	if f := Start(t, ScriptFile(filepath.Join(dir, "s.yaml"))); f.Script() == nil {
		t.Fatal("ScriptFile")
	}
	s := script.MustCompile(script.File{Schema: script.SchemaV1, Sessions: map[string]script.Session{"default": {Steps: []script.Step{{Name: "a", Reply: &script.Reply{Message: &script.Message{Text: "x"}}}}}}})
	if f := Start(t, Script(s)); f.Script() != s {
		t.Fatal("Script")
	}
}

type fakeTB struct {
	testing.TB
	failed string
}

func (f *fakeTB) Helper()                        {}
func (f *fakeTB) Fatalf(format string, a ...any) { f.failed = format; panic(f) }

func TestFailures(t *testing.T) {
	for _, op := range []Option{ScriptFile("/nonexistent.yaml"), YAML("schema: x")} {
		ft := &fakeTB{TB: t}
		func() {
			defer func() { recover() }()
			Start(ft, op)
		}()
		if ft.failed == "" {
			t.Error("no failure")
		}
	}
	f := Start(t)
	ft := &fakeTB{TB: t}
	f.t = ft
	func() {
		defer func() { recover() }()
		f.WaitGate("never", 1, 20*time.Millisecond)
	}()
	if !strings.Contains(ft.failed, "timed out") {
		t.Error(ft.failed)
	}
}

func TestClaudeWithStandIn(t *testing.T) {
	bin, _ := filepath.Abs("testdata/fake-claude.sh")
	t.Setenv("UNDERSTUDY_CLAUDE", bin)
	t.Setenv("ANTHROPIC_API_KEY", "must-be-cleared")
	f := Start(t, YAML(held))
	o := Claude(t, f, Run{Dir: t.TempDir(), Session: "s1", Prompt: "hello", Args: []string{"--model", "x"}})
	if o.Exit != 3 || !o.IsError || o.Version != "0.0.0-fake" || o.Count("system", "") != 2 || o.Count("system", "api_retry") != 1 || len(o.Events) != 3 {
		t.Fatalf("%+v", o)
	}
	if !strings.Contains(o.Result, "--dangerously-skip-permissions --model x hello") {
		t.Fatal(o.Result)
	}
	o = Claude(t, f, Run{Dir: t.TempDir(), Session: "s1", Prompt: "p", Perms: AskPermissions})
	if strings.Contains(o.Result, "dangerously") {
		t.Fatal(o.Result)
	}
	o = Claude(t, f, Run{Dir: t.TempDir(), Session: "wrong", Prompt: "p"})
	if o.Exit != 9 || !strings.Contains(o.Stderr, "bad header") {
		t.Fatalf("%+v", o)
	}
	t.Setenv("UNDERSTUDY_CLAUDE", "/nonexistent/claude")
	ft := &fakeTB{TB: t}
	func() {
		defer func() { recover() }()
		Claude(ft, f, Run{Dir: t.TempDir()})
	}()
	if !strings.Contains(ft.failed, "cannot run") {
		t.Fatal(ft.failed)
	}
	func() {
		defer func() { recover() }()
		Claude(ft, f, Run{})
	}()
}

func TestClaudeSkipsWithoutCLI(t *testing.T) {
	t.Setenv("UNDERSTUDY_CLAUDE", "")
	t.Setenv("PATH", t.TempDir())
	f := Start(t)
	ran := false
	t.Run("inner", func(t *testing.T) {
		defer func() { ran = t.Skipped() }()
		Claude(t, f, Run{Dir: t.TempDir()})
	})
	if !ran {
		t.Fatal("not skipped")
	}
}
