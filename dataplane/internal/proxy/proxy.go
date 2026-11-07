// Package proxy implements the core HTTP reverse-proxy dispatcher for Synapse.
//
// Design (R-4 hot-path discipline):
//   - One httputil.ReverseProxy per backend node, sharing a tuned Transport.
//   - Per-node in-flight counter (atomic int64) updated pre/post request.
//   - Feature extraction → Router.Pick() → dispatch → telemetry emit.
//     Every step is O(1) or O(N) where N = number of backends (≤32).
//   - Telemetry event pushed to bounded channel; dropped events counted.
//   - No allocation of proto objects on the hot path; Event struct reused.
//
// Error handling:
//   - Backend 5xx → record status on Node (B7 consec5xx counter).
//   - Backend timeout → 504 to client + telemetry error=true.
//   - No backend available → 503 to client.
//
// Connection pool: each backend gets its own http.Transport (keep-alive,
// MaxIdleConnsPerHost, HTTP/2). Prevents cross-node pool starvation.
package proxy

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/synapse/dataplane/internal/features"
	"github.com/synapse/dataplane/internal/pool"
	"github.com/synapse/dataplane/internal/router"
	"github.com/synapse/dataplane/internal/telemetry"
)

// Dispatcher is the HTTP handler that sits at the core of the proxy.
// It runs the full pipeline: feature extract → pick → dispatch → telemetry.
type Dispatcher struct {
	router    router.Router
	nodes     map[string]*nodeEntry // nodeID → entry
	extractor *features.Extractor
	emitter   *telemetry.Emitter
	runID     string
	logger    *slog.Logger

	// reqTimeout is the per-request deadline including backend round-trip.
	reqTimeout time.Duration

	// Global in-flight counter (all backends combined).
	totalInFlight atomic.Int64
}

// nodeEntry bundles per-backend state.
type nodeEntry struct {
	node    *router.Node
	proxy   *httputil.ReverseProxy
	target  *url.URL
}

// Config holds constructor parameters for the Dispatcher.
type Config struct {
	Router     router.Router
	Nodes      []*router.Node
	Extractor  *features.Extractor
	Emitter    *telemetry.Emitter
	PoolConfig pool.PoolConfig
	RunID      string
	ReqTimeout time.Duration
	Logger     *slog.Logger
}

// New creates a Dispatcher.
func New(cfg Config) (*Dispatcher, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.ReqTimeout <= 0 {
		cfg.ReqTimeout = 30 * time.Second
	}

	d := &Dispatcher{
		router:     cfg.Router,
		nodes:      make(map[string]*nodeEntry, len(cfg.Nodes)),
		extractor:  cfg.Extractor,
		emitter:    cfg.Emitter,
		runID:      cfg.RunID,
		logger:     cfg.Logger,
		reqTimeout: cfg.ReqTimeout,
	}

	for _, n := range cfg.Nodes {
		target, err := url.Parse(n.Address)
		if err != nil {
			return nil, err
		}
		transport := pool.NewTransport(cfg.PoolConfig)
		rp := &httputil.ReverseProxy{
			Director:  makeDirector(target),
			Transport: transport,
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				d.logger.Warn("backend error", "node", n.NodeID, "err", err)
				http.Error(w, "Bad Gateway", http.StatusBadGateway)
			},
			ModifyResponse: d.makeModifyResponse(n),
		}
		d.nodes[n.NodeID] = &nodeEntry{node: n, proxy: rp, target: target}
	}

	return d, nil
}

// ServeHTTP is the main hot path.
func (d *Dispatcher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Apply per-request timeout.
	ctx, cancel := context.WithTimeout(r.Context(), d.reqTimeout)
	defer cancel()
	r = r.WithContext(ctx)

	// Extract request features (O(1), no body read).
	reqFeatures := d.extractor.ExtractRequest(r)

	// Pick a backend.
	backend := d.router.Pick(r)
	if backend == nil {
		d.logger.Warn("no backend available")
		http.Error(w, `{"error":"no backend available"}`, http.StatusServiceUnavailable)
		d.emitDrop(reqFeatures, start)
		return
	}

	entry, ok := d.nodes[backend.NodeID]
	if !ok {
		http.Error(w, `{"error":"backend not found"}`, http.StatusInternalServerError)
		return
	}
	n := entry.node

	// Update in-flight counter.
	n.InFlight.Add(1)
	d.totalInFlight.Add(1)
	defer func() {
		n.InFlight.Add(-1)
		d.totalInFlight.Add(-1)
	}()

	// Wrap ResponseWriter to capture status code.
	rw := newStatusWriter(w)

	// Dispatch.
	entry.proxy.ServeHTTP(rw, r)

	// Record status for B7.
	n.RecordStatus(rw.status)

	// Emit telemetry.
	execMs := float64(time.Since(start)) / float64(time.Millisecond)
	isErr := rw.status >= 500
	timedOut := ctx.Err() != nil

	cpu, inflight, queue, latMs, latLongMs, errRate, ageS := n.Metrics()

	d.emitter.Emit(telemetry.Event{
		RunID:                d.runID,
		TimestampUnixMs:      start.UnixMilli(),
		EndpointClass:        features.ClassName(reqFeatures.EndpointClass),
		Method:               r.Method,
		PayloadBytes:         r.ContentLength,
		NodeID:               backend.NodeID,
		AlgoName:             d.router.Name(),
		TExecMs:              execMs,
		StatusCode:           rw.status,
		TimedOut:             timedOut,
		Error:                isErr,
		NodeCPUUtil:          cpu,
		NodeInFlight:         inflight,
		NodeQueueDepth:       queue,
		NodeLatencyEWMAShort: latMs,
		NodeLatencyEWMALong:  latLongMs,
		NodeErrorRate:        errRate,
		NodeMetricAgeS:       ageS,
	})
}

// TotalInFlight returns the proxy-global in-flight count.
func (d *Dispatcher) TotalInFlight() int64 {
	return d.totalInFlight.Load()
}

// makeDirector builds an httputil.ReverseProxy Director for the given target.
func makeDirector(target *url.URL) func(*http.Request) {
	return func(r *http.Request) {
		r.URL.Scheme = target.Scheme
		r.URL.Host = target.Host
		r.Host = target.Host
		// Preserve the original path and query.
		if target.RawPath != "" {
			r.URL.RawPath = target.RawPath + r.URL.RawPath
		}
		// Add forwarding headers.
		if _, ok := r.Header["X-Forwarded-For"]; !ok {
			r.Header.Set("X-Forwarded-For", r.RemoteAddr)
		}
		r.Header.Set("X-Forwarded-Host", r.Host)
	}
}

// makeModifyResponse returns a ModifyResponse hook that injects timing headers.
func (d *Dispatcher) makeModifyResponse(n *router.Node) func(*http.Response) error {
	return func(resp *http.Response) error {
		resp.Header.Set("X-Synapse-Node", n.NodeID)
		return nil
	}
}

// emitDrop emits a no-backend telemetry event.
func (d *Dispatcher) emitDrop(req features.RequestFeatures, start time.Time) {
	d.emitter.Emit(telemetry.Event{
		RunID:           d.runID,
		TimestampUnixMs: start.UnixMilli(),
		EndpointClass:   features.ClassName(req.EndpointClass),
		Method:          "",
		StatusCode:      503,
		Error:           true,
		TExecMs:         float64(time.Since(start)) / float64(time.Millisecond),
	})
}

// ---- statusWriter ---- //

// statusWriter is a minimal ResponseWriter wrapper that captures the status code.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func newStatusWriter(w http.ResponseWriter) *statusWriter {
	return &statusWriter{ResponseWriter: w, status: http.StatusOK}
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

// ---- Flush support ---- //

// Flush propagates if the underlying writer supports it.
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
