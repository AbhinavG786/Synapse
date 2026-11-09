package proxy_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/synapse/dataplane/internal/features"
	"github.com/synapse/dataplane/internal/pool"
	"github.com/synapse/dataplane/internal/proxy"
	"github.com/synapse/dataplane/internal/router"
	"github.com/synapse/dataplane/internal/telemetry"
)

// newTestDispatcher builds a Dispatcher pointing at a fake backend server.
func newTestDispatcher(t *testing.T, backendHandler http.Handler) (*proxy.Dispatcher, *httptest.Server) {
	t.Helper()
	backend := httptest.NewServer(backendHandler)
	t.Cleanup(backend.Close)

	node := router.NewNode("test-node", backend.URL, 1.0)
	nodes := []*router.Node{node}

	extractor := features.NewExtractor(map[string]features.EndpointClass{
		"/health":  features.ClassHealth,
		"/crud/":   features.ClassLightCRUD,
		"/query":   features.ClassMediumQuery,
		"/compute": features.ClassHeavyCompute,
		"/io":      features.ClassIOBound,
	})
	emitter := telemetry.NewEmitter(64)
	rr := router.NewRoundRobin(nodes)

	disp, err := proxy.New(proxy.Config{
		Router:     rr,
		Nodes:      nodes,
		Extractor:  extractor,
		Emitter:    emitter,
		PoolConfig: pool.DefaultPoolConfig(),
		RunID:      "test",
		ReqTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("proxy.New: %v", err)
	}
	return disp, backend
}

// TestProxyForwards verifies a basic proxied request reaches the backend.
func TestProxyForwards(t *testing.T) {
	disp, _ := newTestDispatcher(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	disp.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestProxyInFlightCounter verifies in-flight counter increments and decrements.
func TestProxyInFlightCounter(t *testing.T) {
	started := make(chan struct{})
	unblock := make(chan struct{})

	disp, _ := newTestDispatcher(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-unblock
		w.WriteHeader(http.StatusOK)
	}))

	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		rec := httptest.NewRecorder()
		disp.ServeHTTP(rec, req)
	}()

	<-started
	if got := disp.TotalInFlight(); got != 1 {
		t.Errorf("expected in-flight=1, got %d", got)
	}
	close(unblock)
	<-done
	if got := disp.TotalInFlight(); got != 0 {
		t.Errorf("expected in-flight=0 after completion, got %d", got)
	}
}

// TestProxyNoBackend verifies 503 when no backends exist.
func TestProxyNoBackend(t *testing.T) {
	extractor := features.NewExtractor(nil)
	emitter := telemetry.NewEmitter(64)
	rr := router.NewRoundRobin(nil) // empty node list

	disp, err := proxy.New(proxy.Config{
		Router:     rr,
		Nodes:      nil,
		Extractor:  extractor,
		Emitter:    emitter,
		PoolConfig: pool.DefaultPoolConfig(),
		RunID:      "test",
		ReqTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("proxy.New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	disp.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}

// TestProxyXSynapseNodeHeader verifies the X-Synapse-Node response header is set.
func TestProxyXSynapseNodeHeader(t *testing.T) {
	disp, _ := newTestDispatcher(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	disp.ServeHTTP(rec, req)

	if hdr := rec.Header().Get("X-Synapse-Node"); hdr == "" {
		t.Error("expected X-Synapse-Node response header to be set")
	}
}

// TestProxyTelemetryEmit verifies telemetry is emitted after a request.
func TestProxyTelemetryEmit(t *testing.T) {
	emitter := telemetry.NewEmitter(64)
	node := router.NewNode("test-node", "", 1.0) // address will be set below

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)
	node = router.NewNode("test-node", backend.URL, 1.0)

	extractor := features.NewExtractor(nil)
	rr := router.NewRoundRobin([]*router.Node{node})
	disp, err := proxy.New(proxy.Config{
		Router:     rr,
		Nodes:      []*router.Node{node},
		Extractor:  extractor,
		Emitter:    emitter,
		PoolConfig: pool.DefaultPoolConfig(),
		RunID:      "test",
		ReqTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/query", nil)
	rec := httptest.NewRecorder()
	disp.ServeHTTP(rec, req)

	// Check a telemetry event was queued.
	select {
	case ev := <-emitter.C():
		if ev.NodeID != "test-node" {
			t.Errorf("expected NodeID=test-node, got %q", ev.NodeID)
		}
		if ev.TExecMs < 0 {
			t.Errorf("expected non-negative TExecMs, got %.2f", ev.TExecMs)
		}
	case <-time.After(time.Second):
		t.Error("no telemetry event received within 1s")
	}
}

