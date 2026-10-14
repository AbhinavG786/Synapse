// Package policy manages the atomic policy snapshot received from the control
// plane via gRPC. The data plane reads the snapshot with a single atomic
// pointer load (zero allocation, zero lock) on the hot path (R-4).
//
// Phase 0: type definitions and atomic holder stub. Real gRPC wiring in Phase 2.4.
package policy

import (
	"sync/atomic"
	"time"
)

// Algorithm mirrors the proto enum values without importing proto in hot path.
type Algorithm int32

const (
	AlgorithmUnspecified    Algorithm = 0
	AlgorithmRoundRobin     Algorithm = 1
	AlgorithmWeightedRR     Algorithm = 2
	AlgorithmLeastConn      Algorithm = 3
	AlgorithmP2C            Algorithm = 4
	AlgorithmPeakEWMA       Algorithm = 5
	AlgorithmHeuristicScore Algorithm = 6
	AlgorithmLinUCB         Algorithm = 7
	AlgorithmEpsilonGreedy  Algorithm = 8
	AlgorithmThompson       Algorithm = 9
	AlgorithmReactiveEject  Algorithm = 10
)

// ArmParams holds per-arm LinUCB parameters.
// theta = A^{-1} b; AInv stored row-major, length d*d.
// See Section 6.3 and docs/MODEL.md.
type ArmParams struct {
	NodeID  string
	Theta   []float64 // length = d
	AInv    []float64 // length = d*d, row-major
	NPulls  uint64
	Weight  float64 // for WRR / heuristic
}

// Normaliser contains mean and scale arrays for feature normalisation.
// Length = d+1 (including bias term). Must match Python's stored values exactly.
type Normaliser struct {
	Mean  []float64
	Scale []float64
}

// HeuristicParams holds fixed coefficients for B6 heuristic-score policy.
type HeuristicParams struct {
	Alpha float64
	Beta  float64
	Gamma float64
}

// Snapshot is the immutable policy snapshot received from the control plane.
// It must never be mutated after creation; to update, swap the atomic pointer.
type Snapshot struct {
	Version            uint64
	CreatedAt          time.Time
	TTL                time.Duration
	FeatureSpecVersion uint32
	D                  uint32 // context vector dimension (excluding bias)
	Algo               Algorithm
	Alpha              float64 // LinUCB exploration bonus
	Epsilon            float64 // epsilon-floor for propensity logging
	Arms               []ArmParams
	Norm               Normaliser
	Heuristic          HeuristicParams
}

// Holder wraps an atomic pointer to the current Snapshot.
// Use Load() on the hot path; Store() on policy update.
type Holder struct {
	ptr atomic.Pointer[Snapshot]
}

// NewHolder creates a Holder initialised with snap as the first policy.
func NewHolder(snap *Snapshot) *Holder {
	h := &Holder{}
	h.ptr.Store(snap)
	return h
}

// Load returns the current snapshot. Never returns nil if properly initialised.
// This is a single atomic pointer load — O(1), no allocation, no lock (R-4).
func (h *Holder) Load() *Snapshot {
	return h.ptr.Load()
}

// Store atomically replaces the current snapshot.
// Called only by the gRPC stream reader goroutine (Phase 2.4).
func (h *Holder) Store(snap *Snapshot) {
	h.ptr.Store(snap)
}

// IsStale reports whether the snapshot has exceeded its TTL.
func (s *Snapshot) IsStale() bool {
	if s == nil || s.TTL <= 0 {
		return false
	}
	return time.Since(s.CreatedAt) > s.TTL
}

// DefaultSnapshot returns a safe bootstrap snapshot (Least-Connections).
// Used before the control plane delivers the first real snapshot.
func DefaultSnapshot() *Snapshot {
	return &Snapshot{
		Version:            0,
		CreatedAt:          time.Now(),
		TTL:                30 * time.Second,
		FeatureSpecVersion: 1,
		D:                  16,
		Algo:               AlgorithmLeastConn,
		Alpha:              1.0,
		Epsilon:            0.02,
	}
}
