package script

import (
	"errors"
	"strings"
	"testing"
)

const head = "schema: understudy/v1\nsessions:\n  default:\n    steps:\n"

func problems(t *testing.T, src string) Problems {
	t.Helper()
	_, err := Parse([]byte(src))
	if err == nil {
		t.Fatalf("accepted:\n%s", src)
	}
	var ps Problems
	if !errors.As(err, &ps) {
		t.Fatalf("error is not Problems: %v", err)
	}
	for _, p := range ps {
		if p.Fix == "" || p.Message == "" || p.Rule == "" {
			t.Errorf("incomplete problem %+v", p)
		}
	}
	return ps
}

func wantProblem(t *testing.T, src string, path string, rule Rule) Problem {
	t.Helper()
	ps := problems(t, src)
	for _, p := range ps {
		if p.Path == path && p.Rule == rule {
			return p
		}
	}
	t.Fatalf("no %s at %s in:\n%v", rule, path, ps)
	return Problem{}
}

func TestParseGood(t *testing.T) {
	src := `schema: understudy/v1
divergence: lenient
provenance: {source: recorded, run: r1, task: t1, seat: writer, session: s, recorded_at: "2026-11-18T09:00:00Z", cli_version: 2.1.325, note: n}
sessions:
  t1/writer:
    steps:
      - name: read
        reply:
          tool_use:
            thinking: hmm
            text: reading
            calls:
              - {tool: Read, input: {file_path: "{{cwd}}/a.txt"}}
      - name: retry
        attempts:
          - http_error: {error: overloaded, retry_after_seconds: 1, retry: always}
          - stream_error: {error: api_error, partial_text: half}
          - limit: {window: seven_day, resets_in_seconds: 3600}
          - message: {text: ok, thinking: t, stop: max_tokens}
            stall: {gate: g1, at: mid_stream}
            cut: {after_events: 3, how: reset}
            pace: {first_event_ms: 5, between_events_ms: 6}
      - name: judge
        expect: {result: ok}
        cases:
          - when: {tool: Bash, exit_code: 0, contains: PASS, regex: "ok\\s"}
            reply: {message: {text: done}}
          - when: {result: error}
            next: read
            attempts: [{message: {text: again}}]
        otherwise: {reply: {message: {text: hmm}}, next: judge}
`
	s, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if s.Divergence() != Lenient || strings.Join(s.Keys(), ",") != "t1/writer" || s.File().Provenance.CLIVersion != "2.1.325" {
		t.Fatalf("%+v", s.File())
	}
	p, ok := s.Session("t1/writer")
	if !ok || p.Steps() != 3 || p.StepName(2) != "judge" {
		t.Fatal("session")
	}
	if _, ok := s.Session("nope"); ok {
		t.Fatal("unknown session")
	}
	// JSON is YAML too.
	if _, err := Parse([]byte(`{"schema":"understudy/v1","sessions":{"default":{"steps":[{"name":"a","reply":{"message":{"text":"hi"}}}]}}}`)); err != nil {
		t.Fatal(err)
	}
	if s, _ := Parse([]byte(head + "      - {name: a, reply: {message: {text: hi}}}\n")); s.Divergence() != Strict {
		t.Fatal("default divergence")
	}
}

func TestFormatProblems(t *testing.T) {
	step := func(body string) string { return head + "      - name: a\n" + body }
	cases := []struct {
		src  string
		path string
		rule Rule
	}{
		{"schema: [", "", RuleSyntax},
		{"", "", RuleRequired},
		{"schema: understudy/v2\nsessions: {default: {steps: [{name: a, reply: {message: {text: x}}}]}}", "schema", RuleSchema},
		{"sessions: {default: {steps: [{name: a, reply: {message: {text: x}}}]}}", "schema", RuleSchema},
		{head + "      - name: a\n        replies: {message: {text: x}}\n", `sessions["default"].steps[0].replies`, RuleUnknownField},
		{"schema: understudy/v1\nsessions: [a]", "sessions", RuleType},
		{"schema: understudy/v1\nsessions: {default: {steps: {a: 1}}}", `sessions["default"].steps`, RuleType},
		{"schema: understudy/v1\nsessions: {default: 5}", `sessions["default"]`, RuleType},
		{"schema: understudy/v1\nschema: x\nsessions: {}", "schema", RuleUnique},
		{"schema: &a understudy/v1\ndivergence: *a\nsessions: {}", "divergence", RuleType},
		{"- a", "", RuleType},
		{step("        reply: {message: {text: [x]}}\n"), `sessions["default"].steps[0].reply.message.text`, RuleType},
		{step("        reply: {message: {text: ~}}\n"), `sessions["default"].steps[0].reply.message.text`, RuleType},
		{step("        reply: {http_error: {error: overloaded, retry_after_seconds: soon}}\n"), `sessions["default"].steps[0].reply.http_error.retry_after_seconds`, RuleType},
		{step("        reply: {http_error: {error: overloaded, retry_after_seconds: 99999999999999999999}}\n"), `sessions["default"].steps[0].reply.http_error.retry_after_seconds`, RuleType},
		{step("        reply: {tool_use: {calls: [{tool: Read, input: [1]}]}}\n"), `sessions["default"].steps[0].reply.tool_use.calls[0].input`, RuleType},
	}
	for _, c := range cases {
		wantProblem(t, c.src, c.path, c.rule)
	}
	p := wantProblem(t, head+"      - name: a\n        replly: {message: {text: x}}\n", `sessions["default"].steps[0].replly`, RuleUnknownField)
	if !strings.Contains(p.Fix, "did you mean reply?") || p.Line != 6 {
		t.Errorf("fix %+v", p)
	}
	if !strings.Contains(p.String(), "line 6") || !strings.Contains(Problems{p}.Error(), "1 problem") {
		t.Errorf("string %s", p.String())
	}
	if (Problem{Rule: RuleSyntax, Message: "m", Fix: "f"}).String() != "file: m (syntax). Fix: f" {
		t.Error("file string")
	}
	if _, err := Load("/nonexistent/x.yaml"); err == nil {
		t.Error("missing file loaded")
	}
	if _, err := ParseFile([]byte("schema: [")); err == nil {
		t.Error("ParseFile syntax")
	}
}

func TestCheckProblems(t *testing.T) {
	st := func(body string) string { return head + "      - name: a\n" + body }
	r := func(reply string) string { return st("        reply: " + reply + "\n") }
	const p0 = `sessions["default"].steps[0]`
	cases := []struct {
		src  string
		path string
		rule Rule
	}{
		{"schema: understudy/v1\ndivergence: loose\nsessions: {default: {steps: [{name: a, reply: {message: {text: x}}}]}}", "divergence", RuleEnum},
		{"schema: understudy/v1\nprovenance: {run: x}\nsessions: {default: {steps: [{name: a, reply: {message: {text: x}}}]}}", "provenance.source", RuleRequired},
		{"schema: understudy/v1\nprovenance: {source: guessed}\nsessions: {default: {steps: [{name: a, reply: {message: {text: x}}}]}}", "provenance.source", RuleEnum},
		{"schema: understudy/v1\nprovenance: {source: written, recorded_at: yesterday}\nsessions: {default: {steps: [{name: a, reply: {message: {text: x}}}]}}", "provenance.recorded_at", RulePattern},
		{"schema: understudy/v1\nsessions: {}", "sessions", RuleRequired},
		{"schema: understudy/v1\nsessions: {\"bad key!\": {steps: [{name: a, reply: {message: {text: x}}}]}}", `sessions["bad key!"]`, RulePattern},
		{"schema: understudy/v1\nsessions: {default: {steps: []}}", `sessions["default"].steps`, RuleRequired},
		{head + "      - reply: {message: {text: x}}\n", p0 + ".name", RuleRequired},
		{head + "      - {name: Bad Name, reply: {message: {text: x}}}\n", p0 + ".name", RulePattern},
		{head + "      - {name: a, reply: {message: {text: x}}}\n      - {name: a, reply: {message: {text: x}}}\n", `sessions["default"].steps[1].name`, RuleUnique},
		{st(""), p0, RuleOneOf},
		{st("        reply: {message: {text: x}}\n        attempts: [{message: {text: x}}]\n"), p0, RuleOneOf},
		{st("        cases: [{when: {result: ok}, reply: {message: {text: x}}}]\n        next: a\n"), p0 + ".next", RuleNotApplied},
		{st("        cases: [{reply: {message: {text: x}}}]\n"), p0 + ".cases[0].when", RuleRequired},
		{st("        cases: [{when: {result: ok}}]\n"), p0 + ".cases[0]", RuleOneOf},
		{st("        cases: [{when: {result: ok}, reply: {message: {text: x}}, attempts: [{message: {text: x}}]}]\n"), p0 + ".cases[0]", RuleOneOf},
		{st("        reply: {message: {text: x}}\n        otherwise: {reply: {message: {text: x}}}\n"), p0 + ".otherwise", RuleNotApplied},
		{st("        reply: {message: {text: x}}\n        next: nowhere\n"), p0 + ".next", RuleReference},
		{st("        attempts: [" + strings.Repeat("{message: {text: x}},", MaxAttempts+1) + "]\n"), p0 + ".attempts", RuleRange},
		{st("        expect: {}\n        reply: {message: {text: x}}\n"), p0 + ".expect", RuleRequired},
		{st("        expect: {tool: \"no spaces\"}\n        reply: {message: {text: x}}\n"), p0 + ".expect.tool", RulePattern},
		{st("        expect: {result: fine}\n        reply: {message: {text: x}}\n"), p0 + ".expect.result", RuleEnum},
		{st("        expect: {exit_code: 256}\n        reply: {message: {text: x}}\n"), p0 + ".expect.exit_code", RuleRange},
		{st("        expect: {regex: \"(?=x)\"}\n        reply: {message: {text: x}}\n"), p0 + ".expect.regex", RuleRegex},
		{r("{}"), p0 + ".reply", RuleOneOf},
		{r("{message: {text: x}, limit: {window: seven_day, resets_in_seconds: 1}}"), p0 + ".reply", RuleOneOf},
		{r("{message: {thinking: x}}"), p0 + ".reply.message.text", RuleRequired},
		{r("{message: {text: x, stop: done}}"), p0 + ".reply.message.stop", RuleEnum},
		{r("{tool_use: {calls: []}}"), p0 + ".reply.tool_use.calls", RuleRequired},
		{r("{tool_use: {calls: [" + strings.Repeat("{tool: Read, input: {}},", MaxCalls+1) + "]}}"), p0 + ".reply.tool_use.calls", RuleRange},
		{r("{tool_use: {calls: [{input: {}}]}}"), p0 + ".reply.tool_use.calls[0].tool", RuleRequired},
		{r("{tool_use: {calls: [{tool: \"a b\", input: {}}]}}"), p0 + ".reply.tool_use.calls[0].tool", RulePattern},
		{r("{tool_use: {calls: [{tool: Read}]}}"), p0 + ".reply.tool_use.calls[0].input", RuleRequired},
		{r("{tool_use: {calls: [{tool: Read, input: {file_path: \"{{home}}/a\"}}]}}"), p0 + ".reply.tool_use.calls[0].input.file_path", RuleTemplate},
		{r("{tool_use: {calls: [{tool: Read, input: {a: [{b: \"{{x}}\"}]}}]}}"), p0 + ".reply.tool_use.calls[0].input.a[0].b", RuleTemplate},
		{r("{http_error: {}}"), p0 + ".reply.http_error.error", RuleRequired},
		{r("{http_error: {error: overload}}"), p0 + ".reply.http_error.error", RuleEnum},
		{r("{http_error: {error: overloaded, retry_after_seconds: -1}}"), p0 + ".reply.http_error.retry_after_seconds", RuleRange},
		{r("{http_error: {error: overloaded, retry: maybe}}"), p0 + ".reply.http_error.retry", RuleEnum},
		{r("{stream_error: {error: nope}}"), p0 + ".reply.stream_error.error", RuleEnum},
		{r("{limit: {resets_in_seconds: 5}}"), p0 + ".reply.limit.window", RuleRequired},
		{r("{limit: {window: monthly, resets_in_seconds: 5}}"), p0 + ".reply.limit.window", RuleEnum},
		{r("{limit: {window: five_hour}}"), p0 + ".reply.limit.resets_in_seconds", RuleRange},
		{r("{message: {text: x}, stall: {}}"), p0 + ".reply.stall", RuleOneOf},
		{r("{message: {text: x}, stall: {seconds: 1, gate: g}}"), p0 + ".reply.stall", RuleOneOf},
		{r("{message: {text: x}, stall: {seconds: 999999}}"), p0 + ".reply.stall.seconds", RuleRange},
		{r("{message: {text: x}, stall: {gate: \"G!\"}}"), p0 + ".reply.stall.gate", RulePattern},
		{r("{message: {text: x}, stall: {gate: g, at: later}}"), p0 + ".reply.stall.at", RuleEnum},
		{r("{http_error: {error: overloaded}, stall: {gate: g, at: mid_stream}}"), p0 + ".reply.stall.at", RuleNotApplied},
		{r("{limit: {window: seven_day, resets_in_seconds: 9}, cut: {after_events: 0, how: reset}}"), p0 + ".reply.cut", RuleNotApplied},
		{r("{message: {text: x}, cut: {after_events: -1, how: reset}}"), p0 + ".reply.cut.after_events", RuleRange},
		{r("{message: {text: x}, cut: {after_events: 1}}"), p0 + ".reply.cut.how", RuleRequired},
		{r("{message: {text: x}, cut: {after_events: 1, how: drop}}"), p0 + ".reply.cut.how", RuleEnum},
		{r("{message: {text: x}, pace: {first_event_ms: -1}}"), p0 + ".reply.pace.first_event_ms", RuleRange},
		{r("{message: {text: x}, pace: {between_events_ms: 60001}}"), p0 + ".reply.pace.between_events_ms", RuleRange},
		{r("{http_error: {error: overloaded}, pace: {between_events_ms: 5}}"), p0 + ".reply.pace.between_events_ms", RuleNotApplied},
	}
	for _, c := range cases {
		wantProblem(t, c.src, c.path, c.rule)
	}
	p := wantProblem(t, r("{http_error: {error: overload}}"), p0+".reply.http_error.error", RuleEnum)
	if !strings.Contains(p.Fix, "did you mean overloaded?") || p.Line == 0 {
		t.Errorf("%+v", p)
	}
	p = wantProblem(t, st("        reply: {message: {text: x}}\n        next: aa\n"), p0+".next", RuleReference)
	if !strings.Contains(p.Fix, "did you mean a?") {
		t.Errorf("%+v", p)
	}
	// Go values get the same checks, without lines.
	_, err := Compile(File{Schema: SchemaV1, Sessions: map[string]Session{"default": {Steps: []Step{{Name: "a"}}}}})
	var ps Problems
	if !errors.As(err, &ps) || ps[0].Rule != RuleOneOf || ps[0].Line != 0 {
		t.Fatalf("%v", err)
	}
	defer func() {
		if recover() == nil {
			t.Error("MustCompile did not panic")
		}
	}()
	MustCompile(File{})
}

func TestNames(t *testing.T) {
	for k, want := range map[string]bool{"default": true, "task-17/writer": true, "a": true, "-a": false, "": false, "a b": false, strings.Repeat("a", 129): false} {
		if ValidSessionKey(k) != want {
			t.Errorf("key %q", k)
		}
	}
	for g, want := range map[string]bool{"g1": true, "review-gate": true, "G": false, "": false} {
		if ValidGateName(g) != want {
			t.Errorf("gate %q", g)
		}
	}
	if closest("zzzzzz", []string{"reply"}) != "" {
		t.Error("closest too far")
	}
}
