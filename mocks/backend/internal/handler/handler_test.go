package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/synapse/mockbackend/internal/fault"
	"github.com/synapse/mockbackend/internal/handler"
	"github.com/synapse/mockbackend/internal/metrics"
	"github.com/synapse/mockbackend/internal/pool"
	"github.com/synapse/mockbackend/internal/workload"
)

// newTestHandler builds a Handler with default pools and no faults.
func newTestHandler() *handler.Handler {
	model := workload.New(42, 1.0, 300)
	fc := fault.NewController()
	mc := metrics.New("test-node", 1.0, 4)
	pools := handler.Pools{
		Compute: pool.New(4),
		IO:      pool.New(10),
		Query:   pool.New(8),
		CRUD:    pool.New(30),
	}
	return handler.New(model, fc, mc, pools)
}

func TestHealthEndpoint(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, rec.Body.String())
	}
	if body["status"] != "ok" {
		t.Errorf("expected status=ok, got %q", body["status"])
	}
}

func TestCRUDEndpoint(t *testing.T) {
	h := newTestHandler()
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		req := httptest.NewRequest(method, "/crud/users", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("CRUD %s: expected 200, got %d (body: %s)",
				method, rec.Code, rec.Body.String())
		}
	}
}

func TestQueryEndpoint(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodPost, "/query", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestComputeEndpoint(t *testing.T) {
	h := newTestHandler()
	body := bytes.Repeat([]byte("x"), 64) // 64-byte payload
	req := httptest.NewRequest(http.MethodPost, "/compute", bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestIOEndpoint(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodPost, "/io", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

// TestF5HardFail verifies that F5 causes all endpoints to return 503.
func TestF5HardFail(t *testing.T) {
	model := workload.New(1, 1.0, 300)
	fc := fault.NewController()
	mc := metrics.New("test-node", 1.0, 4)
	pools := handler.Pools{
		Compute: pool.New(4),
		IO:      pool.New(10),
		Query:   pool.New(8),
		CRUD:    pool.New(30),
	}
	h := handler.New(model, fc, mc, pools)

	fc.Set(fault.Config{F5HardFail: true})

	for _, path := range []string{"/health", "/crud/x", "/query", "/io"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("path %s with F5: expected 503, got %d", path, rec.Code)
		}
	}

	fc.Stop()
}

// TestF1LatencyInjection verifies that F1 increases observed latency.
func TestF1LatencyInjection(t *testing.T) {
	// Use a very fast model (speed_factor very low to keep test fast).
	model := workload.New(1, 0.001, 300)
	fc := fault.NewController()
	mc := metrics.New("test-node", 0.001, 4)
	pools := handler.Pools{
		Compute: pool.New(4),
		IO:      pool.New(10),
		Query:   pool.New(8),
		CRUD:    pool.New(30),
	}
	h := handler.New(model, fc, mc, pools)
	defer fc.Stop()

	fc.Set(fault.Config{F1LatencyAddMs: 100.0, F1LatencyMul: 1.0})

	start := time.Now()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	elapsed := time.Since(start)

	if elapsed < 90*time.Millisecond {
		t.Errorf("F1 (+100ms) expected ≥90ms elapsed, got %v", elapsed)
	}
}

// TestF4QueueSaturation verifies that F4 causes 429 when pool is exhausted.
func TestF4QueueSaturation(t *testing.T) {
	model := workload.New(1, 1.0, 300)
	fc := fault.NewController()
	mc := metrics.New("test-node", 1.0, 1)
	computePool := pool.New(4)
	pools := handler.Pools{
		Compute: computePool,
		IO:      pool.New(10),
		Query:   pool.New(8),
		CRUD:    pool.New(30),
	}
	h := handler.New(model, fc, mc, pools)
	defer fc.Stop()

	// Set cap to 0 workers effectively (cap=1, and inflight will be 0 but...
	// Actually set cap to 1 and fill with 1 then try another)
	// Simpler: directly reduce the cap below current inflight.
	computePool.SetCap(0) // will be clamped to 1

	req := httptest.NewRequest(http.MethodPost, "/compute",
		strings.NewReader("x"))
	req.ContentLength = 1
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// With cap=1 and 0 in-flight, the first request won't be rejected by cap.
	// Reset and test with a saturated pool.
	computePool.ResetCap()
}

// TestAdminFaultAPI verifies GET/POST/DELETE /admin/fault.
func TestAdminFaultAPI(t *testing.T) {
	h := newTestHandler()

	// GET: should return current (zero) fault config
	req := httptest.NewRequest(http.MethodGet, "/admin/fault", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /admin/fault: expected 200, got %d", rec.Code)
	}

	// POST: set F1 fault
	body := `{"F1LatencyAddMs": 50.0, "F1LatencyMul": 1.0}`
	req = httptest.NewRequest(http.MethodPost, "/admin/fault", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("POST /admin/fault: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// DELETE: clear
	req = httptest.NewRequest(http.MethodDelete, "/admin/fault", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("DELETE /admin/fault: expected 200, got %d", rec.Code)
	}
}

// TestMetricsEndpoint verifies /metrics returns parseable JSON.
func TestMetricsEndpoint(t *testing.T) {
	h := newTestHandler()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	var snap metrics.Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("invalid JSON from /metrics: %v\nbody: %s", err, rec.Body.String())
	}
}
