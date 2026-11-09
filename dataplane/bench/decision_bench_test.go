// Package bench provides Go microbenchmarks for the Synapse proxy hot path.
//
// Measures per-decision cost: feature extraction + router.Pick() + telemetry emit.
// Target (Phase 2.8): 0 allocs/op on the hot path; < 5 µs per routing decision.
//
// Run with:
//
//	go test -bench=. -benchmem -count=5 ./bench/
//	go test -bench=BenchmarkDecision -benchmem -cpuprofile=cpu.prof ./bench/
package bench

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/synapse/dataplane/internal/features"
	"github.com/synapse/dataplane/internal/router"
	"github.com/synapse/dataplane/internal/telemetry"
)

var (
	benchNodes     []*router.Node
	benchExtractor *features.Extractor
	benchEmitter   *telemetry.Emitter
	benchReq       *http.Request
)

func init() {
	benchNodes = []*router.Node{
		router.NewNode("backend-1", "http://localhost:9001", 1.0),
		router.NewNode("backend-2", "http://localhost:9002", 1.0),
		router.NewNode("backend-3", "http://localhost:9003", 0.6),
		router.NewNode("backend-4", "http://localhost:9004", 1.5),
	}
	// Simulate typical system state.
	benchNodes[0].UpdateMetrics(0.4, 10, 25.0, 23.0, 0.001)
	benchNodes[1].UpdateMetrics(0.6, 15, 40.0, 38.0, 0.002)
	benchNodes[2].UpdateMetrics(0.7, 8, 60.0, 55.0, 0.01)
	benchNodes[3].UpdateMetrics(0.3, 5, 15.0, 14.0, 0.0)

	benchExtractor = features.NewExtractor(map[string]features.EndpointClass{
		"/health":  features.ClassHealth,
		"/crud/":   features.ClassLightCRUD,
		"/query":   features.ClassMediumQuery,
		"/compute": features.ClassHeavyCompute,
		"/io":      features.ClassIOBound,
	})
	benchEmitter = telemetry.NewEmitter(4096)
	benchReq = httptest.NewRequest(http.MethodPost, "/query", nil)
	benchReq.Header.Set("Content-Length", "1024")
	benchReq.ContentLength = 1024
}

// BenchmarkFeatureExtract measures feature extraction alone (O(1), no alloc target).
func BenchmarkFeatureExtract(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = benchExtractor.ExtractRequest(benchReq)
	}
}

// BenchmarkRoundRobin measures RR routing decision.
func BenchmarkRoundRobin(b *testing.B) {
	r := router.NewRoundRobin(benchNodes)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.Pick(benchReq)
	}
}

// BenchmarkLeastConnections measures LC routing decision (O(N)).
func BenchmarkLeastConnections(b *testing.B) {
	r := router.NewLeastConnections(benchNodes)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.Pick(benchReq)
	}
}

// BenchmarkPowerOfTwo measures P2C routing decision.
func BenchmarkPowerOfTwo(b *testing.B) {
	r := router.NewPowerOfTwo(benchNodes)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.Pick(benchReq)
	}
}

// BenchmarkPeakEWMA measures Peak-EWMA routing decision.
func BenchmarkPeakEWMA(b *testing.B) {
	r := router.NewPeakEWMA(benchNodes)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.Pick(benchReq)
	}
}

// BenchmarkHeuristicScore measures HeuristicScore routing (paper formula).
func BenchmarkHeuristicScore(b *testing.B) {
	r := router.NewHeuristicScore(benchNodes)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = r.Pick(benchReq)
	}
}

// BenchmarkDecisionPipeline measures the full hot-path:
// feature extract + pick + telemetry emit (non-blocking channel send).
func BenchmarkDecisionPipeline(b *testing.B) {
	r := router.NewLeastConnections(benchNodes)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		reqFeatures := benchExtractor.ExtractRequest(benchReq)
		backend := r.Pick(benchReq)
		if backend != nil {
			benchEmitter.Emit(telemetry.Event{
				EndpointClass: features.ClassName(reqFeatures.EndpointClass),
				NodeID:        backend.NodeID,
				AlgoName:      r.Name(),
				TExecMs:       1.5,
			})
		}
	}
}

// BenchmarkDecisionParallel measures concurrent routing decisions.
func BenchmarkDecisionParallel(b *testing.B) {
	r := router.NewLeastConnections(benchNodes)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = r.Pick(benchReq)
		}
	})
}

