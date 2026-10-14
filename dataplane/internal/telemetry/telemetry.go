// Package telemetry provides a bounded, non-blocking event emitter for the
// Synapse data plane. The hot path pushes TelemetryEvents to a buffered channel;
// a background batcher drains it and forwards events to the control plane via
// Redis Streams or gRPC (Phase 3). Dropped events increment a Prometheus counter
// but never block a request (R-4).
//
// Phase 0: type definitions and stub emitter only. Full pipeline in Phase 2.5/3.
package telemetry

import (
	"log/slog"
	"sync/atomic"
	"time"
)

// Event mirrors the proto TelemetryEvent fields in a Go-native struct.
// Avoids proto allocation on the hot path; serialised in the batcher goroutine.
type Event struct {
	ReqID          string
	RunID          string
	TimestampUnixMs int64
	EndpointClass  string
	Method         string
	PayloadBytes   int64
	AuthTier       string
	NodeID         string
	AlgoName       string
	PolicyVersion  uint64
	Propensity     float64
	TExecMs        float64
	StatusCode     int
	TimedOut       bool
	Error          bool
	Reward         float64
	ContextVector  []float64 // length = d
	// System features at decision time
	NodeCPUUtil          float64
	NodeInFlight         int64
	NodeQueueDepth       int64
	NodeLatencyEWMAShort float64
	NodeLatencyEWMALong  float64
	NodeErrorRate        float64
	NodeMetricAgeS       float64
}

// Emitter is a non-blocking telemetry event sink.
type Emitter struct {
	ch          chan Event
	droppedTotal atomic.Int64
	logger      *slog.Logger
}

// NewEmitter creates an Emitter with a buffered channel of size channelSize.
// Events are dropped (and counted) if the channel is full (R-4).
func NewEmitter(channelSize int) *Emitter {
	return &Emitter{
		ch:     make(chan Event, channelSize),
		logger: slog.Default().With("component", "telemetry"),
	}
}

// Emit sends an event to the internal channel without blocking.
// If the channel is full, the event is dropped and the drop counter incremented.
func (e *Emitter) Emit(ev Event) {
	select {
	case e.ch <- ev:
	default:
		e.droppedTotal.Add(1)
	}
}

// DroppedTotal returns the cumulative number of dropped events.
func (e *Emitter) DroppedTotal() int64 {
	return e.droppedTotal.Load()
}

// C returns the read-side of the event channel (for the batcher goroutine).
func (e *Emitter) C() <-chan Event {
	return e.ch
}

// LogDrops periodically logs the drop counter if nonzero.
// Call this in a goroutine; cancel via ctx. Phase 0 stub — real Prometheus
// counter registration in Phase 2.6.
func (e *Emitter) LogDrops(interval time.Duration, stop <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var last int64
	for {
		select {
		case <-ticker.C:
			current := e.droppedTotal.Load()
			if delta := current - last; delta > 0 {
				e.logger.Warn("telemetry events dropped", "delta", delta, "total", current)
				last = current
			}
		case <-stop:
			return
		}
	}
}
