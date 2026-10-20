package pool_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/synapse/mockbackend/internal/pool"
)

func TestAcquireRelease(t *testing.T) {
	p := pool.New(3)
	ctx := context.Background()

	if err := p.Acquire(ctx); err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}
	if p.InFlight() != 1 {
		t.Errorf("expected InFlight=1, got %d", p.InFlight())
	}
	p.Release()
	if p.InFlight() != 0 {
		t.Errorf("expected InFlight=0 after Release, got %d", p.InFlight())
	}
}

func TestPoolCapacity(t *testing.T) {
	p := pool.New(2)
	ctx := context.Background()

	// Fill the pool
	if err := p.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.Acquire(ctx); err != nil {
		t.Fatal(err)
	}

	// Third acquire should block; use a short-timeout context to prove it blocks.
	shortCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	err := p.Acquire(shortCtx)
	if err == nil {
		t.Error("expected Acquire to block/fail when pool is full")
	}

	p.Release()
	p.Release()
}

func TestF4DynamicCap(t *testing.T) {
	p := pool.New(5)
	ctx := context.Background()

	// Acquire 3 slots
	for i := 0; i < 3; i++ {
		if err := p.Acquire(ctx); err != nil {
			t.Fatalf("Acquire %d failed: %v", i, err)
		}
	}

	// Set cap to 3 — now the pool is at cap
	p.SetCap(3)

	// New arrivals should be rejected immediately (ErrPoolFull)
	err := p.Acquire(ctx)
	if err != pool.ErrPoolFull {
		t.Errorf("expected ErrPoolFull, got %v", err)
	}

	// Release one and reset cap — should allow entry again
	p.Release()
	p.ResetCap()
	if err := p.Acquire(ctx); err != nil {
		t.Errorf("after ResetCap, Acquire failed: %v", err)
		return
	}

	// cleanup
	for range 3 {
		p.Release()
	}
}

func TestConcurrentSafety(t *testing.T) {
	p := pool.New(10)
	ctx := context.Background()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.Acquire(ctx); err != nil {
				return
			}
			time.Sleep(time.Millisecond)
			p.Release()
		}()
	}
	wg.Wait()

	if p.InFlight() != 0 {
		t.Errorf("expected 0 in-flight after all goroutines done, got %d", p.InFlight())
	}
}

func TestQueuedCounter(t *testing.T) {
	p := pool.New(1)
	ctx := context.Background()

	// Fill the single slot
	if err := p.Acquire(ctx); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	done := make(chan struct{})

	go func() {
		close(started)
		shortCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		_ = p.Acquire(shortCtx)
		close(done)
	}()

	<-started
	time.Sleep(10 * time.Millisecond) // let goroutine block on Acquire
	if q := p.Queued(); q < 1 {
		t.Errorf("expected Queued >= 1, got %d", q)
	}

	p.Release() // unblock the waiting goroutine
	<-done
}
