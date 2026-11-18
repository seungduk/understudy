// Package reqlog is the request log: every request the server saw, what the
// script chose for it and how it ended. Safe for concurrent use.
package reqlog

import "sync"

// Outcome is the closed set of ways a request can end.
type Outcome string

const (
	Pending    Outcome = "pending"     // still being answered (stalled or streaming)
	Served     Outcome = "served"      // the whole reply was sent
	CutClose   Outcome = "cut_close"   // the connection was closed on purpose
	CutReset   Outcome = "cut_reset"   // the connection was reset on purpose
	ClientGone Outcome = "client_gone" // the client went away first
	Diverged   Outcome = "diverged"    // the request did not fit the script (strict): a 400 was sent
	NoScript   Outcome = "no_script"   // no script for the session, or a bad session key: a 400 was sent
	BadRequest Outcome = "bad_request" // not a valid Messages request: a 400 was sent
	Other      Outcome = "other"       // another endpoint (models, count_tokens, hello, unknown paths)
)

// Result is one tool result as the log keeps it (text cut to MaxResultText bytes).
type Result struct {
	Tool    string `json:"tool,omitempty"`
	IsError bool   `json:"is_error,omitempty"`
	Text    string `json:"text"`
}

// MaxResultText is how much of each tool result's text the log keeps.
const MaxResultText = 4096

// Entry is one logged request.
type Entry struct {
	Seq           int               `json:"seq"`
	At            string            `json:"at"`
	Method        string            `json:"method"`
	Path          string            `json:"path"`
	Status        int               `json:"status,omitempty"`
	Outcome       Outcome           `json:"outcome"`
	Model         string            `json:"model,omitempty"`
	Stream        bool              `json:"stream,omitempty"`
	Session       string            `json:"session,omitempty"`
	SessionSource string            `json:"session_source,omitempty"`
	Turn          int               `json:"turn"`
	Step          string            `json:"step,omitempty"`
	Branch        string            `json:"branch,omitempty"`
	Attempt       int               `json:"attempt,omitempty"`
	Reply         string            `json:"reply,omitempty"` // the reply kind sent
	Divergence    string            `json:"divergence,omitempty"`
	Error         string            `json:"error,omitempty"` // the error message sent, for 400s of this server
	Trace         []string          `json:"trace,omitempty"`
	ToolsOffered  int               `json:"tools_offered,omitempty"`
	CWD           string            `json:"cwd,omitempty"`
	History       [][]Result        `json:"history,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`
}

// Log is an append-only list of entries.
type Log struct {
	mu      sync.Mutex
	entries []Entry
}

// Add appends e, numbering it, and gives its number.
func (l *Log) Add(e Entry) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	e.Seq = len(l.entries) + 1
	l.entries = append(l.entries, e)
	return e.Seq
}

// Update changes entry seq in place; an unknown seq is ignored.
func (l *Log) Update(seq int, f func(*Entry)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if seq >= 1 && seq <= len(l.entries) {
		f(&l.entries[seq-1])
	}
}

// Entries is a copy of every entry, oldest first, keeping those keep accepts (nil keeps all).
func (l *Log) Entries(keep func(Entry) bool) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Entry, 0, len(l.entries))
	for _, e := range l.entries {
		if keep == nil || keep(e) {
			out = append(out, clone(e))
		}
	}
	return out
}

// Reset forgets every entry.
func (l *Log) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = nil
}

func clone(e Entry) Entry {
	e.Trace = append([]string(nil), e.Trace...)
	if e.History != nil {
		h := make([][]Result, len(e.History))
		for i, r := range e.History {
			h[i] = append([]Result(nil), r...)
		}
		e.History = h
	}
	if e.Headers != nil {
		m := make(map[string]string, len(e.Headers))
		for k, v := range e.Headers {
			m[k] = v
		}
		e.Headers = m
	}
	return e
}
