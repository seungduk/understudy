package understudytest_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seungduk/understudy/harness"
	"github.com/seungduk/understudy/server"
	us "github.com/seungduk/understudy/understudytest"
)

// These examples are the bodies of test functions; t is the test's *testing.T.
// They compile with the package's tests but do not run here, because they
// need the agent CLIs installed.

// Start runs a fake model server for the length of a test. Point any client at
// fake.URL and assert on the request log.
func ExampleStart() {
	var t *testing.T // func TestTheClient(t *testing.T) { ... }

	fake := us.Start(t, us.YAML(`
schema: understudy/v1
sessions:
  default:
    steps:
      - name: answer
        reply: {message: {text: "Hello from the understudy."}}
`))
	resp, err := http.Post(fake.URL+"/v1/messages", "application/json",
		strings.NewReader(`{"model":"claude-opus-5-5","max_tokens":100,"messages":[{"role":"user","content":"say hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	log := fake.Messages()
	if len(log) != 1 || log[0].Step != "answer" || log[0].Outcome != server.Served {
		t.Fatalf("log %+v", log)
	}
}

// Claude runs the real Claude Code CLI against the fake in a fresh folder,
// with dummy credentials, and skips the test when claude is not installed.
func ExampleClaude() {
	var t *testing.T // func TestWriterEditsTheFile(t *testing.T) { ... }

	fake := us.Start(t, us.ScriptFile("testdata/edit.yaml"))
	dir := us.TempDir(t, map[string]string{"greeting.txt": "hello world\n"})

	run := us.Claude(t, fake, us.Run{Dir: dir, Session: "t1/writer", Prompt: "Change the greeting to goodbye."})

	got, _ := os.ReadFile(filepath.Join(dir, "greeting.txt"))
	if run.Exit != 0 || string(got) != "goodbye world\n" {
		t.Fatalf("exit %d, greeting.txt is %q", run.Exit, got)
	}
	for _, e := range fake.Messages() {
		t.Logf("turn %d answered by step %s", e.Turn, e.Step)
	}
}

// Harness runs any agent understudy has a profile for, such as OpenAI's Codex
// CLI, with its own home folder and the fake as its model endpoint.
func ExampleHarness() {
	var t *testing.T // func TestCodexWriter(t *testing.T) { ... }

	fake := us.Start(t, us.ScriptFile("testdata/codex-edit.yaml"))
	dir := us.TempDir(t, map[string]string{"greeting.txt": "hello world\n"})

	o := us.Harness(t, fake, harness.Codex, us.HarnessRun{Dir: dir, Session: "t1/writer", Prompt: "Change the greeting to goodbye."})
	if o.Exit != 0 {
		t.Fatalf("codex exited %d: %s", o.Exit, o.Stderr)
	}
}

// A gate holds a scripted reply until the test releases it, so a test can
// look at the agent while it waits. The virtual clock makes timed stalls
// instant.
func ExampleFake_WaitGate() {
	var t *testing.T // func TestReviewerWaits(t *testing.T) { ... }

	fake := us.Start(t, us.VirtualClock(time.Time{}), us.YAML(`
schema: understudy/v1
sessions:
  t1/reviewer:
    steps:
      - name: verdict
        reply: {stall: {gate: review}, message: {text: "APPROVED"}}
`))
	dir := us.TempDir(t, nil)

	done := make(chan us.Outcome)
	go func() { done <- us.Claude(t, fake, us.Run{Dir: dir, Session: "t1/reviewer", Prompt: "review"}) }()

	fake.WaitGate("review", 1, time.Minute) // the reviewer is now waiting on the model
	fake.Release("review")
	if run := <-done; run.Result != "APPROVED" {
		t.Fatalf("result %q", run.Result)
	}
}
