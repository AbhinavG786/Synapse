// Package policy_test — unit tests for the policy snapshot holder.
// Phase 0: atomic swap and staleness checks.
package policy_test

import (
	"testing"
	"time"

	"github.com/synapse/dataplane/internal/policy"
)

func TestDefaultSnapshot(t *testing.T) {
	snap := policy.DefaultSnapshot()
	if snap == nil {
		t.Fatal("DefaultSnapshot returned nil")
	}
	if snap.Algo != policy.AlgorithmLeastConn {
		t.Errorf("expected LeastConn default, got %v", snap.Algo)
	}
	if snap.D != 16 {
		t.Errorf("expected d=16, got %d", snap.D)
	}
}

func TestHolderLoadStore(t *testing.T) {
	snap := policy.DefaultSnapshot()
	h := policy.NewHolder(snap)

	got := h.Load()
	if got != snap {
		t.Error("Load() did not return the stored snapshot")
	}

	snap2 := policy.DefaultSnapshot()
	snap2.Version = 42
	h.Store(snap2)

	got2 := h.Load()
	if got2.Version != 42 {
		t.Errorf("expected version 42, got %d", got2.Version)
	}
}

func TestSnapshotStaleness(t *testing.T) {
	snap := &policy.Snapshot{
		CreatedAt: time.Now().Add(-60 * time.Second),
		TTL:       30 * time.Second,
	}
	if !snap.IsStale() {
		t.Error("expected snapshot to be stale")
	}

	freshSnap := &policy.Snapshot{
		CreatedAt: time.Now(),
		TTL:       30 * time.Second,
	}
	if freshSnap.IsStale() {
		t.Error("expected fresh snapshot not to be stale")
	}
}
