// Command backend is the Synapse mock backend node.
//
// Configuration via environment variables (see internal/config):
//
//	NODE_ID              = backend-1
//	SPEED_FACTOR         = 1.0
//	LISTEN_ADDR          = :9000
//	SEED                 = <auto from node_id hash>
//	MAX_WORKERS_COMPUTE  = 4
//	MAX_WORKERS_IO       = 10
//	MAX_WORKERS_QUERY    = 8
//	MAX_WORKERS_CRUD     = 30
//
// Endpoints:
//
//	GET  /health                → ~0.2 ms
//	GET,POST /crud/*            → ~5 ms
//	POST /query                 → ~40 ms
//	POST /compute               → ~80-800 ms (CPU burn)
//	POST /io                    → ~100 ms (sleep)
//	GET  /metrics               → JSON metrics snapshot
//	GET  /metrics/prometheus    → Prometheus text
//	POST /admin/fault           → inject fault (see fault.Config)
//	GET  /admin/fault           → get current fault
//	DELETE /admin/fault         → clear all faults
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/synapse/mockbackend/internal/config"
	"github.com/synapse/mockbackend/internal/fault"
	"github.com/synapse/mockbackend/internal/handler"
	"github.com/synapse/mockbackend/internal/metrics"
	"github.com/synapse/mockbackend/internal/pool"
	"github.com/synapse/mockbackend/internal/workload"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg := config.Load()

	slog.Info("mock backend starting",
		"node_id", cfg.NodeID,
		"listen", cfg.ListenAddr,
		"speed_factor", cfg.SpeedFactor,
		"seed", cfg.Seed,
	)

	// Workload model — seeded, deterministic
	model := workload.New(cfg.Seed, cfg.SpeedFactor, cfg.CPUIterationsPerByte)

	// Per-class worker pools
	pools := handler.Pools{
		Compute: pool.New(cfg.MaxWorkersCompute),
		IO:      pool.New(cfg.MaxWorkersIO),
		Query:   pool.New(cfg.MaxWorkersQuery),
		CRUD:    pool.New(cfg.MaxWorkersCRUD),
	}

	// Fault controller
	fc := fault.NewController()
	defer fc.Stop()

	// Metrics collector
	mc := metrics.New(cfg.NodeID, cfg.SpeedFactor, cfg.MaxWorkersCompute)

	// Periodic metrics window reset (every 60s)
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			mc.ResetWindow()
		}
	}()

	// HTTP server
	h := handler.New(model, fc, mc, pools)
	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      h,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		slog.Info("listening", "addr", cfg.ListenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	<-quit
	slog.Info("shutdown signal received")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("graceful shutdown error", "err", err)
	}
	slog.Info("backend stopped", "node_id", cfg.NodeID)
}
