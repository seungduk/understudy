package control

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/seungduk/understudy/internal/version"
	"github.com/seungduk/understudy/script"
	"github.com/seungduk/understudy/server"
)

const gated = `schema: understudy/v1
sessions:
  t1/w:
    steps:
      - {name: a, reply: {message: {text: hi}, stall: {gate: g}}}
`

func setup(t *testing.T) (*Client, *server.Server) {
	s := server.New(server.Options{})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	t.Cleanup(s.Reset)
	c, err := New(srv.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	return c, s
}

func TestNewRefusesNonURLs(t *testing.T) {
	for _, u := range []string{"", "127.0.0.1:8765", "ftp://x", "http://", "http://h/sub", "::"} {
		if _, err := New(u, nil); err == nil || !strings.Contains(err.Error(), "Fix:") {
			t.Errorf("%q: %v", u, err)
		}
	}
	c, err := New("https://h:1", nil)
	if err != nil || c.URL() != "https://h:1" {
		t.Fatal(c, err)
	}
}

func TestEveryCall(t *testing.T) {
	ctx := context.Background()
	c, s := setup(t)
	if h, err := c.Health(ctx); err != nil || !h.OK || h.ScriptLoaded || h.Version != version.String() {
		t.Fatal(h, err)
	}
	if _, err := c.Script(ctx); !errors.Is(err, ErrNoScript) {
		t.Fatal(err)
	}
	_, err := c.Load(ctx, []byte(strings.Replace(gated, "text:", "txt:", 1)))
	var e *Error
	if !errors.As(err, &e) || e.Status != 400 || len(e.Problems) == 0 || e.Problems[0].Rule != script.RuleUnknownField || !strings.Contains(err.Error(), "did you mean text?") {
		t.Fatalf("%#v", err)
	}
	keys, err := c.Load(ctx, []byte(gated))
	if err != nil || len(keys) != 1 || keys[0] != "t1/w" {
		t.Fatal(keys, err)
	}
	if b, err := c.Script(ctx); err != nil || string(b) != gated {
		t.Fatal(string(b), err)
	}
	go func() {
		req, _ := http.NewRequest("POST", c.URL()+"/v1/messages", strings.NewReader(`{"model":"m","stream":true,"messages":[{"role":"user","content":"x"}]}`))
		req.Header.Set("X-Understudy-Session", "t1/w")
		if r, err := http.DefaultClient.Do(req); err == nil {
			r.Body.Close()
		}
	}()
	for {
		gs, err := c.Gates(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(gs) == 1 && gs[0].Waiting == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	ss, err := c.Sessions(ctx)
	if err != nil || len(ss) != 1 || ss[0].State != server.StateStalled || ss[0].Gate != "g" {
		t.Fatal(ss, err)
	}
	if n, err := c.Release(ctx, "g"); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err := c.Release(ctx, "Bad Name"); !errors.As(err, &e) || !strings.Contains(e.Fix, "stall: {gate: NAME}") {
		t.Fatal(err)
	}
	for len(s.Requests()) == 0 || s.Requests()[0].Outcome != server.Served {
		time.Sleep(time.Millisecond)
	}
	if es, err := c.Requests(ctx, "t1/w"); err != nil || len(es) != 1 || es[0].Step != "a" {
		t.Fatal(es, err)
	}
	if es, err := c.Requests(ctx, "other"); err != nil || len(es) != 0 {
		t.Fatal(es, err)
	}
	if err := c.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if es, _ := c.Requests(ctx, ""); len(es) != 0 {
		t.Fatal(es)
	}
}

func TestErrors(t *testing.T) {
	ctx := context.Background()
	down, _ := New("http://127.0.0.1:1", nil)
	_, err := down.Health(ctx)
	var e *Error
	if !errors.As(err, &e) || e.Status != 0 || !strings.Contains(e.Fix, "understudy serve") {
		t.Fatal(err)
	}
	notUS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "health") {
			w.Write([]byte("<html>"))
			return
		}
		http.Error(w, "nope", http.StatusTeapot)
	}))
	defer notUS.Close()
	c, _ := New(notUS.URL, nil)
	if _, err := c.Health(ctx); !errors.As(err, &e) || !strings.Contains(e.Message, "did not answer JSON") {
		t.Fatal(err)
	}
	if _, err := c.Sessions(ctx); !errors.As(err, &e) || e.Status != 418 || !strings.Contains(e.Fix, "is an understudy server") {
		t.Fatal(err)
	}
	if err := c.Reset(ctx); err == nil {
		t.Fatal("reset on a non-understudy server")
	}
}
