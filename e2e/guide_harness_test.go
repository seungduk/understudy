//go:build e2e

package e2e

// The harness guide test: agents that read only SCRIPTS.md write five scripts
// for the tasks in e2e/guide-tasks-harness.md (for Codex, and the same files
// for pi and Gemini CLI), into UNDERSTUDY_GUIDE_DIR; this test runs each one
// with the real harnesses named in UNDERSTUDY_GUIDE_HARNESSES (default codex).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seungduk/understudy/harness"
	us "github.com/seungduk/understudy/understudytest"
)

func guideHarnesses(t *testing.T) []subject {
	names := os.Getenv("UNDERSTUDY_GUIDE_HARNESSES")
	if names == "" {
		names = "codex"
	}
	var out []subject
	for _, s := range subjects {
		if strings.Contains(","+names+",", ","+s.name+",") {
			out = append(out, s)
		}
	}
	return out
}

func guideRun(t *testing.T, s subject, file, session, prompt string, dir string) (us.HarnessOutcome, *us.Fake) {
	t.Helper()
	f := us.Start(t, us.ScriptFile(guideFile(t, file)))
	return s.run(t, f, dir, session, prompt), f
}

func TestGuideHarness(t *testing.T) {
	for _, s := range guideHarnesses(t) {
		t.Run(s.name+"/1-text", func(t *testing.T) {
			o, _ := guideRun(t, s, "1-text.yaml", "", "make a plan", t.TempDir())
			if o.Exit != 0 || !strings.Contains(o.Stdout, "PLAN: read, fix, test") {
				t.Fatalf("exit %d\n%s", o.Exit, tail(o.Stdout, 1500))
			}
		})
		t.Run(s.name+"/2-edit", func(t *testing.T) {
			dir := us.TempDir(t, map[string]string{"notes.txt": "color: red\n"})
			o, _ := guideRun(t, s, "2-edit.yaml", "t1/writer", "change the color", dir)
			b, _ := os.ReadFile(filepath.Join(dir, "notes.txt"))
			if o.Exit != 0 || !strings.Contains(o.Stdout, "DONE") || string(b) != "color: blue\n" {
				t.Fatalf("exit %d file %q\n%s\n%s", o.Exit, b, tail(o.Stdout, 1500), tail(o.Stderr, 800))
			}
		})
		t.Run(s.name+"/3-branch", func(t *testing.T) {
			dir := t.TempDir()
			o, f := guideRun(t, s, "3-branch.yaml", "t2/writer", "get ready", dir)
			if _, err := os.Stat(filepath.Join(dir, "ready")); err != nil || o.Exit != 0 || !strings.Contains(o.Stdout, "PASS") {
				t.Fatalf("exit %d ready: %v\n%s\n%s", o.Exit, err, tail(o.Stdout, 1500), tail(o.Stderr, 800))
			}
			if n := len(scripted(t, f, "t2/writer")); n != 4 {
				t.Fatalf("%d requests, want 4 (test, write, test, final)", n)
			}
			f.Reset()
			o = s.run(t, f, dir, "t2/writer", "get ready")
			if !strings.Contains(o.Stdout, "PASS") || len(scripted(t, f, "t2/writer")) != 2 {
				t.Fatalf("ready from the start: %d requests\n%s", len(scripted(t, f, "t2/writer")), tail(o.Stdout, 1500))
			}
		})
		t.Run(s.name+"/4-limit", func(t *testing.T) {
			if s.limit == "" {
				t.Skip(s.name + " waits out a limit instead of reporting it")
			}
			o, _ := guideRun(t, s, "4-limit.yaml", "t3/writer", "work", t.TempDir())
			if !strings.Contains(o.Stdout+o.Stderr, s.limit) {
				t.Fatalf("exit %d, want %q\n%s", o.Exit, s.limit, tail(o.Stdout, 1500))
			}
		})
		t.Run(s.name+"/5-stall", func(t *testing.T) {
			f := us.Start(t, us.ScriptFile(guideFile(t, "5-stall.yaml")))
			done := make(chan us.HarnessOutcome, 1)
			go func() {
				done <- us.Harness(t, f, s.id, us.HarnessRun{Dir: t.TempDir(), Session: "t4/reviewer", Prompt: "review", Protocol: s.proto})
			}()
			f.WaitGate("review-gate", 1, time.Minute)
			select {
			case o := <-done:
				t.Fatalf("finished before the release\n%s", tail(o.Stdout, 1500))
			case <-time.After(2 * time.Second):
			}
			f.Release("review-gate")
			if o := <-done; o.Exit != 0 || !strings.Contains(o.Stdout, "CAUSE: WORK") {
				t.Fatalf("exit %d\n%s", o.Exit, tail(o.Stdout, 1500))
			}
		})
	}
	_ = harness.Codex
}
