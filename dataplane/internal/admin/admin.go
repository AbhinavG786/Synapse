// Package admin implements the Synapse proxy Admin REST + WebSocket API.
//
// Endpoints:
//   GET  /admin/healthz          → {"status":"ok","algo":"...","nodes":N}
//   GET  /admin/nodes            → list of nodes with live metrics
//   GET  /admin/policy           → current policy snapshot summary
//   POST /admin/algo             → switch routing algorithm live
//   GET  /admin/ws               → WebSocket stream of live metrics (1 Hz)
//
// Admin actions (algo switch) are protected by a shared secret header
// X-Admin-Token when SYNAPSE_ADMIN_TOKEN env is set.
//
// WebSocket stream emits a JSON object every second with:
//
//	{ts_ms, nodes:[{id, inflight, cpu, queue, latency_ewma_ms, error_rate}],
//	 algo, policy_version, total_inflight}
package admin

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/synapse/dataplane/internal/router"
)

// RouterHolder allows the Admin API to swap the active router at runtime.
type RouterHolder struct {
	mu     sync.RWMutex
	active router.Router
}

// NewRouterHolder creates a RouterHolder with the given initial router.
func NewRouterHolder(r router.Router) *RouterHolder {
	return &RouterHolder{active: r}
}

// Get returns the current active router.
func (h *RouterHolder) Get() router.Router {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.active
}

// Set atomically replaces the active router.
func (h *RouterHolder) Set(r router.Router) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.active = r
}

// Handler is the Admin API HTTP handler.
type Handler struct {
	nodes        []*router.Node
	routerHolder *RouterHolder
	algoFactory  func(name string, nodes []*router.Node) (router.Router, error)
	policyVer    atomic.Uint64
	token        string // empty = no auth required
	logger       *slog.Logger
	upgrader     websocket.Upgrader
}

// New creates an Admin HTTP handler.
// algoFactory is called on POST /admin/algo to create the new router.
func New(
	nodes []*router.Node,
	holder *RouterHolder,
	algoFactory func(name string, nodes []*router.Node) (router.Router, error),
	logger *slog.Logger,
) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		nodes:        nodes,
		routerHolder: holder,
		algoFactory:  algoFactory,
		token:        os.Getenv("SYNAPSE_ADMIN_TOKEN"),
		logger:       logger.With("component", "admin"),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

// NewHandler creates a stub handler for Phase 0 compatibility.
// Replaced by New() in Phase 2.
func NewHandler() *Handler {
	return &Handler{
		upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
		logger:   slog.Default(),
	}
}

// Register mounts all admin routes on mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/admin/healthz", h.handleHealthz)
	mux.HandleFunc("/admin/nodes", h.handleNodes)
	mux.HandleFunc("/admin/policy", h.handlePolicy)
	mux.HandleFunc("/admin/algo", h.authMiddleware(h.handleAlgo))
	mux.HandleFunc("/admin/ws", h.handleWS)
}

// RegisterRoutes is an alias for Register (Phase 0 compatibility).
func (h *Handler) RegisterRoutes(mux *http.ServeMux) { h.Register(mux) }

// ---- Handlers ---- //

func (h *Handler) handleHealthz(w http.ResponseWriter, r *http.Request) {
	algoName := "unknown"
	if h.routerHolder != nil {
		algoName = h.routerHolder.Get().Name()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"algo":   algoName,
		"nodes":  len(h.nodes),
	})
}

func (h *Handler) handleNodes(w http.ResponseWriter, r *http.Request) {
	type nodeSummary struct {
		NodeID        string  `json:"node_id"`
		Address       string  `json:"address"`
		InFlight      int64   `json:"in_flight"`
		CPUUtil       float64 `json:"cpu_util"`
		QueueDepth    int64   `json:"queue_depth"`
		LatencyEWMAMs float64 `json:"latency_ewma_ms"`
		ErrorRate     float64 `json:"error_rate"`
		MetricAgeS    float64 `json:"metric_age_s"`
		Ejected       bool    `json:"ejected"`
	}
	out := make([]nodeSummary, len(h.nodes))
	for i, n := range h.nodes {
		cpu, inf, q, lat, _, errRate, age := n.Metrics()
		out[i] = nodeSummary{
			NodeID:        n.NodeID,
			Address:       n.Address,
			InFlight:      inf,
			CPUUtil:       cpu,
			QueueDepth:    q,
			LatencyEWMAMs: lat,
			ErrorRate:     errRate,
			MetricAgeS:    age,
			Ejected:       n.IsEjected(),
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) handlePolicy(w http.ResponseWriter, r *http.Request) {
	algoName := "unknown"
	if h.routerHolder != nil {
		algoName = h.routerHolder.Get().Name()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"algo":           algoName,
		"policy_version": h.policyVer.Load(),
	})
}

func (h *Handler) handleAlgo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.algoFactory == nil || h.routerHolder == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "stub", "phase": "2"})
		return
	}
	var body struct {
		Algo string `json:"algo"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	newRouter, err := h.algoFactory(body.Algo, h.nodes)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unknown algo: "+body.Algo)
		return
	}
	h.routerHolder.Set(newRouter)
	h.logger.Info("algorithm switched", "algo", body.Algo)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "algo": body.Algo})
}

// handleAlgorithm is an alias for handleAlgo (Phase 0 route compat).
func (h *Handler) handleAlgorithm(w http.ResponseWriter, r *http.Request) {
	h.handleAlgo(w, r)
}

// handleMetrics is a stub for Phase 0 compatibility.
func (h *Handler) handleMetrics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleWS upgrades to WebSocket and streams live metrics at 1 Hz.
func (h *Handler) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Warn("ws upgrade failed", "err", err)
		return
	}
	defer conn.Close()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		msg := h.buildWSMessage()
		if err := conn.WriteJSON(msg); err != nil {
			return // client disconnected
		}
	}
}

type wsMessage struct {
	TsMs          int64         `json:"ts_ms"`
	Algo          string        `json:"algo"`
	PolicyVer     uint64        `json:"policy_version"`
	TotalInflight int64         `json:"total_inflight"`
	Nodes         []wsNodeEntry `json:"nodes"`
}

type wsNodeEntry struct {
	ID            string  `json:"id"`
	InFlight      int64   `json:"in_flight"`
	CPUUtil       float64 `json:"cpu_util"`
	QueueDepth    int64   `json:"queue_depth"`
	LatencyEWMAMs float64 `json:"latency_ewma_ms"`
	ErrorRate     float64 `json:"error_rate"`
	Ejected       bool    `json:"ejected"`
}

func (h *Handler) buildWSMessage() wsMessage {
	var totalIF int64
	entries := make([]wsNodeEntry, len(h.nodes))
	for i, n := range h.nodes {
		cpu, inf, q, lat, _, errRate, _ := n.Metrics()
		totalIF += inf
		entries[i] = wsNodeEntry{
			ID:            n.NodeID,
			InFlight:      inf,
			CPUUtil:       cpu,
			QueueDepth:    q,
			LatencyEWMAMs: lat,
			ErrorRate:     errRate,
			Ejected:       n.IsEjected(),
		}
	}
	algoName := "unknown"
	if h.routerHolder != nil {
		algoName = h.routerHolder.Get().Name()
	}
	return wsMessage{
		TsMs:          time.Now().UnixMilli(),
		Algo:          algoName,
		PolicyVer:     h.policyVer.Load(),
		TotalInflight: totalIF,
		Nodes:         entries,
	}
}

// SetPolicyVersion updates the displayed policy version.
func (h *Handler) SetPolicyVersion(v uint64) {
	h.policyVer.Store(v)
}

// ---- Auth middleware ---- //

func (h *Handler) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.token != "" && r.Header.Get("X-Admin-Token") != h.token {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

// ---- JSON helpers ---- //

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
