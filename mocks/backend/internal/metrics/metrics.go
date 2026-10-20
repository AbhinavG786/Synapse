// Package metrics exposes a live JSON snapshot of per-node metrics for the
// Synapse proxy's context-vector builder (feature extractor, Phase 2.3).
//
// GET /metrics → JSON (consumed by the proxy)
// GET /metrics/prometheus → Prometheus text format (consumed by Grafana)
//
// The proxy reads /metrics on each routing decision to populate:
//   - cpu_util, in_flight, queue_depth, latency_ewma_ms, error_rate, metric_age_s
//
// CPU utilisation is approximated as (compute_inflight / compute_max_workers)
// since we cannot read /proc/stat portably from inside Go without OS-specific
// code. This is a valid proxy for a compute-bound mock backend.
package metrics

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Snapshot is the JSON-serialisable metrics payload consumed by the proxy.
type Snapshot struct {
	NodeID         string  `json:"node_id"`
	SpeedFactor    float64 `json:"speed_factor"`
	CPUUtil        float64 `json:"cpu_util"`         // [0, 1]
	InFlight       int64   `json:"in_flight"`        // total in-flight across all pools
	QueueDepth     int64   `json:"queue_depth"`      // total queued across all pools
	LatencyEWMAMs  float64 `json:"latency_ewma_ms"`  // short-window EWMA
	ErrorRate      float64 `json:"error_rate"`       // rolling fraction of errors
	TotalRequests  int64   `json:"total_requests"`
	TotalErrors    int64   `json:"total_errors"`
	TimestampMs    int64   `json:"timestamp_ms"`
}

// Collector accumulates per-request observations and exposes /metrics.
type Collector struct {
	nodeID      string
	speedFactor float64
	maxWorkers  int64 // for CPU util denominator

	// Atomic counters (updated per request, safe for concurrent use)
	totalRequests atomic.Int64
	totalErrors   atomic.Int64
	inFlight      atomic.Int64
	queued        atomic.Int64

	// EWMA latency (protected by mu because it's a float64 update)
	mu           sync.Mutex
	ewmaLatencyMs float64
	ewmaAlpha     float64 // 2/(N+1) for N=20 window → α≈0.095

	// Rolling error rate (simple ratio over last window)
	windowReqs   atomic.Int64
	windowErrors atomic.Int64
}

// New creates a Collector for the given node.
// maxComputeWorkers is used as the denominator for the CPU utilisation proxy.
func New(nodeID string, speedFactor float64, maxComputeWorkers int) *Collector {
	return &Collector{
		nodeID:      nodeID,
		speedFactor: speedFactor,
		maxWorkers:  int64(maxComputeWorkers),
		ewmaAlpha:   2.0 / (20.0 + 1.0), // N=20 window
	}
}

// RecordRequest records the outcome of a completed request.
// latencyMs is the end-to-end service time in milliseconds.
// isError is true for 4xx/5xx or timeout.
func (c *Collector) RecordRequest(latencyMs float64, isError bool) {
	c.totalRequests.Add(1)
	c.windowReqs.Add(1)
	if isError {
		c.totalErrors.Add(1)
		c.windowErrors.Add(1)
	}

	c.mu.Lock()
	if c.ewmaLatencyMs == 0 {
		c.ewmaLatencyMs = latencyMs
	} else {
		c.ewmaLatencyMs = c.ewmaAlpha*latencyMs + (1-c.ewmaAlpha)*c.ewmaLatencyMs
	}
	c.mu.Unlock()
}

// SetInFlight updates the in-flight counter. Called by the handler.
func (c *Collector) SetInFlight(n int64) { c.inFlight.Store(n) }

// SetQueued updates the queued counter. Called by the handler.
func (c *Collector) SetQueued(n int64) { c.queued.Store(n) }

// Snapshot returns a current metrics snapshot.
func (c *Collector) Snapshot() Snapshot {
	c.mu.Lock()
	ewma := c.ewmaLatencyMs
	c.mu.Unlock()

	inFlight := c.inFlight.Load()
	cpuUtil := 0.0
	if c.maxWorkers > 0 {
		cpuUtil = math.Min(float64(inFlight)/float64(c.maxWorkers), 1.0)
	}

	var errRate float64
	wReqs := c.windowReqs.Load()
	if wReqs > 0 {
		errRate = float64(c.windowErrors.Load()) / float64(wReqs)
	}

	return Snapshot{
		NodeID:        c.nodeID,
		SpeedFactor:   c.speedFactor,
		CPUUtil:       cpuUtil,
		InFlight:      inFlight,
		QueueDepth:    c.queued.Load(),
		LatencyEWMAMs: ewma,
		ErrorRate:     errRate,
		TotalRequests: c.totalRequests.Load(),
		TotalErrors:   c.totalErrors.Load(),
		TimestampMs:   time.Now().UnixMilli(),
	}
}

// ResetWindow resets the rolling error-rate window. Call periodically (e.g. every minute).
func (c *Collector) ResetWindow() {
	c.windowReqs.Store(0)
	c.windowErrors.Store(0)
}

// HandleJSON serves GET /metrics as JSON.
func (c *Collector) HandleJSON(w http.ResponseWriter, r *http.Request) {
	snap := c.Snapshot()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(snap)
}

// HandlePrometheus serves GET /metrics/prometheus in Prometheus text format.
func (c *Collector) HandlePrometheus(w http.ResponseWriter, r *http.Request) {
	snap := c.Snapshot()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)
	labels := fmt.Sprintf(`node_id="%s"`, snap.NodeID)
	fmt.Fprintf(w, "# HELP backend_cpu_util CPU utilisation proxy (inflight/maxWorkers)\n")
	fmt.Fprintf(w, "# TYPE backend_cpu_util gauge\n")
	fmt.Fprintf(w, "backend_cpu_util{%s} %.4f\n", labels, snap.CPUUtil)
	fmt.Fprintf(w, "# HELP backend_in_flight In-flight requests\n")
	fmt.Fprintf(w, "# TYPE backend_in_flight gauge\n")
	fmt.Fprintf(w, "backend_in_flight{%s} %d\n", labels, snap.InFlight)
	fmt.Fprintf(w, "# HELP backend_queue_depth Queued requests\n")
	fmt.Fprintf(w, "# TYPE backend_queue_depth gauge\n")
	fmt.Fprintf(w, "backend_queue_depth{%s} %d\n", labels, snap.QueueDepth)
	fmt.Fprintf(w, "# HELP backend_latency_ewma_ms Latency EWMA (ms)\n")
	fmt.Fprintf(w, "# TYPE backend_latency_ewma_ms gauge\n")
	fmt.Fprintf(w, "backend_latency_ewma_ms{%s} %.3f\n", labels, snap.LatencyEWMAMs)
	fmt.Fprintf(w, "# HELP backend_error_rate Rolling error rate fraction\n")
	fmt.Fprintf(w, "# TYPE backend_error_rate gauge\n")
	fmt.Fprintf(w, "backend_error_rate{%s} %.6f\n", labels, snap.ErrorRate)
	fmt.Fprintf(w, "# HELP backend_total_requests Total requests handled\n")
	fmt.Fprintf(w, "# TYPE backend_total_requests counter\n")
	fmt.Fprintf(w, "backend_total_requests{%s} %d\n", labels, snap.TotalRequests)
}
