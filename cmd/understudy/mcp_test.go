package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/seungduk/understudy/internal/ui"
	"github.com/seungduk/understudy/server"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// stdio runs cmdMCP with pipes and connects an MCP client to it.
func stdio(t *testing.T, args ...string) (*mcp.ClientSession, *syncBuf, chan int) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	errb := &syncBuf{}
	code := make(chan int, 1)
	go func() {
		code <- cmdMCP(args, inR, outW, errb)
		outW.Close()
	}()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil).Connect(context.Background(),
		&mcp.IOTransport{Reader: outR, Writer: inW}, nil)
	if err != nil {
		t.Fatalf("%v; stderr: %s", err, errb.String())
	}
	return cs, errb, code
}

func toolJSON(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMCPStartsItsOwnServer(t *testing.T) {
	cs, errb, code := stdio(t, "--url", "http://127.0.0.1:0", "--server", "start", "--script", "../../examples/1-text-only.yaml")
	out := toolJSON(t, cs, "list_sessions", nil)
	if out["script_loaded"] != true || !strings.Contains(errb.String(), "started an understudy server at http://127.0.0.1:") {
		t.Fatalf("%v %s", out, errb.String())
	}
	// The started server also serves the web UI.
	r, err := http.Get(out["server_url"].(string) + ui.Prefix)
	if err != nil || r.StatusCode != 200 {
		t.Fatal(err, r)
	}
	r.Body.Close()
	cs.Close()
	select {
	case c := <-code:
		if c != 0 {
			t.Fatalf("exit %d: %s", c, errb.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("understudy mcp did not stop when stdin closed")
	}
}

func TestMCPAttachesToARunningServer(t *testing.T) {
	s := server.New(server.Options{})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	cs, errb, _ := stdio(t, "--url", srv.URL)
	defer cs.Close()
	out := toolJSON(t, cs, "load_script", map[string]any{"file": "../../examples/2-edit-file.yaml"})
	if out["loaded"] != true || s.Script() == nil || !strings.Contains(errb.String(), "using the understudy server at "+srv.URL) {
		t.Fatalf("%v %s", out, errb.String())
	}
}

func TestMCPFlags(t *testing.T) {
	for _, c := range []struct {
		args []string
		code int
		msg  string
	}{
		{[]string{"--server", "maybe"}, 2, "use auto, attach or start"},
		{[]string{"--url", "localhost:8765"}, 2, "give the base URL only"},
		{[]string{"--url", "http://127.0.0.1:1", "--server", "attach"}, 1, "no understudy server answers"},
		{[]string{"--url", "http://127.0.0.1:0", "--server", "start", "--clock", "sundial"}, 2, "use real or virtual"},
		{[]string{"--url", "http://127.0.0.1:0", "--server", "start", "--script", "missing.yaml"}, 1, "cannot read"},
		{[]string{"--bogus"}, 2, "flag provided but not defined"},
	} {
		var e bytes.Buffer
		if got := cmdMCP(c.args, strings.NewReader(""), io.Discard, &e); got != c.code || !strings.Contains(e.String(), c.msg) {
			t.Errorf("%v: %d %s", c.args, got, e.String())
		}
	}
}

func TestMountUIAndMCPOverHTTP(t *testing.T) {
	s := server.New(server.Options{})
	srv := httptest.NewUnstartedServer(nil)
	self := "http://" + srv.Listener.Addr().String()
	h, err := mount(s, on, on, self)
	if err != nil {
		t.Fatal(err)
	}
	srv.Config.Handler = h
	srv.Start()
	defer srv.Close()
	for path, want := range map[string]int{ui.Prefix: 200, ui.Prefix + "app.js": 200, "/_understudy/ui": 301, "/_understudy/health": 200, "/v1/models": 200} {
		c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		r, err := c.Get(srv.URL + path)
		if err != nil || r.StatusCode != want {
			t.Errorf("%s: %v %v", path, err, r.StatusCode)
			continue
		}
		r.Body.Close()
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil).Connect(context.Background(),
		&mcp.StreamableClientTransport{Endpoint: srv.URL + MCPPath}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if out := toolJSON(t, cs, "validate_script", map[string]any{"yaml": good}); out["ok"] != true {
		t.Fatal(out)
	}
	if out := toolJSON(t, cs, "reset", nil); out["reset"] != true {
		t.Fatal(out)
	}

	// Off switches: the paths fall through to the control API's 404 with a fix.
	h, _ = mount(s, off, off, self)
	for _, p := range []string{ui.Prefix, MCPPath} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != 404 || !strings.Contains(rec.Body.String(), "fix") {
			t.Errorf("%s with the switch off: %d %s", p, rec.Code, rec.Body.String())
		}
	}
}

func TestServeFlags(t *testing.T) {
	if c, _, e := call("serve", "--ui", "maybe"); c != 2 || !strings.Contains(e, "use on or off") {
		t.Fatal(c, e)
	}
	if c, _, e := call("serve", "--mcp", "yes"); c != 2 || !strings.Contains(e, "--mcp") {
		t.Fatal(c, e)
	}
	if c, _, e := call("serve", "--clock", "x"); c != 2 || !strings.Contains(e, "use real or virtual") {
		t.Fatal(c, e)
	}
}
