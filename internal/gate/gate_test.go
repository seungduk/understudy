package gate

import (
	"context"
	"sync"
	"testing"
	"time"
)

func waiting(s *Set, name string) int {
	for _, g := range s.Statuses() {
		if g.Name == name {
			return g.Waiting
		}
	}
	return -1
}

func until(t *testing.T, f func() bool) {
	t.Helper()
	for i := 0; i < 2000; i++ {
		if f() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not reached")
}

func TestWaitRelease(t *testing.T) {
	s := New()
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Wait(context.Background(), "g"); err != nil {
				t.Error(err)
			}
		}()
	}
	until(t, func() bool { return waiting(s, "g") == 5 })
	if n := s.Release("g"); n != 5 {
		t.Fatalf("released %d", n)
	}
	wg.Wait()
	st := s.Statuses()
	if len(st) != 1 || !st[0].Released || st[0].Waiting != 0 || st[0].Passed != 5 {
		t.Fatalf("%+v", st)
	}
	// Released gates stay open; releasing twice is harmless.
	if err := s.Wait(context.Background(), "g"); err != nil || s.Release("g") != 0 {
		t.Fatal("open gate")
	}
}

func TestPreReleaseCancelReset(t *testing.T) {
	s := New()
	s.Release("early")
	if err := s.Wait(context.Background(), "early"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- s.Wait(ctx, "c") }()
	until(t, func() bool { return waiting(s, "c") == 1 })
	cancel()
	if err := <-done; err != context.Canceled || waiting(s, "c") != 0 {
		t.Fatalf("cancel %v", err)
	}
	go func() { done <- s.Wait(context.Background(), "r") }()
	until(t, func() bool { return waiting(s, "r") == 1 })
	s.Reset()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(s.Statuses()) != 0 {
		t.Fatal("reset kept gates")
	}
	s.Release("x")
	s.Reset() // an opened gate is not closed twice
}

func TestOnChange(t *testing.T) {
	var mu sync.Mutex
	n := 0
	s := New()
	s.OnChange = func() { mu.Lock(); n++; mu.Unlock() }
	count := func() int { mu.Lock(); defer mu.Unlock(); return n }
	done := make(chan error)
	go func() { done <- s.Wait(context.Background(), "g") }()
	until(t, func() bool { return count() == 1 }) // started waiting
	s.Release("g")                                // released
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { return count() == 3 }) // passed
	s.Reset()
	if count() != 4 {
		t.Fatal(count())
	}
}
