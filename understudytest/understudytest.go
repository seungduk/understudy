package understudytest

import (
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/seungduk/understudy/script"
	"github.com/seungduk/understudy/server"
)

// Fake is a running server. It embeds the server, so Requests, Release,
// Advance, Gates, Load and Reset are at hand.
type Fake struct {
	*server.Server
	// URL is the base URL to give ANTHROPIC_BASE_URL.
	URL string
	t   testing.TB
}

// Option configures Start.
type Option func(testing.TB, *server.Options)

// Script loads a script made in Go.
func Script(s *script.Script) Option {
	return func(_ testing.TB, o *server.Options) { o.Script = s }
}

// ScriptFile loads a script file; problems fail the test.
func ScriptFile(path string) Option {
	return func(t testing.TB, o *server.Options) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("understudytest: %v", err)
		}
		s, err := server.Parse(data)
		if err != nil {
			t.Fatalf("understudytest: %s: %v", path, err)
		}
		o.Script = s
	}
}

// YAML loads a script from text; problems fail the test.
func YAML(src string) Option {
	return func(t testing.TB, o *server.Options) {
		t.Helper()
		s, err := server.Parse([]byte(src))
		if err != nil {
			t.Fatalf("understudytest: %v", err)
		}
		o.Script = s
	}
}

// VirtualClock runs the server on a virtual clock starting at start (zero: now).
func VirtualClock(start time.Time) Option {
	return func(_ testing.TB, o *server.Options) { o.Clock, o.Start = server.VirtualClock, start }
}

// Start runs a server on a free loopback port until the test ends.
func Start(t testing.TB, opts ...Option) *Fake {
	t.Helper()
	var o server.Options
	for _, op := range opts {
		op(t, &o)
	}
	s := server.New(o)
	hs := httptest.NewServer(s.Handler())
	t.Cleanup(hs.Close)
	return &Fake{Server: s, URL: hs.URL, t: t}
}

// WaitGate waits until n requests are stalled on gate, failing the test after timeout.
func (f *Fake) WaitGate(gate string, n int, timeout time.Duration) {
	f.t.Helper()
	until(f.t, timeout, "requests waiting on gate "+gate, func() bool {
		for _, g := range f.Gates() {
			if g.Name == gate && g.Waiting >= n {
				return true
			}
		}
		return false
	})
}

// WaitRequests waits until the log holds n requests to /v1/messages.
func (f *Fake) WaitRequests(n int, timeout time.Duration) {
	f.t.Helper()
	until(f.t, timeout, "requests in the log", func() bool { return len(f.Messages()) >= n })
}

// Messages is the log without the other endpoints (models, hello, ...).
func (f *Fake) Messages() []server.Entry {
	var out []server.Entry
	for _, e := range f.Requests() {
		if e.Outcome != server.Other {
			out = append(out, e)
		}
	}
	return out
}

func until(t testing.TB, timeout time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("understudytest: timed out after %v waiting for %s", timeout, what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TempDir is a fresh folder with files (name -> content) written into it.
func TempDir(t testing.TB, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(dir+"/"+name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
