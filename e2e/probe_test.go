//go:build probe

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/seungduk/understudy/script"
	"github.com/seungduk/understudy/server"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/seungduk/understudy/harness"
	"github.com/seungduk/understudy/protocol"
	us "github.com/seungduk/understudy/understudytest"
)

// TestProbe runs every action through one harness and prints what came back,
// to measure how the harness reports tool results. UNDERSTUDY_PROBE names the harness.
func TestProbe(t *testing.T) {
	id := harness.ID(os.Getenv("UNDERSTUDY_PROBE"))
	proto, _ := protocol.Parse(os.Getenv("UNDERSTUDY_PROBE_PROTOCOL"))
	f := start(t, `schema: understudy/v2
divergence: lenient
sessions:
  probe:
    steps:
      - name: fail
        reply: {tool_use: {text: "Running a failing command.", calls: [{shell: {command: "echo out; echo err >&2; exit 3"}}]}}
      - name: ok
        reply: {tool_use: {calls: [{shell: {command: "echo fine"}}]}}
      - name: write
        reply: {tool_use: {calls: [{write: {path: "{{cwd}}/new.txt", content: "alpha\nbeta\n"}}]}}
      - name: read
        reply: {tool_use: {calls: [{read: {path: "{{cwd}}/new.txt"}}]}}
      - name: edit
        reply: {tool_use: {calls: [{edit: {path: "{{cwd}}/new.txt", old: "alpha", new: "ALPHA"}}]}}
      - name: readmissing
        reply: {tool_use: {calls: [{read: {path: "{{cwd}}/missing.txt"}}]}}
      - name: done
        reply: {message: {thinking: "All probed.", text: "PROBE DONE"}}
`)
	dir := t.TempDir()
	o := us.Harness(t, f, id, us.HarnessRun{Dir: dir, Session: "probe", Protocol: proto, Prompt: "probe the tools [understudy-session: probe]", Timeout: 4 * time.Minute})
	b, _ := os.ReadFile(dir + "/new.txt")
	defer t.Logf("version %s exit %d timedout %v\nfile: %q\nstdout tail: %s\nstderr tail: %s", o.Version, o.Exit, o.TimedOut, b, tail(o.Stdout, 3000), tail(o.Stderr, 2000))
	es := f.Messages()
	for _, e := range es {
		t.Logf("#%d %s %s %s harness=%s aux=%s session=%s turn=%d step=%s outcome=%s status=%d cwd=%q div=%q ua=%q", e.Seq, e.Method, e.Path, e.Protocol, e.Harness, e.Aux, e.Session, e.Turn, e.Step, e.Outcome, e.Status, e.CWD, e.Divergence, e.Headers["user-agent"])
	}
	for i := len(es) - 1; i >= 0; i-- {
		if es[i].Aux == "" && es[i].Session != "" {
			h, _ := json.MarshalIndent(es[i].History, "", " ")
			t.Logf("history of #%d: %s", es[i].Seq, h)
			break
		}
	}
}

// start is us.Start that also saves every request body under $UNDERSTUDY_PROBE_DUMP.
func start(t *testing.T, src string) *us.Fake {
	sc, err := script.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	s := server.New(server.Options{Script: sc})
	dump := os.Getenv("UNDERSTUDY_PROBE_DUMP")
	n := 0
	var mu sync.Mutex
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if dump != "" {
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			n++
			os.WriteFile(fmt.Sprintf("%s/%02d.json", dump, n), b, 0o644)
			mu.Unlock()
			r.Body = io.NopCloser(bytes.NewReader(b))
		}
		s.Handler().ServeHTTP(w, r)
	}))
	t.Cleanup(hs.Close)
	return &us.Fake{Server: s, URL: hs.URL}
}

func tail(s string, n int) string {
	if len(s) > n {
		return "..." + s[len(s)-n:]
	}
	return s
}

// TestEditWithoutRead measures whether the harness edits a file it has not read.
func TestEditWithoutRead(t *testing.T) {
	id := harness.ID(os.Getenv("UNDERSTUDY_PROBE"))
	f := start(t, `schema: understudy/v2
divergence: lenient
sessions:
  probe:
    steps:
      - name: edit
        reply: {tool_use: {calls: [{edit: {path: "{{cwd}}/a.txt", old: "one", new: "two"}}]}}
      - name: done
        reply: {message: {text: "PROBE DONE"}}
`)
	dir := us.TempDir(t, map[string]string{"a.txt": "one\n"})
	us.Harness(t, f, id, us.HarnessRun{Dir: dir, Session: "probe", Prompt: "edit [understudy-session: probe]"})
	b, _ := os.ReadFile(dir + "/a.txt")
	es := f.Messages()
	h, _ := json.Marshal(es[len(es)-1].History)
	t.Logf("%s: file %q; results %s", id, b, h)
}
