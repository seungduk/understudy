package clock

import (
	"context"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func waitSleepers(t *testing.T, v *Virtual, n int) {
	t.Helper()
	for i := 0; i < 2000 && v.Sleepers() != n; i++ {
		time.Sleep(time.Millisecond)
	}
	if v.Sleepers() != n {
		t.Fatalf("sleepers %d, want %d", v.Sleepers(), n)
	}
}

func TestVirtualAdvanceWakesInOrder(t *testing.T) {
	v := NewVirtual(t0)
	var mu sync.Mutex
	var woke []int
	var wg sync.WaitGroup
	for _, s := range []int{3, 1, 2} {
		wg.Add(1)
		go func(s int) {
			defer wg.Done()
			if err := v.Sleep(context.Background(), time.Duration(s)*time.Second); err != nil {
				t.Error(err)
			}
			mu.Lock()
			woke = append(woke, s)
			mu.Unlock()
		}(s)
	}
	waitSleepers(t, v, 3)
	v.Advance(1500 * time.Millisecond)
	waitSleepers(t, v, 2)
	for i := 0; i < 2000; i++ {
		mu.Lock()
		n := len(woke)
		mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	v.Advance(-time.Hour) // ignored
	if !v.Now().Equal(t0.Add(1500 * time.Millisecond)) {
		t.Fatalf("now %v", v.Now())
	}
	v.Advance(10 * time.Second)
	wg.Wait()
	if len(woke) != 3 || woke[0] != 1 {
		t.Fatalf("woke %v", woke)
	}
}

func TestVirtualSleepCancelAndZero(t *testing.T) {
	v := NewVirtual(t0)
	if err := v.Sleep(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- v.Sleep(ctx, time.Hour) }()
	waitSleepers(t, v, 1)
	cancel()
	if err := <-done; err != context.Canceled {
		t.Fatalf("err %v", err)
	}
	if v.Sleepers() != 0 {
		t.Fatal("sleeper left")
	}
	ctx2, c2 := context.WithCancel(context.Background())
	c2()
	if v.Sleep(ctx2, 0) == nil {
		t.Fatal("zero sleep on ended ctx")
	}
}

func TestReal(t *testing.T) {
	var r Real
	if r.Now().IsZero() {
		t.Fatal("zero")
	}
	if err := r.Sleep(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	ctx, c := context.WithCancel(context.Background())
	c()
	if r.Sleep(ctx, time.Hour) == nil || r.Sleep(ctx, 0) == nil {
		t.Fatal("cancel")
	}
}
