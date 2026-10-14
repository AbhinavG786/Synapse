// Package router defines the Router interface implemented by all policies:
// Round-Robin (B1), Weighted Round-Robin (B2), Least-Connections (B3),
// Power-of-Two-Choices (B4), Peak-EWMA (B5), Heuristic-Score (B6),
// LinUCB (P1), epsilon-greedy (P2a), Thompson (P2b), Reactive-Eject (B7).
//
// All implementations must be safe for concurrent use.
// Phase 0: interface definition only. Implementations in Phase 2.
package router

import "net/http"

// Backend represents a single upstream backend node.
type Backend struct {
	// NodeID is the unique identifier for this backend (matches proto NodeInfo).
	NodeID string
	// Address is the HTTP/HTTPS address, e.g. "http://backend-1:9000".
	Address string
}

// Router selects a backend for an incoming request.
// Implementations must be goroutine-safe.
type Router interface {
	// Pick selects a backend for the given request.
	// It must not block, allocate on the critical path if avoidable,
	// or call any network/DB endpoint (R-4).
	// Returns nil if no backend is available.
	Pick(r *http.Request) *Backend

	// Name returns the algorithm identifier string (e.g. "round-robin").
	Name() string
}
