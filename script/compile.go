package script

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/seungduk/understudy/internal/wire"
)

// Script is a checked script, ready to run. It is immutable.
type Script struct {
	file       File
	divergence Divergence
	sessions   map[string]*SessionPlan
}

// SessionPlan is the checked steps of one session.
type SessionPlan struct {
	key    string
	steps  []*stepPlan
	byName map[string]int
}

type stepPlan struct {
	name      string
	expect    *matcher
	plain     *branch // reply/attempts form
	cases     []casePlan
	otherwise *branch
}

type casePlan struct {
	when *matcher
	br   *branch
}

type branch struct {
	attempts []Reply
	next     int // index of the next step; -1 means the following step
}

type matcher struct {
	src      Matcher
	tool     string
	result   Outcome
	exitCode *int
	contains string
	regex    *regexp.Regexp
}

var (
	sessionKeyRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,127}$`)
	stepNameRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	toolNameRe   = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.:-]{0,127}$`)
	templateRe   = regexp.MustCompile(`\{\{[^}]*\}\}`)
)

// Limits of the format.
const (
	MaxAttempts        = 100
	MaxCalls           = 64
	MaxStallSeconds    = 86400
	MaxRetryAfter      = 86400
	MaxResetsInSeconds = 31536000
	MaxAfterEvents     = 100000
	MaxFirstEventMS    = 600000
	MaxBetweenEventsMS = 60000
)

// Compile checks a File (for example one built in Go) and gives its Script.
// The error is Problems.
func Compile(f File) (*Script, error) {
	s, ps := compile(f, nil)
	if len(ps) > 0 {
		return nil, ps.sorted()
	}
	return s, nil
}

// MustCompile is Compile that panics on problems, for scripts written in Go tests.
func MustCompile(f File) *Script {
	s, err := Compile(f)
	if err != nil {
		panic(err)
	}
	return s
}

type checker struct {
	ls lines
	ps Problems
}

func (c *checker) add(path string, rule Rule, msg, fix string) {
	c.ps = append(c.ps, Problem{Path: path, Line: c.ls[path], Rule: rule, Message: msg, Fix: fix})
}

func enumFix(field string, vals ...string) string {
	return "set " + field + " to one of: " + strings.Join(vals, ", ")
}

func compile(f File, ls lines) (*Script, Problems) {
	c := &checker{ls: ls}
	if ls == nil {
		c.ls = lines{}
	}
	if f.Schema != SchemaV1 {
		msg := "the schema is missing"
		if f.Schema != "" {
			msg = fmt.Sprintf("the schema is %q, this version reads only %q", f.Schema, SchemaV1)
		}
		c.add("schema", RuleSchema, msg, "put 'schema: "+SchemaV1+"' as the first line")
	}
	div := f.Divergence
	switch div {
	case "":
		div = Strict
	case Strict, Lenient:
	default:
		c.add("divergence", RuleEnum, fmt.Sprintf("unknown divergence %q", f.Divergence), enumFix("divergence", "strict", "lenient"))
	}
	if p := f.Provenance; p != nil {
		switch p.Source {
		case Written, Recorded:
		case "":
			c.add("provenance.source", RuleRequired, "provenance needs a source", enumFix("provenance.source", "written", "recorded"))
		default:
			c.add("provenance.source", RuleEnum, fmt.Sprintf("unknown source %q", p.Source), enumFix("provenance.source", "written", "recorded"))
		}
		if p.RecordedAt != "" {
			if _, err := time.Parse(time.RFC3339, p.RecordedAt); err != nil {
				c.add("provenance.recorded_at", RulePattern, fmt.Sprintf("%q is not an RFC 3339 time", p.RecordedAt), "write it like 2026-11-18T09:30:00Z")
			}
		}
	}
	s := &Script{file: f, divergence: div, sessions: map[string]*SessionPlan{}}
	if len(f.Sessions) == 0 {
		c.add("sessions", RuleRequired, "the script has no sessions", "add 'sessions:' with at least one session, such as 'default:' with 'steps:'")
	}
	keys := make([]string, 0, len(f.Sessions))
	for k := range f.Sessions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		path := fmt.Sprintf("sessions[%q]", k)
		if !sessionKeyRe.MatchString(k) {
			c.add(path, RulePattern, fmt.Sprintf("the session key %q has characters a key cannot have", k),
				"use 1-128 letters, digits and . _ : / @ + -, starting with a letter or digit, such as task-17/writer")
		}
		s.sessions[k] = c.session(k, f.Sessions[k], path)
	}
	return s, c.ps
}

func (c *checker) session(key string, ses Session, path string) *SessionPlan {
	sp := &SessionPlan{key: key, byName: map[string]int{}}
	if len(ses.Steps) == 0 {
		c.add(path+".steps", RuleRequired, "the session has no steps", "add 'steps:' with at least one step")
	}
	for i, st := range ses.Steps {
		p := fmt.Sprintf("%s.steps[%d]", path, i)
		switch {
		case st.Name == "":
			c.add(p+".name", RuleRequired, "the step has no name", "give every step a name, such as name: read-file")
		case !stepNameRe.MatchString(st.Name):
			c.add(p+".name", RulePattern, fmt.Sprintf("the step name %q has characters a name cannot have", st.Name), "use 1-64 lower-case letters, digits, - and _, such as run-tests")
		default:
			if j, dup := sp.byName[st.Name]; dup {
				c.add(p+".name", RuleUnique, fmt.Sprintf("the step name %q is also used by step %d", st.Name, j), "give each step of a session its own name")
			} else {
				sp.byName[st.Name] = i
			}
		}
	}
	for i, st := range ses.Steps {
		sp.steps = append(sp.steps, c.step(st, fmt.Sprintf("%s.steps[%d]", path, i), sp.byName))
	}
	return sp
}

func (c *checker) step(st Step, p string, names map[string]int) *stepPlan {
	plan := &stepPlan{name: st.Name}
	if st.Expect != nil {
		plan.expect = c.matcher(*st.Expect, p+".expect")
	}
	forms := 0
	for _, has := range []bool{st.Reply != nil, len(st.Attempts) > 0, len(st.Cases) > 0} {
		if has {
			forms++
		}
	}
	if forms != 1 {
		msg := "the step has none of reply, attempts or cases"
		if forms > 1 {
			msg = "the step has more than one of reply, attempts and cases"
		}
		c.add(p, RuleOneOf, msg, "give the step exactly one of: reply (one answer), attempts (answers for the 1st, 2nd, ... request at this turn), cases (answers chosen by the tool results)")
		return plan
	}
	if len(st.Cases) > 0 {
		if st.Next != "" {
			c.add(p+".next", RuleNotApplied, "next does nothing on a step with cases", "put next inside the case (or otherwise) it belongs to")
		}
		for i, cs := range st.Cases {
			cp := fmt.Sprintf("%s.cases[%d]", p, i)
			var m *matcher
			if cs.When == nil {
				c.add(cp+".when", RuleRequired, "the case has no when", "add when: with a test of the tool results, such as when: {exit_code: 0}")
			} else {
				m = c.matcher(*cs.When, cp+".when")
			}
			plan.cases = append(plan.cases, casePlan{when: m, br: c.branch(cs.Reply, cs.Attempts, cs.Next, cp, names)})
		}
		if st.Otherwise != nil {
			plan.otherwise = c.branch(st.Otherwise.Reply, st.Otherwise.Attempts, st.Otherwise.Next, p+".otherwise", names)
		}
		return plan
	}
	if st.Otherwise != nil {
		c.add(p+".otherwise", RuleNotApplied, "otherwise does nothing on a step without cases", "remove otherwise, or turn reply into cases")
	}
	plan.plain = c.branch(st.Reply, st.Attempts, st.Next, p, names)
	return plan
}

func (c *checker) branch(r *Reply, attempts []Reply, next, p string, names map[string]int) *branch {
	b := &branch{next: -1}
	switch {
	case r != nil && len(attempts) > 0:
		c.add(p, RuleOneOf, "both reply and attempts are given", "keep reply for one answer, or attempts for one answer per retry")
	case r != nil:
		c.reply(*r, p+".reply")
		b.attempts = []Reply{*r}
	case len(attempts) > 0:
		if len(attempts) > MaxAttempts {
			c.add(p+".attempts", RuleRange, fmt.Sprintf("%d attempts, at most %d", len(attempts), MaxAttempts), "the last attempt repeats for every later retry, so fewer are enough")
		}
		for i, a := range attempts {
			c.reply(a, fmt.Sprintf("%s.attempts[%d]", p, i))
		}
		b.attempts = attempts
	default:
		c.add(p, RuleOneOf, "there is no reply or attempts", "add reply: with one answer, or attempts: with a list of answers")
	}
	if next != "" {
		j, ok := names[next]
		if !ok {
			all := make([]string, 0, len(names))
			for n := range names {
				all = append(all, n)
			}
			sort.Strings(all)
			fix := "name a step of this session: " + strings.Join(all, ", ")
			if cl := closest(next, all); cl != "" {
				fix = "did you mean " + cl + "? " + fix
			}
			c.add(p+".next", RuleReference, fmt.Sprintf("next names %q, which is not a step of this session", next), fix)
		}
		b.next = j
	}
	return b
}

func (c *checker) matcher(m Matcher, p string) *matcher {
	cm := &matcher{src: m, tool: m.Tool, result: m.Result, exitCode: m.ExitCode, contains: m.Contains}
	if m.Tool == "" && m.Result == "" && m.ExitCode == nil && m.Contains == "" && m.Regex == "" {
		c.add(p, RuleRequired, "the test is empty", "give at least one of: tool, result (ok or error), exit_code, contains, regex")
	}
	if m.Tool != "" && !toolNameRe.MatchString(m.Tool) {
		c.add(p+".tool", RulePattern, fmt.Sprintf("%q is not a tool name", m.Tool), "write the tool's name as Claude Code calls it, such as Bash or Edit")
	}
	switch m.Result {
	case "", OK, Error:
	default:
		c.add(p+".result", RuleEnum, fmt.Sprintf("unknown result %q", m.Result), enumFix("result", "ok", "error"))
	}
	if m.ExitCode != nil && (*m.ExitCode < 0 || *m.ExitCode > 255) {
		c.add(p+".exit_code", RuleRange, fmt.Sprintf("exit code %d is outside 0-255", *m.ExitCode), "write an exit code from 0 to 255")
	}
	if m.Regex != "" {
		re, err := regexp.Compile(m.Regex)
		if err != nil {
			c.add(p+".regex", RuleRegex, "the regex does not compile: "+err.Error(), "write an RE2 regular expression (no look-ahead or back-references), or use contains for plain text")
		}
		cm.regex = re
	}
	return cm
}

func (c *checker) reply(r Reply, p string) {
	kinds := []string{}
	if r.Message != nil {
		kinds = append(kinds, "message")
	}
	if r.ToolUse != nil {
		kinds = append(kinds, "tool_use")
	}
	if r.HTTPError != nil {
		kinds = append(kinds, "http_error")
	}
	if r.StreamError != nil {
		kinds = append(kinds, "stream_error")
	}
	if r.Limit != nil {
		kinds = append(kinds, "limit")
	}
	const kindFix = "give the reply exactly one of: message, tool_use, http_error, stream_error, limit"
	if len(kinds) != 1 {
		msg := "the reply has no kind"
		if len(kinds) > 1 {
			msg = "the reply has several kinds: " + strings.Join(kinds, ", ")
		}
		c.add(p, RuleOneOf, msg, kindFix)
		return
	}
	streamed := r.HTTPError == nil && r.Limit == nil
	switch {
	case r.Message != nil:
		m := r.Message
		if m.Text == "" {
			c.add(p+".message.text", RuleRequired, "the message has no text", "write the final answer in text:")
		}
		switch m.Stop {
		case "", EndTurn, MaxTokens:
		default:
			c.add(p+".message.stop", RuleEnum, fmt.Sprintf("unknown stop %q", m.Stop), enumFix("stop", "end_turn", "max_tokens"))
		}
	case r.ToolUse != nil:
		t := r.ToolUse
		if len(t.Calls) == 0 {
			c.add(p+".tool_use.calls", RuleRequired, "tool_use has no calls", "add calls: with at least one {tool: Name, input: {...}}; for a plain answer use message instead")
		}
		if len(t.Calls) > MaxCalls {
			c.add(p+".tool_use.calls", RuleRange, fmt.Sprintf("%d calls, at most %d", len(t.Calls), MaxCalls), "split the calls over several steps")
		}
		for i, call := range t.Calls {
			cp := fmt.Sprintf("%s.tool_use.calls[%d]", p, i)
			if call.Tool == "" {
				c.add(cp+".tool", RuleRequired, "the call has no tool", "name the tool, such as tool: Read")
			} else if !toolNameRe.MatchString(call.Tool) {
				c.add(cp+".tool", RulePattern, fmt.Sprintf("%q is not a tool name", call.Tool), "write the tool's name as Claude Code calls it, such as Bash or Edit")
			}
			if call.Input == nil {
				c.add(cp+".input", RuleRequired, "the call has no input", "add input: with the tool's fields, such as {file_path: \"{{cwd}}/a.txt\"}; write input: {} for a tool without fields")
			}
			c.templates(call.Input, cp+".input")
		}
	case r.HTTPError != nil:
		e := r.HTTPError
		c.errorName(e.Error, p+".http_error.error")
		if e.RetryAfterSeconds != nil && (*e.RetryAfterSeconds < 0 || *e.RetryAfterSeconds > MaxRetryAfter) {
			c.add(p+".http_error.retry_after_seconds", RuleRange, fmt.Sprintf("%d is outside 0-%d", *e.RetryAfterSeconds, MaxRetryAfter), "write seconds from 0 to 86400")
		}
		switch e.Retry {
		case "", RetryDefault, RetryNever, RetryAlways:
		default:
			c.add(p+".http_error.retry", RuleEnum, fmt.Sprintf("unknown retry %q", e.Retry), enumFix("retry", "default", "never", "always"))
		}
	case r.StreamError != nil:
		c.errorName(r.StreamError.Error, p+".stream_error.error")
	case r.Limit != nil:
		l := r.Limit
		switch l.Window {
		case FiveHour, SevenDay, SevenDayOpus, SevenDaySonnet:
		case "":
			c.add(p+".limit.window", RuleRequired, "the limit has no window", enumFix("window", "five_hour", "seven_day", "seven_day_opus", "seven_day_sonnet"))
		default:
			c.add(p+".limit.window", RuleEnum, fmt.Sprintf("unknown window %q", l.Window), enumFix("window", "five_hour", "seven_day", "seven_day_opus", "seven_day_sonnet"))
		}
		if l.ResetsInSeconds < 1 || l.ResetsInSeconds > MaxResetsInSeconds {
			c.add(p+".limit.resets_in_seconds", RuleRange, fmt.Sprintf("resets_in_seconds %d is outside 1-%d", l.ResetsInSeconds, MaxResetsInSeconds), "write how many seconds until the limit resets, such as 3600")
		}
	}
	if s := r.Stall; s != nil {
		sp := p + ".stall"
		if (s.Seconds != 0) == (s.Gate != "") {
			c.add(sp, RuleOneOf, "a stall needs exactly one of seconds or gate", "write stall: {seconds: 30} for a timed stall, or stall: {gate: my-gate} to hold until the gate is released")
		}
		if s.Seconds < 0 || s.Seconds > MaxStallSeconds {
			c.add(sp+".seconds", RuleRange, fmt.Sprintf("%d is outside 1-%d", s.Seconds, MaxStallSeconds), "write seconds from 1 to 86400")
		}
		if s.Gate != "" && !stepNameRe.MatchString(s.Gate) {
			c.add(sp+".gate", RulePattern, fmt.Sprintf("the gate name %q has characters a name cannot have", s.Gate), "use 1-64 lower-case letters, digits, - and _")
		}
		switch s.At {
		case "", BeforeResponse:
		case MidStream:
			if !streamed {
				c.add(sp+".at", RuleNotApplied, "an error response has no stream to stall in", "use at: before_response, or stall a message, tool_use or stream_error")
			}
		default:
			c.add(sp+".at", RuleEnum, fmt.Sprintf("unknown at %q", s.At), enumFix("at", "before_response", "mid_stream"))
		}
	}
	if ct := r.Cut; ct != nil {
		cp := p + ".cut"
		if !streamed {
			c.add(cp, RuleNotApplied, "cut does nothing on "+kinds[0], "cut a streamed reply (message, tool_use, stream_error); to fail before any response use a message with cut: {after_events: 0, how: reset}")
		}
		if ct.AfterEvents < 0 || ct.AfterEvents > MaxAfterEvents {
			c.add(cp+".after_events", RuleRange, fmt.Sprintf("%d is outside 0-%d", ct.AfterEvents, MaxAfterEvents), "write how many SSE events to send before the cut; 0 cuts before the response")
		}
		switch ct.How {
		case Close, Reset:
		case "":
			c.add(cp+".how", RuleRequired, "the cut has no how", enumFix("how", "close", "reset"))
		default:
			c.add(cp+".how", RuleEnum, fmt.Sprintf("unknown how %q", ct.How), enumFix("how", "close", "reset"))
		}
	}
	if pc := r.Pace; pc != nil {
		pp := p + ".pace"
		if pc.FirstEventMS < 0 || pc.FirstEventMS > MaxFirstEventMS {
			c.add(pp+".first_event_ms", RuleRange, fmt.Sprintf("%d is outside 0-%d", pc.FirstEventMS, MaxFirstEventMS), "write milliseconds from 0 to 600000")
		}
		if pc.BetweenEventsMS < 0 || pc.BetweenEventsMS > MaxBetweenEventsMS {
			c.add(pp+".between_events_ms", RuleRange, fmt.Sprintf("%d is outside 0-%d", pc.BetweenEventsMS, MaxBetweenEventsMS), "write milliseconds from 0 to 60000")
		}
		if !streamed && pc.BetweenEventsMS != 0 {
			c.add(pp+".between_events_ms", RuleNotApplied, "an error response has no events to space out", "use first_event_ms to delay the error")
		}
	}
}

func (c *checker) errorName(name, p string) {
	if name == "" {
		c.add(p, RuleRequired, "the error has no kind", enumFix("error", wire.ErrorKindNames()...))
		return
	}
	if _, ok := wire.ParseErrorKind(name); !ok {
		fix := enumFix("error", wire.ErrorKindNames()...)
		if cl := closest(name, wire.ErrorKindNames()); cl != "" {
			fix = "did you mean " + cl + "? " + fix
		}
		c.add(p, RuleEnum, fmt.Sprintf("unknown error %q", name), fix)
	}
}

func (c *checker) templates(v any, p string) {
	switch x := v.(type) {
	case string:
		for _, t := range templateRe.FindAllString(x, -1) {
			if t != "{{cwd}}" {
				c.add(p, RuleTemplate, fmt.Sprintf("%s is not a known placeholder", t), "the only placeholder is {{cwd}}, the client's working directory; write other text literally")
			}
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			c.templates(x[k], p+"."+k)
		}
	case []any:
		for i, e := range x {
			c.templates(e, fmt.Sprintf("%s[%d]", p, i))
		}
	}
}

// Divergence is the script's divergence policy (strict when not written).
func (s *Script) Divergence() Divergence { return s.divergence }

// File is the script as written.
func (s *Script) File() File { return s.file }

// Session is the plan of the session with this key.
func (s *Script) Session(key string) (*SessionPlan, bool) {
	p, ok := s.sessions[key]
	return p, ok
}

// Keys is every session key, sorted.
func (s *Script) Keys() []string {
	out := make([]string, 0, len(s.sessions))
	for k := range s.sessions {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ValidSessionKey says whether k can be a session key.
func ValidSessionKey(k string) bool { return sessionKeyRe.MatchString(k) }

// ValidGateName says whether g can be a gate name.
func ValidGateName(g string) bool { return stepNameRe.MatchString(g) }
