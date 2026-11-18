package script

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Result is one tool result that a request carries.
type Result struct {
	Tool    string // the tool whose call this answers ("" when the call is not in the history)
	IsError bool
	Text    string
}

// Branch labels of a Choice.
const (
	BranchReply     = "reply"
	BranchOtherwise = "otherwise"
	BranchNone      = "none"
)

// Choice is what a session's script answers to one request.
type Choice struct {
	Turn      int    // assistant turns already in the request's history
	Step      string // the step that answers; "" when the script has ended
	StepIndex int    // -1 when the script has ended
	Branch    string // "reply", "case N" (0-based), "otherwise" or "none"
	// Attempts are the answers for the 1st, 2nd, ... request at this position
	// (the last repeats). Nil when the request diverged and the policy is strict.
	Attempts []Reply
	// Divergence says, in plain words, how the request did not fit the script; "" when it fits.
	Divergence string
	// Trace explains the choice: which tests ran and why they passed or failed.
	Trace []string
}

// Steps is how many steps the session has.
func (p *SessionPlan) Steps() int { return len(p.steps) }

// StepName is the name of step i.
func (p *SessionPlan) StepName(i int) string { return p.steps[i].name }

// Choose picks the answer for a request. history[k] holds the tool results the
// request carries before its assistant turn k, for k = 0..n, where n is the
// number of assistant turns in the request (so history has n+1 entries and
// history[n] is what the model is answering now). The past turns are walked
// again to find the position, so no state is kept between requests.
func (p *SessionPlan) Choose(history [][]Result, policy Divergence) Choice {
	if len(history) == 0 {
		history = [][]Result{nil}
	}
	n := len(history) - 1
	pos := 0
	var walk []string
	for k := 0; k < n; k++ {
		if pos >= len(p.steps) {
			if policy == Lenient {
				pos = len(p.steps) - 1
			} else {
				break
			}
		}
		d := p.decide(pos, history[k], Lenient)
		walk = append(walk, fmt.Sprintf("turn %d was answered by step %q (%s)", k, p.steps[pos].name, d.label))
		pos = d.nextPos
	}
	ch := Choice{Turn: n, StepIndex: -1, Branch: BranchNone, Trace: walk}
	if pos >= len(p.steps) {
		last := p.steps[len(p.steps)-1]
		ch.Divergence = fmt.Sprintf("the script of session %q has ended: its last step %q was answered, and the client asked for turn %d", p.key, last.name, n)
		if policy != Lenient {
			ch.Trace = append(ch.Trace, "the script has ended")
			return ch
		}
		ch.Trace = append(ch.Trace, "the script has ended; lenient: repeating the last step")
		pos = len(p.steps) - 1
	}
	d := p.decide(pos, history[n], policy)
	ch.Step, ch.StepIndex, ch.Branch = p.steps[pos].name, pos, d.label
	ch.Trace = append(ch.Trace, d.trace...)
	if d.divergence != "" {
		if ch.Divergence != "" {
			ch.Divergence += "; "
		}
		ch.Divergence += d.divergence
	}
	if d.br != nil {
		ch.Attempts = d.br.attempts
	}
	return ch
}

type decision struct {
	br         *branch
	label      string
	nextPos    int
	divergence string
	trace      []string
}

func (p *SessionPlan) decide(pos int, results []Result, policy Divergence) decision {
	st := p.steps[pos]
	d := decision{label: BranchNone, nextPos: pos + 1}
	where := fmt.Sprintf("step %q", st.name)
	if st.expect != nil {
		ok, why := st.expect.match(results)
		if ok {
			d.trace = append(d.trace, fmt.Sprintf("%s expect %s: yes, %s", where, st.expect.describe(), why))
		} else {
			d.trace = append(d.trace, fmt.Sprintf("%s expect %s: no, %s", where, st.expect.describe(), why))
			d.divergence = fmt.Sprintf("%s expected %s, but %s", where, st.expect.describe(), why)
			if policy != Lenient {
				return d
			}
		}
	}
	take := func(b *branch, label string) decision {
		d.br, d.label = b, label
		if b.next >= 0 {
			d.nextPos = b.next
		}
		return d
	}
	if st.plain != nil {
		return take(st.plain, BranchReply)
	}
	for i, cs := range st.cases {
		ok, why := cs.when.match(results)
		yes := "no"
		if ok {
			yes = "yes"
		}
		d.trace = append(d.trace, fmt.Sprintf("%s case %d when %s: %s, %s", where, i, cs.when.describe(), yes, why))
		if ok {
			return take(cs.br, fmt.Sprintf("case %d", i))
		}
	}
	if st.otherwise != nil {
		d.trace = append(d.trace, where+" otherwise: taken")
		return take(st.otherwise, BranchOtherwise)
	}
	div := fmt.Sprintf("%s: no case matched the tool results and the step has no otherwise", where)
	if d.divergence != "" {
		d.divergence += "; " + div
	} else {
		d.divergence = div
	}
	if policy != Lenient || len(st.cases) == 0 {
		return d
	}
	d.trace = append(d.trace, where+" lenient: taking case 0")
	return take(st.cases[0].br, "case 0")
}

var exitCodeRe = regexp.MustCompile(`(?m)^(?:Error: )?Exit code (\d+)`)

// ExitCode reads the exit code from a Bash tool result: 0 for a result that is
// not an error, the number of an "Exit code N" line for an error, false otherwise.
func ExitCode(r Result) (int, bool) {
	if !r.IsError {
		return 0, true
	}
	m := exitCodeRe.FindStringSubmatch(r.Text)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	return n, err == nil
}

func (m *matcher) describe() string {
	var parts []string
	if m.tool != "" {
		parts = append(parts, "tool: "+m.tool)
	}
	if m.result != "" {
		parts = append(parts, "result: "+string(m.result))
	}
	if m.exitCode != nil {
		parts = append(parts, "exit_code: "+strconv.Itoa(*m.exitCode))
	}
	if m.contains != "" {
		parts = append(parts, "contains: "+strconv.Quote(m.contains))
	}
	if m.regex != nil {
		parts = append(parts, "regex: "+strconv.Quote(m.regex.String()))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// match says whether any result passes every test, and why (or why not).
func (m *matcher) match(results []Result) (bool, string) {
	if len(results) == 0 {
		return false, "the request carries no tool results"
	}
	var reasons []string
	considered := 0
	for i, r := range results {
		name := r.Tool
		if name == "" {
			name = "unknown tool"
		}
		label := fmt.Sprintf("result %d (%s)", i, name)
		if m.tool != "" && r.Tool != m.tool {
			continue
		}
		considered++
		if why := m.fail(r); why != "" {
			reasons = append(reasons, label+" "+why)
			continue
		}
		return true, label + " passes"
	}
	if considered == 0 {
		return false, fmt.Sprintf("no result is from tool %s", m.tool)
	}
	return false, strings.Join(reasons, "; ")
}

func (m *matcher) fail(r Result) string {
	if m.result == OK && r.IsError {
		return "is an error"
	}
	if m.result == Error && !r.IsError {
		return "is not an error"
	}
	if m.exitCode != nil {
		code, ok := ExitCode(r)
		if !ok {
			return "shows no exit code"
		}
		if code != *m.exitCode {
			return fmt.Sprintf("has exit code %d", code)
		}
	}
	if m.contains != "" && !strings.Contains(r.Text, m.contains) {
		return "does not contain " + strconv.Quote(m.contains)
	}
	if m.regex != nil && !m.regex.MatchString(r.Text) {
		return "does not match " + strconv.Quote(m.regex.String())
	}
	return ""
}
