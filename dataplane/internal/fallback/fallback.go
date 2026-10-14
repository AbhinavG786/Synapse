// Package fallback implements the staleness-TTL fallback logic for R-3.
// When the policy snapshot is stale or invalid, the proxy falls back to
// Least-Connections. This is tested by E5 (control-plane outage experiment).
//
// Phase 0: stub only. Full integration in Phase 2.4.
package fallback

import (
	"log/slog"
	"sync/atomic"
	"time"
)

// Mode describes the current routing mode.
type Mode int32

const (
	ModePolicy   Mode = 0 // using the live policy snapshot
	ModeFallback Mode = 1 // snapshot is stale; using fallback algorithm
)

// Guard monitors snapshot freshness and tracks the current mode.
type Guard struct {
	mode       atomic.Int32
	lastSwitch atomic.Int64 // UnixNano
	logger     *slog.Logger
}

// NewGuard creates a new Guard in ModePolicy.
func NewGuard() *Guard {
	g := &Guard{logger: slog.Default().With("component", "fallback")}
	g.mode.Store(int32(ModePolicy))
	return g
}

// Current returns the current routing mode.
func (g *Guard) Current() Mode {
	return Mode(g.mode.Load())
}

// SetFallback transitions to fallback mode and logs the event.
func (g *Guard) SetFallback(reason string) {
	if Mode(g.mode.Swap(int32(ModeFallback))) != ModeFallback {
		g.lastSwitch.Store(time.Now().UnixNano())
		g.logger.Warn("falling back to least-connections", "reason", reason)
	}
}

// SetPolicy transitions back to policy mode when a fresh snapshot arrives.
func (g *Guard) SetPolicy() {
	if Mode(g.mode.Swap(int32(ModePolicy))) != ModePolicy {
		g.lastSwitch.Store(time.Now().UnixNano())
		g.logger.Info("resumed live policy routing")
	}
}

// LastSwitchAt returns the time of the last mode transition.
func (g *Guard) LastSwitchAt() time.Time {
	ns := g.lastSwitch.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}
