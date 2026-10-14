// Package pool manages persistent keep-alive HTTP connection pools per backend.
// Tuned MaxIdleConnsPerHost, timeouts, and HTTP/2 where supported.
// Watches for socket exhaustion and TIME_WAIT growth (see docs/RUNBOOK.md).
//
// Phase 0: stub only. Full implementation in Phase 2.1.
package pool

import (
	"net"
	"net/http"
	"time"
)

// PoolConfig holds tuning parameters for the per-backend transport.
type PoolConfig struct {
	MaxIdleConnsPerHost int
	DialTimeout         time.Duration
	RequestTimeout      time.Duration
	IdleConnTimeout     time.Duration
	EnableHTTP2         bool
}

// DefaultPoolConfig returns conservative defaults.
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxIdleConnsPerHost: 100,
		DialTimeout:         2 * time.Second,
		RequestTimeout:      30 * time.Second,
		IdleConnTimeout:     90 * time.Second,
		EnableHTTP2:         true,
	}
}

// NewTransport creates an http.Transport tuned for reverse-proxy use.
// Each backend should get its own transport to avoid cross-node pool sharing.
func NewTransport(cfg PoolConfig) *http.Transport {
	dialer := &net.Dialer{
		Timeout:   cfg.DialTimeout,
		KeepAlive: 30 * time.Second,
	}
	t := &http.Transport{
		DialContext:           dialer.DialContext,
		MaxIdleConnsPerHost:   cfg.MaxIdleConnsPerHost,
		MaxConnsPerHost:       cfg.MaxIdleConnsPerHost * 2,
		IdleConnTimeout:       cfg.IdleConnTimeout,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DisableCompression:    false,
		ForceAttemptHTTP2:     cfg.EnableHTTP2,
	}
	return t
}
