// Package examples shows understudy used from Go tests the way an agent
// factory would: a scripted writer seat edits a real file through the real
// Claude Code CLI, and the test asserts on the file, the CLI's result and the
// request log. The test skips when the claude CLI is not installed.
package examples

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/seungduk/understudy/script"
	us "github.com/seungduk/understudy/understudytest"
)

func TestWriterSeatEditsTheFile(t *testing.T) {
	fake := us.Start(t, us.ScriptFile("../2-edit-file.yaml"))
	dir := us.TempDir(t, map[string]string{"greeting.txt": "hello world\n"})

	run := us.Claude(t, fake, us.Run{Dir: dir, Session: "t1/writer", Prompt: "Change the greeting to goodbye."})

	if run.Exit != 0 || run.Result != "DONE: greeting.txt now says goodbye" {
		t.Fatalf("the writer did not finish: exit %d, result %q", run.Exit, run.Result)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "greeting.txt"))
	if string(got) != "goodbye world\n" {
		t.Fatalf("greeting.txt is %q", got)
	}
	var steps []string
	for _, e := range fake.Messages() {
		steps = append(steps, e.Step)
	}
	if len(steps) != 3 || steps[0] != "read" || steps[2] != "done" {
		t.Fatalf("steps %v", steps)
	}
	t.Logf("passed with Claude Code %s", run.Version)
}

// Every example script in the guide checks.
func TestExamplesValidate(t *testing.T) {
	files, _ := filepath.Glob("../*.yaml")
	if len(files) != 5 {
		t.Fatalf("%d examples", len(files))
	}
	for _, f := range files {
		if _, err := script.Load(f); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}
