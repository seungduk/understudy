package conv

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/seungduk/understudy/core"
	"github.com/seungduk/understudy/harness"
	"github.com/seungduk/understudy/script"
)

func text(role core.Role, s string) core.Turn {
	return core.Turn{Role: role, Parts: []core.Part{{Kind: core.Text, Text: s}}}
}

func TestKey(t *testing.T) {
	plain := core.Request{Turns: []core.Turn{text(core.User, "hi")}}
	sys := core.Request{System: "x [understudy-session: t1/planner] y", Turns: []core.Turn{text(core.User, "[understudy-session: other]")}}
	sysTurn := core.Request{Turns: []core.Turn{text(core.System, "[understudy-session: dev/turn]"), text(core.User, "[understudy-session: other]")}}
	user := core.Request{Turns: []core.Turn{text(core.User, "do it [understudy-session:t2/writer]")}}
	second := core.Request{Turns: []core.Turn{text(core.User, "<environment_context/>"), text(core.User, "go [understudy-session: c/1]")}}
	later := core.Request{Turns: []core.Turn{text(core.User, "a"), text(core.Assistant, "b"), text(core.User, "[understudy-session: late]")}}
	bad := core.Request{System: "[understudy-session: ]", Turns: []core.Turn{text(core.User, "a")}}
	hdr := http.Header{}
	hdr.Set(SessionHeader, " t9/reviewer ")
	badHdr := http.Header{}
	badHdr.Set(SessionHeader, "has space")
	cases := []struct {
		h   http.Header
		r   core.Request
		key string
		src KeySource
		bad string
	}{
		{http.Header{}, plain, "default", FromDefault, ""},
		{hdr, sys, "t9/reviewer", FromHeader, ""},
		{http.Header{}, sys, "t1/planner", FromSystemPrompt, ""},
		{http.Header{}, sysTurn, "dev/turn", FromSystemPrompt, ""},
		{http.Header{}, user, "t2/writer", FromUserPrompt, ""},
		{http.Header{}, second, "c/1", FromUserPrompt, ""},
		{http.Header{}, later, "default", FromDefault, ""},
		{http.Header{}, bad, "", FromSystemPrompt, ""},
		{badHdr, plain, "", FromHeader, "has space"},
	}
	for i, c := range cases {
		k := SessionKey(c.h, c.r)
		if k.Key != c.key || k.Source != c.src || k.Bad != c.bad {
			t.Errorf("%d: %+v", i, k)
		}
	}
	for s, want := range map[KeySource]string{FromHeader: "header", FromSystemPrompt: "system_prompt", FromUserPrompt: "user_prompt", FromDefault: "default", 0: "unknown"} {
		if s.String() != want {
			t.Errorf("%v", s)
		}
	}
}

func TestHistoryAndCWD(t *testing.T) {
	cc, _ := harness.Lookup(string(harness.ClaudeCode))
	r := core.Request{Turns: []core.Turn{
		text(core.User, "go"),
		text(core.System, "# Environment\n - Primary working directory: /tmp/w x\n - Platform: darwin"),
		{Role: core.Assistant, Parts: []core.Part{{Kind: core.ToolCall, CallID: "t1", Tool: "Read"}, {Kind: core.ToolCall, CallID: "t2", Tool: "Bash"}}},
		{Role: core.User, Parts: []core.Part{{Kind: core.ToolResult, CallID: "t1", Text: "file", Mark: core.MarkedOK},
			{Kind: core.ToolResult, CallID: "t2", Mark: core.MarkedError, Text: "Exit code 1"}}},
		text(core.Assistant, "hm"),
		{Role: core.User, Parts: []core.Part{{Kind: core.ToolResult, CallID: "zz", Text: "orphan"}, {Kind: core.ToolResult, Tool: "Bash", Text: "named by the wire"}}},
	}}
	want := [][]script.Result{nil,
		{{Tool: "Read", Action: core.ReadFile, Text: "file"}, {Tool: "Bash", Action: core.Shell, IsError: true, Text: "Exit code 1", Exit: 1, HasExit: true}},
		{{Tool: "", Text: "orphan"}, {Tool: "Bash", Action: core.Shell, Text: "named by the wire", HasExit: true}}}
	if got := History(r, cc); !reflect.DeepEqual(got, want) {
		t.Errorf("history %+v", got)
	}
	if d := WorkingDir(r, cc); d != "/tmp/w x" {
		t.Errorf("cwd %q", d)
	}
	if d := WorkingDir(core.Request{System: "Working directory: /a", Turns: []core.Turn{text(core.User, "x")}}, cc); d != "/a" {
		t.Errorf("system cwd %q", d)
	}
	plain := core.Request{Turns: []core.Turn{text(core.User, "x")}}
	if d := WorkingDir(plain, cc); d != "" || len(History(plain, cc)) != 1 {
		t.Error("none")
	}
}

func TestPrompt(t *testing.T) {
	call := core.Turn{Role: core.Assistant, Parts: []core.Part{{Kind: core.ToolCall, CallID: "t", Tool: "Bash"}}}
	result := core.Part{Kind: core.ToolResult, CallID: "t", Text: "out"}
	cases := []struct {
		turns []core.Turn
		want  string
	}{
		{[]core.Turn{text(core.User, "fix the bug")}, "fix the bug"},
		{[]core.Turn{{Role: core.User, Parts: []core.Part{{Kind: core.Text, Text: "<system-reminder>x</system-reminder>"}, {Kind: core.Text, Text: "  "}, {Kind: core.Text, Text: "task"}}}},
			"<system-reminder>x</system-reminder>\n\ntask"},
		{[]core.Turn{text(core.User, "a"), call, {Role: core.User, Parts: []core.Part{result}}}, ""},
		{[]core.Turn{text(core.User, "a"), call, {Role: core.User, Parts: []core.Part{result, {Kind: core.Text, Text: "also this"}}}}, "also this"},
		{[]core.Turn{text(core.User, "a"), text(core.Assistant, "prefill")}, ""},
		{[]core.Turn{text(core.User, "task"), text(core.System, "env")}, "task"},
		{[]core.Turn{text(core.System, "env")}, ""},
		{[]core.Turn{text(core.User, "<environment_context/>"), text(core.User, "the task")}, "the task"},
	}
	for i, c := range cases {
		if got := Prompt(core.Request{Turns: c.turns}); got != c.want {
			t.Errorf("%d: prompt %q, want %q", i, got, c.want)
		}
	}
}
