package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/seungduk/understudy/core"
	"github.com/seungduk/understudy/harness"
	"github.com/seungduk/understudy/internal/conv"
	"github.com/seungduk/understudy/internal/deliver"
	"github.com/seungduk/understudy/internal/protocol/registry"
	"github.com/seungduk/understudy/internal/reqlog"
	"github.com/seungduk/understudy/protocol"
	"github.com/seungduk/understudy/script"
)

// MaxBody is the largest request body read.
const MaxBody = 64 << 20

// routes registers every codec's routes; a route several codecs share goes to
// the first codec that accepts the request, else to the last of them.
func (s *Server) routes() {
	byPattern := map[string][]protocol.Codec{}
	var order []string
	for _, c := range registry.All() {
		for _, p := range c.Routes() {
			if byPattern[p] == nil {
				order = append(order, p)
			}
			byPattern[p] = append(byPattern[p], c)
		}
	}
	for _, p := range order {
		cs := byPattern[p]
		s.mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			c := cs[len(cs)-1]
			for _, x := range cs {
				if x.Accepts(r) {
					c = x
					break
				}
			}
			s.serve(c, w, r)
		})
	}
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

func write(w http.ResponseWriter, e protocol.Encoded) {
	for k, v := range e.Header {
		w.Header()[k] = v
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(e.Status)
	_, _ = w.Write(e.Body)
}

// refuse answers with an API error that tells the client not to retry, and logs why.
func (s *Server) refuse(c protocol.Codec, w http.ResponseWriter, seq int, m protocol.Meta, outcome reqlog.Outcome, kind core.ErrorKind, msg string) {
	m.Stream = false
	e := c.Encode(m, protocol.Out{Kind: protocol.OutError, Error: protocol.ErrorSpec{Kind: kind, Message: msg, Retry: core.RetryNever}})
	s.log.Update(seq, func(en *Entry) { en.Outcome, en.Status, en.Error = outcome, e.Status, msg })
	write(w, e)
}

func (s *Server) serve(c protocol.Codec, w http.ResponseWriter, r *http.Request) {
	en := s.entry(r)
	en.Protocol = c.ID().Name()
	seq := s.log.Add(en)
	meta := protocol.Meta{RequestID: requestID(seq), Now: s.clk.Now(), Seed: requestID(seq)}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBody+1))
	if err != nil {
		s.log.Update(seq, func(e *Entry) { e.Outcome = reqlog.ClientGone })
		return
	}
	if len(body) > MaxBody {
		s.refuse(c, w, seq, meta, reqlog.BadRequest, core.RequestTooLarge, fmt.Sprintf("understudy: the request body is larger than %d bytes", MaxBody))
		return
	}
	d, err := c.Decode(r, body)
	if err != nil {
		s.refuse(c, w, seq, meta, reqlog.BadRequest, core.InvalidRequest, err.Error())
		return
	}
	meta.Op = d.Op
	other := func(o protocol.Out) {
		e := c.Encode(meta, o)
		s.log.Update(seq, func(en *Entry) { en.Outcome, en.Status = reqlog.Other, e.Status })
		write(w, e)
	}
	switch d.Op {
	case protocol.Probe:
		other(protocol.Out{Kind: protocol.OutProbe})
		return
	case protocol.CountTokens:
		other(protocol.Out{Kind: protocol.OutTokens, Tokens: core.EstimateTokens(len(body))})
		return
	case protocol.ListModels:
		other(protocol.Out{Kind: protocol.OutModels, Models: s.modelIDs()})
		return
	case protocol.GetModel:
		other(protocol.Out{Kind: protocol.OutModels, Models: []string{d.Model}})
		return
	}
	s.generate(c, w, r, seq, meta, d.Request, len(body))
}

func (s *Server) modelIDs() []string {
	s.mu.Lock()
	ids := make([]string, 0, len(s.models)+1)
	for m := range s.models {
		ids = append(ids, m)
	}
	s.mu.Unlock()
	if len(ids) == 0 {
		ids = append(ids, DefaultModel)
	}
	sort.Strings(ids)
	return ids
}

// profileFor is the harness that reads a request: the one the script's
// session names, else the X-Understudy-Harness header, else the one whose tools
// the request offers.
func profileFor(plan *script.SessionPlan, h http.Header, p protocol.ID, req core.Request) (*harness.Profile, string) {
	if plan != nil && plan.Harness() != "" {
		if pr, ok := harness.Lookup(plan.Harness()); ok {
			return pr, "script"
		}
	}
	if name := strings.TrimSpace(h.Get(conv.HarnessHeader)); name != "" {
		if pr, ok := harness.Lookup(name); ok {
			return pr, "header"
		}
	}
	return harness.Detect(p, req), "detected"
}

func (s *Server) generate(c protocol.Codec, w http.ResponseWriter, r *http.Request, seq int, meta protocol.Meta, req core.Request, bodyLen int) {
	s.sawModel(req.Model)
	meta.Model, meta.Stream = req.Model, req.Stream
	meta.Usage.Input = core.EstimateTokens(bodyLen)
	k := conv.SessionKey(r.Header, req)
	sc := s.script.Load()
	var plan *script.SessionPlan
	if sc != nil && k.Key != "" {
		plan, _ = sc.Session(k.Key)
	}
	pr, _ := profileFor(plan, r.Header, c.ID(), req)
	history := conv.History(req, pr)
	cwd := conv.WorkingDir(req, pr)
	s.log.Update(seq, func(e *Entry) {
		e.Model, e.Stream, e.Session, e.SessionSource, e.Harness = req.Model, req.Stream, k.Key, k.Source.String(), string(pr.ID)
		e.Turn, e.ToolsOffered, e.CWD, e.History = len(history)-1, len(req.Tools), cwd, logHistory(history)
		e.Prompt = cutText(conv.Prompt(req), reqlog.MaxPromptText)
	})
	if name, text, ok := pr.AuxReply(c.ID(), req); ok {
		e := c.Encode(meta, protocol.Out{Kind: protocol.OutReply, Reply: core.Reply{Text: text, Stop: core.EndTurn}})
		s.log.Update(seq, func(en *Entry) {
			en.Aux, en.Reply, en.Status, en.Outcome = name, KindMessage, http.StatusOK, reqlog.Pending
		})
		out := deliver.Send(r.Context(), w, deliver.Plan{Status: e.Status, Headers: e.Header, Body: e.Body, Frames: e.Frames}, deliver.Deps{Clock: s.clk, Gates: s.gates})
		s.log.Update(seq, func(en *Entry) { en.Outcome = out })
		return
	}
	if k.Bad != "" || k.Key == "" {
		s.refuse(c, w, seq, meta, reqlog.NoScript, core.InvalidRequest, fmt.Sprintf(
			"understudy: the session key %q (from the %s) is not a valid key. Fix: use 1-128 letters, digits and . _ : / @ + -, such as task-17/writer",
			k.Bad, k.Source))
		return
	}
	if sc == nil {
		s.refuse(c, w, seq, meta, reqlog.NoScript, core.InvalidRequest,
			"understudy: no script is loaded. Fix: start with --script FILE, or PUT the script to "+ControlPrefix+"script")
		return
	}
	if plan == nil {
		s.refuse(c, w, seq, meta, reqlog.NoScript, core.InvalidRequest, fmt.Sprintf(
			"understudy: the script has no session %q (key from the %s). Fix: add sessions[%q] to the script, or send one of its keys (%s) in the %s header or as [understudy-session: KEY] in the system prompt",
			k.Key, k.Source, k.Key, strings.Join(sc.Keys(), ", "), conv.SessionHeader))
		return
	}
	ch := plan.Choose(history, sc.Divergence())
	s.log.Update(seq, func(e *Entry) {
		e.Step, e.Branch, e.Divergence, e.Trace = ch.Step, ch.Branch, ch.Divergence, ch.Trace
		if ch.Divergence != "" {
			e.Expected, e.Actual = ch.Expected, actualOf(history[len(history)-1], ch.Turn)
		}
	})
	if ch.Attempts == nil {
		s.refuse(c, w, seq, meta, reqlog.Diverged, core.InvalidRequest, "understudy: the request does not fit the script: "+ch.Divergence)
		return
	}
	n := s.nextAttempt(attemptKey{session: k.Key, step: ch.Step, branch: ch.Branch, turn: ch.Turn})
	reply := ch.Attempts[min(n, len(ch.Attempts))-1]
	rc := renderCtx{session: k.Key, turn: ch.Turn, step: ch.Step, attempt: n, cwd: cwd, offered: req.Tools, profile: pr, now: s.clk.Now()}
	meta.Seed = rc.id("")
	out, faults, ans, div := render(reply, rc)
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
			s.refuse(c, w, seq, meta, reqlog.Diverged, core.InvalidRequest, "understudy: the request does not fit the script: "+div.text)
			return
		}
	}
	if out.Kind == protocol.OutReply {
		outLen := len(out.Reply.Text) + len(out.Reply.Thinking)
		for _, cl := range out.Reply.Calls {
			outLen += len(cl.Input) + len(cl.Raw)
		}
		meta.Usage.Output = core.EstimateTokens(outLen)
	} else if out.Kind == protocol.OutStreamError {
		meta.Usage.Output = core.EstimateTokens(len(out.Partial))
	}
	e := c.Encode(meta, out)
	p := faults
	p.Status, p.Headers, p.Body, p.Frames = e.Status, e.Header, e.Body, e.Frames
	if p.Headers == nil {
		p.Headers = http.Header{}
	}
	p.Headers.Set("Request-Id", requestID(seq))
	s.log.Update(seq, func(e *Entry) { e.Status, e.Outcome = p.Status, reqlog.Pending })
	if p.Frames != nil {
		s.log.Update(seq, func(e *Entry) { e.Status = http.StatusOK })
	}
	hold := &reqlog.Hold{Gate: p.StallGate, Seconds: int(p.StallFor / time.Second)}
	res := deliver.Send(r.Context(), w, p, deliver.Deps{Clock: s.clk, Gates: s.gates,
		OnStall: func() {
			s.log.Update(seq, func(e *Entry) {
				h := *hold
				h.Since = s.clk.Now().UTC().Format(time.RFC3339Nano)
				e.Held = &h
			})
		},
		OnResume: func() { s.log.Update(seq, func(e *Entry) { e.Held = nil }) },
	})
	s.log.Update(seq, func(e *Entry) { e.Outcome = res })
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

// actualOf describes the tool results a diverging request carried, one line each,
// as the harness profile read them.
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
		if r.HasExit {
			how += fmt.Sprintf(", exit code %d", r.Exit)
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
	session, step, cwd string
	turn, attempt      int
	offered            []core.Tool
	profile            *harness.Profile
	now                time.Time
}

func (c renderCtx) id(prefix string, parts ...string) string {
	return core.DeriveID(prefix, append([]string{c.session, strconv.Itoa(c.turn), c.step, strconv.Itoa(c.attempt)}, parts...)...)
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

// render turns a script reply into a protocol-neutral outcome, its delivery
// faults and the answer the log keeps. div.text is non-empty when the reply
// cannot be sent as written (a tool the client does not offer, an action the
// harness has no tool for, {{cwd}} without a working directory).
func render(r script.Reply, c renderCtx) (out protocol.Out, p deliver.Plan, ans reqlog.Answer, div renderDiv) {
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
	switch {
	case r.Message != nil:
		stop := core.EndTurn
		ans.Kind, ans.Thinking, ans.Text, ans.Stop = KindMessage, r.Message.Thinking, r.Message.Text, string(script.EndTurn)
		if r.Message.Stop == script.MaxTokens {
			stop, ans.Stop = core.MaxTokens, string(script.MaxTokens)
		}
		rep := core.Reply{Thinking: r.Message.Thinking, Text: r.Message.Text, Stop: stop}
		if rep.Thinking != "" {
			rep.ThinkingSignature = c.id("sig_", rep.Thinking)
		}
		return protocol.Out{Kind: protocol.OutReply, Reply: rep}, p, ans, renderDiv{}
	case r.ToolUse != nil:
		rep, d := renderCalls(r.ToolUse, c)
		ans.Kind, ans.Thinking, ans.Text, ans.Stop = KindToolUse, r.ToolUse.Thinking, r.ToolUse.Text, "tool_use"
		for _, cl := range rep.Calls {
			in := cl.Input
			if cl.Kind == core.Freeform {
				in, _ = json.Marshal(map[string]string{"input": cl.Raw})
			}
			ans.Calls = append(ans.Calls, reqlog.Call{Tool: cl.Tool, Input: in})
		}
		return protocol.Out{Kind: protocol.OutReply, Reply: rep}, p, ans, d
	case r.HTTPError != nil:
		e := r.HTTPError
		ans.Kind, ans.Error, ans.Message, ans.Retry = KindHTTPError, e.Error, e.Message, string(e.Retry)
		if ans.Retry == "" {
			ans.Retry = string(script.RetryDefault)
		}
		k, _ := core.ParseErrorKind(e.Error)
		spec := protocol.ErrorSpec{Kind: k, Message: e.Message}
		if e.RetryAfterSeconds != nil {
			spec.RetryAfter, spec.HasRetryAfter = time.Duration(*e.RetryAfterSeconds)*time.Second, true
		}
		switch e.Retry {
		case script.RetryNever:
			spec.Retry = core.RetryNever
		case script.RetryAlways:
			spec.Retry = core.RetryAlways
		}
		return protocol.Out{Kind: protocol.OutError, Error: spec}, p, ans, renderDiv{}
	case r.Limit != nil:
		ans.Kind, ans.Window, ans.ResetsInSeconds = KindLimit, string(r.Limit.Window), r.Limit.ResetsInSeconds
		w, _ := core.ParseWindow(string(r.Limit.Window))
		return protocol.Out{Kind: protocol.OutLimit, Limit: protocol.LimitSpec{Window: w, Now: c.now,
			Resets: c.now.Add(time.Duration(r.Limit.ResetsInSeconds) * time.Second)}}, p, ans, renderDiv{}
	case r.StreamError != nil:
		e := r.StreamError
		ans.Kind, ans.Error, ans.Message, ans.Text = KindStreamError, e.Error, e.Message, e.PartialText
		k, _ := core.ParseErrorKind(e.Error)
		return protocol.Out{Kind: protocol.OutStreamError, Error: protocol.ErrorSpec{Kind: k, Message: e.Message}, Partial: e.PartialText}, p, ans, renderDiv{}
	}
	panic("server: a compiled reply has no kind")
}

func renderCalls(t *script.ToolUse, c renderCtx) (core.Reply, renderDiv) {
	rep := core.Reply{Thinking: t.Thinking, Text: t.Text, Stop: core.ToolUse}
	if rep.Thinking != "" {
		rep.ThinkingSignature = c.id("sig_", rep.Thinking)
	}
	offered := map[string]core.Tool{}
	for _, o := range c.offered {
		offered[o.Name] = o
	}
	var missing, needCWD, noTool []string
	for i, call := range t.Calls {
		var cl core.Call
		usedCWD := false
		if a, args, ok := actionOf(call); ok {
			sub := func(s string) string {
				if strings.Contains(s, "{{cwd}}") {
					usedCWD = true
				}
				return strings.ReplaceAll(s, "{{cwd}}", c.cwd)
			}
			args.Command, args.Path, args.Content, args.Old, args.New = sub(args.Command), sub(args.Path), sub(args.Content), sub(args.Old), sub(args.New)
			var err error
			cl, err = c.profile.Call(a, args)
			if err != nil {
				noTool = append(noTool, err.Error())
				continue
			}
		} else {
			input, used := substitute(call.Input, c.cwd)
			usedCWD = used
			cl = core.Call{Tool: call.Tool}
			if o, ok := offered[call.Tool]; ok && o.Kind == core.Freeform {
				// A native call of a freeform tool writes its raw text as input: {input: ...}.
				m, _ := input.(map[string]any)
				s, _ := m["input"].(string)
				cl.Kind, cl.Raw = core.Freeform, s
			} else {
				cl.Input, _ = json.Marshal(input)
			}
		}
		cl.ID = c.id("", strconv.Itoa(i))
		if _, ok := offered[cl.Tool]; !ok {
			missing = append(missing, cl.Tool)
		}
		if usedCWD && c.cwd == "" {
			needCWD = append(needCWD, cl.Tool)
		}
		rep.Calls = append(rep.Calls, cl)
	}
	var div renderDiv
	var texts []string
	for _, n := range noTool {
		texts = append(texts, n)
		div.expected = append(div.expected, "a harness with a tool for the action ("+n+")")
		div.actual = append(div.actual, fmt.Sprintf("the request was read as %s", c.profile.ID))
	}
	names := make([]string, 0, len(c.offered))
	for _, o := range c.offered {
		names = append(names, o.Name)
	}
	sort.Strings(names)
	if len(missing) > 0 {
		texts = append(texts, fmt.Sprintf("the script calls %s, which the client does not offer (it offers: %s)", strings.Join(missing, ", "), strings.Join(names, ", ")))
		div.expected = append(div.expected, "the client offers the tool(s) "+strings.Join(missing, ", "))
		div.actual = append(div.actual, "the client offers: "+strings.Join(names, ", "))
	}
	if len(needCWD) > 0 {
		texts = append(texts, fmt.Sprintf("the script uses {{cwd}} but the request does not say its working directory (the %s profile found none)", c.profile.ID))
		div.expected = append(div.expected, "a working directory in the prompt for {{cwd}}")
		div.actual = append(div.actual, "the request names no working directory")
	}
	div.text = strings.Join(texts, "; ")
	return rep, div
}

func actionOf(call script.Call) (core.Action, core.ActionArgs, bool) {
	switch {
	case call.Shell != nil:
		return core.Shell, core.ActionArgs{Command: call.Shell.Command}, true
	case call.Read != nil:
		return core.ReadFile, core.ActionArgs{Path: call.Read.Path}, true
	case call.Write != nil:
		return core.WriteFile, core.ActionArgs{Path: call.Write.Path, Content: call.Write.Content}, true
	case call.Edit != nil:
		return core.EditFile, core.ActionArgs{Path: call.Edit.Path, Old: call.Edit.Old, New: call.Edit.New}, true
	}
	return core.NoAction, core.ActionArgs{}, false
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
