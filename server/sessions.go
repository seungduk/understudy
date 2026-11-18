package server

import (
	"sort"

	"github.com/seungduk/understudy/internal/reqlog"
	"github.com/seungduk/understudy/script"
)

// SessionState is the closed set of states a session can be in, read from its
// newest request.
type SessionState string

const (
	// StateIdle: the script has the session, and no request of it has come yet.
	StateIdle SessionState = "idle"
	// StateWaiting: the last answer asks the client for more (tool results, or a retry
	// after an error it may retry); the next request has not come yet.
	StateWaiting SessionState = "waiting"
	// StateStreaming: a reply is being sent now.
	StateStreaming SessionState = "streaming"
	// StateStalled: a reply waits in a stall (a timer, or a gate to release).
	StateStalled SessionState = "stalled"
	// StateDiverged: the last request did not fit the script, and was refused.
	StateDiverged SessionState = "diverged"
	// StateDone: the last answer ended the client's turn (a final message, a usage
	// limit, or an error the client is told not to retry).
	StateDone SessionState = "done"
)

// SessionStatus is one session as the control API shows it.
type SessionStatus struct {
	Key   string       `json:"key"`
	State SessionState `json:"state"`
	// InScript says whether the loaded script has the session; ScriptSteps is how many steps it has there.
	InScript    bool `json:"in_script"`
	ScriptSteps int  `json:"script_steps,omitempty"`
	// Requests counts the session's /v1/messages requests; Divergences how many of them diverged (strict or lenient).
	Requests    int `json:"requests"`
	Divergences int `json:"divergences"`
	// Turn, Step, Branch, Attempt and Outcome are the newest request's.
	Turn    int            `json:"turn"`
	Step    string         `json:"step,omitempty"`
	Branch  string         `json:"branch,omitempty"`
	Attempt int            `json:"attempt,omitempty"`
	Reply   string         `json:"reply,omitempty"`
	Outcome reqlog.Outcome `json:"outcome,omitempty"`
	LastSeq int            `json:"last_seq,omitempty"`
	LastAt  string         `json:"last_at,omitempty"`
	// Gate is the gate the newest request waits on, when it does.
	Gate string `json:"gate,omitempty"`
	// Divergence is the newest request's divergence, if any.
	Divergence string `json:"divergence,omitempty"`
}

// Sessions is every session seen in the log or named by the loaded script, by key.
func (s *Server) Sessions() []SessionStatus {
	return SessionsOf(s.Requests(), s.script.Load())
}

// SessionsOf derives the sessions from a request log and a script (nil for none).
func SessionsOf(entries []Entry, sc *script.Script) []SessionStatus {
	by := map[string]*SessionStatus{}
	get := func(k string) *SessionStatus {
		st, ok := by[k]
		if !ok {
			st = &SessionStatus{Key: k, State: StateIdle}
			by[k] = st
		}
		return st
	}
	if sc != nil {
		for _, k := range sc.Keys() {
			st := get(k)
			p, _ := sc.Session(k)
			st.InScript, st.ScriptSteps = true, p.Steps()
		}
	}
	for _, e := range entries {
		if e.Outcome == reqlog.Other || e.Session == "" {
			continue
		}
		st := get(e.Session)
		st.Requests++
		if e.Divergence != "" || e.Outcome == reqlog.Diverged || e.Outcome == reqlog.NoScript {
			st.Divergences++
		}
		st.Turn, st.Step, st.Branch, st.Attempt, st.Reply = e.Turn, e.Step, e.Branch, e.Attempt, e.Reply
		st.Outcome, st.LastSeq, st.LastAt, st.Divergence, st.Gate = e.Outcome, e.Seq, e.At, e.Divergence, ""
		if e.Held != nil {
			st.Gate = e.Held.Gate
		}
		st.State = stateOf(e)
	}
	out := make([]SessionStatus, 0, len(by))
	for _, st := range by {
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func stateOf(e Entry) SessionState {
	switch e.Outcome {
	case reqlog.Pending:
		if e.Held != nil {
			return StateStalled
		}
		return StateStreaming
	case reqlog.Diverged, reqlog.NoScript, reqlog.BadRequest:
		return StateDiverged
	case reqlog.Served:
		a := e.Answer
		switch {
		case a == nil:
			return StateWaiting
		case a.Kind == KindMessage, a.Kind == KindLimit:
			return StateDone
		case a.Kind == KindHTTPError && a.Retry == string(script.RetryNever):
			return StateDone
		}
	}
	// tool_use, a retryable error, a cut or a client that went away: the client comes back.
	return StateWaiting
}
