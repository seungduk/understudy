// Package clock gives the server its time: the real wall clock, or a virtual
// clock that moves only when it is told to, for deterministic timing in tests.
package clock

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Clock is what the server reads time from and sleeps on.
type Clock interface {
	Now() time.Time
	// Sleep waits d, or until ctx ends (it then returns ctx.Err()).
	Sleep(ctx context.Context, d time.Duration) error
}

// Real is the wall clock.
type Real struct{}

func (Real) Now() time.Time { return time.Now() }

func (Real) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Virtual is a clock that moves only by Advance. Sleepers wake in deadline order.
type Virtual struct {
	mu       sync.Mutex
	now      time.Time
	sleepers []*sleeper
}

type sleeper struct {
	until time.Time
	done  chan struct{}
}

// NewVirtual starts a virtual clock at start.
func NewVirtual(start time.Time) *Virtual { return &Virtual{now: start} }

func (v *Virtual) Now() time.Time {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.now
}

func (v *Virtual) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	v.mu.Lock()
	s := &sleeper{until: v.now.Add(d), done: make(chan struct{})}
	v.sleepers = append(v.sleepers, s)
	v.mu.Unlock()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		v.mu.Lock()
		v.remove(s)
		v.mu.Unlock()
		return ctx.Err()
	}
}

func (v *Virtual) remove(s *sleeper) {
	for i, x := range v.sleepers {
		if x == s {
			v.sleepers = append(v.sleepers[:i], v.sleepers[i+1:]...)
			return
		}
	}
}

// Advance moves the clock forward by d (d < 0 is treated as 0) and wakes every
// sleeper whose deadline is now reached.
func (v *Virtual) Advance(d time.Duration) {
	if d < 0 {
		d = 0
	}
	v.mu.Lock()
	v.now = v.now.Add(d)
	sort.SliceStable(v.sleepers, func(i, j int) bool { return v.sleepers[i].until.Before(v.sleepers[j].until) })
	keep := v.sleepers[:0]
	for _, s := range v.sleepers {
		if !s.until.After(v.now) {
			close(s.done)
		} else {
			keep = append(keep, s)
		}
	}
	v.sleepers = keep
	v.mu.Unlock()
}

// Sleepers is how many sleeps are waiting.
func (v *Virtual) Sleepers() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.sleepers)
}
