// Package server is the fake Anthropic Messages API: an http.Handler that
// answers POST /v1/messages from a script, plus the endpoints Claude Code also
// calls and a control API. Use it in-process from Go tests, or through the
// understudy command.
package server

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/seungduk/understudy/internal/clock"
	"github.com/seungduk/understudy/internal/gate"
	"github.com/seungduk/understudy/internal/reqlog"
	"github.com/seungduk/understudy/script"
)

// Version is this build's version.
const Version = "0.1.0"

// ControlPrefix is where the control API lives on the same address.
const ControlPrefix = "/_understudy/"

// ClockMode is which clock the server runs on.
type ClockMode int

const (
	// RealClock: stalls and pacing take wall time.
	RealClock ClockMode = iota
	// VirtualClock: time moves only by Advance (or POST /_understudy/clock/advance).
	VirtualClock
)

// Options configure a Server. The zero value is a server on the real clock with no script.
type Options struct {
	Clock ClockMode
	// Start is the virtual clock's first time; zero means the wall time at New.
	Start time.Time
	// Script is the first script; nil means none until Load.
	Script *script.Script
}

// Entry is one request of the log.
type Entry = reqlog.Entry

// Outcome is how a logged request ended.
type Outcome = reqlog.Outcome

// The outcomes of a logged request.
const (
	Pending    = reqlog.Pending
	Served     = reqlog.Served
	CutClose   = reqlog.CutClose
	CutReset   = reqlog.CutReset
	ClientGone = reqlog.ClientGone
	Diverged   = reqlog.Diverged
	NoScript   = reqlog.NoScript
	BadRequest = reqlog.BadRequest
	Other      = reqlog.Other
)

// GateStatus is one gate's state.
type GateStatus = gate.Status

// Server is the fake API. Safe for concurrent use.
type Server struct {
	clk     clock.Clock
	virtual *clock.Virtual
	gates   *gate.Set
	log     reqlog.Log
	script  atomic.Pointer[script.Script]
	mux     *http.ServeMux

	mu       sync.Mutex
	attempts map[attemptKey]int
	models   map[string]bool
}

type attemptKey struct {
	session, step, branch string
	turn                  int
}

// New makes a server.
func New(o Options) *Server {
	s := &Server{gates: gate.New(), attempts: map[attemptKey]int{}, models: map[string]bool{}}
	if o.Clock == VirtualClock {
		start := o.Start
		if start.IsZero() {
			start = time.Now()
		}
		s.virtual = clock.NewVirtual(start)
		s.clk = s.virtual
	} else {
		s.clk = clock.Real{}
	}
	if o.Script != nil {
		s.script.Store(o.Script)
	}
	s.mux = http.NewServeMux()
	s.routes()
	return s
}

// Handler is the whole HTTP surface: the API and, under ControlPrefix, the control API.
func (s *Server) Handler() http.Handler { return s.mux }

// Load replaces the script. Requests already being answered keep the old one.
func (s *Server) Load(sc *script.Script) { s.script.Store(sc) }

// Script is the loaded script, nil when none is.
func (s *Server) Script() *script.Script { return s.script.Load() }

// Release opens a gate and says how many requests were waiting on it.
func (s *Server) Release(gate string) int { return s.gates.Release(gate) }

// Gates lists every gate seen.
func (s *Server) Gates() []GateStatus { return s.gates.Statuses() }

// Requests is the request log, oldest first.
func (s *Server) Requests() []Entry { return s.log.Entries(nil) }

// SessionRequests is the log of one session.
func (s *Server) SessionRequests(key string) []Entry {
	return s.log.Entries(func(e Entry) bool { return e.Session == key })
}

// Reset forgets the log, the attempt counts and the gates (releasing whoever waits).
// The script stays loaded.
func (s *Server) Reset() {
	s.log.Reset()
	s.gates.Reset()
	s.mu.Lock()
	s.attempts = map[attemptKey]int{}
	s.mu.Unlock()
}

// ErrRealClock is returned by Advance on a server that runs on the real clock.
var ErrRealClock = errors.New("the server runs on the real clock; start it with the virtual clock to advance time")

// Advance moves the virtual clock forward.
func (s *Server) Advance(d time.Duration) error {
	if s.virtual == nil {
		return ErrRealClock
	}
	s.virtual.Advance(d)
	return nil
}

// Now is the server's time.
func (s *Server) Now() time.Time { return s.clk.Now() }

// nextAttempt counts one more request at a position and gives its 1-based number.
func (s *Server) nextAttempt(k attemptKey) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts[k]++
	return s.attempts[k]
}

func (s *Server) sawModel(m string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.models[m] = true
}

// logHeaders keeps the request headers useful for debugging and never a credential.
func logHeaders(h http.Header) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		lk := strings.ToLower(k)
		if lk == "cookie" || strings.Contains(lk, "auth") || strings.Contains(lk, "key") || strings.Contains(lk, "token") || strings.Contains(lk, "secret") {
			continue
		}
		val := strings.Join(v, ", ")
		if len(val) > 256 {
			val = val[:256]
		}
		out[lk] = val
	}
	return out
}
