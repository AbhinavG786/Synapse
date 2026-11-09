// Command proxy is the Synapse Layer-7 reverse proxy.
//
// Configuration is read from environment variables (see internal/config).
// Backends are specified as a comma-separated SYNAPSE_BACKENDS env var in the
// form "id=http://addr,id2=http://addr2,...". Static weights via
// SYNAPSE_WEIGHTS="id=1.0,id2=2.0,...".
//
// Endpoints exposed by the proxy:
//
//	:<SYNAPSE_LISTEN>         → reverse proxy (default :8080)
//	:<SYNAPSE_METRICS_ADDR>   → Prometheus scrape (default :9091)
//	/admin/*                  → Admin REST + WebSocket
//	/healthz                  → proxy health
//
// Graceful shutdown on SIGINT/SIGTERM with 10s drain timeout.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/synapse/dataplane/internal/admin"
	"github.com/synapse/dataplane/internal/config"
	"github.com/synapse/dataplane/internal/features"
	"github.com/synapse/dataplane/internal/metrics"
	"github.com/synapse/dataplane/internal/nodepoller"
	"github.com/synapse/dataplane/internal/pool"
	"github.com/synapse/dataplane/internal/proxy"
	"github.com/synapse/dataplane/internal/router"
	"github.com/synapse/dataplane/internal/telemetry"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("config load failed", "err", err)
		os.Exit(1)
	}

	logger.Info("synapse proxy starting",
		"listen", cfg.ListenAddr,
		"algo", cfg.DefaultAlgo,
		"run_id", cfg.RunID,
	)

	// ---- Build nodes from config ---- //
	nodes := buildNodes(cfg)
	if len(nodes) == 0 {
		logger.Warn("no backend nodes configured; proxy will return 503 for all requests")
	}

	// ---- Build initial router ---- //
	algoFactory := makeAlgoFactory()
	initialRouter, err := algoFactory(cfg.DefaultAlgo, nodes)
	if err != nil {
		logger.Error("failed to build router", "algo", cfg.DefaultAlgo, "err", err)
		os.Exit(1)
	}
	routerHolder := admin.NewRouterHolder(initialRouter)

	// A thin delegating router that always reads from routerHolder (for live swap).
	delegating := &delegatingRouter{holder: routerHolder}

	// ---- Telemetry emitter ---- //
	emitter := telemetry.NewEmitter(cfg.TelemetryChannelSize)
	stopTelLog := make(chan struct{})
	go emitter.LogDrops(30*time.Second, stopTelLog)

	// ---- Feature extractor ---- //
	extractor := features.NewExtractor(buildRouteTable(cfg))

	// ---- Connection pool config ---- //
	poolCfg := pool.PoolConfig{
		MaxIdleConnsPerHost: cfg.MaxIdleConnsPerHost,
		DialTimeout:         cfg.DialTimeout,
		RequestTimeout:      cfg.RequestTimeout,
		IdleConnTimeout:     cfg.IdleConnTimeout,
		EnableHTTP2:         true,
	}

	// ---- Dispatcher ---- //
	dispatcher, err := proxy.New(proxy.Config{
		Router:     delegating,
		Nodes:      nodes,
		Extractor:  extractor,
		Emitter:    emitter,
		PoolConfig: poolCfg,
		RunID:      cfg.RunID,
		ReqTimeout: cfg.RequestTimeout,
		Logger:     logger,
	})
	if err != nil {
		logger.Error("failed to build dispatcher", "err", err)
		os.Exit(1)
	}

	// ---- Prometheus metrics ---- //
	metricsReg := metrics.New()

	// ---- Admin handler ---- //
	adminHandler := admin.New(nodes, routerHolder, algoFactory, logger)

	// ---- Node metrics poller ---- //
	pollerCtx, pollerCancel := context.WithCancel(context.Background())
	poller := nodepoller.New(nodes, 2*time.Second, logger)
	go poller.Run(pollerCtx)

	// ---- Main proxy mux ---- //
	mainMux := http.NewServeMux()
	mainMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","component":"synapse-proxy"}`))
	})
	adminHandler.Register(mainMux)
	mainMux.Handle("/", dispatcher)

	mainSrv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      mainMux,
		ReadTimeout:  cfg.RequestTimeout + 5*time.Second,
		WriteTimeout: cfg.RequestTimeout + 5*time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// ---- Metrics server ---- //
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", metricsReg.Handler())
	metricsMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	metricsSrv := &http.Server{
		Addr:         cfg.MetricsAddr,
		Handler:      metricsMux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	// ---- Start servers ---- //
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		logger.Info("proxy listening", "addr", cfg.ListenAddr)
		if err := mainSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("proxy server error", "err", err)
			os.Exit(1)
		}
	}()

	go func() {
		logger.Info("metrics listening", "addr", cfg.MetricsAddr)
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Warn("metrics server error", "err", err)
		}
	}()

	<-quit
	logger.Info("shutdown signal received — draining")

	pollerCancel()
	close(stopTelLog)

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()

	_ = mainSrv.Shutdown(shutCtx)
	_ = metricsSrv.Shutdown(shutCtx)

	logger.Info("proxy stopped")
}

// ---- Helpers ---- //

// delegatingRouter is a Router that always delegates to the RouterHolder.
// This lets the Admin API swap the algorithm without touching the Dispatcher.
type delegatingRouter struct {
	holder *admin.RouterHolder
}

func (d *delegatingRouter) Pick(r *http.Request) *router.Backend {
	return d.holder.Get().Pick(r)
}

func (d *delegatingRouter) Name() string {
	return d.holder.Get().Name()
}

// buildNodes constructs Node slice from config backends.
func buildNodes(cfg *config.ProxyConfig) []*router.Node {
	nodes := make([]*router.Node, 0, len(cfg.Backends))
	for _, b := range cfg.Backends {
		w := b.Weight
		if w <= 0 {
			w = 1.0
		}
		nodes = append(nodes, router.NewNode(b.NodeID, b.Address, w))
	}
	// If no static config, try SYNAPSE_BACKENDS env.
	if len(nodes) == 0 {
		if raw := os.Getenv("SYNAPSE_BACKENDS"); raw != "" {
			for _, part := range strings.Split(raw, ",") {
				kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
				if len(kv) == 2 {
					nodes = append(nodes, router.NewNode(kv[0], kv[1], 1.0))
				}
			}
		}
	}
	return nodes
}

// buildRouteTable builds the endpoint-class prefix map from config.
func buildRouteTable(cfg *config.ProxyConfig) map[string]features.EndpointClass {
	table := map[string]features.EndpointClass{
		"/health":  features.ClassHealth,
		"/healthz": features.ClassHealth,
		"/crud/":   features.ClassLightCRUD,
		"/query":   features.ClassMediumQuery,
		"/compute": features.ClassHeavyCompute,
		"/io":      features.ClassIOBound,
	}
	for _, rc := range cfg.Routes {
		switch rc.Class {
		case "health":
			table[rc.Prefix] = features.ClassHealth
		case "light_crud":
			table[rc.Prefix] = features.ClassLightCRUD
		case "medium_query":
			table[rc.Prefix] = features.ClassMediumQuery
		case "heavy_compute":
			table[rc.Prefix] = features.ClassHeavyCompute
		case "io_bound":
			table[rc.Prefix] = features.ClassIOBound
		}
	}
	return table
}

// makeAlgoFactory returns a function that constructs a Router by name.
func makeAlgoFactory() func(name string, nodes []*router.Node) (router.Router, error) {
	return func(name string, nodes []*router.Node) (router.Router, error) {
		switch name {
		case "round-robin", "rr":
			return router.NewRoundRobin(nodes), nil
		case "weighted-round-robin", "wrr":
			return router.NewWeightedRoundRobin(nodes), nil
		case "least-conn", "lc":
			return router.NewLeastConnections(nodes), nil
		case "power-of-two", "p2c":
			return router.NewPowerOfTwo(nodes), nil
		case "peak-ewma":
			return router.NewPeakEWMA(nodes), nil
		case "heuristic-score", "heuristic":
			return router.NewHeuristicScore(nodes), nil
		case "reactive-eject", "reactive":
			return router.NewReactiveEject(nodes), nil
		default:
			// Default to least-conn (safe baseline, R-3 fallback).
			return router.NewLeastConnections(nodes), nil
		}
	}
}
