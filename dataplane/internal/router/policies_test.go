package router_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/synapse/dataplane/internal/router"
)

// makeNodes creates n equal-weight nodes for testing.
func makeNodes(n int) []*router.Node {
	nodes := make([]*router.Node, n)
	for i := range nodes {
		nodes[i] = router.NewNode(
			"node-"+string(rune('1'+i)),
			"http://localhost:900"+string(rune('1'+i)),
			1.0,
		)
	}
	return nodes
}

func dummyReq() *http.Request {
	return httptest.NewRequest(http.MethodGet, "/health", nil)
}

// ---- Round-Robin ----

func TestRoundRobinDistribution(t *testing.T) {
	nodes := makeNodes(3)
	rr := router.NewRoundRobin(nodes)

	counts := map[string]int{}
	for i := 0; i < 300; i++ {
		b := rr.Pick(dummyReq())
		if b == nil {
			t.Fatal("Pick returned nil")
		}
		counts[b.NodeID]++
	}
	// Each node should get ~100 ± 1
	for id, c := range counts {
		if c < 99 || c > 101 {
			t.Errorf("RR: node %s got %d/300 (want ~100)", id, c)
		}
	}
}

func TestRoundRobinEmpty(t *testing.T) {
	rr := router.NewRoundRobin(nil)
	if rr.Pick(dummyReq()) != nil {
		t.Error("expected nil for empty pool")
	}
}

func TestRoundRobinName(t *testing.T) {
	rr := router.NewRoundRobin(makeNodes(1))
	if rr.Name() != "round-robin" {
		t.Errorf("unexpected name: %q", rr.Name())
	}
}

// ---- Weighted Round-Robin ----

func TestWeightedRRDistribution(t *testing.T) {
	nodes := []*router.Node{
		router.NewNode("node-1", "http://localhost:9001", 2.0),
		router.NewNode("node-2", "http://localhost:9002", 1.0),
		router.NewNode("node-3", "http://localhost:9003", 1.0),
	}
	wrr := router.NewWeightedRoundRobin(nodes)

	counts := map[string]int{}
	total := 400
	for i := 0; i < total; i++ {
		b := wrr.Pick(dummyReq())
		if b == nil {
			t.Fatal("Pick returned nil")
		}
		counts[b.NodeID]++
	}
	// node-1 should get ~50%, others ~25%
	got0 := float64(counts[nodes[0].NodeID]) / float64(total)
	got1 := float64(counts[nodes[1].NodeID]) / float64(total)
	got2 := float64(counts[nodes[2].NodeID]) / float64(total)
	if got0 < 0.45 || got0 > 0.55 {
		t.Errorf("WRR: node-1 share=%.2f (want ~0.50)", got0)
	}
	if got1 < 0.20 || got1 > 0.30 {
		t.Errorf("WRR: node-2 share=%.2f (want ~0.25)", got1)
	}
	if got2 < 0.20 || got2 > 0.30 {
		t.Errorf("WRR: node-3 share=%.2f (want ~0.25)", got2)
	}
}

// ---- Least-Connections ----

func TestLeastConnections(t *testing.T) {
	nodes := makeNodes(3)
	lc := router.NewLeastConnections(nodes)

	// Artificially set in-flight
	nodes[0].InFlight.Store(5)
	nodes[1].InFlight.Store(1)
	nodes[2].InFlight.Store(3)

	b := lc.Pick(dummyReq())
	if b == nil {
		t.Fatal("Pick returned nil")
	}
	if b.NodeID != nodes[1].NodeID {
		t.Errorf("expected %s (least-conn=1), got %s", nodes[1].NodeID, b.NodeID)
	}
}

func TestLeastConnectionsEjected(t *testing.T) {
	nodes := makeNodes(2)
	lc := router.NewLeastConnections(nodes)

	// Eject node-1 by recording 5 consecutive 5xx
	for i := 0; i < 5; i++ {
		nodes[0].RecordStatus(503)
	}
	if !nodes[0].IsEjected() {
		t.Fatal("node should be ejected after 5 consecutive 5xx")
	}

	// All picks should go to node-2
	for i := 0; i < 20; i++ {
		b := lc.Pick(dummyReq())
		if b == nil || b.NodeID != nodes[1].NodeID {
			t.Errorf("expected routing to non-ejected node, got %v", b)
		}
	}
}

// ---- Power-of-Two ----

func TestPowerOfTwo(t *testing.T) {
	nodes := makeNodes(4)
	nodes[0].InFlight.Store(10)
	nodes[1].InFlight.Store(1)
	nodes[2].InFlight.Store(5)
	nodes[3].InFlight.Store(8)
	p2c := router.NewPowerOfTwo(nodes)

	// Over many picks, node-1 (inflight=1) should be picked most often.
	counts := map[string]int{}
	for i := 0; i < 1000; i++ {
		b := p2c.Pick(dummyReq())
		if b != nil {
			counts[b.NodeID]++
		}
	}
	if counts[nodes[1].NodeID] < 300 {
		t.Errorf("P2C: node-1 (least in-flight) expected >300/1000, got %d", counts[nodes[1].NodeID])
	}
}

// ---- HeuristicScore ----

func TestHeuristicScorePrefersLowerLatency(t *testing.T) {
	nodes := makeNodes(2)
	hs := router.NewHeuristicScore(nodes)

	nodes[0].UpdateMetrics(0.5, 10, 100.0, 90.0, 0.01) // high latency
	nodes[1].UpdateMetrics(0.2, 2, 5.0, 5.0, 0.0)      // low latency

	for i := 0; i < 20; i++ {
		b := hs.Pick(dummyReq())
		if b == nil {
			t.Fatal("nil pick")
		}
		if b.NodeID != nodes[1].NodeID {
			t.Errorf("heuristic should prefer node-1 (lower latency), got %s", b.NodeID)
		}
	}
}

// ---- Concurrency safety ----

func TestAllPoliciesConcurrentSafe(t *testing.T) {
	nodes := makeNodes(4)
	for i, n := range nodes {
		n.InFlight.Store(int64(i * 2))
		n.Weight = float64(i + 1)
	}
	policies := []router.Router{
		router.NewRoundRobin(nodes),
		router.NewWeightedRoundRobin(nodes),
		router.NewLeastConnections(nodes),
		router.NewPowerOfTwo(nodes),
		router.NewPeakEWMA(nodes),
		router.NewHeuristicScore(nodes),
		router.NewReactiveEject(nodes),
	}

	var wg sync.WaitGroup
	for _, pol := range policies {
		pol := pol
		for j := 0; j < 10; j++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for k := 0; k < 200; k++ {
					_ = pol.Pick(dummyReq())
				}
			}()
		}
	}
	wg.Wait()
}
