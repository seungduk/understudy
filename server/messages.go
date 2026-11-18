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
	"unicode/utf8"

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
		e.Prompt = cutText(v.Prompt, reqlog.MaxPromptText)
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
		if ch.Divergence != "" {
			e.Expected, e.Actual = ch.Expected, actualOf(v.History[len(v.History)-1], ch.Turn)
		}
	})
	if ch.Attempts == nil {
		s.refuse(w, seq, reqlog.Diverged, wire.InvalidRequest, "understudy: the request does not fit the script: "+ch.Divergence)
		return
	}
	n := s.nextAttempt(attemptKey{session: v.Key, step: ch.Step, branch: ch.Branch, turn: ch.Turn})
	reply := ch.Attempts[min(n, len(ch.Attempts))-1]
	rc := renderCtx{session: v.Key, turn: ch.Turn, step: ch.Step, attempt: n, model: req.Model, cwd: v.CWD,
		offered: req.ToolNames, stream: req.Stream, inputTokens: wire.EstimateTokens(len(body)), now: s.clk.Now(), seq: seq}
	p, ans, div := render(reply, rc)
	s.log.Update(seq, func(e *Entry) { e.Attempt, e.Reply, e.Answer = n, ans.Kind, &ans })
	if div.text != "" {
		s.log.Update(seq, func(e *Entry) {
			if e.Divergence != "" {
				e.Divergence += "; "
			}
			e.Divergence += div.text
			e.Expected = append(e.Expected, div.expected...)
			e.Actual = append(e.Actual, div.actual...)
		})
		if sc.Divergence() != script.Lenient {
			s.log.Update(seq, func(e *Entry) { e.Answer = nil })
			s.refuse(w, seq, reqlog.Diverged, wire.InvalidRequest, "understudy: the request does not fit the script: "+div.text)
			return
		}
	}
	s.log.Update(seq, func(e *Entry) { e.Status, e.Outcome = p.Status, reqlog.Pending })
	if p.Events != nil {
		s.log.Update(seq, func(e *Entry) { e.Status = http.StatusOK })
	}
	hold := &reqlog.Hold{Gate: p.StallGate, Seconds: int(p.StallFor / time.Second)}
	out := deliver.Send(r.Context(), w, p, deliver.Deps{Clock: s.clk, Gates: s.gates,
		OnStall: func() {
			s.log.Update(seq, func(e *Entry) {
				h := *hold
				h.Since = s.clk.Now().UTC().Format(time.RFC3339Nano)
				e.Held = &h
			})
		},
		OnResume: func() { s.log.Update(seq, func(e *Entry) { e.Held = nil }) },
	})
	s.log.Update(seq, func(e *Entry) { e.Outcome = out })
}

// cutText cuts t to at most n bytes, at a rune boundary.
func cutText(t string, n int) string {
	if len(t) <= n {
		return t
	}
	for n > 0 && !utf8.RuneStart(t[n]) {
		n--
	}
	return t[:n]
}

// MaxActualText is how much of each tool result an Actual line quotes.
const MaxActualText = 300

// actualOf describes the tool results a diverging request carried, one line each.
func actualOf(rs []script.Result, turn int) []string {
	if len(rs) == 0 {
		if turn == 0 {
			return []string{"the first request of the session: no tool results"}
		}
		return []string{fmt.Sprintf("turn %d carries no tool results", turn)}
	}
	out := make([]string, 0, len(rs))
	for i, r := range rs {
		name, how := r.Tool, "ok"
		if name == "" {
			name = "unknown tool"
		}
		if r.IsError {
			how = "error"
		}
		if code, ok := script.ExitCode(r); ok && name == "Bash" {
			how += fmt.Sprintf(", exit code %d", code)
		}
		t := strings.TrimSpace(r.Text)
		if len(t) > MaxActualText {
			t = cutText(t, MaxActualText) + "..."
		}
		out = append(out, fmt.Sprintf("result %d (%s): %s: %s", i, name, how, strconv.Quote(t)))
	}
	return out
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

// renderDiv is how a reply cannot be sent as written: in words, what the
// script wanted and what the request had instead.
type renderDiv struct {
	text             string
	expected, actual []string
}

// render turns a script reply into a delivery plan and the answer the log keeps.
// div.text is non-empty when the reply cannot be sent as written (a tool the
// client does not offer, {{cwd}} without a working directory).
func render(r script.Reply, c renderCtx) (p deliver.Plan, ans reqlog.Answer, div renderDiv) {
	p.Headers = http.Header{"Request-Id": {requestID(c.seq)}}
	meta := wire.Meta{ID: c.id("msg_"), Model: c.model}
	if s := r.Stall; s != nil {
		p.StallFor, p.StallGate, p.StallMid = time.Duration(s.Seconds)*time.Second, s.Gate, s.At == script.MidStream
		where := "before the response"
		if p.StallMid {
			where = "mid-stream"
		}
		if s.Gate != "" {
			ans.Faults = append(ans.Faults, fmt.Sprintf("stall %s until gate %s is released", where, s.Gate))
		} else {
			ans.Faults = append(ans.Faults, fmt.Sprintf("stall %s for %d s", where, s.Seconds))
		}
	}
	if ct := r.Cut; ct != nil {
		p.Cut, p.CutAfter, p.CutReset = true, ct.AfterEvents, ct.How == script.Reset
		ans.Faults = append(ans.Faults, fmt.Sprintf("cut (%s) after %d event(s)", ct.How, ct.AfterEvents))
	}
	if pc := r.Pace; pc != nil {
		p.FirstDelay, p.Between = time.Duration(pc.FirstEventMS)*time.Millisecond, time.Duration(pc.BetweenEventsMS)*time.Millisecond
		ans.Faults = append(ans.Faults, fmt.Sprintf("pace: first event after %d ms, %d ms between events", pc.FirstEventMS, pc.BetweenEventsMS))
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
		ans.Kind, ans.Thinking, ans.Text, ans.Stop = KindMessage, r.Message.Thinking, r.Message.Text, string(script.EndTurn)
		thinking(r.Message.Thinking)
		blocks = append(blocks, wire.OutBlock{Kind: wire.OutText, Text: r.Message.Text})
		if r.Message.Stop == script.MaxTokens {
			stop, ans.Stop = wire.MaxTokens, string(script.MaxTokens)
		}
	case r.ToolUse != nil:
		ans.Kind, ans.Thinking, ans.Text, ans.Stop = KindToolUse, r.ToolUse.Thinking, r.ToolUse.Text, "tool_use"
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
			ans.Calls = append(ans.Calls, reqlog.Call{Tool: call.Tool, Input: b})
		}
		if len(missing) > 0 {
			names := append([]string(nil), c.offered...)
			sort.Strings(names)
			div.text = fmt.Sprintf("the script calls %s, which the client does not offer (it offers: %s)", strings.Join(missing, ", "), strings.Join(names, ", "))
			div.expected = append(div.expected, "the client offers the tool(s) "+strings.Join(missing, ", "))
			div.actual = append(div.actual, "the client offers: "+strings.Join(names, ", "))
		}
		if len(needCWD) > 0 {
			if div.text != "" {
				div.text += "; "
			}
			div.text += "the script uses {{cwd}} but the request does not say its working directory (no 'Primary working directory:' line)"
			div.expected = append(div.expected, "a 'Primary working directory:' line for {{cwd}}")
			div.actual = append(div.actual, "the request names no working directory")
		}
	case r.HTTPError != nil:
		e := r.HTTPError
		k, _ := wire.ParseErrorKind(e.Error)
		ans.Kind, ans.Error, ans.Message, ans.Retry = KindHTTPError, e.Error, e.Message, string(e.Retry)
		if ans.Retry == "" {
			ans.Retry = string(script.RetryDefault)
		}
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
		return p, ans, renderDiv{}
	case r.Limit != nil:
		ans.Kind, ans.Window, ans.ResetsInSeconds = KindLimit, string(r.Limit.Window), r.Limit.ResetsInSeconds
		p.Status, p.Body = 429, wire.ErrorBody(wire.RateLimit, limitMessage(r.Limit.Window), requestID(c.seq))
		for k, v := range LimitHeaders(r.Limit.Window, c.now, r.Limit.ResetsInSeconds) {
			p.Headers.Set(k, v)
		}
		return p, ans, renderDiv{}
	case r.StreamError != nil:
		e := r.StreamError
		ans.Kind, ans.Error, ans.Message, ans.Text = KindStreamError, e.Error, e.Message, e.PartialText
		k, _ := wire.ParseErrorKind(e.Error)
		u := wire.Usage{Input: c.inputTokens, Output: wire.EstimateTokens(len(e.PartialText))}
		if !c.stream {
			// A non-streamed request cannot carry an in-stream error; send the same error as HTTP.
			p.Status, p.Body = k.Status(), wire.ErrorBody(k, e.Message, requestID(c.seq))
			return p, ans, renderDiv{}
		}
		p.Events = wire.ErrorEvents(meta, u, e.PartialText, k, e.Message)
		return p, ans, renderDiv{}
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
	return p, ans, div
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
