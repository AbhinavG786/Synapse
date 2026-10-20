// Package fault manages the injectable fault state for Synapse mock backends.
//
// Seven fault types are supported (Section 7 of the spec):
//
//	F1 Latency injection: add X ms and/or multiply by N
//	F2 CPU throttle: spin a competing CPU-burner goroutine
//	F3 GC-pause-like stall: periodic stop-the-world sleep
//	F4 Queue saturation: reduce pool cap (handled in pool.Pool.SetCap)
//	F5 Hard failure: return 503 for all requests
//	F6 Gradual degradation ramp: linearly increasing latency multiplier
//	F7 Flapping node: periodic alternation between hard fail and normal
//
// FaultState is stored in an atomic.Pointer so reads on the hot path are
// lock-free. Writes use a mutex to safely replace the pointer.
//
// Background goroutines implement F2 (CPU burner), F3 (stall ticker),
// F6 (ramp updater), and F7 (flap timer). They are managed by the Controller.
package fault

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// Config is an immutable snapshot of the current fault parameters.
// Zero value = no faults active (safe default).
type Config struct {
	// F1: latency injection
	F1LatencyAddMs float64 // additional ms per request (additive)
	F1LatencyMul   float64 // service-time multiplier (1.0 = no effect)

	// F2: CPU throttle — spin N burner goroutines
	F2CPUBurners int

	// F3: periodic GC-pause-like stalls
	F3PauseMs         float64 // stall duration in ms
	F3PauseIntervalMs float64 // stall every N ms (0 = disabled)

	// F4: queue saturation — pool cap is set externally via pool.Pool.SetCap
	F4WorkerCap int // 0 = not active (pool uses its maxCap)

	// F5: hard failure — all requests return 503 immediately
	F5HardFail bool

	// F6: gradual degradation ramp
	F6RampRateMs float64 // latency added per second of fault time (ms/s)

	// F7: flapping node
	F7FlappingIntervalMs float64 // alternate hard-fail every N ms (0 = disabled)
}

// EffectiveMultiplier returns the combined latency multiplier for a request.
// Includes F1 multiplier and the accumulated F6 ramp factor. Does NOT include
// the additive F1 component (handled separately in the handler).
func (c *Config) EffectiveMultiplier() float64 {
	mul := c.F1LatencyMul
	if mul <= 0 {
		mul = 1.0
	}
	return mul
}

// IsHardFail reports whether requests should get an immediate 503.
func (c *Config) IsHardFail() bool {
	return c.F5HardFail
}

// Controller manages the live fault state and its background goroutines.
type Controller struct {
	mu           sync.Mutex
	ptr          atomic.Pointer[Config] // hot-path read: lock-free
	cancelBg     context.CancelFunc     // cancels all background goroutines
	bgCtx        context.Context
	wg           sync.WaitGroup
	f6StartTime  time.Time    // when F6 was activated
	f6RampMs     atomic.Value // float64: accumulated F6 latency addition
	f7HardFail   atomic.Bool  // F7's current flip state
	f3StallActive atomic.Bool // set by F3 stall ticker goroutine
}

// NewController creates a Controller with no faults active.
func NewController() *Controller {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Controller{
		bgCtx:    ctx,
		cancelBg: cancel,
	}
	c.ptr.Store(&Config{F1LatencyMul: 1.0})
	c.f6RampMs.Store(float64(0))
	return c
}

// Load returns the current FaultConfig. Lock-free and safe to call on hot path.
func (c *Controller) Load() *Config {
	return c.ptr.Load()
}

// Set atomically replaces the fault config and manages background goroutines.
// Cancels existing background goroutines and starts new ones as needed.
func (c *Controller) Set(cfg Config) {
	// Normalise
	if cfg.F1LatencyMul <= 0 {
		cfg.F1LatencyMul = 1.0
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Stop all existing background goroutines
	c.cancelBg()
	c.wg.Wait()

	// Reset background state
	c.f6RampMs.Store(float64(0))
	c.f7HardFail.Store(false)
	ctx, cancel := context.WithCancel(context.Background())
	c.bgCtx = ctx
	c.cancelBg = cancel

	c.ptr.Store(&cfg)

	// Start background goroutines for active faults
	if cfg.F2CPUBurners > 0 {
		for i := 0; i < cfg.F2CPUBurners; i++ {
			c.wg.Add(1)
			go c.runCPUBurner(ctx)
		}
	}
	if cfg.F3PauseIntervalMs > 0 && cfg.F3PauseMs > 0 {
		c.wg.Add(1)
		go c.runStallTicker(ctx, cfg.F3PauseIntervalMs, cfg.F3PauseMs)
	}
	if cfg.F6RampRateMs > 0 {
		c.f6StartTime = time.Now()
		c.wg.Add(1)
		go c.runF6Ramp(ctx, cfg.F6RampRateMs)
	}
	if cfg.F7FlappingIntervalMs > 0 {
		c.wg.Add(1)
		go c.runF7Flap(ctx, cfg.F7FlappingIntervalMs)
	}
}

// Clear removes all active faults and stops background goroutines.
func (c *Controller) Clear() {
	c.Set(Config{F1LatencyMul: 1.0})
}

// Stop shuts down all background goroutines. Call when the server exits.
func (c *Controller) Stop() {
	c.mu.Lock()
	c.cancelBg()
	c.mu.Unlock()
	c.wg.Wait()
}

// ExtraLatency returns the total additional latency to inject for a request,
// combining F1 additive, F6 ramp, and F7 state, in milliseconds.
func (c *Controller) ExtraLatency() float64 {
	cfg := c.ptr.Load()
	total := cfg.F1LatencyAddMs

	// F6: accumulated ramp
	if v, ok := c.f6RampMs.Load().(float64); ok {
		total += v
	}

	return total
}

// IsHardFail returns true if requests should be rejected with 503.
// Combines F5 (permanent) and F7 (flapping).
func (c *Controller) IsHardFail() bool {
	cfg := c.ptr.Load()
	if cfg.F5HardFail {
		return true
	}
	return c.f7HardFail.Load()
}

// StallDuration returns the stall duration to inject if F3 fires on this call.
// Returns 0 if F3 is not active or the random check doesn't fire.
// The caller should check this once per request.
func (c *Controller) StallDuration() time.Duration {
	cfg := c.ptr.Load()
	if cfg.F3PauseMs <= 0 || cfg.F3PauseIntervalMs <= 0 {
		return 0
	}
	// The background ticker (runStallTicker) sets a flag; we read it here.
	// For simplicity: stall is applied on every request while the ticker fires.
	// The ticker goroutine sets stall via atomic; handler reads once.
	if c.f3StallActive.Load() {
		return time.Duration(cfg.F3PauseMs * float64(time.Millisecond))
	}
	return 0
}


// ---- Background goroutines ----

// runCPUBurner spins a CPU-intensive loop until ctx is cancelled (F2).
func (c *Controller) runCPUBurner(ctx context.Context) {
	defer c.wg.Done()
	var sink float64
	i := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
			sink += math.Sqrt(float64(i)*3.14159) + math.Log1p(float64(i+1))
			i++
			if i%10_000 == 0 {
				// Yield briefly so other goroutines can run
				time.Sleep(time.Microsecond)
			}
		}
	}
	_ = sink
}

// runStallTicker periodically sets the F3 stall signal (F3).
func (c *Controller) runStallTicker(ctx context.Context, intervalMs, _ float64) {
	defer c.wg.Done()
	ticker := time.NewTicker(time.Duration(intervalMs * float64(time.Millisecond)))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Signal a stall: set flag for a short window
			c.f3StallActive.Store(true)
			time.Sleep(10 * time.Millisecond) // brief stall window
			c.f3StallActive.Store(false)
		}
	}
}

// runF6Ramp increases the ramp latency at rampRateMs per second until ctx cancels.
func (c *Controller) runF6Ramp(ctx context.Context, rampRateMs float64) {
	defer c.wg.Done()
	ticker := time.NewTicker(100 * time.Millisecond) // update every 100ms
	defer ticker.Stop()
	start := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			elapsed := time.Since(start).Seconds()
			ramp := elapsed * rampRateMs
			// Cap at 2000 ms to avoid runaway latency
			if ramp > 2000 {
				ramp = 2000
			}
			c.f6RampMs.Store(ramp)
		}
	}
}

// runF7Flap alternates the hard-fail state at the given interval (F7).
func (c *Controller) runF7Flap(ctx context.Context, intervalMs float64) {
	defer c.wg.Done()
	ticker := time.NewTicker(time.Duration(intervalMs * float64(time.Millisecond)))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.f7HardFail.Store(!c.f7HardFail.Load())
		}
	}
}
