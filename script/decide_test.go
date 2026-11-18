package script

import (
	"strings"
	"testing"
)

const branching = `schema: understudy/v1
sessions:
  s:
    steps:
      - name: edit
        reply: {tool_use: {calls: [{tool: Edit, input: {}}]}}
      - name: test
        reply: {tool_use: {calls: [{tool: Bash, input: {command: go test}}]}}
      - name: judge
        cases:
          - when: {tool: Bash, exit_code: 0}
            reply: {message: {text: passed}}
          - when: {tool: Bash, contains: FAIL}
            next: edit
            attempts: [{message: {text: fix1}}, {message: {text: fix2}}]
      - name: never
        reply: {message: {text: unreachable}}
`

func plan(t *testing.T, src, key string) (*SessionPlan, *Script) {
	t.Helper()
	s, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := s.Session(key)
	return p, s
}

var (
	editOK = []Result{{Tool: "Edit", Text: "The file has been updated."}}
	pass   = []Result{{Tool: "Bash", Text: "ok  pkg 0.1s"}}
	fail   = []Result{{Tool: "Bash", IsError: true, Text: "Exit code 1\n--- FAIL: TestX"}}
)

func TestChooseLinearAndBranches(t *testing.T) {
	p, _ := plan(t, branching, "s")
	cases := []struct {
		hist   [][]Result
		step   string
		branch string
		text   string
		div    bool
	}{
		{nil, "edit", BranchReply, "", false},
		{[][]Result{nil}, "edit", BranchReply, "", false},
		{[][]Result{nil, editOK}, "test", BranchReply, "", false},
		{[][]Result{nil, editOK, pass}, "judge", "case 0", "passed", false},
		{[][]Result{nil, editOK, fail}, "judge", "case 1", "fix1", false},
		// After a failing test the script jumps back to edit, then test, then judge again.
		{[][]Result{nil, editOK, fail, nil}, "edit", BranchReply, "", false},
		{[][]Result{nil, editOK, fail, nil, editOK, pass}, "judge", "case 0", "passed", false},
		// A Bash result that neither passes nor shows FAIL: no case, strict divergence.
		{[][]Result{nil, editOK, {{Tool: "Bash", IsError: true, Text: "Exit code 2\nboom"}}}, "judge", BranchNone, "", true},
		// A case without next goes on to the following step.
		{[][]Result{nil, editOK, pass, nil}, "never", BranchReply, "unreachable", false},
		// Past the last step the script has ended.
		{[][]Result{nil, editOK, pass, nil, nil}, "", BranchNone, "", true},
	}
	for i, c := range cases {
		ch := p.Choose(c.hist, Strict)
		if ch.Step != c.step || ch.Branch != c.branch || (ch.Divergence != "") != c.div {
			t.Errorf("%d: %+v", i, ch)
			continue
		}
		if c.text != "" && ch.Attempts[0].Message.Text != c.text {
			t.Errorf("%d: text %q", i, ch.Attempts[0].Message.Text)
		}
		if c.div && ch.Attempts != nil {
			t.Errorf("%d: strict divergence has attempts", i)
		}
		if len(ch.Trace) == 0 && c.branch != BranchReply {
			t.Errorf("%d: no trace", i)
		}
	}
	ch := p.Choose([][]Result{nil, editOK, fail}, Strict)
	if len(ch.Attempts) != 2 || !strings.Contains(strings.Join(ch.Trace, "\n"), `case 0 when {tool: Bash, exit_code: 0}: no, result 0 (Bash) has exit code 1`) {
		t.Errorf("trace %q", ch.Trace)
	}
}

func TestChooseLenient(t *testing.T) {
	p, _ := plan(t, branching, "s")
	ch := p.Choose([][]Result{nil, editOK, {{Tool: "Bash", IsError: true, Text: "Exit code 2"}}}, Lenient)
	if ch.Branch != "case 0" || ch.Divergence == "" || ch.Attempts[0].Message.Text != "passed" {
		t.Errorf("no match lenient %+v", ch)
	}
	ch = p.Choose([][]Result{nil, editOK, pass, nil}, Lenient)
	if ch.Step != "never" || ch.Divergence != "" {
		t.Errorf("past the final message the next step answers %+v", ch)
	}
	ch = p.Choose([][]Result{nil, editOK, pass, nil, nil, nil}, Lenient)
	if ch.Step != "never" || !strings.Contains(ch.Divergence, "has ended") || ch.Attempts == nil {
		t.Errorf("ended lenient %+v", ch)
	}
	ch = p.Choose([][]Result{nil, editOK, pass, nil, nil, nil}, Strict)
	if ch.Step != "" || ch.StepIndex != -1 || ch.Attempts != nil {
		t.Errorf("ended strict %+v", ch)
	}
}

func TestChooseExpectAndOtherwise(t *testing.T) {
	src := `schema: understudy/v1
sessions:
  s:
    steps:
      - name: one
        reply: {tool_use: {calls: [{tool: Read, input: {}}]}}
      - name: two
        expect: {tool: Read, result: ok}
        cases:
          - when: {regex: "^v\\d"}
            reply: {message: {text: version}}
        otherwise: {reply: {message: {text: other}}}
`
	p, _ := plan(t, src, "s")
	if ch := p.Choose([][]Result{nil, {{Tool: "Read", Text: "v2"}}}, Strict); ch.Branch != "case 0" || ch.Divergence != "" {
		t.Errorf("%+v", ch)
	}
	if ch := p.Choose([][]Result{nil, {{Tool: "Read", Text: "x"}}}, Strict); ch.Branch != BranchOtherwise || ch.Attempts[0].Message.Text != "other" {
		t.Errorf("%+v", ch)
	}
	ch := p.Choose([][]Result{nil, {{Tool: "Read", IsError: true, Text: "v2"}}}, Strict)
	if ch.Attempts != nil || !strings.Contains(ch.Divergence, `step "two" expected {tool: Read, result: ok}, but result 0 (Read) is an error`) {
		t.Errorf("%+v", ch)
	}
	ch = p.Choose([][]Result{nil, {{Tool: "Read", IsError: true, Text: "v2"}}}, Lenient)
	if ch.Attempts == nil || ch.Divergence == "" || ch.Branch != "case 0" {
		t.Errorf("lenient expect %+v", ch)
	}
	ch = p.Choose([][]Result{nil, nil}, Strict)
	if !strings.Contains(ch.Divergence, "carries no tool results") {
		t.Errorf("%+v", ch)
	}
	ch = p.Choose([][]Result{nil, {{Tool: "Bash"}}}, Strict)
	if !strings.Contains(ch.Divergence, "no result is from tool Read") {
		t.Errorf("%+v", ch)
	}
}

func TestMatcherFields(t *testing.T) {
	zero, one := 0, 1
	mk := func(m Matcher) *matcher {
		c := &checker{ls: lines{}}
		cm := c.matcher(m, "m")
		if len(c.ps) > 0 {
			t.Fatal(c.ps)
		}
		return cm
	}
	r := func(isErr bool, text string) []Result { return []Result{{Tool: "Bash", IsError: isErr, Text: text}} }
	cases := []struct {
		m    Matcher
		in   []Result
		want bool
	}{
		{Matcher{Result: OK}, r(false, ""), true},
		{Matcher{Result: OK}, r(true, ""), false},
		{Matcher{Result: Error}, r(true, ""), true},
		{Matcher{Result: Error}, r(false, ""), false},
		{Matcher{ExitCode: &zero}, r(false, "x"), true},
		{Matcher{ExitCode: &one}, r(true, "Exit code 1\nx"), true},
		{Matcher{ExitCode: &one}, r(true, "Error: Exit code 1"), true},
		{Matcher{ExitCode: &one}, r(true, "boom"), false},
		{Matcher{ExitCode: &zero}, r(true, "Exit code 3"), false},
		{Matcher{Contains: "ok"}, r(false, "is ok"), true},
		{Matcher{Contains: "ok"}, r(false, "no"), false},
		{Matcher{Regex: "^a+$"}, r(false, "aaa"), true},
		{Matcher{Regex: "^a+$"}, r(false, "ab"), false},
		{Matcher{Tool: "Bash"}, r(false, ""), true},
		{Matcher{Tool: "Read"}, r(false, ""), false},
		{Matcher{Tool: "Bash", Contains: "b"}, []Result{{Tool: "Bash", Text: "a"}, {Tool: "Bash", Text: "b"}}, true},
		{Matcher{Contains: "x"}, []Result{{Text: "x"}}, true},
		{Matcher{Contains: "x"}, nil, false},
	}
	for i, c := range cases {
		if got, why := mk(c.m).match(c.in); got != c.want || why == "" {
			t.Errorf("%d: %v %q", i, got, why)
		}
	}
	if d := mk(Matcher{Tool: "Bash", Result: OK, ExitCode: &zero, Contains: "a", Regex: "b"}).describe(); d != `{tool: Bash, result: ok, exit_code: 0, contains: "a", regex: "b"}` {
		t.Error(d)
	}
	if n, ok := ExitCode(Result{IsError: true, Text: "Exit code 99999999999999999999999"}); ok {
		t.Error("overflow exit code", n)
	}
}

func TestChooseExpected(t *testing.T) {
	p, _ := plan(t, branching, "s")
	if ch := p.Choose([][]Result{nil, editOK, pass}, Strict); ch.Expected != nil {
		t.Fatalf("a fitting request has no expected: %q", ch.Expected)
	}
	odd := []Result{{Tool: "Bash", IsError: true, Text: "Exit code 2\nno go files"}}
	ch := p.Choose([][]Result{nil, editOK, odd}, Strict)
	want := []string{`step "judge" case 0 when {tool: Bash, exit_code: 0}`, `step "judge" case 1 when {tool: Bash, contains: "FAIL"}`}
	if strings.Join(ch.Expected, "|") != strings.Join(want, "|") {
		t.Fatalf("no case: %q", ch.Expected)
	}
	ex, _ := plan(t, `schema: understudy/v1
sessions:
  s:
    steps:
      - {name: a, reply: {tool_use: {calls: [{tool: Read, input: {}}]}}}
      - {name: b, expect: {tool: Read, result: ok}, reply: {message: {text: done}}}
`, "s")
	ch = ex.Choose([][]Result{nil, {{Tool: "Read", IsError: true, Text: "no such file"}}}, Strict)
	if len(ch.Expected) != 1 || ch.Expected[0] != `step "b" expect {tool: Read, result: ok}` {
		t.Fatalf("expect: %q", ch.Expected)
	}
	ch = ex.Choose([][]Result{nil, {{Tool: "Read", Text: "ok"}}, nil}, Strict)
	if len(ch.Expected) != 1 || ch.Expected[0] != `no turn after step "b": the session has 2 step(s)` {
		t.Fatalf("ended: %q", ch.Expected)
	}
	ch = ex.Choose([][]Result{nil, {{Tool: "Read", IsError: true, Text: "x"}}}, Lenient)
	if len(ch.Expected) != 1 || ch.Attempts == nil {
		t.Fatalf("lenient keeps what was expected and answers: %+v", ch)
	}
}

func TestOutlineAndSource(t *testing.T) {
	src := "schema: understudy/v1\n# a comment\nsessions:\n  b:\n    steps:\n      - {name: one, reply: {message: {text: x}}}\n      - name: two\n        reply: {message: {text: y}}\n  a:\n    steps:\n      - {name: only, reply: {message: {text: z}}}\n"
	s, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if string(s.Source()) != src {
		t.Fatalf("source %q", s.Source())
	}
	o := s.Outline()
	if len(o) != 2 || o[0].Key != "a" || o[0].Line != 10 || o[0].Steps[0] != (StepOutline{Name: "only", Line: 11}) ||
		o[1].Key != "b" || o[1].Line != 5 || len(o[1].Steps) != 2 || o[1].Steps[0].Line != 6 || o[1].Steps[1] != (StepOutline{Name: "two", Line: 7}) {
		t.Fatalf("%+v", o)
	}
	c := MustCompile(s.File())
	if c.Source() != nil || c.Outline()[1].Steps[1] != (StepOutline{Name: "two"}) {
		t.Fatalf("compiled from a File: %q %+v", c.Source(), c.Outline())
	}
}
