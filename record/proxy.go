package record

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/seungduk/understudy/core"
	"github.com/seungduk/understudy/harness"
	"github.com/seungduk/understudy/internal/conv"
	"github.com/seungduk/understudy/internal/protocol/registry"
	"github.com/seungduk/understudy/protocol"
	"github.com/seungduk/understudy/script"
)

// Proxy is a recording reverse proxy for every protocol understudy speaks:
// point a harness's base URL at it, and it forwards every request to the
// upstream API and keeps, per session and turn, the reply the model gave, read
// back by the request's protocol. Credentials and headers pass through to the
// upstream and are never kept. Safe for concurrent use.
type Proxy struct {
	upstream *url.URL
	client   *http.Client
	codecs   *http.ServeMux // pattern -> the codec that answers it, found with Handler

	mu       sync.Mutex
	sessions map[string]*recSession
}

type recSession struct {
	harness harness.ID
	turns   map[int]*recTurn
}

type recTurn struct {
	attempts []script.Reply
	done     bool // a non-error reply was recorded; later requests at this turn are ignored
	expect   *script.Matcher
}

// codecHandler marks which codec a route belongs to.
type codecHandler struct{ cs []protocol.Codec }

func (codecHandler) ServeHTTP(http.ResponseWriter, *http.Request) {}

// NewProxy makes a proxy to upstream, such as https://api.anthropic.com.
func NewProxy(upstream string, client *http.Client) (*Proxy, error) {
	u, err := url.Parse(upstream)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("the upstream %q is not an http(s) URL. Fix: give the API's base URL, such as https://api.anthropic.com", upstream)
	}
	if client == nil {
		client = &http.Client{}
	}
	p := &Proxy{upstream: u, client: client, sessions: map[string]*recSession{}, codecs: http.NewServeMux()}
	byPattern := map[string][]protocol.Codec{}
	var order []string
	for _, c := range registry.All() {
		for _, pat := range c.Routes() {
			if byPattern[pat] == nil {
				order = append(order, pat)
			}
			byPattern[pat] = append(byPattern[pat], c)
		}
	}
	for _, pat := range order {
		p.codecs.Handle(pat, codecHandler{cs: byPattern[pat]})
	}
	return p, nil
}

func (p *Proxy) codecOf(r *http.Request) protocol.Codec {
	h, pat := p.codecs.Handler(r)
	ch, ok := h.(codecHandler)
	if !ok || pat == "" {
		return nil
	}
	for _, c := range ch.cs {
		if c.Accepts(r) {
			return c
		}
	}
	return ch.cs[len(ch.cs)-1]
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
	if c := p.codecOf(r); c != nil {
		p.keep(c, r, body, resp.StatusCode, resp.Header, kept.Bytes())
	}
}

func (p *Proxy) keep(c protocol.Codec, r *http.Request, body []byte, status int, h http.Header, resp []byte) {
	d, err := c.Decode(r, body)
	if err != nil || d.Op != protocol.Generate {
		return
	}
	k := conv.SessionKey(r.Header, d.Request)
	if k.Key == "" {
		return
	}
	pr := harness.Detect(c.ID(), d.Request)
	if name := strings.TrimSpace(r.Header.Get(conv.HarnessHeader)); name != "" {
		if x, ok := harness.Lookup(name); ok {
			pr = x
		}
	}
	if _, _, aux := pr.AuxReply(c.ID(), d.Request); aux {
		return
	}
	history := conv.History(d.Request, pr)
	cwd := conv.WorkingDir(d.Request, pr)
	o, ok := c.Observe(protocol.Generate, status, h, resp)
	if !ok {
		return
	}
	reply := replyOf(o, d.Request.Tools)
	if cwd != "" && reply.ToolUse != nil {
		for i := range reply.ToolUse.Calls {
			reply.ToolUse.Calls[i].Input = rebase(reply.ToolUse.Calls[i].Input, cwd).(map[string]any)
		}
	}
	isErr := o.Kind != protocol.SawReply
	turnNo := len(history) - 1
	p.mu.Lock()
	defer p.mu.Unlock()
	ses := p.sessions[k.Key]
	if ses == nil {
		ses = &recSession{harness: pr.ID, turns: map[int]*recTurn{}}
		p.sessions[k.Key] = ses
	}
	t := ses.turns[turnNo]
	if t == nil {
		t = &recTurn{expect: expectFor(history[turnNo])}
		ses.turns[turnNo] = t
	}
	if t.done {
		return
	}
	t.attempts = append(t.attempts, reply)
	t.done = !isErr
}

// replyOf is an observed response as a script reply, with native tool calls
// (a freeform call's text as input: {input: ...}).
func replyOf(o protocol.Observed, offered []core.Tool) script.Reply {
	switch o.Kind {
	case protocol.SawError:
		return script.Reply{HTTPError: &script.HTTPError{Error: o.Error.Name(), Message: o.Message}}
	case protocol.SawStreamError:
		return script.Reply{StreamError: &script.StreamError{Error: o.Error.Name(), Message: o.Message, PartialText: o.Reply.Text}}
	case protocol.SawLimit:
		secs := int(o.ResetsIn.Seconds())
		if secs < 1 {
			secs = 3600
		}
		return script.Reply{Limit: &script.Limit{Window: script.Window(o.Window.Name()), ResetsInSeconds: secs}}
	}
	r := o.Reply
	if len(r.Calls) > 0 {
		tu := &script.ToolUse{Thinking: r.Thinking, Text: r.Text}
		for _, c := range r.Calls {
			input := map[string]any{}
			if c.Kind == core.Freeform {
				input["input"] = c.Raw
			} else if len(c.Input) > 0 {
				_ = json.Unmarshal(c.Input, &input)
			}
			tu.Calls = append(tu.Calls, script.Call{Tool: c.Tool, Input: input})
		}
		return script.Reply{ToolUse: tu}
	}
	t := r.Text
	if t == "" {
		t = "(no text)"
	}
	msg := &script.Message{Thinking: r.Thinking, Text: t}
	if r.Stop == core.MaxTokens {
		msg.Stop = script.MaxTokens
	}
	return script.Reply{Message: msg}
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
// ends the session there. A session of a harness other than Claude Code names
// its harness (schema understudy/v2). m gives the provenance (SessionKey and
// CWD are not used).
func (p *Proxy) File(m Meta) script.File {
	p.mu.Lock()
	defer p.mu.Unlock()
	f := script.File{Schema: script.SchemaV1, Divergence: m.Divergence, Sessions: map[string]script.Session{},
		Provenance: &script.Provenance{Source: script.Recorded, Run: m.Run, Task: m.Task, Seat: m.Seat}}
	if !m.RecordedAt.IsZero() {
		f.Provenance.RecordedAt = m.RecordedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	for key, ses := range p.sessions {
		var steps []script.Step
		for i := 0; ; i++ {
			t, ok := ses.turns[i]
			if !ok {
				break
			}
			st := script.Step{Name: fmt.Sprintf("turn-%d", i+1), Expect: t.expect}
			for _, a := range t.attempts {
				if a.Limit != nil && (a.Limit.Window == script.Daily || a.Limit.Window == script.PerMinute) {
					f.Schema = script.SchemaV2
				}
			}
			if len(t.attempts) == 1 {
				r := t.attempts[0]
				st.Reply = &r
			} else {
				st.Attempts = append([]script.Reply(nil), t.attempts...)
			}
			steps = append(steps, st)
		}
		if len(steps) == 0 {
			continue
		}
		s := script.Session{Steps: steps}
		if ses.harness != harness.ClaudeCode && ses.harness != harness.Generic {
			s.Harness = string(ses.harness)
			f.Schema = script.SchemaV2
		}
		f.Sessions[key] = s
	}
	return f
}
