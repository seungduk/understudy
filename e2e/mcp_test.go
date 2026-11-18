//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/seungduk/understudy/clienv"
	"github.com/seungduk/understudy/control"
	"github.com/seungduk/understudy/mcpserver"
	"github.com/seungduk/understudy/server"
	us "github.com/seungduk/understudy/understudytest"
)

// TestMCPWorkflow is the agent's loop through the MCP tools only: load a
// script, run the real CLI with client_env's environment, find the divergence
// in read_log, fix the script, reset and run again.
func TestMCPWorkflow(t *testing.T) {
	bin, err := us.ClaudePath()
	if err != nil {
		t.Skip("the claude CLI is not installed")
	}
	s := server.New(server.Options{})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	defer s.Reset()
	c, _ := control.New(srv.URL, nil)
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := mcpserver.New(mcpserver.Options{Control: c}).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out)
		if res.IsError {
			t.Fatalf("%s: %v", name, out)
		}
		return out
	}
	wrong := `schema: understudy/v1
sessions:
  t1/checker:
    steps:
      - name: check
        reply: {tool_use: {calls: [{tool: Bash, input: {command: "test -f ready.txt || exit 1", description: Is it ready}}]}}
      - name: verdict
        expect: {tool: Bash, exit_code: 0}
        reply: {message: {text: "READY"}}
`
	if out := call("load_script", map[string]any{"yaml": wrong}); out["loaded"] != true {
		t.Fatal(out)
	}
	env := call("client_env", map[string]any{"session": "t1/checker"})["env"].(map[string]any)
	runOnce := func() string {
		var kv []string
		for k, v := range env {
			kv = append(kv, k+"="+v.(string))
		}
		cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(cctx, bin, "-p", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions", "check it")
		cmd.Dir = t.TempDir() // no ready.txt: the check exits 1
		cmd.Env = clienv.Merge(os.Environ(), append(kv, "CLAUDE_CONFIG_DIR="+t.TempDir()))
		b, _ := cmd.Output()
		return string(b)
	}
	if out := runOnce(); !strings.Contains(out, "does not fit the script") {
		t.Fatalf("the first run should diverge: %s", out)
	}
	log := call("read_log", map[string]any{"session": "t1/checker", "outcome": "divergence", "limit": 1})
	d := log["requests"].([]any)[0].(map[string]any)
	if d["step"] != "verdict" || !strings.Contains(d["actual"].([]any)[0].(string), "exit code 1") {
		t.Fatalf("%v", log)
	}
	ss := call("list_sessions", nil)["sessions"].([]any)
	if ss[0].(map[string]any)["state"] != "diverged" {
		t.Fatal(ss)
	}
	fixed := strings.Replace(wrong, "expect: {tool: Bash, exit_code: 0}\n        reply: {message: {text: \"READY\"}}",
		"cases:\n          - when: {tool: Bash, exit_code: 0}\n            reply: {message: {text: \"READY\"}}\n          - when: {tool: Bash, exit_code: 1}\n            reply: {message: {text: \"NOT READY\"}}", 1)
	if v := call("validate_script", map[string]any{"yaml": fixed}); v["ok"] != true {
		t.Fatal(v)
	}
	call("load_script", map[string]any{"yaml": fixed})
	call("reset", nil)
	if out := runOnce(); !strings.Contains(out, `"result":"NOT READY"`) {
		t.Fatalf("the fixed run: %s", out)
	}
	if ss := call("list_sessions", nil)["sessions"].([]any); ss[0].(map[string]any)["state"] != "done" {
		t.Fatal(ss)
	}
}
