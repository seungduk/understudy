package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	understudy "github.com/seungduk/understudy"
	"github.com/seungduk/understudy/control"
	"github.com/seungduk/understudy/internal/version"
	"github.com/seungduk/understudy/script"
	"github.com/seungduk/understudy/server"
)

const good = `schema: understudy/v1
sessions:
  t1/writer:
    steps:
      - name: read
        reply: {tool_use: {calls: [{tool: Read, input: {file_path: "{{cwd}}/a.txt"}}]}}
      - name: done
        expect: {tool: Read, result: ok}
        reply: {message: {text: DONE}}
  t2/gated:
    steps:
      - {name: wait, reply: {message: {text: released}, stall: {gate: review}}}
`

type rig struct {
	t   *testing.T
	s   *server.Server
	url string
	cs  *mcp.ClientSession
	dir string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	s := server.New(server.Options{})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	t.Cleanup(s.Reset) // release held requests before the server closes
	c, err := control.New(srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ms := New(Options{Control: c, Dir: dir})
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := ms.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return &rig{t: t, s: s, url: srv.URL, cs: cs, dir: dir}
}

// call calls a tool and decodes its JSON answer; isErr is the result's IsError.
func (r *rig) call(name string, args map[string]any) (map[string]any, bool) {
	r.t.Helper()
	res, err := r.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		r.t.Fatalf("%s: protocol error %v", name, err)
	}
	if len(res.Content) != 1 {
		r.t.Fatalf("%s: %d content blocks", name, len(res.Content))
	}
	txt := res.Content[0].(*mcp.TextContent).Text
	var out map[string]any
	if err := json.Unmarshal([]byte(txt), &out); err != nil {
		r.t.Fatalf("%s: not JSON: %s", name, txt)
	}
	if res.IsError {
		if out["error"] == "" || out["fix"] == "" || out["ok"] != false {
			r.t.Fatalf("%s: an error result needs ok false, error and fix: %s", name, txt)
		}
	}
	return out, res.IsError
}

func (r *rig) write(name, body string) string {
	p := filepath.Join(r.dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
	return p
}

// post sends a Claude-Code-like request for session at turn len(results).
func (r *rig) post(session string, results ...string) *http.Response {
	r.t.Helper()
	msgs := []any{map[string]any{"role": "user", "content": "do it\n- Primary working directory: /w"}}
	for i, res := range results {
		id := "toolu_" + string(rune('a'+i))
		msgs = append(msgs, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": id, "name": "Read", "input": map[string]any{}}}},
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "is_error": res == "ERR", "content": res}}})
	}
	b, _ := json.Marshal(map[string]any{"model": "m", "stream": true, "messages": msgs, "tools": []any{map[string]any{"name": "Read"}}})
	req, _ := http.NewRequest("POST", r.url+"/v1/messages", strings.NewReader(string(b)))
	req.Header.Set("X-Understudy-Session", session)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestListsToolsAndResources(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	var names []string
	for tool, err := range r.cs.Tools(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, tool.Name)
		if len(tool.Description) < 80 {
			t.Errorf("%s: the description is too short to guide an agent: %q", tool.Name, tool.Description)
		}
	}
	want := "client_env explain list_sessions load_script read_log record release_gate reset validate_script"
	if strings.Join(names, " ") != want {
		t.Fatalf("tools %q", names)
	}
	init := r.cs.InitializeResult()
	if !strings.Contains(init.Instructions, GuideURI) || init.ServerInfo.Version != version.String() {
		t.Fatalf("%+v", init)
	}
	for uri, want := range map[string]string{GuideURI: understudy.ScriptsGuide, SchemaURI: string(script.SchemaJSON)} {
		res, err := r.cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err != nil || len(res.Contents) != 1 || res.Contents[0].Text != want || want == "" {
			t.Fatalf("%s: %v", uri, err)
		}
	}
	n := 0
	for _, err := range r.cs.Resources(ctx, nil) {
		if err != nil {
			t.Fatal(err)
		}
		n++
	}
	if n != 2 {
		t.Fatal(n)
	}
}

func TestValidateScript(t *testing.T) {
	r := newRig(t)
	out, isErr := r.call("validate_script", map[string]any{"yaml": good})
	if isErr || out["ok"] != true || len(out["sessions"].([]any)) != 2 || len(out["problems"].([]any)) != 0 {
		t.Fatal(out)
	}
	bad := r.write("bad.yaml", strings.Replace(good, "text: DONE", "txt: DONE", 1))
	out, isErr = r.call("validate_script", map[string]any{"file": "bad.yaml"}) // relative to Dir
	ps := out["problems"].([]any)
	p := ps[0].(map[string]any)
	if isErr || out["ok"] != false || p["rule"] != "unknown_field" || !strings.Contains(p["fix"].(string), "did you mean text?") || p["line"] != float64(9) {
		t.Fatal(out)
	}
	if out, _ = r.call("validate_script", map[string]any{"file": bad}); out["ok"] != false {
		t.Fatal("absolute path", out)
	}
	for _, args := range []map[string]any{{}, {"yaml": good, "file": bad}, {"file": "missing.yaml"}} {
		if out, isErr := r.call("validate_script", args); !isErr {
			t.Errorf("%v: %v", args, out)
		}
	}
	if out, isErr := r.call("validate_script", map[string]any{"yml": good}); !isErr || out["fix"] != "send {file: string; yaml: string}" {
		t.Error("an unknown argument is refused with the shape of the arguments", out)
	}
}

func TestLoadScript(t *testing.T) {
	r := newRig(t)
	out, isErr := r.call("load_script", map[string]any{"yaml": "schema: understudy/v1\nsessions: {}\n"})
	if !isErr || len(out["problems"].([]any)) == 0 || r.s.Script() != nil {
		t.Fatal(out)
	}
	out, isErr = r.call("load_script", map[string]any{"file": r.write("g.yaml", good)})
	if isErr || out["loaded"] != true || out["server_url"] != r.url || !strings.Contains(out["next"].(string), "client_env") || r.s.Script() == nil {
		t.Fatal(out)
	}
	if _, isErr := r.call("load_script", map[string]any{}); !isErr {
		t.Fatal("no script")
	}
}

func TestClientEnv(t *testing.T) {
	r := newRig(t)
	out, isErr := r.call("client_env", map[string]any{"session": "t1/writer"})
	env := out["env"].(map[string]any)
	if isErr || env["ANTHROPIC_BASE_URL"] != r.url || env["ANTHROPIC_API_KEY"] == nil || env["ANTHROPIC_CUSTOM_HEADERS"] != "X-Understudy-Session: t1/writer" ||
		!strings.Contains(out["shell"].(string), "unset ANTHROPIC_AUTH_TOKEN") || !strings.Contains(out["shell"].(string), "CLAUDE_CONFIG_DIR") ||
		!strings.Contains(out["command"].(string), "claude -p --output-format stream-json") {
		t.Fatal(out)
	}
	out, _ = r.call("client_env", map[string]any{"auth": "gateway"})
	if env := out["env"].(map[string]any); env["ANTHROPIC_AUTH_TOKEN"] == nil || env["ANTHROPIC_API_KEY"] != nil || env["ANTHROPIC_CUSTOM_HEADERS"] != nil {
		t.Fatal(out)
	}
	if _, isErr := r.call("client_env", map[string]any{"session": "has space"}); !isErr {
		t.Fatal("bad key")
	}
	if _, isErr := r.call("client_env", map[string]any{"auth": "oauth"}); !isErr {
		t.Fatal("bad auth is refused by the schema")
	}
	// Another harness: its config is written into a fresh home, and the command runs it there.
	t.Setenv("UNDERSTUDY_PI", "/opt/pi")
	out, isErr = r.call("client_env", map[string]any{"harness": "pi", "protocol": "gemini", "session": "t1/writer", "dir": "/work"})
	if env, _ := out["env"].(map[string]any); isErr || !strings.Contains(out["command"].(string), "'/opt/pi' '-p' '--mode' 'json' '--model' 'us-gemini/fake-gemini'") ||
		env["PI_CODING_AGENT_DIR"] == nil || !strings.Contains(out["note"].(string), "/work") {
		t.Fatal(out)
	}
	models, err := os.ReadFile(out["env"].(map[string]any)["PI_CODING_AGENT_DIR"].(string) + "/models.json")
	if err != nil || !strings.Contains(string(models), r.url+"/v1beta") || !strings.Contains(string(models), "t1/writer") {
		t.Fatal(err, string(models))
	}
	for _, bad := range []map[string]any{{"harness": "nope"}, {"harness": "codex", "protocol": "gemini"}} {
		if _, isErr := r.call("client_env", bad); !isErr {
			t.Fatal(bad)
		}
	}
}

func TestSessionsLogExplainReleaseReset(t *testing.T) {
	r := newRig(t)
	r.call("load_script", map[string]any{"yaml": good})
	out, _ := r.call("list_sessions", nil)
	ss := out["sessions"].([]any)
	if out["script_loaded"] != true || len(ss) != 2 || ss[0].(map[string]any)["state"] != "idle" {
		t.Fatal(out)
	}
	r.post("t1/writer")
	r.post("t1/writer", "ERR") // done expects Read ok: strict divergence
	done := make(chan struct{})
	go func() { r.post("t2/gated"); close(done) }()
	for {
		out, _ = r.call("list_sessions", nil)
		if s := out["sessions"].([]any)[1].(map[string]any); s["state"] == "stalled" && s["gate"] == "review" {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if s := out["sessions"].([]any)[0].(map[string]any); s["state"] != "diverged" || s["divergences"] != float64(1) || s["step"] != "done" {
		t.Fatal(s)
	}
	if g := out["gates"].([]any)[0].(map[string]any); g["name"] != "review" || g["waiting"] != float64(1) {
		t.Fatal(g)
	}

	out, _ = r.call("read_log", map[string]any{"outcome": "divergence"})
	reqs := out["requests"].([]any)
	d := reqs[0].(map[string]any)
	if out["total"] != float64(1) || d["step"] != "done" || d["expected"].([]any)[0] != `step "done" expect {tool: Read, result: ok}` ||
		!strings.Contains(d["actual"].([]any)[0].(string), "(Read): error") || d["tool_results"].([]any)[0].(map[string]any)["is_error"] != true || d["history"] != nil {
		t.Fatal(out)
	}
	out, _ = r.call("read_log", map[string]any{"session": "t1/writer", "detail": "full", "limit": 1})
	if f := out["requests"].([]any)[0].(map[string]any); out["total"] != float64(2) || out["shown"] != float64(1) || f["seq"] != float64(2) || f["history"] == nil || f["trace"] == nil {
		t.Fatal(out)
	}
	out, _ = r.call("read_log", map[string]any{"step": "read"})
	if a := out["requests"].([]any)[0].(map[string]any)["answer"].(map[string]any); out["total"] != float64(1) || a["kind"] != "tool_use" ||
		a["calls"].([]any)[0].(map[string]any)["input"].(map[string]any)["file_path"] != "/w/a.txt" {
		t.Fatal(out)
	}
	if out, _ = r.call("read_log", map[string]any{"since_seq": 2}); out["total"] != float64(1) || out["requests"].([]any)[0].(map[string]any)["held"] == nil {
		t.Fatal(out)
	}
	if out, _ = r.call("read_log", map[string]any{"outcome": "pending"}); out["total"] != float64(1) {
		t.Fatal(out)
	}
	if out, _ = r.call("read_log", map[string]any{"outcome": "other"}); out["total"] != float64(0) {
		t.Fatal(out)
	}
	for _, bad := range []map[string]any{{"outcome": "weird"}, {"detail": "all"}, {"limit": 0}, {"limit": 5000}, {"session": 3}} {
		if out, isErr := r.call("read_log", bad); !isErr || !strings.Contains(out["fix"].(string), "detail: one of summary, full; limit: integer from 1 to 1000") {
			t.Errorf("%v: %v", bad, out)
		}
	}

	out, _ = r.call("explain", map[string]any{"session": "t1/writer"})
	xs := out["requests"].([]any)
	if out["summary"] != "2 request(s): served=1 diverged=1" || len(xs) != 2 || xs[1].(map[string]any)["expected"] == nil {
		t.Fatal(out)
	}
	fixed := strings.Replace(good, "expect: {tool: Read, result: ok}", "expect: {tool: Read, result: error}", 1)
	out, _ = r.call("explain", map[string]any{"session": "t1/writer", "script_yaml": fixed})
	if re := out["requests"].([]any)[1].(map[string]any)["reevaluated"].(map[string]any); re["step"] != "done" || re["divergence"] != nil {
		t.Fatal(out)
	}
	if out, isErr := r.call("explain", map[string]any{"script_yaml": "nope: 1"}); !isErr || out["problems"] == nil {
		t.Fatal(out)
	}
	if out, isErr := r.call("explain", map[string]any{"script_yaml": good, "script_file": "x"}); !isErr {
		t.Fatal(out)
	}

	out, isErr := r.call("release_gate", map[string]any{"gate": "review"})
	if isErr || out["released"] != "review" || out["waiting"] != float64(1) || out["note"] != nil {
		t.Fatal(out)
	}
	<-done
	if out, _ = r.call("release_gate", map[string]any{"gate": "later"}); out["waiting"] != float64(0) || !strings.Contains(out["note"].(string), "open now") {
		t.Fatal(out)
	}
	if out, isErr = r.call("release_gate", map[string]any{"gate": "Not A Gate"}); !isErr || !strings.Contains(out["fix"].(string), "stall: {gate: NAME}") {
		t.Fatal(out)
	}
	if _, isErr = r.call("release_gate", map[string]any{}); !isErr {
		t.Fatal("gate is required")
	}

	if out, isErr = r.call("reset", nil); isErr || out["reset"] != true || len(r.s.Requests()) != 0 || r.s.Script() == nil {
		t.Fatal(out)
	}
}

func TestRecord(t *testing.T) {
	r := newRig(t)
	tr, _ := filepath.Abs("../../record/testdata/toolloop-2.1.325.jsonl")
	out, isErr := r.call("record", map[string]any{"transcript_file": tr, "session": "t1/writer"})
	if isErr || out["steps"].(float64) < 2 || out["cli_version"] != "2.1.325" || out["loaded"] != false || !strings.Contains(out["yaml"].(string), "t1/writer:") {
		t.Fatal(out)
	}
	if v := server.Validate([]byte(out["yaml"].(string))); !v.OK {
		t.Fatalf("the recorded script does not check: %+v", v)
	}
	out, isErr = r.call("record", map[string]any{"transcript_file": tr, "divergence": "lenient", "out_file": "rec.yaml", "then": "load"})
	if isErr || out["loaded"] != true || out["written_to"] != filepath.Join(r.dir, "rec.yaml") || r.s.Script() == nil || r.s.Script().Divergence() != script.Lenient {
		t.Fatal(out)
	}
	if b, err := os.ReadFile(filepath.Join(r.dir, "rec.yaml")); err != nil || !strings.Contains(string(b), "default:") {
		t.Fatal(err)
	}
	for _, bad := range []map[string]any{{}, {"transcript_file": "missing.jsonl"}, {"transcript_file": tr, "session": "bad key"},
		{"transcript_file": r.write("empty.jsonl", "")}, {"transcript_file": tr, "out_file": "no/such/dir/x.yaml"},
		{"transcript_file": tr, "then": "save"}, {"transcript_file": tr, "divergence": "loose"}} {
		if out, isErr := r.call("record", bad); !isErr {
			t.Errorf("%v: %v", bad, out)
		}
	}
}

func TestServerDown(t *testing.T) {
	c, _ := control.New("http://127.0.0.1:1", nil)
	ms := New(Options{Control: c})
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ms.Connect(ctx, st, nil)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	r := &rig{t: t, cs: cs}
	for _, tool := range []string{"list_sessions", "read_log", "explain", "reset"} {
		if out, isErr := r.call(tool, nil); !isErr || !strings.Contains(out["fix"].(string), "understudy serve") {
			t.Errorf("%s: %v", tool, out)
		}
	}
	if out, isErr := r.call("load_script", map[string]any{"yaml": good}); !isErr || !strings.Contains(out["error"].(string), "cannot reach") {
		t.Error(out)
	}
	if out, isErr := r.call("release_gate", map[string]any{"gate": "g"}); !isErr {
		t.Error(out)
	}
	// Tools that need no server still work.
	if out, isErr := r.call("validate_script", map[string]any{"yaml": good}); isErr || out["ok"] != true {
		t.Error(out)
	}
}
