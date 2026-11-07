// Package metrics registers Prometheus metrics for the Synapse proxy data plane.
//
// Registered metrics (Phase 2.6):
//   synapse_requests_total{algo, endpoint, node, status}   counter
//   synapse_request_duration_ms{algo, endpoint, node}       histogram
//   synapse_telemetry_dropped_total                         counter
//   synapse_in_flight{node}                                 gauge
//   synapse_policy_version                                  gauge
//   synapse_node_cpu_util{node}                             gauge
//   synapse_node_queue_depth{node}                          gauge
//
// All hot-path operations use pre-computed label values and direct counter/gauge
// updates to avoid string allocation. Histograms use a custom bucket set.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry holds all Synapse proxy Prometheus metrics.
type Registry struct {
	RequestsTotal    *prometheus.CounterVec
	RequestDurationMs *prometheus.HistogramVec
	TelemetryDropped prometheus.Counter
	InFlight         *prometheus.GaugeVec
	PolicyVersion    prometheus.Gauge
	NodeCPUUtil      *prometheus.GaugeVec
	NodeQueueDepth   *prometheus.GaugeVec

	reg *prometheus.Registry
}

// latencyBuckets covers 0.5ms to 10s — appropriate for the proxy's range.
var latencyBuckets = []float64{
	0.5, 1, 2, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10000,
}

// New creates and registers all Synapse proxy metrics on a new Prometheus registry.
func New() *Registry {
	reg := prometheus.NewRegistry()
	r := &Registry{reg: reg}

	r.RequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "synapse_requests_total",
		Help: "Total requests proxied, by algorithm, endpoint class, node, and HTTP status.",
	}, []string{"algo", "endpoint", "node", "status"})

	r.RequestDurationMs = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "synapse_request_duration_ms",
		Help:    "End-to-end request duration in milliseconds as observed by the proxy.",
		Buckets: latencyBuckets,
	}, []string{"algo", "endpoint", "node"})

	r.TelemetryDropped = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "synapse_telemetry_dropped_total",
		Help: "Total telemetry events dropped because the channel was full.",
	})

	r.InFlight = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "synapse_in_flight",
		Help: "Current in-flight request count per backend node.",
	}, []string{"node"})

	r.PolicyVersion = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "synapse_policy_version",
		Help: "Current policy snapshot version received from the control plane.",
	})

	r.NodeCPUUtil = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "synapse_node_cpu_util",
		Help: "Last-polled CPU utilisation per backend node (0–1).",
	}, []string{"node"})

	r.NodeQueueDepth = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "synapse_node_queue_depth",
		Help: "Last-polled queue depth per backend node.",
	}, []string{"node"})

	// Register all.
	reg.MustRegister(
		r.RequestsTotal,
		r.RequestDurationMs,
		r.TelemetryDropped,
		r.InFlight,
		r.PolicyVersion,
		r.NodeCPUUtil,
		r.NodeQueueDepth,
		prometheus.NewGoCollector(),
		prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}),
	)

	return r
}

// Handler returns the Prometheus HTTP handler for the /metrics scrape endpoint.
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
}

// ObserveRequest records a completed proxied request into the appropriate metrics.
// Call this after each request completes. Status is the HTTP status code string (e.g. "200").
func (r *Registry) ObserveRequest(algo, endpoint, node, status string, durationMs float64) {
	r.RequestsTotal.WithLabelValues(algo, endpoint, node, status).Inc()
	r.RequestDurationMs.WithLabelValues(algo, endpoint, node).Observe(durationMs)
}

