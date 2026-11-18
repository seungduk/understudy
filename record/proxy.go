package record

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/seungduk/understudy/internal/conv"
	"github.com/seungduk/understudy/internal/wire"
	"github.com/seungduk/understudy/script"
)

// Proxy is a recording reverse proxy: point Claude Code's ANTHROPIC_BASE_URL at
// it, and it forwards every request to the upstream API and keeps, per session
// and turn, the reply the model gave. Credentials and headers pass through to
// the upstream and are never kept. Safe for concurrent use.
type Proxy struct {
	upstream *url.URL
	client   *http.Client

	mu       sync.Mutex
	sessions map[string]map[int]*recTurn
}

type recTurn struct {
	attempts []script.Reply
	done     bool // a non-error reply was recorded; later requests at this turn are ignored
	expect   *script.Matcher
}

// NewProxy makes a proxy to upstream, such as https://api.anthropic.com.
func NewProxy(upstream string, client *http.Client) (*Proxy, error) {
	u, err := url.Parse(upstream)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("the upstream %q is not an http(s) URL. Fix: give the API's base URL, such as https://api.anthropic.com", upstream)
	}
	if client == nil {
		client = &http.Client{}
	}
	return &Proxy{upstream: u, client: client, sessions: map[string]map[int]*recTurn{}}, nil
}

var hopHeaders = []string{"Connection", "Keep-Alive", "Proxy-Connection", "Transfer-Encoding", "Upgrade", "Te", "Trailer", "Content-Length"}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		http.Error(w, "cannot read the request", http.StatusBadRequest)
		return
	}
	target := *p.upstream
	target.Path = strings.TrimSuffix(p.upstream.Path, "/") + r.URL.Path
	target.RawQuery = r.URL.RawQuery
	out, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	out.Header = r.Header.Clone()
	for _, h := range hopHeaders {
		out.Header.Del(h)
	}
	resp, err := p.client.Do(out)
	if err != nil {
		http.Error(w, "understudy record: the upstream did not answer: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	for _, h := range hopHeaders {
		w.Header().Del(h)
	}
	w.WriteHeader(resp.StatusCode)
	var kept bytes.Buffer
	rc := http.NewResponseController(w)
	buf := make([]byte, 32<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			kept.Write(buf[:n])
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			_ = rc.Flush()
		}
		if rerr != nil {
			break
		}
	}
	if r.Method == http.MethodPost && r.URL.Path == "/v1/messages" {
		p.keep(r.Header, body, resp.StatusCode, resp.Header.Get("Content-Type"), kept.Bytes())
	}
}

func (p *Proxy) keep(h http.Header, body []byte, status int, ctype string, resp []byte) {
	req, err := wire.ParseRequest(body)
	if err != nil {
		return
	}
	v := conv.Read(h, req)
	if v.Key == "" {
		return
	}
	turnNo := len(v.History) - 1
	var reply script.Reply
	ok := false
	switch {
	case status != http.StatusOK:
		reply, ok = statusReply(status, resp)
	case strings.HasPrefix(ctype, "text/event-stream"):
		reply, ok = sseReply(resp)
	default:
		reply, ok = jsonReply(resp)
	}
	if !ok {
		return
	}
	if v.CWD != "" {
		if reply.ToolUse != nil {
			for i := range reply.ToolUse.Calls {
				reply.ToolUse.Calls[i].Input = rebase(reply.ToolUse.Calls[i].Input, v.CWD).(map[string]any)
			}
		}
	}
	isErr := reply.HTTPError != nil || reply.StreamError != nil || reply.Limit != nil
	var rs []result
	for _, x := range v.History[turnNo] {
		rs = append(rs, result{text: x.Text, isError: x.IsError})
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	turns := p.sessions[v.Key]
	if turns == nil {
		turns = map[int]*recTurn{}
		p.sessions[v.Key] = turns
	}
	t := turns[turnNo]
	if t == nil {
		t = &recTurn{expect: expectFor(rs)}
		turns[turnNo] = t
	}
	if t.done {
		return
	}
	t.attempts = append(t.attempts, reply)
	t.done = !isErr
}

func statusReply(status int, body []byte) (script.Reply, bool) {
	k, ok := kindOfStatus(status)
	if !ok {
		return script.Reply{}, false
	}
	var env struct {
		Error struct{ Message string } `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	return script.Reply{HTTPError: &script.HTTPError{Error: k.Name(), Message: env.Error.Message}}, true
}

func kindOfType(typ string) (wire.ErrorKind, bool) {
	for _, k := range wire.ErrorKinds {
		if k.Type() == typ {
			return k, true
		}
	}
	return 0, false
}

type block struct {
	kind  string
	name  string
	text  strings.Builder
	input strings.Builder
}

func sseReply(raw []byte) (script.Reply, bool) {
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	var blocks []*block
	byIndex := map[int]*block{}
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev struct {
			Type         string `json:"type"`
			Index        int    `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
			Error struct{ Type, Message string } `json:"error"`
		}
		if json.Unmarshal([]byte(line[6:]), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "content_block_start":
			b := &block{kind: ev.ContentBlock.Type, name: ev.ContentBlock.Name}
			blocks = append(blocks, b)
			byIndex[ev.Index] = b
		case "content_block_delta":
			b := byIndex[ev.Index]
			if b == nil {
				continue
			}
			b.text.WriteString(ev.Delta.Text + ev.Delta.Thinking)
			b.input.WriteString(ev.Delta.PartialJSON)
		case "error":
			k, ok := kindOfType(ev.Error.Type)
			if !ok {
				k = wire.APIError
			}
			partial := ""
			for _, b := range blocks {
				if b.kind == "text" {
					partial += b.text.String()
				}
			}
			return script.Reply{StreamError: &script.StreamError{Error: k.Name(), Message: ev.Error.Message, PartialText: partial}}, true
		}
	}
	return assemble(blocks)
}

func jsonReply(raw []byte) (script.Reply, bool) {
	var m struct {
		Content []struct {
			Type     string         `json:"type"`
			Text     string         `json:"text"`
			Thinking string         `json:"thinking"`
			Name     string         `json:"name"`
			Input    map[string]any `json:"input"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return script.Reply{}, false
	}
	var blocks []*block
	for _, c := range m.Content {
		b := &block{kind: c.Type, name: c.Name}
		b.text.WriteString(c.Text + c.Thinking)
		if c.Input != nil {
			in, _ := json.Marshal(c.Input)
			b.input.Write(in)
		}
		blocks = append(blocks, b)
	}
	return assemble(blocks)
}

func assemble(blocks []*block) (script.Reply, bool) {
	var thinking, text []string
	var calls []script.Call
	for _, b := range blocks {
		switch b.kind {
		case "thinking":
			thinking = append(thinking, b.text.String())
		case "text":
			text = append(text, b.text.String())
		case "tool_use":
			input := map[string]any{}
			if s := b.input.String(); s != "" {
				if json.Unmarshal([]byte(s), &input) != nil {
					return script.Reply{}, false
				}
			}
			calls = append(calls, script.Call{Tool: b.name, Input: input})
		}
	}
	if len(calls) > 0 {
		return script.Reply{ToolUse: &script.ToolUse{Thinking: strings.Join(thinking, "\n"), Text: strings.Join(text, "\n"), Calls: calls}}, true
	}
	t := strings.Join(text, "\n")
	if t == "" {
		t = "(no text)"
	}
	return script.Reply{Message: &script.Message{Thinking: strings.Join(thinking, "\n"), Text: t}}, true
}

// Sessions is the keys recorded so far, sorted.
func (p *Proxy) Sessions() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.sessions))
	for k := range p.sessions {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// File is everything recorded so far as one script, a session per key. The
// steps of a session are its turns in order; a turn the proxy never saw (a gap)
// ends the session there. m gives the provenance (SessionKey and CWD are not used).
func (p *Proxy) File(m Meta) script.File {
	p.mu.Lock()
	defer p.mu.Unlock()
	f := script.File{Schema: script.SchemaV1, Divergence: m.Divergence, Sessions: map[string]script.Session{},
		Provenance: &script.Provenance{Source: script.Recorded, Run: m.Run, Task: m.Task, Seat: m.Seat}}
	if !m.RecordedAt.IsZero() {
		f.Provenance.RecordedAt = m.RecordedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	for key, turns := range p.sessions {
		var steps []script.Step
		for i := 0; ; i++ {
			t, ok := turns[i]
			if !ok {
				break
			}
			st := script.Step{Name: fmt.Sprintf("turn-%d", i+1), Expect: t.expect}
			if len(t.attempts) == 1 {
				r := t.attempts[0]
				st.Reply = &r
			} else {
				st.Attempts = append([]script.Reply(nil), t.attempts...)
			}
			steps = append(steps, st)
		}
		if len(steps) > 0 {
			f.Sessions[key] = script.Session{Steps: steps}
		}
	}
	return f
}
