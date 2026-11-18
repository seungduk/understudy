package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/seungduk/understudy/internal/conv"
	"github.com/seungduk/understudy/internal/deliver"
	"github.com/seungduk/understudy/internal/reqlog"
	"github.com/seungduk/understudy/internal/wire"
	"github.com/seungduk/understudy/script"
)

// MaxBody is the largest request body read.
const MaxBody = 64 << 20

func (s *Server) routes() {
	s.mux.HandleFunc("POST /v1/messages", s.messages)
	s.mux.HandleFunc("POST /v1/messages/count_tokens", s.countTokens)
	s.mux.HandleFunc("GET /v1/models", s.listModels)
	s.mux.HandleFunc("GET /v1/models/{id}", s.getModel)
	s.mux.HandleFunc("/api/hello", s.hello)
	s.controlRoutes()
	s.mux.HandleFunc("/", s.notFound)
}

func (s *Server) entry(r *http.Request) Entry {
	return Entry{At: s.clk.Now().UTC().Format(time.RFC3339Nano), Method: r.Method, Path: r.URL.RequestURI(), Headers: logHeaders(r.Header)}
}

func requestID(seq int) string { return fmt.Sprintf("req_understudy_%06d", seq) }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// refuse answers with an API error that tells the client not to retry, and logs why.
func (s *Server) refuse(w http.ResponseWriter, seq int, outcome reqlog.Outcome, kind wire.ErrorKind, msg string) {
	s.log.Update(seq, func(e *Entry) { e.Outcome, e.Status, e.Error = outcome, kind.Status(), msg })
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Request-Id", requestID(seq))
	w.Header().Set("X-Should-Retry", "false")
	w.WriteHeader(kind.Status())
	_, _ = w.Write(wire.ErrorBody(kind, msg, requestID(seq)))
}

func (s *Server) messages(w http.ResponseWriter, r *http.Request) {
	seq := s.log.Add(s.entry(r))
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBody+1))
	if err != nil {
		s.log.Update(seq, func(e *Entry) { e.Outcome = reqlog.ClientGone })
		return
	}
	if len(body) > MaxBody {
		s.refuse(w, seq, reqlog.BadRequest, wire.RequestTooLarge, wire.RequestTooLarge.DefaultMessage())
		return
	}
	req, err := wire.ParseRequest(body)
	if err != nil {
		s.refuse(w, seq, reqlog.BadRequest, wire.InvalidRequest, err.Error())
		return
	}
	s.sawModel(req.Model)
	v := conv.Read(r.Header, req)
	s.log.Update(seq, func(e *Entry) {
		e.Model, e.Stream, e.Session, e.SessionSource = req.Model, req.Stream, v.Key, v.KeySource.String()
		e.Turn, e.ToolsOffered, e.CWD, e.History = len(v.History)-1, len(req.ToolNames), v.CWD, logHistory(v.History)
	})
	if v.BadKey != "" || v.Key == "" {
		s.refuse(w, seq, reqlog.NoScript, wire.InvalidRequest, fmt.Sprintf(
			"understudy: the session key %q (from the %s) is not a valid key. Fix: use 1-128 letters, digits and . _ : / @ + -, such as task-17/writer",
			v.BadKey, v.KeySource))
		return
	}
	sc := s.script.Load()
	if sc == nil {
		s.refuse(w, seq, reqlog.NoScript, wire.InvalidRequest,
			"understudy: no script is loaded. Fix: start with --script FILE, or PUT the script to "+ControlPrefix+"script")
		return
	}
	plan, ok := sc.Session(v.Key)
	if !ok {
		s.refuse(w, seq, reqlog.NoScript, wire.InvalidRequest, fmt.Sprintf(
			"understudy: the script has no session %q (key from the %s). Fix: add sessions[%q] to the script, or send one of its keys (%s) in the %s header or as [understudy-session: KEY] in the system prompt",
			v.Key, v.KeySource, v.Key, strings.Join(sc.Keys(), ", "), conv.SessionHeader))
		return
	}
	ch := plan.Choose(v.History, sc.Divergence())
	s.log.Update(seq, func(e *Entry) {
		e.Step, e.Branch, e.Divergence, e.Trace = ch.Step, ch.Branch, ch.Divergence, ch.Trace
	})
	if ch.Attempts == nil {
		s.refuse(w, seq, reqlog.Diverged, wire.InvalidRequest, "understudy: the request does not fit the script: "+ch.Divergence)
		return
	}
	n := s.nextAttempt(attemptKey{session: v.Key, step: ch.Step, branch: ch.Branch, turn: ch.Turn})
	reply := ch.Attempts[min(n, len(ch.Attempts))-1]
	rc := renderCtx{session: v.Key, turn: ch.Turn, step: ch.Step, attempt: n, model: req.Model, cwd: v.CWD,
		offered: req.ToolNames, stream: req.Stream, inputTokens: wire.EstimateTokens(len(body)), now: s.clk.Now(), seq: seq}
	p, kind, div := render(reply, rc)
	s.log.Update(seq, func(e *Entry) { e.Attempt, e.Reply = n, kind })
	if div != "" {
		s.log.Update(seq, func(e *Entry) {
			if e.Divergence != "" {
				e.Divergence += "; "
			}
			e.Divergence += div
		})
		if sc.Divergence() != script.Lenient {
			s.refuse(w, seq, reqlog.Diverged, wire.InvalidRequest, "understudy: the request does not fit the script: "+div)
			return
		}
	}
	s.log.Update(seq, func(e *Entry) { e.Status, e.Outcome = p.Status, reqlog.Pending })
	if p.Events != nil {
		s.log.Update(seq, func(e *Entry) { e.Status = http.StatusOK })
	}
	out := deliver.Send(r.Context(), w, p, deliver.Deps{Clock: s.clk, Gates: s.gates})
	s.log.Update(seq, func(e *Entry) { e.Outcome = out })
}

func logHistory(h [][]script.Result) [][]reqlog.Result {
	out := make([][]reqlog.Result, len(h))
	for i, rs := range h {
		for _, r := range rs {
			t := r.Text
			if len(t) > reqlog.MaxResultText {
				t = t[:reqlog.MaxResultText]
			}
			out[i] = append(out[i], reqlog.Result{Tool: r.Tool, IsError: r.IsError, Text: t})
		}
	}
	return out
}

type renderCtx struct {
	session, step, model, cwd string
	turn, attempt, seq        int
	offered                   []string
	stream                    bool
	inputTokens               int
	now                       time.Time
}

func (c renderCtx) id(prefix string, parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(append([]string{c.session, strconv.Itoa(c.turn), c.step, strconv.Itoa(c.attempt)}, parts...), "\x00")))
	return prefix + hex.EncodeToString(h[:])[:24]
}

// Reply kind names as the log shows them.
const (
	KindMessage     = "message"
	KindToolUse     = "tool_use"
	KindHTTPError   = "http_error"
	KindStreamError = "stream_error"
	KindLimit       = "limit"
)

// render turns a script reply into a delivery plan. div is non-empty when the
// reply cannot be sent as written (a tool the client does not offer, {{cwd}}
// without a working directory).
func render(r script.Reply, c renderCtx) (p deliver.Plan, kind, div string) {
	p.Headers = http.Header{"Request-Id": {requestID(c.seq)}}
	meta := wire.Meta{ID: c.id("msg_"), Model: c.model}
	if s := r.Stall; s != nil {
		p.StallFor, p.StallGate, p.StallMid = time.Duration(s.Seconds)*time.Second, s.Gate, s.At == script.MidStream
	}
	if ct := r.Cut; ct != nil {
		p.Cut, p.CutAfter, p.CutReset = true, ct.AfterEvents, ct.How == script.Reset
	}
	if pc := r.Pace; pc != nil {
		p.FirstDelay, p.Between = time.Duration(pc.FirstEventMS)*time.Millisecond, time.Duration(pc.BetweenEventsMS)*time.Millisecond
	}
	var blocks []wire.OutBlock
	stop := wire.EndTurn
	thinking := func(t string) {
		if t != "" {
			blocks = append(blocks, wire.OutBlock{Kind: wire.OutThinking, Text: t, Signature: c.id("sig_", t)})
		}
	}
	switch {
	case r.Message != nil:
		kind = KindMessage
		thinking(r.Message.Thinking)
		blocks = append(blocks, wire.OutBlock{Kind: wire.OutText, Text: r.Message.Text})
		if r.Message.Stop == script.MaxTokens {
			stop = wire.MaxTokens
		}
	case r.ToolUse != nil:
		kind = KindToolUse
		stop = wire.ToolUse
		thinking(r.ToolUse.Thinking)
		if r.ToolUse.Text != "" {
			blocks = append(blocks, wire.OutBlock{Kind: wire.OutText, Text: r.ToolUse.Text})
		}
		offered := map[string]bool{}
		for _, t := range c.offered {
			offered[t] = true
		}
		var missing, needCWD []string
		for i, call := range r.ToolUse.Calls {
			if !offered[call.Tool] {
				missing = append(missing, call.Tool)
			}
			input, usedCWD := substitute(call.Input, c.cwd)
			if usedCWD && c.cwd == "" {
				needCWD = append(needCWD, call.Tool)
			}
			b, _ := json.Marshal(input)
			blocks = append(blocks, wire.OutBlock{Kind: wire.OutToolUse, ID: c.id("toolu_", strconv.Itoa(i)), Name: call.Tool, Input: b})
		}
		if len(missing) > 0 {
			names := append([]string(nil), c.offered...)
			sort.Strings(names)
			div = fmt.Sprintf("the script calls %s, which the client does not offer (it offers: %s)", strings.Join(missing, ", "), strings.Join(names, ", "))
		}
		if len(needCWD) > 0 {
			if div != "" {
				div += "; "
			}
			div += "the script uses {{cwd}} but the request does not say its working directory (no 'Primary working directory:' line)"
		}
	case r.HTTPError != nil:
		kind = KindHTTPError
		e := r.HTTPError
		k, _ := wire.ParseErrorKind(e.Error)
		p.Status, p.Body = k.Status(), wire.ErrorBody(k, e.Message, requestID(c.seq))
		if e.RetryAfterSeconds != nil {
			p.Headers.Set("Retry-After", strconv.Itoa(*e.RetryAfterSeconds))
		}
		switch e.Retry {
		case script.RetryNever:
			p.Headers.Set("X-Should-Retry", "false")
		case script.RetryAlways:
			p.Headers.Set("X-Should-Retry", "true")
		}
		return p, kind, ""
	case r.Limit != nil:
		kind = KindLimit
		p.Status, p.Body = 429, wire.ErrorBody(wire.RateLimit, limitMessage(r.Limit.Window), requestID(c.seq))
		for k, v := range LimitHeaders(r.Limit.Window, c.now, r.Limit.ResetsInSeconds) {
			p.Headers.Set(k, v)
		}
		return p, kind, ""
	case r.StreamError != nil:
		kind = KindStreamError
		e := r.StreamError
		k, _ := wire.ParseErrorKind(e.Error)
		u := wire.Usage{Input: c.inputTokens, Output: wire.EstimateTokens(len(e.PartialText))}
		if !c.stream {
			// A non-streamed request cannot carry an in-stream error; send the same error as HTTP.
			p.Status, p.Body = k.Status(), wire.ErrorBody(k, e.Message, requestID(c.seq))
			return p, kind, ""
		}
		p.Events = wire.ErrorEvents(meta, u, e.PartialText, k, e.Message)
		return p, kind, ""
	}
	outLen := 0
	for _, b := range blocks {
		outLen += len(b.Text) + len(b.Input)
	}
	u := wire.Usage{Input: c.inputTokens, Output: wire.EstimateTokens(outLen)}
	if c.stream {
		p.Events = wire.StreamEvents(meta, blocks, stop, u)
	} else {
		p.Status, p.Body = http.StatusOK, wire.MessageJSON(meta, blocks, stop, u)
	}
	return p, kind, div
}

// substitute replaces {{cwd}} in every string of v, and says whether it was used.
func substitute(v any, cwd string) (any, bool) {
	switch x := v.(type) {
	case string:
		if strings.Contains(x, "{{cwd}}") {
			return strings.ReplaceAll(x, "{{cwd}}", cwd), true
		}
		return x, false
	case map[string]any:
		out := make(map[string]any, len(x))
		used := false
		for k, e := range x {
			var u bool
			out[k], u = substitute(e, cwd)
			used = used || u
		}
		return out, used
	case []any:
		out := make([]any, len(x))
		used := false
		for i, e := range x {
			var u bool
			out[i], u = substitute(e, cwd)
			used = used || u
		}
		return out, used
	}
	return v, false
}

func limitMessage(w script.Window) string {
	switch w {
	case script.FiveHour:
		return "You have reached your 5-hour usage limit."
	case script.SevenDayOpus:
		return "You have reached your weekly Opus usage limit."
	case script.SevenDaySonnet:
		return "You have reached your weekly Sonnet usage limit."
	}
	return "You have reached your weekly usage limit."
}

// LimitHeaders are the headers of a usage-limit 429 for window, resetting
// resetsIn seconds after now. Measured against Claude Code 2.1.325: with these
// (and a gateway token, see the README) it prints "You've hit your weekly limit".
func LimitHeaders(w script.Window, now time.Time, resetsIn int) map[string]string {
	reset := strconv.FormatInt(now.Unix()+int64(resetsIn), 10)
	h := map[string]string{
		"X-Should-Retry":                     "false",
		"Retry-After":                        strconv.Itoa(resetsIn),
		"Anthropic-Ratelimit-Unified-Status": "rejected",
		"Anthropic-Ratelimit-Unified-Representative-Claim": string(w),
		"Anthropic-Ratelimit-Unified-Reset":                reset,
	}
	win := "7d"
	if w == script.FiveHour {
		win = "5h"
	}
	h["Anthropic-Ratelimit-Unified-"+win+"-Status"] = "rejected"
	h["Anthropic-Ratelimit-Unified-"+win+"-Reset"] = reset
	h["Anthropic-Ratelimit-Unified-"+win+"-Utilization"] = "1.0"
	return h
}
