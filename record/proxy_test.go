package record

import (
	"encoding/json"
	"fmt"
	"github.com/seungduk/understudy/internal/protocol/registry"
	"github.com/seungduk/understudy/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/seungduk/understudy/script"
	"github.com/seungduk/understudy/server"
)

const upstreamScript = `schema: understudy/v1
sessions:
  t1/writer:
    steps:
      - name: read
        attempts:
          - http_error: {error: overloaded}
          - tool_use: {thinking: look first, text: Reading., calls: [{tool: Read, input: {file_path: /work/dir/a.txt}}]}
      - name: test
        attempts:
          - stream_error: {error: api_error, partial_text: "Runn"}
          - tool_use: {calls: [{tool: Bash, input: {command: go test}}]}
      - name: done
        reply: {message: {text: All good.}}
`

func reqBody(stream bool, results ...[2]string) string {
	msgs := []any{
		map[string]any{"role": "user", "content": "go"},
		map[string]any{"role": "system", "content": []any{map[string]any{"type": "text", "text": " - Primary working directory: /work/dir"}}},
	}
	for i, r := range results {
		id := fmt.Sprintf("toolu_%d", i)
		msgs = append(msgs, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": id, "name": r[0], "input": map[string]any{}}}})
		msgs = append(msgs, map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": id, "content": r[1], "is_error": r[1] == "boom"}}})
	}
	b, _ := json.Marshal(map[string]any{"model": "m", "stream": stream, "messages": msgs, "tools": []any{map[string]any{"name": "Read"}, map[string]any{"name": "Bash"}}})
	return string(b)
}

func TestProxyRecordsAndReplays(t *testing.T) {
	sc, err := script.Parse([]byte(upstreamScript))
	if err != nil {
		t.Fatal(err)
	}
	up := httptest.NewServer(server.New(server.Options{Script: sc}).Handler())
	defer up.Close()
	p, err := NewProxy(up.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	px := httptest.NewServer(p)
	defer px.Close()
	post := func(stream bool, results ...[2]string) (int, string) {
		req, _ := http.NewRequest("POST", px.URL+"/v1/messages?beta=true", strings.NewReader(reqBody(stream, results...)))
		req.Header.Set("X-Understudy-Session", "t1/writer")
		req.Header.Set("Authorization", "Bearer secret-must-not-be-kept")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		return r.StatusCode, string(b)
	}
	if s, _ := post(true); s != 529 {
		t.Fatal(s)
	}
	if s, b := post(true); s != 200 || !strings.Contains(b, "Reading.") {
		t.Fatal(s, b)
	}
	post(true) // a retry after success at the same turn is not recorded
	if _, b := post(true, [2]string{"Read", "boom"}); !strings.Contains(b, "event: error") {
		t.Fatal(b)
	}
	post(true, [2]string{"Read", "boom"})
	if s, b := post(false, [2]string{"Read", "boom"}, [2]string{"Bash", "ok"}); s != 200 || !strings.Contains(b, "All good.") {
		t.Fatal(s, b)
	}
	// Other paths pass through and are not recorded.
	if r, err := http.Get(px.URL + "/v1/models"); err != nil || r.StatusCode != 200 {
		t.Fatal(err)
	}
	if got := p.Sessions(); len(got) != 1 || got[0] != "t1/writer" {
		t.Fatal(got)
	}
	f := p.File(Meta{Run: "r9", RecordedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)})
	out, err := script.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "secret") {
		t.Fatal("credential kept")
	}
	rec, err := script.Parse(out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	st := f.Sessions["t1/writer"].Steps
	if len(st) != 3 || len(st[0].Attempts) != 2 || st[0].Attempts[0].HTTPError.Error != "overloaded" {
		t.Fatalf("%s", out)
	}
	tu := st[0].Attempts[1].ToolUse
	if tu.Thinking != "look first" || tu.Text != "Reading." || tu.Calls[0].Input["file_path"] != "{{cwd}}/a.txt" {
		t.Fatalf("%+v", tu)
	}
	if se := st[1].Attempts[0].StreamError; se == nil || se.Error != "api_error" || se.PartialText != "Runn" {
		t.Fatalf("%s", out)
	}
	if st[1].Expect.Result != script.Error || st[2].Reply.Message.Text != "All good." || f.Provenance.Run != "r9" || f.Provenance.RecordedAt != "2026-01-02T03:04:05Z" {
		t.Fatalf("%s", out)
	}
	// The recording replays against the same client requests.
	replay := httptest.NewServer(server.New(server.Options{Script: rec}).Handler())
	defer replay.Close()
	req, _ := http.NewRequest("POST", replay.URL+"/v1/messages", strings.NewReader(reqBody(true)))
	req.Header.Set("X-Understudy-Session", "t1/writer")
	r, _ := http.DefaultClient.Do(req)
	r.Body.Close()
	if r.StatusCode != 529 {
		t.Fatal(r.StatusCode)
	}
}

func TestProxyEdges(t *testing.T) {
	for _, bad := range []string{"ftp://x", "::", "http://"} {
		if _, err := NewProxy(bad, nil); err == nil {
			t.Error(bad)
		}
	}
	p, _ := NewProxy("http://127.0.0.1:1", nil)
	px := httptest.NewServer(p)
	defer px.Close()
	r, err := http.Post(px.URL+"/v1/messages", "application/json", strings.NewReader("{}"))
	if err != nil || r.StatusCode != http.StatusBadGateway {
		t.Fatal(err, r.StatusCode)
	}
	// Replies the recorder cannot read are skipped.
	anth := registry.For(protocol.Anthropic)
	keep := func(h http.Header, body []byte, status int, ctype string, resp []byte) {
		r := httptest.NewRequest("POST", "/v1/messages", nil)
		r.Header = h
		rh := http.Header{}
		if ctype != "" {
			rh.Set("Content-Type", ctype)
		}
		p.keep(anth, r, body, status, rh, resp)
	}
	keep(http.Header{}, []byte("bad"), 200, "", nil)
	keep(http.Header{}, []byte(reqBody(true)), 299, "", nil)
	keep(http.Header{}, []byte(reqBody(true)), 200, "application/json", []byte("bad"))
	keep(http.Header{"X-Understudy-Session": {"bad key"}}, []byte(reqBody(true)), 200, "application/json", []byte(`{"content":[]}`))
	if len(p.Sessions()) != 0 {
		t.Fatal(p.Sessions())
	}
	keep(http.Header{}, []byte(reqBody(true)), 200, "text/event-stream", []byte("data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"name\":\"X\"}}\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"partial_json\":\"{bad\"}}\ndata: nope\ndata: {\"type\":\"content_block_delta\",\"index\":5}\n"))
	keep(http.Header{}, []byte(reqBody(true)), 200, "text/event-stream", []byte("data: {\"type\":\"error\",\"error\":{\"type\":\"weird\"}}\n"))
	keep(http.Header{}, []byte(reqBody(true)), 200, "application/json", []byte(`{"content":[{"type":"thinking","thinking":"t"}]}`))
	f := p.File(Meta{})
	st := f.Sessions["default"].Steps
	if len(st) != 1 || st[0].Attempts[0].StreamError.Error != "api_error" || st[0].Attempts[1].Message.Text != "(no text)" {
		t.Fatalf("%+v", st)
	}
}
