// Package handler implements the HTTP endpoint handlers for the mock backend.
//
// Endpoints:
//   GET  /health          → light (~0.2 ms, no pool)
//   GET  /crud/*          → light CRUD pool (~5 ms)
//   POST /crud/*          → light CRUD pool (~5 ms)
//   POST /query           → query pool (~40 ms)
//   POST /compute         → compute pool + CPU burn (~80 ms + payload)
//   POST /io              → io pool + sleep (~100 ms)
//   GET  /metrics         → JSON metrics snapshot
//   GET  /metrics/prometheus → Prometheus text format
//   POST /admin/fault     → set fault config (JSON body)
//   GET  /admin/fault     → get current fault config (JSON)
//   DELETE /admin/fault   → clear all faults
//
// Every handler:
//  1. Checks the fault controller (F5/F7 hard fail → 503 immediately).
//  2. Applies F3 stall (sleep) if signalled.
//  3. Acquires a pool token (if the endpoint uses a pool).
//  4. Samples service time from the workload model.
//  5. Applies F1 additive latency and F6 ramp.
//  6. Sleeps or burns CPU for the computed duration.
//  7. Records metrics.
package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/synapse/mockbackend/internal/fault"
	"github.com/synapse/mockbackend/internal/metrics"
	"github.com/synapse/mockbackend/internal/pool"
	"github.com/synapse/mockbackend/internal/workload"
)

// Pools groups the per-class worker pools.
type Pools struct {
	Compute *pool.Pool
	IO      *pool.Pool
	Query   *pool.Pool
	CRUD    *pool.Pool
}

// Handler is the root HTTP handler wiring all endpoints together.
type Handler struct {
	model   *workload.Model
	modelMu sync.Mutex // protects model (lognormal rng is not goroutine-safe)
	fault   *fault.Controller
	metrics *metrics.Collector
	pools   Pools
	mux     *http.ServeMux
}

// New creates a Handler and registers all routes on an internal ServeMux.
func New(
	model *workload.Model,
	fc *fault.Controller,
	mc *metrics.Collector,
	pools Pools,
) *Handler {
	h := &Handler{
		model:   model,
		fault:   fc,
		metrics: mc,
		pools:   pools,
		mux:     http.NewServeMux(),
	}
	h.registerRoutes()
	return h
}

// ServeHTTP satisfies http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) registerRoutes() {
	h.mux.HandleFunc("/health", h.handleHealth)
	h.mux.HandleFunc("/crud/", h.handleCRUD)
	h.mux.HandleFunc("/query", h.handleQuery)
	h.mux.HandleFunc("/compute", h.handleCompute)
	h.mux.HandleFunc("/io", h.handleIO)
	h.mux.HandleFunc("/metrics", h.metrics.HandleJSON)
	h.mux.HandleFunc("/metrics/prometheus", h.metrics.HandlePrometheus)
	h.mux.HandleFunc("/admin/fault", h.handleAdminFault)
}

// ------------------------------------------------------------------ //
// Endpoint handlers                                                   //
// ------------------------------------------------------------------ //

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	if h.fault.IsHardFail() {
		writeError(w, http.StatusServiceUnavailable, "fault: hard fail (F5/F7)")
		return
	}
	h.applyF3Stall()
	start := time.Now()

	dur := h.sampleDuration(workload.ClassHealth, 0)
	dur = h.applyFaultLatency(dur)
	time.Sleep(dur)

	latMs := float64(time.Since(start)) / float64(time.Millisecond)
	h.metrics.RecordRequest(latMs, false)
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"node_id": h.metrics.Snapshot().NodeID,
	})
}

func (h *Handler) handleCRUD(w http.ResponseWriter, r *http.Request) {
	if !h.checkMethods(w, r, http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete) {
		return
	}
	h.serveWithPool(w, r, h.pools.CRUD, workload.ClassLightCRUD, r.ContentLength)
}

func (h *Handler) handleQuery(w http.ResponseWriter, r *http.Request) {
	if !h.checkMethods(w, r, http.MethodPost, http.MethodGet) {
		return
	}
	h.serveWithPool(w, r, h.pools.Query, workload.ClassMediumQuery, r.ContentLength)
}

func (h *Handler) handleCompute(w http.ResponseWriter, r *http.Request) {
	if !h.checkMethods(w, r, http.MethodPost) {
		return
	}

	if h.fault.IsHardFail() {
		writeError(w, http.StatusServiceUnavailable, "fault: hard fail")
		return
	}
	h.applyF3Stall()

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// Update queued metrics while waiting for pool
	h.metrics.SetQueued(h.pools.Compute.Queued() + 1)
	err := h.pools.Compute.Acquire(ctx)
	h.metrics.SetQueued(h.pools.Compute.Queued())
	if err != nil {
		code := http.StatusServiceUnavailable
		if err == pool.ErrPoolFull {
			code = http.StatusTooManyRequests
		}
		h.metrics.RecordRequest(0, true)
		writeError(w, code, err.Error())
		return
	}
	defer h.pools.Compute.Release()
	h.metrics.SetInFlight(h.pools.Compute.InFlight())

	start := time.Now()
	payloadBytes := r.ContentLength

	// Sample base latency
	baseDur := h.sampleDuration(workload.ClassHeavyCompute, payloadBytes)
	baseDur = h.applyFaultLatency(baseDur)

	// Actually burn CPU — this is what makes /compute a CPU-bound endpoint.
	h.modelMu.Lock()
	iters := h.model.CPUIterations(payloadBytes)
	h.modelMu.Unlock()
	sink := workload.BurnCPU(iters)
	_ = sink

	// Sleep any remaining time (base latency - actual CPU time spent)
	elapsed := time.Since(start)
	if baseDur > elapsed {
		time.Sleep(baseDur - elapsed)
	}

	latMs := float64(time.Since(start)) / float64(time.Millisecond)
	h.metrics.RecordRequest(latMs, false)
	h.metrics.SetInFlight(h.pools.Compute.InFlight())

	writeJSON(w, http.StatusOK, map[string]any{
		"status":       "ok",
		"latency_ms":   latMs,
		"payload_bytes": payloadBytes,
	})
}

func (h *Handler) handleIO(w http.ResponseWriter, r *http.Request) {
	if !h.checkMethods(w, r, http.MethodPost) {
		return
	}
	h.serveWithPool(w, r, h.pools.IO, workload.ClassIOBound, r.ContentLength)
}

// serveWithPool is the generic handler for pool-backed endpoints.
func (h *Handler) serveWithPool(
	w http.ResponseWriter,
	r *http.Request,
	p *pool.Pool,
	class workload.Class,
	payloadBytes int64,
) {
	if h.fault.IsHardFail() {
		writeError(w, http.StatusServiceUnavailable, "fault: hard fail (F5/F7)")
		h.metrics.RecordRequest(0, true)
		return
	}
	h.applyF3Stall()

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if err := p.Acquire(ctx); err != nil {
		code := http.StatusServiceUnavailable
		if err == pool.ErrPoolFull {
			code = http.StatusTooManyRequests
		}
		h.metrics.RecordRequest(0, true)
		writeError(w, code, err.Error())
		return
	}
	defer p.Release()
	h.metrics.SetInFlight(h.pools.Compute.InFlight() + h.pools.IO.InFlight() +
		h.pools.Query.InFlight() + h.pools.CRUD.InFlight())

	start := time.Now()
	dur := h.sampleDuration(class, payloadBytes)
	dur = h.applyFaultLatency(dur)
	time.Sleep(dur)

	latMs := float64(time.Since(start)) / float64(time.Millisecond)
	h.metrics.RecordRequest(latMs, false)
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"latency_ms": latMs,
		"class":      class,
	})
}

// ------------------------------------------------------------------ //
// Admin fault API                                                     //
// ------------------------------------------------------------------ //

func (h *Handler) handleAdminFault(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, h.fault.Load())

	case http.MethodPost:
		var cfg fault.Config
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		// F4: adjust pool cap if requested
		if cfg.F4WorkerCap > 0 {
			h.pools.Compute.SetCap(int64(cfg.F4WorkerCap))
			h.pools.IO.SetCap(int64(cfg.F4WorkerCap * 2))
		} else {
			h.pools.Compute.ResetCap()
			h.pools.IO.ResetCap()
		}
		h.fault.Set(cfg)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})

	case http.MethodDelete:
		h.fault.Clear()
		h.pools.Compute.ResetCap()
		h.pools.IO.ResetCap()
		h.pools.Query.ResetCap()
		h.pools.CRUD.ResetCap()
		writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ------------------------------------------------------------------ //
// Helpers                                                             //
// ------------------------------------------------------------------ //

// sampleDuration samples the service time for a given class.
// Protected by modelMu since the lognormal RNG is not goroutine-safe.
func (h *Handler) sampleDuration(class workload.Class, payloadBytes int64) time.Duration {
	h.modelMu.Lock()
	d := h.model.Sample(class, payloadBytes)
	h.modelMu.Unlock()
	return d
}

// applyFaultLatency adds the fault controller's extra latency to d.
func (h *Handler) applyFaultLatency(d time.Duration) time.Duration {
	cfg := h.fault.Load()
	mul := cfg.EffectiveMultiplier()
	extra := h.fault.ExtraLatency()
	result := time.Duration(float64(d)*mul) +
		time.Duration(extra*float64(time.Millisecond))
	if result < 0 {
		return 0
	}
	return result
}

// applyF3Stall sleeps for the F3 stall duration if the stall ticker is active.
func (h *Handler) applyF3Stall() {
	if d := h.fault.StallDuration(); d > 0 {
		time.Sleep(d)
	}
}

// checkMethods returns true if the request method is in the allowed set.
func (h *Handler) checkMethods(w http.ResponseWriter, r *http.Request, methods ...string) bool {
	for _, m := range methods {
		if r.Method == m {
			return true
		}
	}
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": strings.TrimSpace(msg),
	})
}
