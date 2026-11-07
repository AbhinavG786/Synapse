// Package nodepoller periodically fetches /metrics from each backend node and
// updates the Node's cached system metrics. This feeds the PeakEWMA, HeuristicScore,
// and feature extractor with fresh CPU/queue/latency data.
//
// Design (R-4): polling is fully out of band. The hot path only reads
// the atomic/mu-protected fields in Node; this goroutine only writes them.
package nodepoller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/synapse/dataplane/internal/router"
)

// MetricsResponse mirrors the JSON schema of the mock backend's GET /metrics.
type MetricsResponse struct {
	NodeID        string  `json:"node_id"`
	SpeedFactor   float64 `json:"speed_factor"`
	CPUUtil       float64 `json:"cpu_util"`
	InFlight      int64   `json:"in_flight"`
	QueueDepth    int64   `json:"queue_depth"`
	LatencyEWMAMs float64 `json:"latency_ewma_ms"`
	ErrorRate     float64 `json:"error_rate"`
	TotalRequests int64   `json:"total_requests"`
	TotalErrors   int64   `json:"total_errors"`
	TimestampMs   int64   `json:"timestamp_ms"`
}

// Poller fetches /metrics from all registered backend nodes at a fixed interval.
type Poller struct {
	nodes    []*router.Node
	interval time.Duration
	client   *http.Client
	logger   *slog.Logger
}

// New creates a Poller.
// interval controls how often /metrics is fetched per node.
// A per-request timeout of interval/2 is applied to each HTTP fetch.
func New(nodes []*router.Node, interval time.Duration, logger *slog.Logger) *Poller {
	if logger == nil {
		logger = slog.Default()
	}
	timeout := interval / 2
	if timeout < 500*time.Millisecond {
		timeout = 500 * time.Millisecond
	}
	return &Poller{
		nodes:    nodes,
		interval: interval,
		client:   &http.Client{Timeout: timeout},
		logger:   logger.With("component", "node-poller"),
	}
}

// Run starts the polling loop and blocks until ctx is cancelled.
// Call in a dedicated goroutine.
func (p *Poller) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	// Immediately fetch once on startup.
	p.pollAll(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.pollAll(ctx)
		}
	}
}

// pollAll fetches metrics from all nodes concurrently.
func (p *Poller) pollAll(ctx context.Context) {
	done := make(chan struct{}, len(p.nodes))
	for _, n := range p.nodes {
		n := n
		go func() {
			defer func() { done <- struct{}{} }()
			p.poll(ctx, n)
		}()
	}
	// Wait for all fetches to complete (or context done).
	for range p.nodes {
		select {
		case <-done:
		case <-ctx.Done():
			return
		}
	}
}

// poll fetches /metrics from a single node and updates its cached state.
func (p *Poller) poll(ctx context.Context, n *router.Node) {
	url := fmt.Sprintf("%s/metrics", n.Address)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return
	}

	resp, err := p.client.Do(req)
	if err != nil {
		p.logger.Debug("metrics fetch failed", "node", n.NodeID, "err", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return
	}

	var m MetricsResponse
	if err := json.Unmarshal(body, &m); err != nil {
		p.logger.Warn("metrics parse failed", "node", n.NodeID, "err", err)
		return
	}

	// Long-window EWMA: we don't have it from the backend directly,
	// so approximate as 2× the short-window EWMA for the first implementation.
	// Phase 3 will add a proper long-window EWMA to the backend /metrics.
	longEWMA := m.LatencyEWMAMs * 1.1 // placeholder; replaced in Phase 3

	n.UpdateMetrics(m.CPUUtil, m.QueueDepth, m.LatencyEWMAMs, longEWMA, m.ErrorRate)
}

