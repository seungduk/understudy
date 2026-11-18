package harness

import (
	"testing"

	"github.com/seungduk/understudy/core"
	"github.com/seungduk/understudy/protocol"
)

// Antigravity's profile, as measured on agy 1.5.11 (see the probe in e2e).
func TestAntigravityProfile(t *testing.T) {
	pr := mustLookup(t, Antigravity)
	reads := []struct {
		tool, text string
		mark       core.Mark
		isErr      bool
		exit       int
		hasExit    bool
	}{
		{"run_command", "Created At: x\nCompleted At: x\nCommand exited with code 3.\n\nCommand output:\nout\r\n", core.Unmarked, true, 3, true},
		{"run_command", "Command exited with code 0.\n\nCommand output:\nfine\r\n", core.Unmarked, false, 0, true},
		{"read_file", "Encountered error in tool execution: file does not exist", core.MarkedError, true, 0, false},
		{"write_file", "Created At: x\nCreated file.", core.Unmarked, false, 0, false},
	}
	for i, c := range reads {
		r := pr.Read(c.tool, c.text, c.mark)
		if r.IsError != c.isErr || r.Exit != c.exit || r.HasExit != c.hasExit {
			t.Errorf("%d %s: %+v", i, c.tool, r)
		}
	}
	tools := core.Request{Tools: []core.Tool{{Name: "run_command"}, {Name: "read_file"}, {Name: "write_file"}, {Name: "edit_file"}, {Name: "manage_task"}}}
	if got := Detect(protocol.Gemini, tools).ID; got != Antigravity {
		t.Errorf("detected %s", got)
	}
	if got := pr.WorkingDir([]string{"Workspaces:\n- /x\nCommand Working Directory: /w/a b\nFiles relating to"}); got != "/w/a b" {
		t.Errorf("cwd %q", got)
	}
	c, err := pr.Call(core.EditFile, core.ActionArgs{Path: "/a", Old: "x", New: "y"})
	if err != nil || c.Tool != "edit_file" || string(c.Input) != `{"Path":"/a","Replacement":"y","Target":"x"}` {
		t.Error(c, err)
	}
	t.Setenv("UNDERSTUDY_AGY", "/bin/agy")
	s, err := pr.Prepare(Target{URL: "http://h:1", Session: "t1/w", Home: "/h", Dir: "/w", Prompt: "go"})
	if err != nil || s.Args[1] != "go [understudy-session: t1/w]" || s.Files["/h/.gemini/antigravity-cli/settings.json"] == "" {
		t.Errorf("%+v %v", s, err)
	}
}
