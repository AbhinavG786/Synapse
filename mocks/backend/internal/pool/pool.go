// Package pool implements a bounded semaphore for per-endpoint worker pools.
//
// Each endpoint class has its own Pool so that head-of-line blocking is
// realistic: saturating the /compute pool does not block /crud requests.
//
// Design:
//   - tokens is a buffered channel of size maxWorkers (the hard ceiling).
//   - InFlight is the number of goroutines currently executing (holding a token).
//   - Queued is the number of goroutines waiting to acquire a token.
//   - The F4 fault dynamically reduces the effective cap below maxWorkers:
//     if InFlight >= Cap, new arrivals are immediately rejected (503-style),
//     simulating queue saturation without resizing the channel.
//
// Acquire blocks until a token is available or the context is cancelled.
// Release MUST be called exactly once after Acquire succeeds (use defer).
package pool

import (
	"context"
	"errors"
	"sync/atomic"
)

// ErrPoolFull is returned when the dynamic cap (F4) is exceeded.
var ErrPoolFull = errors.New("pool: capacity cap exceeded (F4 fault active)")

// ErrContextCancelled is returned when ctx is done before a token is obtained.
var ErrContextCancelled = errors.New("pool: context cancelled while waiting")

// Pool is a bounded concurrency limiter with dynamic cap support.
// It is safe for concurrent use.
type Pool struct {
	tokens  chan struct{} // buffered semaphore of size maxWorkers
	maxCap  int64        // absolute ceiling (len of tokens channel)
	cap     atomic.Int64 // current effective cap, can be reduced by F4
	inflight atomic.Int64
	queued  atomic.Int64
}

// New creates a Pool with capacity n. n must be > 0.
func New(n int) *Pool {
	if n <= 0 {
		n = 1
	}
	p := &Pool{
		tokens: make(chan struct{}, n),
		maxCap: int64(n),
	}
	p.cap.Store(int64(n))
	return p
}

// Acquire waits until a worker slot is available or ctx is cancelled.
// On success, callers MUST call Release() exactly once when done.
//
// Returns ErrPoolFull immediately if the dynamic cap (SetCap) is reached,
// without waiting for a slot — this is the F4 (queue saturation) behaviour.
func (p *Pool) Acquire(ctx context.Context) error {
	// F4: dynamic cap check — reject immediately if already at cap.
	if p.inflight.Load() >= p.cap.Load() {
		return ErrPoolFull
	}

	p.queued.Add(1)
	defer p.queued.Add(-1)

	select {
	case p.tokens <- struct{}{}:
		// Re-check after acquiring the channel slot — the cap may have
		// changed while we were waiting. Accept anyway; F4 is "soft" in
		// that it rejects new arrivals but lets queued ones through.
		p.inflight.Add(1)
		return nil
	case <-ctx.Done():
		return ErrContextCancelled
	}
}

// Release returns a worker slot to the pool.
// Must be called exactly once per successful Acquire.
func (p *Pool) Release() {
	<-p.tokens
	p.inflight.Add(-1)
}

// SetCap sets the dynamic effective capacity. Values are clamped to [1, maxCap].
// Used by the F4 (queue saturation) fault to simulate a shrinking worker pool.
// Does not affect goroutines already holding a token.
func (p *Pool) SetCap(n int64) {
	if n < 1 {
		n = 1
	}
	if n > p.maxCap {
		n = p.maxCap
	}
	p.cap.Store(n)
}

// ResetCap restores the cap to the original maxWorkers value, clearing F4.
func (p *Pool) ResetCap() {
	p.cap.Store(p.maxCap)
}

// InFlight returns the number of goroutines currently executing (holding a slot).
func (p *Pool) InFlight() int64 {
	return p.inflight.Load()
}

// Queued returns the number of goroutines currently waiting for a slot.
func (p *Pool) Queued() int64 {
	return p.queued.Load()
}

// Cap returns the current effective capacity.
func (p *Pool) Cap() int64 {
	return p.cap.Load()
}

// MaxCap returns the absolute maximum capacity of this pool.
func (p *Pool) MaxCap() int64 {
	return p.maxCap
}
