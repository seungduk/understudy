// Package gate holds named gates: a stalled reply waits on its gate until the
// control API releases it. A gate released before anyone waits stays open.
package gate

import (
	"context"
	"sort"
	"sync"
)

// Set is a set of named gates, safe for concurrent use.
type Set struct {
	// OnChange, when set before first use, is called after a gate is seen, waited on, passed or released.
	OnChange func()

	mu    sync.Mutex
	gates map[string]*state
}

func (s *Set) changed() {
	if s.OnChange != nil {
		s.OnChange()
	}
}

type state struct {
	open    chan struct{}
	opened  bool
	waiting int
	passed  int
}

// Status is one gate as the control API shows it.
type Status struct {
	Name     string `json:"name"`
	Released bool   `json:"released"`
	Waiting  int    `json:"waiting"`
	Passed   int    `json:"passed"`
}

// New is an empty set.
func New() *Set { return &Set{gates: map[string]*state{}} }

func (s *Set) get(name string) *state {
	g, ok := s.gates[name]
	if !ok {
		g = &state{open: make(chan struct{})}
		s.gates[name] = g
	}
	return g
}

// Wait blocks until the gate is released or ctx ends (then it returns ctx.Err()).
func (s *Set) Wait(ctx context.Context, name string) error {
	s.mu.Lock()
	g := s.get(name)
	g.waiting++
	open := g.open
	s.mu.Unlock()
	s.changed()
	defer s.changed()
	select {
	case <-open:
		s.mu.Lock()
		g.waiting--
		g.passed++
		s.mu.Unlock()
		return nil
	case <-ctx.Done():
		s.mu.Lock()
		g.waiting--
		s.mu.Unlock()
		return ctx.Err()
	}
}

// Release opens the gate for every present and later waiter, and says how many were waiting.
func (s *Set) Release(name string) int {
	s.mu.Lock()
	g := s.get(name)
	if !g.opened {
		g.opened = true
		close(g.open)
	}
	n := g.waiting
	s.mu.Unlock()
	s.changed()
	return n
}

// Reset releases every waiter and forgets every gate.
func (s *Set) Reset() {
	s.mu.Lock()
	for _, g := range s.gates {
		if !g.opened {
			g.opened = true
			close(g.open)
		}
	}
	s.gates = map[string]*state{}
	s.mu.Unlock()
	s.changed()
}

// Statuses lists every gate seen, by name.
func (s *Set) Statuses() []Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Status, 0, len(s.gates))
	for n, g := range s.gates {
		out = append(out, Status{Name: n, Released: g.opened, Waiting: g.waiting, Passed: g.passed})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
