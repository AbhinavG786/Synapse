// Package fallback_test — unit tests for the fallback guard.
package fallback_test

import (
	"testing"

	"github.com/synapse/dataplane/internal/fallback"
)

func TestGuardDefaultMode(t *testing.T) {
	g := fallback.NewGuard()
	if g.Current() != fallback.ModePolicy {
		t.Errorf("expected ModePolicy on init, got %v", g.Current())
	}
}

func TestGuardTransitions(t *testing.T) {
	g := fallback.NewGuard()
	g.SetFallback("snapshot stale")
	if g.Current() != fallback.ModeFallback {
		t.Error("expected ModeFallback after SetFallback")
	}
	g.SetPolicy()
	if g.Current() != fallback.ModePolicy {
		t.Error("expected ModePolicy after SetPolicy")
	}
}

func TestGuardIdempotent(t *testing.T) {
	g := fallback.NewGuard()
	g.SetFallback("first")
	g.SetFallback("second") // should not panic or change lastSwitch again
	if g.Current() != fallback.ModeFallback {
		t.Error("still expected ModeFallback")
	}
}
