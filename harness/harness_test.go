package harness

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/seungduk/understudy/core"
	"github.com/seungduk/understudy/protocol"
)

// Every profile is well-formed: its regular expressions compile, its templates
// are JSON (or freeform), every action it maps has a tool its Answers name, and
// its launch data names a binary and the server URL.
func TestProfilesWellFormed(t *testing.T) {
	if len(All()) < 5 {
		t.Fatalf("profiles: %v", Names())
	}
	args := core.ActionArgs{Command: `echo "a" 'b'`, Path: "/w/a b.txt", Content: "x\ny\n", Old: "o\"ld", New: "n\new"}
	for _, pr := range append(All(), mustLookup(t, Generic)) {
		if pr.Name == "" || len(pr.Protocols) == 0 {
			t.Errorf("%s: no name or protocols", pr.ID)
		}
		pr.compile()
		for a, tm := range pr.Tools {
			c, err := pr.Call(a, args)
			if err != nil {
				t.Fatalf("%s %s: %v", pr.ID, a.Name(), err)
			}
			if c.Tool != tm.Tool || c.Kind != tm.Kind || (tm.Kind == core.Function && !json.Valid(c.Input)) || (tm.Kind == core.Freeform && c.Raw == "") {
				t.Errorf("%s %s: %+v", pr.ID, a.Name(), c)
			}
			if pr.ActionOf(tm.Tool) == core.NoAction {
				t.Errorf("%s: Answers does not name %s", pr.ID, tm.Tool)
			}
		}
		if pr.ID == Generic {
			continue
		}
		if len(pr.Detect.Tools) == 0 || pr.Launch.Binary == "" || pr.Launch.EnvVar == "" {
			t.Errorf("%s: no signature or binary", pr.ID)
		}
		all := strings.Join(pr.Launch.Env, " ") + strings.Join(pr.Launch.Args, " ")
		for _, f := range pr.Launch.Files {
			all += f
		}
		if !strings.Contains(all, "{{url}}") || !strings.Contains(all, "{{prompt}}") {
			t.Errorf("%s: the launch data does not use {{url}} and {{prompt}}", pr.ID)
		}
		for _, p := range pr.Protocols {
			if pr.Model(p) == "" {
				t.Errorf("%s: no model for %s", pr.ID, p.Name())
			}
		}
	}
}

func mustLookup(t *testing.T, id ID) *Profile {
	t.Helper()
	p, ok := Lookup(string(id))
	if !ok {
		t.Fatalf("no profile %s", id)
	}
	return p
}

func TestTemplates(t *testing.T) {
	cases := []struct {
		t    string
		a    core.ActionArgs
		want string
	}{
		{"{{command}}", core.ActionArgs{Command: "ls -l"}, "ls -l"},
		{"cat {{path|sh}}", core.ActionArgs{Path: "/a/it's"}, `cat '/a/it'\''s'`},
		{"{{content|plus}}", core.ActionArgs{Content: "a\nb\n"}, "+a\n+b"},
		{"{{old|minus}}", core.ActionArgs{Old: "x"}, "-x"},
		{"{{new|plus}}", core.ActionArgs{}, "+"},
		{"{{path}} {{content}} {{old}} {{new}} {{other}}", core.ActionArgs{Path: "p", Content: "c", Old: "o", New: "n"}, "p c o n {{other}}"},
	}
	for _, c := range cases {
		if got := fill(c.t, c.a); got != c.want {
			t.Errorf("%q: %q, want %q", c.t, got, c.want)
		}
	}
	// A JSON template keeps its structure; values are escaped by JSON, not by hand.
	pi := mustLookup(t, Pi)
	c, _ := pi.Call(core.EditFile, core.ActionArgs{Path: "/a", Old: `"q"`, New: "line\nnext"})
	if string(c.Input) != `{"edits":[{"newText":"line\nnext","oldText":"\"q\""}],"path":"/a"}` {
		t.Error(string(c.Input))
	}
	codex := mustLookup(t, Codex)
	c, _ = codex.Call(core.EditFile, core.ActionArgs{Path: "/w/a.txt", Old: "hello old", New: "hello new"})
	if c.Kind != core.Freeform || c.Raw != "*** Begin Patch\n*** Update File: /w/a.txt\n@@\n-hello old\n+hello new\n*** End Patch\n" {
		t.Errorf("%q", c.Raw)
	}
	if _, err := mustLookup(t, Generic).Call(core.Shell, core.ActionArgs{Command: "ls"}); err == nil || !strings.Contains(err.Error(), "natively") {
		t.Error(err)
	}
}

// The result rules as measured on each real harness (see the probe in e2e).
func TestReadResults(t *testing.T) {
	cases := []struct {
		id         ID
		tool, text string
		mark       core.Mark
		isErr      bool
		exit       int
		hasExit    bool
	}{
		{ClaudeCode, "Bash", "Exit code 2\nboom", core.MarkedError, true, 2, true},
		{ClaudeCode, "Bash", "(Bash completed with no output)", core.MarkedOK, false, 0, true},
		{ClaudeCode, "Bash", "boom", core.MarkedError, true, 0, false},
		{ClaudeCode, "Read", "1\tx", core.MarkedOK, false, 0, false},
		{Codex, "exec_command", "Chunk ID: 1\nWall time: 0.0 seconds\nProcess exited with code 3\nOutput:\nx", core.Unmarked, true, 3, true},
		{Codex, "exec_command", "Process exited with code 0\nOutput:\nfine", core.Unmarked, false, 0, true},
		{Codex, "apply_patch", "Exit code: 0\nWall time: 0 seconds\nOutput:\nSuccess.", core.Unmarked, false, 0, true},
		{Codex, "apply_patch", "apply_patch verification failed: Failed to find expected lines", core.Unmarked, true, 0, false},
		{OpenCode, "bash", "out\nerr\n", core.Unmarked, false, 0, false},
		{OpenCode, "read", "File not found: /a", core.Unmarked, true, 0, false},
		{Pi, "bash", "out\n\n\nCommand exited with code 3", core.Unmarked, true, 3, true},
		{Pi, "bash", "fine\n", core.Unmarked, false, 0, true},
		{Pi, "read", "ENOENT: no such file or directory", core.Unmarked, true, 0, false},
		{GeminiCLI, "run_shell_command", "<untrusted_context>\nOutput: out\nExit Code: 3\nProcess Group PGID: 1\n</untrusted_context>", core.Unmarked, true, 3, true},
		{GeminiCLI, "run_shell_command", "Output: fine\nProcess Group PGID: 1", core.Unmarked, false, 0, true},
		{GeminiCLI, "read_file", "File not found: /a", core.MarkedError, true, 0, false},
		{Generic, "x", "y", core.MarkedError, true, 0, false},
		{Generic, "x", "y", core.Unmarked, false, 0, false},
	}
	for i, c := range cases {
		r := mustLookup(t, c.id).Read(c.tool, c.text, c.mark)
		if r.IsError != c.isErr || r.Exit != c.exit || r.HasExit != c.hasExit {
			t.Errorf("%d %s %s: %+v", i, c.id, c.tool, r)
		}
	}
}

func req(tools ...string) core.Request {
	r := core.Request{Turns: []core.Turn{{Role: core.User, Parts: []core.Part{{Kind: core.Text, Text: "x"}}}}}
	for _, n := range tools {
		r.Tools = append(r.Tools, core.Tool{Name: n})
	}
	return r
}

func TestActionOfCall(t *testing.T) {
	codex := mustLookup(t, Codex)
	call := func(a core.Action, args core.ActionArgs) core.Part {
		c, _ := codex.Call(a, args)
		return core.Part{Kind: core.ToolCall, Tool: c.Tool, Input: c.Input, Text: c.Raw, ToolKind: c.Kind}
	}
	cases := map[core.Action]core.Part{
		core.Shell:     call(core.Shell, core.ActionArgs{Command: "go test ./..."}),
		core.ReadFile:  call(core.ReadFile, core.ActionArgs{Path: "/w/a b.txt"}),
		core.WriteFile: call(core.WriteFile, core.ActionArgs{Path: "/w/a", Content: "x\n"}),
		core.EditFile:  call(core.EditFile, core.ActionArgs{Path: "/w/a", Old: "x", New: "y"}),
	}
	for want, p := range cases {
		if got := codex.ActionOfCall(p); got != want {
			t.Errorf("%s: %s", want.Name(), got.Name())
		}
	}
	// A call the model made itself: a shell command is shell, a patch is an edit.
	if a := codex.ActionOfCall(core.Part{Tool: "exec_command", Input: json.RawMessage(`{"cmd":"ls -la"}`)}); a != core.Shell {
		t.Error(a.Name())
	}
	if a := codex.ActionOfCall(core.Part{Tool: "apply_patch", ToolKind: core.Freeform, Text: "*** Begin Patch\n*** Delete File: /a\n*** End Patch"}); a != core.EditFile {
		t.Error(a.Name())
	}
	if a := mustLookup(t, Pi).ActionOfCall(core.Part{Tool: "read", Input: json.RawMessage(`{"path":"/a"}`)}); a != core.ReadFile {
		t.Error(a.Name())
	}
	if a := codex.ActionOfCall(core.Part{Tool: "exec_command", Input: json.RawMessage(`nope`)}); a != core.Shell {
		t.Error(a.Name())
	}
}

func TestDetect(t *testing.T) {
	cases := []struct {
		p     protocol.ID
		tools []string
		want  ID
	}{
		{protocol.Anthropic, []string{"Bash", "Read", "Edit", "Write", "Glob"}, ClaudeCode},
		{protocol.OpenAIResponses, []string{"exec_command", "write_stdin", "apply_patch"}, Codex},
		{protocol.OpenAIChat, []string{"bash", "edit", "glob", "read", "todowrite", "write"}, OpenCode},
		{protocol.Anthropic, []string{"bash", "edit", "read", "todowrite", "write"}, OpenCode},
		{protocol.OpenAIChat, []string{"read", "bash", "edit", "write"}, Pi},
		{protocol.Gemini, []string{"read", "bash", "edit", "write"}, Pi},
		{protocol.Gemini, []string{"run_shell_command", "read_file", "replace", "write_file"}, GeminiCLI},
		{protocol.OpenAIChat, []string{"exec_command", "apply_patch"}, Generic}, // Codex does not speak chat
		{protocol.Gemini, nil, Generic},
	}
	for _, c := range cases {
		if got := Detect(c.p, req(c.tools...)).ID; got != c.want {
			t.Errorf("%s %v: %s, want %s", c.p.Name(), c.tools, got, c.want)
		}
	}
}

func TestAuxAndWorkingDir(t *testing.T) {
	title := core.Request{System: "You are a title generator. You output ONLY a thread title.", Turns: []core.Turn{{Role: core.User, Parts: []core.Part{{Kind: core.Text, Text: "x"}}}}}
	if n, rep, ok := mustLookup(t, OpenCode).AuxReply(protocol.OpenAIChat, title); !ok || n != "title" || rep == "" {
		t.Error(n, rep, ok)
	}
	// An unknown client's side request is found by the profiles that speak its protocol.
	if n, _, ok := mustLookup(t, Generic).AuxReply(protocol.OpenAIChat, title); !ok || n != "title" {
		t.Error(n, ok)
	}
	if _, _, ok := mustLookup(t, Generic).AuxReply(protocol.Gemini, title); ok {
		t.Error("opencode's title found on gemini")
	}
	route := core.Request{System: "You are a specialized Task Routing AI.", Turns: req().Turns}
	if _, rep, ok := mustLookup(t, Generic).AuxReply(protocol.Gemini, route); !ok || !json.Valid([]byte(rep)) {
		t.Error(rep, ok)
	}
	if _, _, ok := mustLookup(t, OpenCode).AuxReply(protocol.OpenAIChat, req("bash")); ok {
		t.Error("plain request read as aux")
	}
	firstUser := &Profile{ID: "x", Aux: []Aux{{Name: "n", Contains: "magic", InFirstUser: true, Reply: "r"}}}
	if _, _, ok := firstUser.AuxReply(protocol.Gemini, core.Request{Turns: []core.Turn{{Role: core.User, Parts: []core.Part{{Kind: core.Text, Text: "a magic word"}}}}}); !ok {
		t.Error("first-user aux")
	}
	dirs := map[ID]string{
		ClaudeCode: " - Primary working directory: /w/a b\n",
		Codex:      "<environment_context>\n  <cwd>/w/a b</cwd>\n</environment_context>",
		OpenCode:   "  Working directory: /w/a b\n",
		Pi:         "Current working directory: /w/a b",
		GeminiCLI:  "- **Workspace Directories:**\n  - /w/a b\n- **Directory Structure:**",
	}
	for id, text := range dirs {
		if got := mustLookup(t, id).WorkingDir([]string{"", text}); got != "/w/a b" {
			t.Errorf("%s: %q", id, got)
		}
	}
}

func TestPrepare(t *testing.T) {
	t.Setenv("UNDERSTUDY_PI", "/bin/echo")
	pr := mustLookup(t, Pi)
	s, err := pr.Prepare(Target{URL: "http://h:1/", Home: "/home/x", Dir: "/w", Prompt: "do it", Protocol: protocol.Gemini})
	if err != nil {
		t.Fatal(err)
	}
	if s.Binary != "/bin/echo" || strings.Join(s.Args, " ") != "-p --mode json --model us-gemini/fake-gemini do it" {
		t.Errorf("%+v", s)
	}
	env := strings.Join(s.Env, "\n")
	if !strings.Contains(env, "PI_CODING_AGENT_DIR=/home/x/agent") || !strings.Contains(env, "TMPDIR=/home/x/tmp") || strings.Contains(env, "{{") {
		t.Error(env)
	}
	if f := s.Files["/home/x/agent/models.json"]; !strings.Contains(f, `"baseUrl": "http://h:1/v1beta"`) || !strings.Contains(f, `"X-Understudy-Session": "default"`) || !json.Valid([]byte(f)) {
		t.Error(f)
	}
	if _, err := mustLookup(t, Codex).Prepare(Target{Protocol: protocol.Gemini}); err == nil {
		t.Error("codex prepared for gemini")
	}
	t.Setenv("UNDERSTUDY_CODEX", "")
	t.Setenv("PATH", "")
	if _, err := mustLookup(t, Codex).Binary(); err == nil || !strings.Contains(err.Error(), "UNDERSTUDY_CODEX") {
		t.Error(err)
	}
	if _, ok := Lookup("nope"); ok {
		t.Error("unknown profile")
	}
	for _, n := range []string{"claude-code", "codex", "gemini-cli", "generic", "opencode", "pi"} {
		if !strings.Contains(","+strings.Join(Names(), ",")+",", ","+n+",") {
			t.Error(n, Names())
		}
	}
	for _, pr := range All() {
		if pr.ID == ClaudeCode {
			continue
		}
		for _, f := range pr.Launch.Files {
			if strings.HasSuffix(strings.TrimSpace(f), "}") && !json.Valid([]byte(strings.NewReplacer("{{url}}", "u", "{{session}}", "s", "{{home}}", "h", "{{dir}}", "d", "{{prompt}}", "p", "{{model}}", "m").Replace(f))) {
				t.Errorf("%s: a config file is not JSON:\n%s", pr.ID, f)
			}
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("no panic on a duplicate profile")
		}
	}()
	register(&Profile{ID: Pi})
}
