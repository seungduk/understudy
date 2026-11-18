package notify

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestBumpWakesEveryWaiter(t *testing.T) {
	var n Notifier
	if n.Version() != 0 {
		t.Fatal(n.Version())
	}
	var wg sync.WaitGroup
	got := make([]uint64, 8)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := n.Wait(context.Background(), 0)
			if err != nil {
				t.Error(err)
			}
			got[i] = v
		}()
	}
	time.Sleep(10 * time.Millisecond)
	n.Bump()
	wg.Wait()
	for _, v := range got {
		if v != 1 {
			t.Fatalf("%v", got)
		}
	}
}

func TestWaitReturnsAtOnceWhenBehind(t *testing.T) {
	var n Notifier
	n.Bump()
	n.Bump()
	if v, err := n.Wait(context.Background(), 1); v != 2 || err != nil {
		t.Fatal(v, err)
	}
}

func TestWaitEndsWithContext(t *testing.T) {
	var n Notifier
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := n.Wait(ctx, 0); err != context.DeadlineExceeded {
		t.Fatal(err)
	}
	n.Bump() // no waiter: nothing to wake, nothing breaks
	if n.Version() != 1 {
		t.Fatal(n.Version())
	}
}
