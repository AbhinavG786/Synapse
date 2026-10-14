// Package config loads and validates Synapse proxy configuration.
// Phase 0: struct definitions and ENV-based loader stub.
// Full YAML + Postgres-backed config in Phase 2.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// BackendNode is a single upstream backend in the static config.
type BackendNode struct {
	NodeID  string `json:"node_id"`
	Address string `json:"address"`
	Weight  float64 `json:"weight"` // for WRR; 1.0 default
}

// RouteClass maps a URL path prefix to an endpoint class name.
type RouteClass struct {
	Prefix string `json:"prefix"`
	Class  string `json:"class"` // "health","light_crud","medium_query","heavy_compute","io_bound"
}

// ProxyConfig holds the full configuration for the data-plane proxy.
type ProxyConfig struct {
	// ListenAddr is the address the proxy listens on, e.g. ":8080".
	ListenAddr string

	// Backends is the initial set of upstream nodes.
	// Updated at runtime via Admin API in Phase 2.7.
	Backends []BackendNode

	// Routes maps URL prefixes to endpoint classes.
	Routes []RouteClass

	// Policy
	DefaultAlgo     string        // e.g. "least-conn"
	SnapshotTTL     time.Duration // staleness TTL before fallback
	FallbackAlgo    string        // algo to use when snapshot is stale

	// Telemetry
	TelemetryChannelSize int // bounded drop channel (R-4)
	TelemetryBatchSize   int

	// Timeouts
	DialTimeout     time.Duration
	RequestTimeout  time.Duration
	IdleConnTimeout time.Duration
	MaxIdleConnsPerHost int

	// Observability
	MetricsAddr string // Prometheus scrape endpoint
	PProfAddr   string

	// Connections to control plane and storage
	ControlPlaneAddr string // gRPC address
	RedisAddr        string
	PostgresDSN      string
	TimescaleDSN     string

	// Run identification
	RunID string
}

// Load reads configuration from environment variables.
// Missing variables fall back to sensible defaults.
// Phase 0: ENV only. Phase 2 will add YAML overlay.
func Load() (*ProxyConfig, error) {
	cfg := &ProxyConfig{
		ListenAddr:           getEnv("SYNAPSE_LISTEN", ":8080"),
		DefaultAlgo:          getEnv("SYNAPSE_DEFAULT_ALGO", "least-conn"),
		FallbackAlgo:         getEnv("SYNAPSE_FALLBACK_ALGO", "least-conn"),
		ControlPlaneAddr:     getEnv("SYNAPSE_CP_ADDR", "localhost:9090"),
		RedisAddr:            getEnv("REDIS_ADDR", "localhost:6379"),
		PostgresDSN:          getEnv("POSTGRES_DSN", ""),
		TimescaleDSN:         getEnv("TIMESCALE_DSN", ""),
		MetricsAddr:          getEnv("SYNAPSE_METRICS_ADDR", ":9091"),
		PProfAddr:            getEnv("SYNAPSE_PPROF_ADDR", ":6060"),
		RunID:                getEnv("SYNAPSE_RUN_ID", "local"),
		SnapshotTTL:          parseDuration("SYNAPSE_SNAPSHOT_TTL", 30*time.Second),
		TelemetryChannelSize: parseInt("SYNAPSE_TELEMETRY_CHAN", 4096),
		TelemetryBatchSize:   parseInt("SYNAPSE_TELEMETRY_BATCH", 64),
		DialTimeout:          parseDuration("SYNAPSE_DIAL_TIMEOUT", 2*time.Second),
		RequestTimeout:       parseDuration("SYNAPSE_REQUEST_TIMEOUT", 30*time.Second),
		IdleConnTimeout:      parseDuration("SYNAPSE_IDLE_CONN_TIMEOUT", 90*time.Second),
		MaxIdleConnsPerHost:  parseInt("SYNAPSE_MAX_IDLE_CONNS", 100),
	}

	if cfg.ListenAddr == "" {
		return nil, fmt.Errorf("SYNAPSE_LISTEN must not be empty")
	}
	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
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
