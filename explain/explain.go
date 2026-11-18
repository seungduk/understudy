// Package explain says, for each request of a log, which session, turn and
// step answered it and why the other cases did not, optionally re-evaluated
// against another script (to see what an edited script would do).
package explain

import (
	"fmt"
	"io"
	"strings"

	"github.com/seungduk/understudy/script"
	"github.com/seungduk/understudy/server"
)

// Explanation is one request, explained.
type Explanation struct {
	Seq        int      `json:"seq"`
	Path       string   `json:"path"`
	Session    string   `json:"session,omitempty"`
	Source     string   `json:"session_source,omitempty"`
	Turn       int      `json:"turn"`
	Step       string   `json:"step,omitempty"`
	Branch     string   `json:"branch,omitempty"`
	Attempt    int      `json:"attempt,omitempty"`
	Reply      string   `json:"reply,omitempty"`
	Outcome    string   `json:"outcome"`
	Divergence string   `json:"divergence,omitempty"`
	Error      string   `json:"error,omitempty"`
	Why        []string `json:"why,omitempty"`
	// Reevaluated is set when a script was given: the choice it makes for the same request.
	Reevaluated *Choice `json:"reevaluated,omitempty"`
}

// Choice is a re-evaluated choice.
type Choice struct {
	Step       string   `json:"step,omitempty"`
	Branch     string   `json:"branch"`
	Divergence string   `json:"divergence,omitempty"`
	Why        []string `json:"why,omitempty"`
}

// Entries explains log entries of /v1/messages (other paths are left out). With
// sc, each request is also chosen again by sc from its logged tool results.
func Entries(entries []server.Entry, sc *script.Script) []Explanation {
	var out []Explanation
	for _, e := range entries {
		if e.Outcome == server.Other {
			continue
		}
		x := Explanation{Seq: e.Seq, Path: e.Path, Session: e.Session, Source: e.SessionSource, Turn: e.Turn, Step: e.Step, Branch: e.Branch,
			Attempt: e.Attempt, Reply: e.Reply, Outcome: string(e.Outcome), Divergence: e.Divergence, Error: e.Error, Why: e.Trace}
		if sc != nil && e.Session != "" {
			if plan, ok := sc.Session(e.Session); ok {
				h := make([][]script.Result, len(e.History))
				for i, rs := range e.History {
					for _, r := range rs {
						h[i] = append(h[i], script.Result{Tool: r.Tool, IsError: r.IsError, Text: r.Text})
					}
				}
				if len(h) == 0 {
					h = [][]script.Result{nil}
				}
				ch := plan.Choose(h, sc.Divergence())
				x.Reevaluated = &Choice{Step: ch.Step, Branch: ch.Branch, Divergence: ch.Divergence, Why: ch.Trace}
			} else {
				x.Reevaluated = &Choice{Branch: script.BranchNone, Divergence: fmt.Sprintf("the script has no session %q", e.Session)}
			}
		}
		out = append(out, x)
	}
	return out
}

// Write prints explanations for people (and agents) to read.
func Write(w io.Writer, xs []Explanation) {
	for _, x := range xs {
		fmt.Fprintf(w, "#%d %s session=%q (%s) turn=%d", x.Seq, x.Path, x.Session, x.Source, x.Turn)
		if x.Step != "" {
			fmt.Fprintf(w, " step=%q branch=%q attempt=%d reply=%s", x.Step, x.Branch, x.Attempt, x.Reply)
		}
		fmt.Fprintf(w, " outcome=%s\n", x.Outcome)
		for _, l := range x.Why {
			fmt.Fprintf(w, "    %s\n", l)
		}
		if x.Divergence != "" {
			fmt.Fprintf(w, "    DIVERGED: %s\n", x.Divergence)
		}
		if x.Error != "" && x.Error != "understudy: the request does not fit the script: "+x.Divergence {
			fmt.Fprintf(w, "    sent: %s\n", x.Error)
		}
		if r := x.Reevaluated; r != nil {
			fmt.Fprintf(w, "    with the given script: step=%q branch=%q\n", r.Step, r.Branch)
			for _, l := range r.Why {
				fmt.Fprintf(w, "        %s\n", l)
			}
			if r.Divergence != "" {
				fmt.Fprintf(w, "        DIVERGES: %s\n", r.Divergence)
			}
		}
	}
	if len(xs) == 0 {
		fmt.Fprintln(w, "no /v1/messages requests in the log")
	}
}

// Summary is one line: how many requests, served, diverged.
func Summary(xs []Explanation) string {
	counts := map[string]int{}
	for _, x := range xs {
		counts[x.Outcome]++
	}
	var parts []string
	for _, o := range []string{"served", "pending", "cut_close", "cut_reset", "client_gone", "diverged", "no_script", "bad_request"} {
		if counts[o] > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", o, counts[o]))
		}
	}
	return fmt.Sprintf("%d request(s): %s", len(xs), strings.Join(parts, " "))
}
