// Package admin provides the REST + WebSocket admin API for the Synapse proxy.
// Used by the React portal and CLI tools to:
//   - List backend nodes and their live metrics
//   - Switch the routing algorithm live
//   - Stream live metrics over WebSocket
//
// Phase 0: stub HTTP mux only. Full implementation in Phase 2.7.
package admin

import (
	"encoding/json"
	"net/http"
)

// Handler is the admin HTTP handler.
// Dependencies (router, policy holder, etc.) are injected in Phase 2.7.
type Handler struct{}

// NewHandler creates a new admin Handler.
func NewHandler() *Handler {
	return &Handler{}
}

// RegisterRoutes registers all admin routes on the given mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/admin/nodes", h.handleNodes)
	mux.HandleFunc("/admin/algorithm", h.handleAlgorithm)
	mux.HandleFunc("/admin/metrics", h.handleMetrics)
	mux.HandleFunc("/admin/ws", h.handleWS)
}

func (h *Handler) handleNodes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "stub", "phase": "0"})
}

func (h *Handler) handleAlgorithm(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "stub", "phase": "0"})
}

func (h *Handler) handleMetrics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "stub", "phase": "0"})
}

func (h *Handler) handleWS(w http.ResponseWriter, r *http.Request) {
	// WebSocket upgrade implemented in Phase 2.7
	http.Error(w, "WebSocket not yet implemented (Phase 0 stub)", http.StatusNotImplemented)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
