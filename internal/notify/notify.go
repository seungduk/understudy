// Package notify is a change counter that many watchers can wait on: every
// change bumps the version and wakes every waiter. Safe for concurrent use.
package notify

import (
	"context"
	"sync"
)

// Notifier counts changes. The zero value is ready to use.
type Notifier struct {
	mu      sync.Mutex
	version uint64
	wake    chan struct{}
}

// Bump records one change and wakes every waiter.
func (n *Notifier) Bump() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.version++
	if n.wake != nil {
		close(n.wake)
		n.wake = nil
	}
}

// Version is the number of changes so far.
func (n *Notifier) Version() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.version
}

// Wait blocks until the version is greater than after, and gives it; or until
// ctx ends, and gives ctx.Err().
func (n *Notifier) Wait(ctx context.Context, after uint64) (uint64, error) {
	for {
		n.mu.Lock()
		if n.version > after {
			v := n.version
			n.mu.Unlock()
			return v, nil
		}
		if n.wake == nil {
			n.wake = make(chan struct{})
		}
		w := n.wake
		n.mu.Unlock()
		select {
		case <-w:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}
