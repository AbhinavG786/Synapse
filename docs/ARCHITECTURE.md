# docs/ARCHITECTURE.md — System Architecture

## Overview

Synapse is a Layer-7 reverse proxy with an out-of-band ML control plane.
Three planes:

```
CLIENT
  │
  ▼
[Data Plane — Go]
  Listener → Feature Extractor → Router (atomic policy snapshot)
           → Dispatcher (connection pool) → Backend Node
           → Response to client + async TelemetryEvent emit
  │
  │ gRPC server-stream (PolicySnapshot, ~500 ms)
  │ Redis Streams (TelemetryEvents)
  ▼
[Control Plane — Python]
  Telemetry Consumer → LinUCB Trainer → Policy Publisher
  FastAPI REST + WebSocket → React Portal
  │
  ▼
[UI Plane — React] (Phase 8)
  Topology / Chaos Panel / Bandit Introspection / Policy Console
```

## Data Plane (Go)

- **Reverse proxy**: `net/http` + `httputil.ReverseProxy`. fasthttp variant is
  a stretch goal (Phase 2.9), benchmarked against standard library.
- **Router interface**: all 10 policies implement the same `router.Router`
  interface. Algorithm switch is live (Admin API) and takes effect on the next
  request.
- **Policy snapshot**: `atomic.Pointer[policy.Snapshot]` — one pointer load on
  the hot path, zero locks, zero allocation (R-4).
- **Telemetry**: bounded channel + background batcher. Full event never blocks
  a request (R-4).
- **Fallback**: staleness TTL → Least-Connections (R-3).

## Control Plane (Python)

- **Trainer**: consumes telemetry from Redis Streams, updates LinUCB arms
  (~500 ms cadence), publishes snapshot via gRPC stream.
- **OPE tools**: replay, IPS, SNIPS, doubly-robust with logged propensities.
- **Simulator**: discrete-event queueing model calibrated from Phase 1/4 data.

## Storage

| Store | Purpose | Schema |
|-------|---------|--------|
| Redis | Hot policy copy, telemetry streams, pub/sub | Key-value + Streams |
| TimescaleDB | `events.request_events` hypertable | Time-partitioned, 1-day chunks |
| PostgreSQL | `config.*`: nodes, routes, policies, runs | Relational |

## Ports

| Service | Port | Protocol |
|---------|------|----------|
| Proxy (HTTP) | 8080 | HTTP/1.1, HTTP/2 |
| Proxy (Metrics) | 9091 | Prometheus scrape |
| Control Plane (REST) | 9080 | HTTP/1.1 |
| Control Plane (gRPC) | 9090 | gRPC/HTTP2 |
| Redis | 6379 | Redis protocol |
| TimescaleDB | 5432 | PostgreSQL wire |
| Grafana | 3000 | HTTP (observability profile) |

## Context Vector Layout (feature_spec_version = 1, d = 16)

See `docs/MODEL.md` for full specification and `docs/DECISIONS.md` D-003.

| Index | Feature | Notes |
|-------|---------|-------|
| 0–5 | Endpoint class one-hot | 6 classes |
| 6–10 | Method class one-hot | 5 classes |
| 11 | log1p(payload_bytes) | normalised |
| 12 | auth_tier | 0/1/2 |
| 13 | cpu_util | [0,1] |
| 14 | in_flight | normalised |
| 15 | queue_depth | normalised |
