// Package config loads mock-backend configuration from environment variables.
// All parameters have sensible defaults so the binary runs standalone without
// any environment set (useful for unit tests).
package config

import (
	"hash/fnv"
	"os"
	"strconv"
)

// NodeConfig holds the full runtime configuration for one mock backend node.
type NodeConfig struct {
	// Identity
	NodeID      string  // NODE_ID (default: "backend-unknown")
	ListenAddr  string  // LISTEN_ADDR (default: ":9000")
	SpeedFactor float64 // SPEED_FACTOR: multiplied into all service times (default: 1.0)
	Seed        uint64  // RNG seed for lognormal sampling (default: hash of NodeID)

	// Bounded worker-pool sizes per endpoint class.
	// Each class has its own semaphore; heavy classes are small so queues form.
	MaxWorkersCompute int // MAX_WORKERS_COMPUTE (default: 4)
	MaxWorkersIO      int // MAX_WORKERS_IO      (default: 10)
	MaxWorkersQuery   int // MAX_WORKERS_QUERY   (default: 8)
	MaxWorkersCRUD    int // MAX_WORKERS_CRUD    (default: 30)

	// Request timeout for proxied calls into the pool (milliseconds).
	QueueTimeoutMs int // QUEUE_TIMEOUT_MS (default: 5000)

	// CPU burn calibration for /compute endpoint.
	// Iterations of float64 work performed per payload byte.
	CPUIterationsPerByte int // CPU_ITERS_PER_BYTE (default: 300)
}

// Load reads configuration from the environment.
func Load() *NodeConfig {
	cfg := &NodeConfig{
		NodeID:               getEnv("NODE_ID", "backend-unknown"),
		ListenAddr:           getEnv("LISTEN_ADDR", ":9000"),
		SpeedFactor:          parseFloat("SPEED_FACTOR", 1.0),
		MaxWorkersCompute:    parseInt("MAX_WORKERS_COMPUTE", 4),
		MaxWorkersIO:         parseInt("MAX_WORKERS_IO", 10),
		MaxWorkersQuery:      parseInt("MAX_WORKERS_QUERY", 8),
		MaxWorkersCRUD:       parseInt("MAX_WORKERS_CRUD", 30),
		QueueTimeoutMs:       parseInt("QUEUE_TIMEOUT_MS", 5000),
		CPUIterationsPerByte: parseInt("CPU_ITERS_PER_BYTE", 300),
	}

	// Seed: explicit SEED env overrides; otherwise hash of NodeID for
	// deterministic, node-specific randomness without requiring an env var.
	if s := os.Getenv("SEED"); s != "" {
		if v, err := strconv.ParseUint(s, 10, 64); err == nil {
			cfg.Seed = v
		}
	} else {
		h := fnv.New64a()
		_, _ = h.Write([]byte(cfg.NodeID))
		cfg.Seed = h.Sum64()
	}

	return cfg
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseFloat(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}

func parseInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
