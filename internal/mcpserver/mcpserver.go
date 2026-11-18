// Package mcpserver is understudy's Model Context Protocol server: tools that
// let an agent write, check and load scripts, read what a running understudy
// server saw, release gates and record transcripts, over the control API of
// that server. Every tool answers JSON; a failed call is an error result whose
// JSON has "error" and "fix" (and "problems" for a script that does not check).
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	understudy "github.com/seungduk/understudy"
	"github.com/seungduk/understudy/clienv"
	"github.com/seungduk/understudy/control"
	"github.com/seungduk/understudy/explain"
	"github.com/seungduk/understudy/harness"
	"github.com/seungduk/understudy/internal/version"
	"github.com/seungduk/understudy/protocol"
	"github.com/seungduk/understudy/record"
	"github.com/seungduk/understudy/script"
	"github.com/seungduk/understudy/server"
)

// Resource URIs.
const (
	GuideURI  = "understudy://docs/SCRIPTS.md"
	SchemaURI = "understudy://schema/understudy-v1.schema.json"
)

// Options configure the MCP server.
type Options struct {
	// Control is the understudy server the tools act on.
	Control *control.Client
	// Dir is where relative file paths resolve; "" is the process's working directory.
	Dir string
}

// Instructions is what the server tells a connecting agent.
const Instructions = `understudy is a scriptable fake of the Anthropic Messages API: the real Claude Code CLI runs against it and a YAML script plays the model.
Workflow: read the resource ` + GuideURI + ` (the whole script format); write a script; validate_script; load_script; client_env gives the environment and command to run 'claude -p' against the server; after the run, list_sessions and read_log (outcome: divergence) or explain show which step answered each request and, for a divergence, what the script expected and what the request carried; fix the script and load_script again; reset clears the log between runs. A request held by stall: {gate: NAME} waits until release_gate. record turns a real session's transcript into a script.`

// New makes the MCP server with every tool and resource.
func New(o Options) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "understudy", Title: "understudy", Version: version.String()}, &mcp.ServerOptions{Instructions: Instructions})
	t := &tools{c: o.Control, dir: o.Dir}
	add(s, "validate_script", "Check an understudy script (YAML, schema understudy/v1) without loading it. Give yaml (the text) or file (a path). "+
		"Answers {ok, sessions, problems: [{path, line, rule, message, fix}]}; ok false means fix each problem as its fix says, then validate again. "+
		"The format is in the resource "+GuideURI+".", nil, t.validate)
	add(s, "load_script", "Load a script into the running understudy server, replacing the one loaded; the log is kept (call reset for a clean run). "+
		"Give yaml or file. Answers {loaded, sessions, server_url}. A script that does not check is not loaded: the error result lists problems with fixes. "+
		"Next: client_env, then run claude -p.", nil, t.load)
	add(s, "client_env", "The environment and command that run a real harness against the understudy server, with dummy credentials only: Claude Code ('claude -p') by default, "+
		"or give harness (codex, opencode, pi, gemini-cli, antigravity) to get its command, with its config files written into a fresh home folder. "+
		"Give session to send the X-Understudy-Session header (the script's session key; without it requests are in session default). "+
		"auth gateway makes usage-limit replies show as subscription limits ('You've hit your weekly limit'); api_key (default) shows them as 429 errors. "+
		"Answers {env, unset, shell, command}: run shell in a POSIX shell from the directory the session works in.", func(sc *jsonschema.Schema) {
		enum(sc, "auth", "api_key", "api_key", "gateway")
	}, t.clientEnv)
	add(s, "list_sessions", "Every session the server has seen or the loaded script names, with its state: idle (no request yet), waiting (the client owes the next request, such as tool results), "+
		"streaming, stalled (held by a stall; gate names the gate to release), diverged (the last request did not fit the script) or done (the client got a final answer or a limit). "+
		"Also the turn, the step that answered, request and divergence counts, and the gates.", nil, t.sessions)
	add(s, "read_log", "The request log of the running server, newest last. Filters: session, outcome (served, pending, diverged, no_script, bad_request, cut_close, cut_reset, client_gone, other, "+
		"or divergence for every request that diverged, strict or lenient), step, since_seq (only requests after this seq), limit (newest N, default 50). "+
		"detail summary (default) gives per request: seq, session, turn, step, branch, attempt, outcome, the answer sent, the prompt (cut), divergence with expected and actual; "+
		"full adds the tool results of every turn, the trace of every test and the headers.", func(sc *jsonschema.Schema) {
		enum(sc, "outcome", "", "", "served", "pending", "diverged", "no_script", "bad_request", "cut_close", "cut_reset", "client_gone", "other", "divergence")
		enum(sc, "detail", "summary", "summary", "full")
		bound(sc, "limit", 1, 1000)
		bound(sc, "since_seq", 0, 1<<31-1)
	}, t.readLog)
	add(s, "explain", "Why each /v1/messages request got its answer: the session, turn, step and case, every test that ran and why it passed or failed, and, for a divergence, "+
		"what the script expected and what the request carried. Give session to see one. Give script_yaml or script_file to also see what that (edited) script would choose "+
		"for the same requests, before loading it.", nil, t.explain)
	add(s, "release_gate", "Release a gate: every request held by stall: {gate: NAME} goes on, and later stalls on it pass at once until reset. Answers {released, waiting} "+
		"(waiting is how many requests were held). list_sessions shows which session waits on which gate.", nil, t.release)
	add(s, "reset", "Forget the request log, the retry counts and the gates (held requests are released). The loaded script stays. Use between runs.", nil, t.reset)
	add(s, "record", "Turn a real Claude Code session into a script: give transcript_file, the stream-json output of 'claude -p --output-format stream-json --verbose' "+
		"or a session file (~/.claude/projects/*/*.jsonl). Each step expects what the recording saw, so a replay that differs diverges. "+
		"session is the key the script answers under (default default); divergence strict (default) or lenient; out_file writes the script; then load also loads it. "+
		"Answers {yaml, steps, retries, cli_version, notes, written_to, loaded}.", func(sc *jsonschema.Schema) {
		enum(sc, "divergence", "strict", "strict", "lenient")
		enum(sc, "then", "show", "show", "load")
	}, t.record)
	s.AddResource(&mcp.Resource{URI: GuideURI, Name: "SCRIPTS.md", Title: "Writing understudy scripts", MIMEType: "text/markdown",
		Description: "The one-page guide to the script format: shape, replies, faults, retries, branches, divergence, five complete scripts."},
		text(GuideURI, "text/markdown", understudy.ScriptsGuide))
	s.AddResource(&mcp.Resource{URI: SchemaURI, Name: "understudy-v1.schema.json", Title: "understudy script JSON Schema", MIMEType: "application/schema+json",
		Description: "The JSON Schema of understudy/v1 scripts, for editors and validators."},
		text(SchemaURI, "application/schema+json", string(script.SchemaJSON)))
	return s
}

func text(uri, mime, body string) mcp.ResourceHandler {
	return func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: mime, Text: body}}}, nil
	}
}

// add registers a tool whose input schema is inferred from In and then
// adjusted. The arguments are checked here, not by the SDK, so that a wrong
// argument gets the same {ok, error, fix} answer as every other failure.
func add[In any](s *mcp.Server, name, desc string, adjust func(*jsonschema.Schema), h func(context.Context, In) (any, *failure)) {
	sc, err := jsonschema.For[In](nil)
	if err != nil {
		panic(fmt.Sprintf("mcpserver: schema of %s: %v", name, err))
	}
	if adjust != nil {
		adjust(sc)
	}
	sc.AdditionalProperties = &jsonschema.Schema{Not: &jsonschema.Schema{}}
	rs, err := sc.Resolve(nil)
	if err != nil {
		panic(fmt.Sprintf("mcpserver: schema of %s: %v", name, err))
	}
	shape := argsShape(sc)
	s.AddTool(&mcp.Tool{Name: name, Description: desc, InputSchema: sc}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var out any
		var f *failure
		args := map[string]any{}
		raw := req.Params.Arguments
		if len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &args); err != nil {
				f = fail("the arguments are not a JSON object: "+err.Error(), "send the arguments as an object: "+shape)
			}
		}
		if f == nil {
			if err := rs.Validate(args); err != nil {
				f = fail("the arguments do not fit the tool: "+strings.TrimPrefix(err.Error(), "validating root: "), "send "+shape)
			}
		}
		if f == nil {
			var in In
			b, _ := json.Marshal(args)
			if err := json.Unmarshal(b, &in); err != nil {
				f = fail("the arguments do not fit the tool: "+err.Error(), "send "+shape)
			} else {
				out, f = h(ctx, in)
			}
		}
		if f != nil {
			b, _ := json.MarshalIndent(f, "", "  ")
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}, StructuredContent: f}, nil
		}
		b, err := json.Marshal(out)
		if err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}, StructuredContent: json.RawMessage(b)}, nil
	})
}

// argsShape describes a tool's arguments in one line, for fix-it messages.
func argsShape(sc *jsonschema.Schema) string {
	if len(sc.Properties) == 0 {
		return "no arguments ({})"
	}
	req := map[string]bool{}
	for _, r := range sc.Required {
		req[r] = true
	}
	names := make([]string, 0, len(sc.Properties))
	for n := range sc.Properties {
		names = append(names, n)
	}
	sort.Strings(names)
	var parts []string
	for _, n := range names {
		p := sc.Properties[n]
		d := n + ": " + p.Type
		if len(p.Enum) > 0 {
			var vs []string
			for _, v := range p.Enum {
				if v != "" {
					vs = append(vs, fmt.Sprint(v))
				}
			}
			d = n + ": one of " + strings.Join(vs, ", ")
		}
		if p.Minimum != nil && p.Maximum != nil {
			d += fmt.Sprintf(" from %g to %g", *p.Minimum, *p.Maximum)
		}
		if req[n] {
			d += " (required)"
		}
		parts = append(parts, d)
	}
	return "{" + strings.Join(parts, "; ") + "}"
}

func enum(sc *jsonschema.Schema, prop, def string, vals ...string) {
	p := sc.Properties[prop]
	for _, v := range vals {
		p.Enum = append(p.Enum, v)
	}
	if def != "" {
		p.Default = json.RawMessage(`"` + def + `"`)
	}
}

func bound(sc *jsonschema.Schema, prop string, lo, hi float64) {
	p := sc.Properties[prop]
	p.Minimum, p.Maximum = &lo, &hi
}

// failure is a tool's error result: what went wrong, how to fix it, and for scripts every problem.
type failure struct {
	OK       bool             `json:"ok"`
	Error    string           `json:"error"`
	Fix      string           `json:"fix"`
	Problems []script.Problem `json:"problems,omitempty"`
}

func fail(msg, fix string) *failure { return &failure{Error: msg, Fix: fix} }

func failFrom(err error) *failure {
	var e *control.Error
	if errors.As(err, &e) {
		return &failure{Error: e.Message, Fix: e.Fix, Problems: e.Problems}
	}
	return fail(err.Error(), "try again; if it keeps failing, check that the understudy server runs")
}

type tools struct {
	c   *control.Client
	dir string
}

func (t *tools) path(p string) string {
	if filepath.IsAbs(p) || t.dir == "" {
		return p
	}
	return filepath.Join(t.dir, p)
}

func (t *tools) read(p, what string) ([]byte, *failure) {
	b, err := os.ReadFile(t.path(p))
	if err != nil {
		return nil, fail(fmt.Sprintf("cannot read the %s %s: %v", what, p, err), "give an absolute path, or one relative to "+t.where()+"; or give the text instead")
	}
	return b, nil
}

func (t *tools) where() string {
	if t.dir != "" {
		return t.dir
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "the MCP server's working directory"
}

// ScriptSource is a script given as text or as a file.
type ScriptSource struct {
	YAML string `json:"yaml,omitempty" jsonschema:"the script text (YAML, schema understudy/v1). Give this or file, not both."`
	File string `json:"file,omitempty" jsonschema:"path of a script file, absolute or relative to the MCP server's working directory. Give this or yaml, not both."`
}

func (t *tools) source(in ScriptSource, yamlName, fileName string) ([]byte, *failure) {
	switch {
	case in.YAML != "" && in.File != "":
		return nil, fail("both "+yamlName+" and "+fileName+" are given", "give one of them")
	case in.YAML != "":
		return []byte(in.YAML), nil
	case in.File != "":
		return t.read(in.File, "script file")
	}
	return nil, fail("no script is given", "give "+yamlName+" (the script text) or "+fileName+" (a path)")
}

func (t *tools) validate(_ context.Context, in ScriptSource) (any, *failure) {
	src, f := t.source(in, "yaml", "file")
	if f != nil {
		return nil, f
	}
	return server.Validate(src), nil
}

type loaded struct {
	Loaded    bool     `json:"loaded"`
	Sessions  []string `json:"sessions"`
	ServerURL string   `json:"server_url"`
	Next      string   `json:"next"`
}

func (t *tools) load(ctx context.Context, in ScriptSource) (any, *failure) {
	src, f := t.source(in, "yaml", "file")
	if f != nil {
		return nil, f
	}
	if v := server.Validate(src); !v.OK {
		return nil, &failure{Error: "the script was not loaded: it has problems", Fix: "fix each problem as its fix says, then load again", Problems: v.Problems}
	}
	keys, err := t.c.Load(ctx, src)
	if err != nil {
		return nil, failFrom(err)
	}
	return loaded{Loaded: true, Sessions: keys, ServerURL: t.c.URL(),
		Next: "call client_env with one of these sessions, run the command it gives, then list_sessions or read_log"}, nil
}

// EnvIn is client_env's input.
type EnvIn struct {
	Session  string `json:"session,omitempty" jsonschema:"the session key to send as the X-Understudy-Session header, one of the script's sessions; empty sends none (session default)"`
	Auth     string `json:"auth,omitempty" jsonschema:"api_key (default) or gateway (usage limits show as subscription limits); Claude Code only"`
	Harness  string `json:"harness,omitempty" jsonschema:"the harness to run: claude-code (default), codex, opencode, pi, gemini-cli or antigravity"`
	Protocol string `json:"protocol,omitempty" jsonschema:"for a harness that speaks several protocols (opencode, pi): anthropic, openai-chat, openai-responses or gemini; empty is its usual one"`
	Dir      string `json:"dir,omitempty" jsonschema:"the working directory of the run (for harnesses other than Claude Code); empty is the current directory"`
}

type envOut struct {
	ServerURL string            `json:"server_url"`
	Env       map[string]string `json:"env"`
	Unset     []string          `json:"unset"`
	Shell     string            `json:"shell"`
	Command   string            `json:"command"`
	Note      string            `json:"note"`
}

func (t *tools) clientEnv(_ context.Context, in EnvIn) (any, *failure) {
	if in.Session != "" && !script.ValidSessionKey(in.Session) {
		return nil, fail(fmt.Sprintf("%q is not a session key", in.Session), "use the key as the script names it: 1-128 letters, digits and . _ : / @ + -")
	}
	if in.Harness != "" && in.Harness != string(harness.ClaudeCode) {
		return t.harnessEnv(in)
	}
	a := clienv.APIKey
	switch in.Auth {
	case "", "api_key":
	case "gateway":
		a = clienv.Gateway
	default:
		return nil, fail(fmt.Sprintf("unknown auth %q", in.Auth), "use api_key or gateway")
	}
	out := envOut{ServerURL: t.c.URL(), Env: map[string]string{}, Unset: clienv.Cleared}
	var b strings.Builder
	for _, k := range clienv.Cleared {
		fmt.Fprintf(&b, "unset %s\n", k)
	}
	for _, kv := range clienv.Env(t.c.URL(), in.Session, a) {
		k, v, _ := strings.Cut(kv, "=")
		out.Env[k] = v
		fmt.Fprintf(&b, "export %s='%s'\n", k, strings.ReplaceAll(v, "'", `'\''`))
	}
	b.WriteString("export CLAUDE_CONFIG_DIR=\"$(mktemp -d)\"\n")
	out.Shell = b.String()
	out.Command = `claude -p --output-format stream-json --verbose --dangerously-skip-permissions "YOUR PROMPT" </dev/null`
	out.Note = "run shell, then command, in the session's working directory (the script's {{cwd}}). CLAUDE_CONFIG_DIR is an empty folder so the CLI never reads a real login; " +
		"--dangerously-skip-permissions lets scripted Edit, Write and Bash calls run. The final stream-json line is the result; then call list_sessions or read_log."
	return out, nil
}

// harnessEnv is client_env for a harness other than Claude Code: its config
// files are written into a new home folder, and the command runs it there.
func (t *tools) harnessEnv(in EnvIn) (any, *failure) {
	pr, ok := harness.Lookup(in.Harness)
	if !ok || pr.ID == harness.Generic {
		return nil, fail(fmt.Sprintf("there is no harness %q", in.Harness), "use one of: "+strings.Join(harnessNames(), ", "))
	}
	var p protocol.ID
	if in.Protocol != "" {
		if p, ok = protocol.Parse(in.Protocol); !ok || !pr.Speaks(p) {
			return nil, fail(fmt.Sprintf("%s does not speak %q", pr.ID, in.Protocol), "leave protocol out, or use one the harness speaks")
		}
	}
	home, err := os.MkdirTemp("", "understudy-"+string(pr.ID)+"-home-")
	if err != nil {
		return nil, fail("cannot make a home folder: "+err.Error(), "free some disk space")
	}
	dir := in.Dir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if pr.Launch.EnvVar != "" && os.Getenv(pr.Launch.EnvVar) == "" {
		if _, err := exec.LookPath(pr.Launch.Binary); err != nil {
			os.Setenv(pr.Launch.EnvVar, pr.Launch.Binary) // set up anyway; the command names the usual binary
		}
	}
	s, err := pr.Prepare(harness.Target{URL: t.c.URL(), Session: in.Session, Home: home, Dir: dir, Prompt: "YOUR PROMPT", Protocol: p})
	if err != nil {
		return nil, fail(err.Error(), "install the harness, or set "+pr.Launch.EnvVar+" to its binary")
	}
	for path, content := range s.Files {
		if os.MkdirAll(filepath.Dir(path), 0o755) != nil || os.WriteFile(path, []byte(content), 0o644) != nil {
			return nil, fail("cannot write "+path, "check the temporary folder is writable")
		}
	}
	out := envOut{ServerURL: t.c.URL(), Env: map[string]string{}, Unset: []string{}}
	var b strings.Builder
	var env []string
	for _, kv := range s.Env {
		k, v, _ := strings.Cut(kv, "=")
		out.Env[k] = v
		env = append(env, k+"='"+strings.ReplaceAll(v, "'", `'\''`)+"'")
	}
	b.WriteString("# the config files are written under " + home + "\n")
	out.Shell = b.String()
	args := []string{"'" + strings.ReplaceAll(s.Binary, "'", `'\''`) + "'"}
	for _, a := range s.Args {
		args = append(args, "'"+strings.ReplaceAll(a, "'", `'\''`)+"'")
	}
	out.Command = "env -i " + strings.Join(env, " ") + " " + strings.Join(args, " ") + " </dev/null"
	out.Note = "run command from " + dir + " (replace YOUR PROMPT); it starts " + pr.Name + " in an empty environment with dummy keys and a home folder of its own, so it never reads a real login."
	if pr.Launch.SessionInPrompt {
		out.Note += " " + pr.Name + " sends no custom headers: the session marker is in the prompt."
	}
	return out, nil
}

func harnessNames() []string {
	var out []string
	for _, pr := range harness.All() {
		out = append(out, string(pr.ID))
	}
	return out
}

type sessionsOut struct {
	ServerURL    string                 `json:"server_url"`
	ScriptLoaded bool                   `json:"script_loaded"`
	Sessions     []server.SessionStatus `json:"sessions"`
	Gates        []server.GateStatus    `json:"gates"`
}

// None is the input of tools that take none.
type None struct{}

func (t *tools) sessions(ctx context.Context, _ None) (any, *failure) {
	h, err := t.c.Health(ctx)
	if err != nil {
		return nil, failFrom(err)
	}
	ss, err := t.c.Sessions(ctx)
	if err != nil {
		return nil, failFrom(err)
	}
	gs, err := t.c.Gates(ctx)
	if err != nil {
		return nil, failFrom(err)
	}
	return sessionsOut{ServerURL: t.c.URL(), ScriptLoaded: h.ScriptLoaded, Sessions: ss, Gates: gs}, nil
}

// LogIn is read_log's input.
type LogIn struct {
	Session  string `json:"session,omitempty" jsonschema:"only this session's requests"`
	Outcome  string `json:"outcome,omitempty" jsonschema:"only requests that ended so; divergence: every request that diverged, strict or lenient"`
	Step     string `json:"step,omitempty" jsonschema:"only requests answered by the step of this name"`
	SinceSeq int    `json:"since_seq,omitempty" jsonschema:"only requests whose seq is greater than this"`
	Limit    int    `json:"limit,omitempty" jsonschema:"at most this many requests, the newest (default 50)"`
	Detail   string `json:"detail,omitempty" jsonschema:"summary (default) or full"`
}

// MaxSummaryPrompt is how much of a request's prompt a summary shows.
const MaxSummaryPrompt = 400

type logSummary struct {
	Seq        int             `json:"seq"`
	At         string          `json:"at"`
	Path       string          `json:"path"`
	Status     int             `json:"status,omitempty"`
	Outcome    server.Outcome  `json:"outcome"`
	Session    string          `json:"session,omitempty"`
	Turn       int             `json:"turn"`
	Step       string          `json:"step,omitempty"`
	Branch     string          `json:"branch,omitempty"`
	Attempt    int             `json:"attempt,omitempty"`
	Prompt     string          `json:"prompt,omitempty"`
	Results    []resultSummary `json:"tool_results,omitempty"`
	Answer     *server.Answer  `json:"answer,omitempty"`
	Held       *server.Hold    `json:"held,omitempty"`
	Divergence string          `json:"divergence,omitempty"`
	Expected   []string        `json:"expected,omitempty"`
	Actual     []string        `json:"actual,omitempty"`
	Error      string          `json:"error,omitempty"`
}

type resultSummary struct {
	Tool    string `json:"tool,omitempty"`
	IsError bool   `json:"is_error,omitempty"`
	Text    string `json:"text"`
}

type logOut struct {
	Total    int   `json:"total"`
	Shown    int   `json:"shown"`
	Requests []any `json:"requests"`
}

func (t *tools) readLog(ctx context.Context, in LogIn) (any, *failure) {
	es, err := t.c.Requests(ctx, in.Session)
	if err != nil {
		return nil, failFrom(err)
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	var keep []server.Entry
	for _, e := range es {
		switch {
		case e.Seq <= in.SinceSeq:
		case in.Step != "" && e.Step != in.Step:
		case in.Outcome == "divergence" && e.Divergence == "" && e.Outcome != server.Diverged && e.Outcome != server.NoScript:
		case in.Outcome != "" && in.Outcome != "divergence" && string(e.Outcome) != in.Outcome:
		case in.Outcome != "other" && e.Outcome == server.Other:
		default:
			keep = append(keep, e)
		}
	}
	total := len(keep)
	if len(keep) > limit {
		keep = keep[len(keep)-limit:]
	}
	out := logOut{Total: total, Shown: len(keep), Requests: []any{}}
	for _, e := range keep {
		if in.Detail == "full" {
			out.Requests = append(out.Requests, e)
			continue
		}
		s := logSummary{Seq: e.Seq, At: e.At, Path: e.Path, Status: e.Status, Outcome: e.Outcome, Session: e.Session, Turn: e.Turn, Step: e.Step,
			Branch: e.Branch, Attempt: e.Attempt, Answer: e.Answer, Held: e.Held, Divergence: e.Divergence, Expected: e.Expected, Actual: e.Actual}
		if e.Error != "" && !strings.HasSuffix(e.Error, e.Divergence) {
			s.Error = e.Error
		}
		s.Prompt = e.Prompt
		if len(s.Prompt) > MaxSummaryPrompt {
			s.Prompt = s.Prompt[:MaxSummaryPrompt] + "..."
		}
		if n := len(e.History); n > 0 && e.Turn < n {
			for _, r := range e.History[e.Turn] {
				txt := r.Text
				if len(txt) > MaxSummaryPrompt {
					txt = txt[:MaxSummaryPrompt] + "..."
				}
				s.Results = append(s.Results, resultSummary{Tool: r.Tool, IsError: r.IsError, Text: txt})
			}
		}
		out.Requests = append(out.Requests, s)
	}
	return out, nil
}

// ExplainIn is explain's input.
type ExplainIn struct {
	Session    string `json:"session,omitempty" jsonschema:"only this session"`
	ScriptYAML string `json:"script_yaml,omitempty" jsonschema:"also choose each request again with this script text (it is not loaded)"`
	ScriptFile string `json:"script_file,omitempty" jsonschema:"also choose each request again with the script in this file (it is not loaded)"`
}

type explainOut struct {
	Summary  string                `json:"summary"`
	Requests []explain.Explanation `json:"requests"`
}

func (t *tools) explain(ctx context.Context, in ExplainIn) (any, *failure) {
	var sc *script.Script
	if in.ScriptYAML != "" || in.ScriptFile != "" {
		src, f := t.source(ScriptSource{YAML: in.ScriptYAML, File: in.ScriptFile}, "script_yaml", "script_file")
		if f != nil {
			return nil, f
		}
		var err error
		if sc, err = script.Parse(src); err != nil {
			var ps script.Problems
			errors.As(err, &ps)
			return nil, &failure{Error: "the script to compare with has problems", Fix: "fix each problem as its fix says", Problems: ps}
		}
	}
	es, err := t.c.Requests(ctx, in.Session)
	if err != nil {
		return nil, failFrom(err)
	}
	xs := explain.Entries(es, sc)
	if xs == nil {
		xs = []explain.Explanation{}
	}
	return explainOut{Summary: explain.Summary(xs), Requests: xs}, nil
}

// GateIn is release_gate's input.
type GateIn struct {
	Gate string `json:"gate" jsonschema:"the gate name, as in the script's stall: {gate: NAME}"`
}

type releaseOut struct {
	Released string `json:"released"`
	Waiting  int    `json:"waiting"`
	Note     string `json:"note,omitempty"`
}

func (t *tools) release(ctx context.Context, in GateIn) (any, *failure) {
	n, err := t.c.Release(ctx, in.Gate)
	if err != nil {
		return nil, failFrom(err)
	}
	out := releaseOut{Released: in.Gate, Waiting: n}
	if n == 0 {
		out.Note = "no request was waiting on this gate; it is open now, so a later stall on it passes at once (reset closes it again)"
	}
	return out, nil
}

func (t *tools) reset(ctx context.Context, _ None) (any, *failure) {
	if err := t.c.Reset(ctx); err != nil {
		return nil, failFrom(err)
	}
	return map[string]any{"reset": true, "note": "the log, retry counts and gates are cleared; the script stays loaded"}, nil
}

// RecordIn is record's input.
type RecordIn struct {
	TranscriptFile string `json:"transcript_file" jsonschema:"the transcript: stream-json output of claude -p --output-format stream-json --verbose, or a Claude Code session file (.jsonl)"`
	Session        string `json:"session,omitempty" jsonschema:"the session key the script answers under (default: default)"`
	Divergence     string `json:"divergence,omitempty" jsonschema:"what a replay does when it differs: strict (default, stop) or lenient (log and go on)"`
	OutFile        string `json:"out_file,omitempty" jsonschema:"write the script to this file too"`
	Then           string `json:"then,omitempty" jsonschema:"show (default): only give the script; load: also load it into the server"`
}

type recordOut struct {
	YAML       string   `json:"yaml"`
	Steps      int      `json:"steps"`
	Retries    int      `json:"retries"`
	CLIVersion string   `json:"cli_version,omitempty"`
	Notes      []string `json:"notes,omitempty"`
	WrittenTo  string   `json:"written_to,omitempty"`
	Loaded     bool     `json:"loaded"`
	Sessions   []string `json:"sessions,omitempty"`
}

func (t *tools) record(ctx context.Context, in RecordIn) (any, *failure) {
	if in.TranscriptFile == "" {
		return nil, fail("transcript_file is not given", "give the path of a stream-json transcript or a Claude Code session file")
	}
	session := in.Session
	if session == "" {
		session = "default"
	}
	if !script.ValidSessionKey(session) {
		return nil, fail(fmt.Sprintf("%q is not a session key", session), "use 1-128 letters, digits and . _ : / @ + -, such as t1/writer")
	}
	m := record.Meta{SessionKey: session, Divergence: script.Strict}
	switch in.Divergence {
	case "", "strict":
	case "lenient":
		m.Divergence = script.Lenient
	default:
		return nil, fail(fmt.Sprintf("unknown divergence %q", in.Divergence), "use strict or lenient")
	}
	switch in.Then {
	case "", "show", "load":
	default:
		return nil, fail(fmt.Sprintf("unknown then %q", in.Then), "use show or load")
	}
	f, err := os.Open(t.path(in.TranscriptFile))
	if err != nil {
		return nil, fail(fmt.Sprintf("cannot read the transcript %s: %v", in.TranscriptFile, err), "give an absolute path, or one relative to "+t.where())
	}
	defer f.Close()
	file, rep, err := record.FromTranscript(f, m)
	if err != nil {
		return nil, fail("cannot record the transcript: "+err.Error(), "give the stream-json output of claude -p --output-format stream-json --verbose, or a session file from ~/.claude/projects")
	}
	b, err := script.Marshal(file)
	if err != nil {
		return nil, fail(err.Error(), "report this as a bug")
	}
	out := recordOut{YAML: string(b), Steps: rep.Steps, Retries: rep.Retries, CLIVersion: rep.CLIVersion, Notes: rep.Notes}
	if in.OutFile != "" {
		p := t.path(in.OutFile)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return nil, fail(fmt.Sprintf("cannot write %s: %v", in.OutFile, err), "give a path in a folder that exists and can be written")
		}
		out.WrittenTo = p
	}
	if in.Then == "load" {
		keys, err := t.c.Load(ctx, b)
		if err != nil {
			return nil, failFrom(err)
		}
		out.Loaded, out.Sessions = true, keys
	}
	return out, nil
}
