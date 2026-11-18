package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/seungduk/understudy/script"
	"github.com/seungduk/understudy/server"
)

func call(args ...string) (int, string, string) {
	var o, e bytes.Buffer
	code := run(args, &o, &e)
	return code, o.String(), e.String()
}

const good = "schema: understudy/v1\nsessions:\n  default:\n    steps:\n      - {name: a, reply: {message: {text: hi}}}\n"

func TestValidate(t *testing.T) {
	dir := t.TempDir()
	g, b := filepath.Join(dir, "g.yaml"), filepath.Join(dir, "b.yaml")
	os.WriteFile(g, []byte(good), 0o644)
	os.WriteFile(b, []byte(strings.Replace(good, "reply:", "replly:", 1)), 0o644)
	if c, o, _ := call("validate", g); c != 0 || !strings.Contains(o, "ok (1 session(s): default)") {
		t.Fatal(c, o)
	}
	if c, o, _ := call("validate", b); c != 1 || !strings.Contains(o, "did you mean reply?") {
		t.Fatal(c, o)
	}
	c, o, _ := call("validate", b, "--json")
	var rep struct {
		OK       bool
		Problems []script.Problem
	}
	if c != 1 || json.Unmarshal([]byte(o), &rep) != nil || rep.OK || rep.Problems[0].Rule != script.RuleUnknownField || rep.Problems[0].Line != 5 {
		t.Fatal(c, o)
	}
	if c, o, _ := call("validate", "--json", g); c != 0 || !strings.Contains(o, `"problems": []`) || !strings.Contains(o, `"sessions"`) {
		t.Fatal(c, o)
	}
	if c, o, _ := call("validate", filepath.Join(dir, "missing.yaml")); c != 1 || !strings.Contains(o, "cannot read") {
		t.Fatal(c, o)
	}
	if c, _, e := call("validate"); c != 2 || !strings.Contains(e, "give the script file") {
		t.Fatal(c, e)
	}
	if c, _, _ := call("validate", g, "--bogus"); c != 2 {
		t.Fatal(c)
	}
}

func TestRecordExplainEnv(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "rec.yaml")
	c, _, e := call("record", "--transcript", "../../record/testdata/toolloop-2.1.325.jsonl", "--session", "t1/writer", "--task", "t1", "--seat", "writer", "--run", "r1", "--recorded-at", "2026-11-18T09:00:00Z", "--divergence", "lenient", "--out", out)
	if c != 0 || !strings.Contains(e, "5 step(s)") {
		t.Fatal(c, e)
	}
	sc, err := script.Load(out)
	if err != nil || sc.Divergence() != script.Lenient || sc.File().Provenance.RecordedAt != "2026-11-18T09:00:00Z" {
		t.Fatal(err)
	}
	if c, o, _ := call("record", "--transcript", "../../record/testdata/limit-2.1.325.jsonl"); c != 0 || !strings.Contains(o, "window: seven_day") {
		t.Fatal(c, o)
	}
	for _, bad := range [][]string{{"record"}, {"record", "--transcript", "x", "--divergence", "loose"}, {"record", "--transcript", "x", "--recorded-at", "today"}, {"record", "--nope"}} {
		if c, _, _ := call(bad...); c != 2 {
			t.Error(bad, c)
		}
	}
	if c, _, _ := call("record", "--transcript", filepath.Join(dir, "none.jsonl")); c != 1 {
		t.Error(c)
	}
	os.WriteFile(filepath.Join(dir, "empty.jsonl"), nil, 0o644)
	if c, _, _ := call("record", "--transcript", filepath.Join(dir, "empty.jsonl")); c != 1 {
		t.Error(c)
	}
	if c, _, _ := call("record", "--transcript", "../../record/testdata/limit-2.1.325.jsonl", "--out", filepath.Join(dir, "no", "such", "dir")); c != 1 {
		t.Error(c)
	}

	// explain from a live server and from a saved log, re-evaluated with a script.
	s := server.New(server.Options{Script: sc})
	hs := httptest.NewServer(s.Handler())
	defer hs.Close()
	http := func(body string) {
		r, err := hs.Client().Post(hs.URL+"/v1/messages", "application/json", strings.NewReader(body))
		if err == nil {
			r.Body.Close()
		}
	}
	http(`{"model":"m","stream":true,"system":"[understudy-session: t1/writer]","tools":[{"name":"Read"}],"messages":[{"role":"user","content":"x"},{"role":"system","content":" - Primary working directory: /w"}]}`)
	http(`{"model":"m","messages":[{"role":"user","content":"x"}]}`)
	c, o, _ := call("explain", "--url", hs.URL)
	if c != 0 || !strings.Contains(o, `step="turn-1"`) || !strings.Contains(o, "2 request(s)") {
		t.Fatal(c, o)
	}
	logFile := filepath.Join(dir, "log.json")
	lb, _ := json.Marshal(map[string]any{"requests": s.Requests()})
	os.WriteFile(logFile, lb, 0o644)
	g := filepath.Join(dir, "g.yaml")
	os.WriteFile(g, []byte(good), 0o644)
	c, o, _ = call("explain", "--log", logFile, "--script", g, "--session", "t1/writer", "--json")
	if c != 0 || !strings.Contains(o, `"reevaluated"`) || !strings.Contains(o, `"summary": "1 request(s)`) {
		t.Fatal(c, o)
	}
	for _, bad := range [][]string{{"explain"}, {"explain", "--log", "a", "--url", "b"}, {"explain", "--x"}} {
		if c, _, _ := call(bad...); c != 2 {
			t.Error(bad, c)
		}
	}
	os.WriteFile(filepath.Join(dir, "bad.json"), []byte("nope"), 0o644)
	for _, bad := range [][]string{{"explain", "--log", filepath.Join(dir, "none.json")}, {"explain", "--log", filepath.Join(dir, "bad.json")}, {"explain", "--log", logFile, "--script", filepath.Join(dir, "none.yaml")}} {
		if c, _, _ := call(bad...); c != 1 {
			t.Error(bad, c)
		}
	}

	c, o, _ = call("env", "--url", "http://h:1", "--session", "t1/writer", "--auth", "gateway")
	if c != 0 || !strings.Contains(o, "export ANTHROPIC_AUTH_TOKEN='") || !strings.Contains(o, "export ANTHROPIC_CUSTOM_HEADERS='X-Understudy-Session: t1/writer'") || !strings.Contains(o, "unset ANTHROPIC_API_KEY") {
		t.Fatal(o)
	}
	if c, o, _ := call("env"); c != 0 || !strings.Contains(o, "ANTHROPIC_API_KEY='understudy-dummy-key'") {
		t.Fatal(o)
	}
	for _, bad := range [][]string{{"env", "--auth", "oauth"}, {"env", "--session", "a b"}, {"env", "--x"}} {
		if c, _, _ := call(bad...); c != 2 {
			t.Error(bad, c)
		}
	}
}

func TestMisc(t *testing.T) {
	if c, o, _ := call("schema"); c != 0 || !strings.Contains(o, "2020-12") {
		t.Fatal(c)
	}
	if c, o, _ := call("version"); c != 0 || !strings.Contains(o, server.Version) {
		t.Fatal(c, o)
	}
	if c, o, _ := call("help"); c != 0 || !strings.Contains(o, "understudy serve") {
		t.Fatal(c)
	}
	if c, _, _ := call(); c != 2 {
		t.Fatal(c)
	}
	if c, _, e := call("frobnicate"); c != 2 || !strings.Contains(e, "unknown command") {
		t.Fatal(c)
	}
	for _, bad := range [][]string{{"serve", "--clock", "fast"}, {"serve", "--x"}, {"record-proxy"}, {"record-proxy", "--out", "x", "--upstream", "ftp://x"}, {"record-proxy", "--x"}} {
		if c, _, _ := call(bad...); c != 2 {
			t.Error(bad, c)
		}
	}
	if c, _, _ := call("serve", "--script", "/nonexistent.yaml"); c != 1 {
		t.Error(c)
	}
	if c, _, _ := call("serve", "--addr", "256.0.0.1:1"); c != 1 {
		t.Error(c)
	}
	if c, _, _ := call("record-proxy", "--out", "x", "--addr", "256.0.0.1:1"); c != 1 {
		t.Error(c)
	}
}
