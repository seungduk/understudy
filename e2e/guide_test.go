//go:build e2e

package e2e

// The guide test: agents that read only SCRIPTS.md write five scripts for the
// tasks in e2e/guide-tasks.md, into the folder UNDERSTUDY_GUIDE_DIR; this test
// checks each one with the real CLI. Skipped when the folder is not given.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/seungduk/understudy/clienv"
	us "github.com/seungduk/understudy/understudytest"
)

func guideFile(t *testing.T, name string) string {
	dir := os.Getenv("UNDERSTUDY_GUIDE_DIR")
	if dir == "" {
		t.Skip("UNDERSTUDY_GUIDE_DIR is not set: no agent-written scripts to check")
	}
	return filepath.Join(dir, name)
}

func TestGuide1Text(t *testing.T) {
	f := us.Start(t, us.ScriptFile(guideFile(t, "1-text.yaml")))
	o := us.Claude(t, f, us.Run{Dir: t.TempDir(), Prompt: "make a plan"})
	if o.Exit != 0 || o.Result != "PLAN: read, fix, test" {
		t.Fatalf("exit %d result %q", o.Exit, o.Result)
	}
}

func TestGuide2Edit(t *testing.T) {
	f := us.Start(t, us.ScriptFile(guideFile(t, "2-edit.yaml")))
	dir := us.TempDir(t, map[string]string{"notes.txt": "color: red\n"})
	o := us.Claude(t, f, us.Run{Dir: dir, Session: "t1/writer", Prompt: "change the color"})
	b, _ := os.ReadFile(filepath.Join(dir, "notes.txt"))
	if o.Exit != 0 || o.Result != "DONE" || string(b) != "color: blue\n" {
		t.Fatalf("exit %d result %q file %q", o.Exit, o.Result, b)
	}
}

func TestGuide3Branch(t *testing.T) {
	f := us.Start(t, us.ScriptFile(guideFile(t, "3-branch.yaml")))
	dir := t.TempDir()
	o := us.Claude(t, f, us.Run{Dir: dir, Session: "t2/writer", Prompt: "get ready"})
	if _, err := os.Stat(filepath.Join(dir, "ready")); err != nil || o.Exit != 0 || o.Result != "PASS" {
		t.Fatalf("exit %d result %q ready: %v", o.Exit, o.Result, err)
	}
	if n := len(f.Messages()); n != 4 {
		t.Fatalf("%d requests, want 4 (test, write, test, final)", n)
	}
	// And when the file is there from the start: test, then PASS at once.
	f.Reset()
	o = us.Claude(t, f, us.Run{Dir: dir, Session: "t2/writer", Prompt: "get ready"})
	if o.Result != "PASS" || len(f.Messages()) != 2 {
		t.Fatalf("ready from the start: result %q, %d requests", o.Result, len(f.Messages()))
	}
}

func TestGuide4Limit(t *testing.T) {
	f := us.Start(t, us.ScriptFile(guideFile(t, "4-limit.yaml")))
	o := us.Claude(t, f, us.Run{Dir: t.TempDir(), Session: "t3/writer", Prompt: "work", Auth: clienv.Gateway})
	if o.Exit != 1 || !strings.HasPrefix(o.Result, "You've hit your weekly limit") {
		t.Fatalf("exit %d result %q", o.Exit, o.Result)
	}
}

func TestGuide5Stall(t *testing.T) {
	f := us.Start(t, us.ScriptFile(guideFile(t, "5-stall.yaml")))
	done := make(chan us.Outcome, 1)
	go func() { done <- us.Claude(t, f, us.Run{Dir: t.TempDir(), Session: "t4/reviewer", Prompt: "review"}) }()
	f.WaitGate("review-gate", 1, time.Minute)
	select {
	case o := <-done:
		t.Fatalf("finished before the release: %q", o.Result)
	case <-time.After(2 * time.Second):
	}
	f.Release("review-gate")
	if o := <-done; o.Exit != 0 || o.Result != "CAUSE: WORK" {
		t.Fatalf("exit %d result %q", o.Exit, o.Result)
	}
}
