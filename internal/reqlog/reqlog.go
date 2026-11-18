// Package reqlog is the request log: every request the server saw, what the
// script chose for it and how it ended. Safe for concurrent use.
package reqlog

import (
	"encoding/json"
	"sync"
)

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

// MaxPromptText is how much of a request's newest user text the log keeps.
const MaxPromptText = 8192

// Call is one tool call of an answer, with its input as sent (after {{cwd}}).
type Call struct {
	Tool  string          `json:"tool"`
	Input json.RawMessage `json:"input"`
}

// Answer is what the server sent for a request, as the script wrote it.
type Answer struct {
	Kind     string `json:"kind"` // message, tool_use, http_error, stream_error or limit
	Thinking string `json:"thinking,omitempty"`
	Text     string `json:"text,omitempty"` // message or tool_use text, or a stream error's partial text
	Stop     string `json:"stop,omitempty"` // end_turn, max_tokens or tool_use
	Calls    []Call `json:"calls,omitempty"`
	// Error is the error kind of http_error and stream_error (overloaded, api_error, ...).
	Error   string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
	Retry   string `json:"retry,omitempty"` // http_error: default, never or always
	// Window and ResetsInSeconds are the limit's.
	Window          string `json:"window,omitempty"`
	ResetsInSeconds int    `json:"resets_in_seconds,omitempty"`
	// Faults are the delivery faults in plain words, such as "stall until gate review is released".
	Faults []string `json:"faults,omitempty"`
}

// Hold is a stall a request is waiting in now.
type Hold struct {
	Gate    string `json:"gate,omitempty"`    // the gate it waits on, or
	Seconds int    `json:"seconds,omitempty"` // how long a timed stall lasts
	Since   string `json:"since"`
}

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
	// Prompt is the text of the request's last user message (not its tool results),
	// cut to MaxPromptText bytes: the task at turn 0, and any text sent with results later.
	Prompt string `json:"prompt,omitempty"`
	// Answer is what was sent, nil when nothing was (a refused request).
	Answer *Answer `json:"answer,omitempty"`
	// Held is set while the reply waits in a stall.
	Held *Hold `json:"held,omitempty"`
	// Expected and Actual are set when the request diverged: what the script wanted,
	// and what the request carried instead.
	Expected []string `json:"expected,omitempty"`
	Actual   []string `json:"actual,omitempty"`
}

// Log is an append-only list of entries.
type Log struct {
	// OnChange, when set before first use, is called after every Add, Update and Reset.
	OnChange func()

	mu      sync.Mutex
	entries []Entry
}

func (l *Log) changed() {
	if l.OnChange != nil {
		l.OnChange()
	}
}

// Add appends e, numbering it, and gives its number.
func (l *Log) Add(e Entry) int {
	l.mu.Lock()
	e.Seq = len(l.entries) + 1
	l.entries = append(l.entries, e)
	l.mu.Unlock()
	l.changed()
	return e.Seq
}

// Update changes entry seq in place; an unknown seq is ignored.
func (l *Log) Update(seq int, f func(*Entry)) {
	l.mu.Lock()
	ok := seq >= 1 && seq <= len(l.entries)
	if ok {
		f(&l.entries[seq-1])
	}
	l.mu.Unlock()
	if ok {
		l.changed()
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
	l.entries = nil
	l.mu.Unlock()
	l.changed()
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
	e.Expected = append([]string(nil), e.Expected...)
	e.Actual = append([]string(nil), e.Actual...)
	if e.Answer != nil {
		a := *e.Answer
		a.Calls = append([]Call(nil), a.Calls...)
		a.Faults = append([]string(nil), a.Faults...)
		e.Answer = &a
	}
	if e.Held != nil {
		h := *e.Held
		e.Held = &h
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
