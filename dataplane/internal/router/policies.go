// Package router defines the Router interface and all routing policy
// implementations for the Synapse data plane.
//
// Policies implemented:
//   B1 RoundRobin          — simple modulo counter, O(1)
//   B2 WeightedRoundRobin  — smooth weighted RR (Nginx-style interleaving), O(N)
//   B3 LeastConnections    — least in-flight across all nodes, O(N)
//   B4 PowerOfTwoChoices   — random pair, pick lower in-flight, O(1)
//   B5 PeakEWMA            — peak-EWMA latency score (like Envoy), O(N)
//   B6 HeuristicScore      — paper formula: α*L̂ + β*C + γ*Q, O(N)
//   B7 ReactiveEject       — LC with consecutive-5xx ejection, O(N)
//
// All implementations are goroutine-safe.
// The Router interface (Pick, Name) is defined in router.go (unchanged from Phase 0).
package router

import (
	"math"
	"math/rand/v2"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// NewNode creates a Node for the given backend with an optional weight.
// Weight defaults to 1.0 if <= 0.
func NewNode(nodeID, address string, weight float64) *Node {
	if weight <= 0 {
		weight = 1.0
	}
	return &Node{
		Backend: Backend{NodeID: nodeID, Address: address},
		Weight:  weight,
	}
}

// ---- Node state shared across policies ---- //

// Node extends Backend with live per-node state tracked by the proxy.
// All fields are updated atomically or under a RWMutex.
type Node struct {
	Backend

	// Proxy-side in-flight counter (atomic; R-4 hot-path safe).
	InFlight atomic.Int64

	// Weight for WRR (read-only after init).
	Weight float64

	// System metrics pulled from /metrics (updated by NodeMetricsPoller).
	mu              sync.RWMutex
	cpuUtil         float64
	queueDepth      int64
	latencyEWMAMs   float64 // short-window EWMA
	latencyEWMALong float64 // long-window EWMA
	errorRate       float64
	metricAgeS      float64
	metricFetchedAt time.Time

	// Peak-EWMA: peak latency tracker (B5).
	// peak = max(latencyEWMA, peak * decay)
	peakLatency float64

	// ReactiveEject: consecutive 5xx counter (B7).
	consec5xx    atomic.Int64
	ejectedUntil atomic.Int64 // unix nano when ejection expires; 0 = not ejected
}

// UpdateMetrics overwrites system metrics for this node.
// Called by the NodeMetricsPoller goroutine (not on hot path).
func (n *Node) UpdateMetrics(
	cpu float64, queueDepth int64,
	latencyMs, latencyLongMs, errRate float64,
) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cpuUtil = cpu
	n.queueDepth = queueDepth
	// Peak-EWMA: decay existing peak, then take max with new latency.
	const decayFactor = 0.9 // per update interval
	n.peakLatency = math.Max(latencyMs, n.peakLatency*decayFactor)
	n.latencyEWMAMs = latencyMs
	n.latencyEWMALong = latencyLongMs
	n.errorRate = errRate
	n.metricAgeS = 0
	n.metricFetchedAt = time.Now()
}

// Metrics returns a snapshot of system metrics for feature extraction.
func (n *Node) Metrics() (cpu float64, inflight, queue int64, latMs, latLongMs, errRate, ageS float64) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	age := 0.0
	if !n.metricFetchedAt.IsZero() {
		age = time.Since(n.metricFetchedAt).Seconds()
	}
	return n.cpuUtil, n.InFlight.Load(), n.queueDepth,
		n.latencyEWMAMs, n.latencyEWMALong, n.errorRate, age
}

// IsEjected reports whether B7 has ejected this node.
func (n *Node) IsEjected() bool {
	until := n.ejectedUntil.Load()
	if until == 0 {
		return false
	}
	if time.Now().UnixNano() < until {
		return true
	}
	n.ejectedUntil.Store(0) // expiry elapsed — clear lazily
	return false
}

// RecordStatus registers a response status code (for B7).
func (n *Node) RecordStatus(statusCode int) {
	if statusCode >= 500 {
		if n.consec5xx.Add(1) >= 5 { // 5 consecutive 5xx → eject for 10s
			n.ejectedUntil.Store(time.Now().Add(10 * time.Second).UnixNano())
		}
	} else {
		n.consec5xx.Store(0)
	}
}

// ---- Helper: candidate list ---- //

// available returns non-nil, non-ejected nodes from nodes.
func available(nodes []*Node) []*Node {
	out := make([]*Node, 0, len(nodes))
	for _, n := range nodes {
		if n != nil && !n.IsEjected() {
			out = append(out, n)
		}
	}
	return out
}

// =========================================================
// B1: Round-Robin
// =========================================================

// RoundRobin routes requests in a simple cyclic order.
type RoundRobin struct {
	nodes   []*Node
	counter atomic.Uint64
}

// NewRoundRobin creates a Round-Robin router over nodes.
func NewRoundRobin(nodes []*Node) *RoundRobin {
	return &RoundRobin{nodes: nodes}
}

func (r *RoundRobin) Name() string { return "round-robin" }

func (r *RoundRobin) Pick(_ *http.Request) *Backend {
	if len(r.nodes) == 0 {
		return nil
	}
	idx := r.counter.Add(1) - 1
	n := r.nodes[idx%uint64(len(r.nodes))]
	return &n.Backend
}

// =========================================================
// B2: Weighted Round-Robin (smooth, Nginx-style)
// =========================================================

// WeightedRoundRobin uses the smooth-weighted-RR algorithm so heavier
// backends receive more requests without burst imbalance.
type WeightedRoundRobin struct {
	mu      sync.Mutex
	nodes   []*Node
	current []float64 // effective weights (updated per pick)
}

// NewWeightedRoundRobin creates a Weighted Round-Robin router.
// Node.Weight should be set before calling this.
func NewWeightedRoundRobin(nodes []*Node) *WeightedRoundRobin {
	w := &WeightedRoundRobin{
		nodes:   nodes,
		current: make([]float64, len(nodes)),
	}
	return w
}

func (w *WeightedRoundRobin) Name() string { return "weighted-round-robin" }

func (w *WeightedRoundRobin) Pick(_ *http.Request) *Backend {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.nodes) == 0 {
		return nil
	}

	var totalWeight float64
	for _, n := range w.nodes {
		totalWeight += n.Weight
	}

	// Smooth-WRR: each pick, add each node's configured weight to its current
	// weight, then pick the highest, then subtract totalWeight from the winner.
	for i, n := range w.nodes {
		w.current[i] += n.Weight
	}

	best := 0
	for i := 1; i < len(w.nodes); i++ {
		if w.current[i] > w.current[best] {
			best = i
		}
	}
	w.current[best] -= totalWeight
	return &w.nodes[best].Backend
}

// =========================================================
// B3: Least-Connections (least in-flight)
// =========================================================

// LeastConnections routes to the backend with the fewest in-flight requests.
type LeastConnections struct {
	nodes []*Node
}

func NewLeastConnections(nodes []*Node) *LeastConnections {
	return &LeastConnections{nodes: nodes}
}

func (l *LeastConnections) Name() string { return "least-conn" }

func (l *LeastConnections) Pick(_ *http.Request) *Backend {
	avail := available(l.nodes)
	if len(avail) == 0 {
		return nil
	}
	best := avail[0]
	bestVal := best.InFlight.Load()
	for _, n := range avail[1:] {
		if v := n.InFlight.Load(); v < bestVal {
			best = n
			bestVal = v
		}
	}
	return &best.Backend
}

// =========================================================
// B4: Power-of-Two-Choices (on in-flight)
// =========================================================

// PowerOfTwo picks two nodes at random and routes to whichever has fewer
// in-flight requests (O'Neil et al.; Mitzenmacher 2001).
type PowerOfTwo struct {
	nodes []*Node
}

func NewPowerOfTwo(nodes []*Node) *PowerOfTwo {
	return &PowerOfTwo{nodes: nodes}
}

func (p *PowerOfTwo) Name() string { return "power-of-two" }

func (p *PowerOfTwo) Pick(_ *http.Request) *Backend {
	avail := available(p.nodes)
	switch len(avail) {
	case 0:
		return nil
	case 1:
		return &avail[0].Backend
	}
	i := rand.IntN(len(avail))
	j := rand.IntN(len(avail) - 1)
	if j >= i {
		j++
	}
	a, b := avail[i], avail[j]
	if b.InFlight.Load() < a.InFlight.Load() {
		a = b
	}
	return &a.Backend
}

// =========================================================
// B5: Peak-EWMA (latency-aware, Finagle-style)
// =========================================================

// PeakEWMA routes to the backend with the lowest "load score":
//
//	score = peakLatency * (inflight + 1)
//
// Lower is better (less congested, lower observed latency).
type PeakEWMA struct {
	nodes []*Node
}

func NewPeakEWMA(nodes []*Node) *PeakEWMA {
	return &PeakEWMA{nodes: nodes}
}

func (p *PeakEWMA) Name() string { return "peak-ewma" }

func (p *PeakEWMA) Pick(_ *http.Request) *Backend {
	avail := available(p.nodes)
	if len(avail) == 0 {
		return nil
	}
	best := avail[0]
	bestScore := peakEWMAScore(best)
	for _, n := range avail[1:] {
		if s := peakEWMAScore(n); s < bestScore {
			best = n
			bestScore = s
		}
	}
	return &best.Backend
}

func peakEWMAScore(n *Node) float64 {
	n.mu.RLock()
	peak := n.peakLatency
	n.mu.RUnlock()
	if peak <= 0 {
		peak = 1.0 // cold start: use 1ms as default
	}
	return peak * float64(n.InFlight.Load()+1)
}

// =========================================================
// B6: Heuristic Score (paper formula)
// =========================================================

// HeuristicScore implements the paper's routing score:
//
//	Score(N_i) = alpha*L̂ + beta*C + gamma*Q
//
// Lower is better (cost view). L̂ is the per-(endpoint,node) EWMA latency.
// C = cpu_util, Q = queue_depth (normalised).
type HeuristicScore struct {
	nodes  []*Node
	Alpha  float64 // weight on EWMA latency
	Beta   float64 // weight on CPU utilisation
	Gamma  float64 // weight on queue depth
	QScale float64 // divisor to normalise queue_depth
}

// NewHeuristicScore creates a HeuristicScore router.
// Default coefficients from the paper (Section 4).
func NewHeuristicScore(nodes []*Node) *HeuristicScore {
	return &HeuristicScore{
		nodes:  nodes,
		Alpha:  0.6,
		Beta:   0.3,
		Gamma:  0.1,
		QScale: 100.0,
	}
}

func (h *HeuristicScore) Name() string { return "heuristic-score" }

func (h *HeuristicScore) Pick(_ *http.Request) *Backend {
	avail := available(h.nodes)
	if len(avail) == 0 {
		return nil
	}
	best := avail[0]
	bestScore := h.score(best)
	for _, n := range avail[1:] {
		if s := h.score(n); s < bestScore {
			best = n
			bestScore = s
		}
	}
	return &best.Backend
}

func (h *HeuristicScore) score(n *Node) float64 {
	n.mu.RLock()
	lat := n.latencyEWMAMs
	cpu := n.cpuUtil
	q := float64(n.queueDepth)
	n.mu.RUnlock()
	if lat <= 0 {
		lat = 1.0
	}
	qNorm := q / h.QScale
	return h.Alpha*lat + h.Beta*cpu + h.Gamma*qNorm
}

// =========================================================
// B7: Reactive Ejector (consecutive-5xx based)
// =========================================================

// ReactiveEject is Least-Connections with automatic node ejection after
// EjectThreshold consecutive 5xx responses. Ejected nodes recover after
// EjectDuration. This is the "reactive health check" baseline (spec B7).
type ReactiveEject struct {
	lc *LeastConnections
}

func NewReactiveEject(nodes []*Node) *ReactiveEject {
	return &ReactiveEject{lc: NewLeastConnections(nodes)}
}

func (r *ReactiveEject) Name() string { return "reactive-eject" }

func (r *ReactiveEject) Pick(req *http.Request) *Backend {
	return r.lc.Pick(req)
}
