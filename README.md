# Synapse

**A Contextual Multi-Armed Bandit Approach for Layer-7 Predictive API Load Balancing in Microservice Meshes**

---

## Overview

Synapse is a Layer-7 reverse proxy that uses a **LinUCB contextual bandit** to route API requests to backend nodes based on request context (endpoint class, payload size) and real-time system metrics (CPU, queue depth, latency EWMA). The goal is to reduce tail latency (P95/P99) on heterogeneous workloads versus classical baselines (Round-Robin, Least-Connections, Power-of-Two-Choices).

The project produces:
- **(A)** An engineering artifact: a Go reverse proxy + Python ML control plane + React observability portal.
- **(B)** A research result: honest, reproducible experimental comparisons with 95% bootstrap CIs.

---

## Quick Start

### Prerequisites

- Go 1.22+
- Python 3.11+
- Docker + Docker Compose v2

### Start the stack

```bash
# Clone and enter repo
git clone <repo-url> synapse && cd synapse

# Start all services (Redis, TimescaleDB, proxy stub, control plane stub)
make up

# Verify proxy health
curl http://localhost:8080/healthz
# -> {"status":"ok","phase":"0-stub"}

# Verify control plane health
curl http://localhost:9080/healthz
# -> {"status":"ok","phase":"0-stub"}
```

### Run tests

```bash
make test        # Go (race detector) + Python (pytest)
make lint        # go vet/staticcheck + ruff/mypy
```

### Stop

```bash
make down        # stops all containers and removes volumes
```

---

## Repository Layout

```
synapse/
├── proto/              synapse.proto (PolicySnapshot, TelemetryEvent, Admin RPC)
├── dataplane/          Go module: proxy binary + internal packages
│   ├── cmd/proxy/      main.go (entry point)
│   └── internal/
│       ├── router/     Router interface (all policies implement this)
│       ├── policy/     Atomic snapshot holder
│       ├── features/   Context vector extractor
│       ├── pool/       Connection pool per backend
│       ├── telemetry/  Bounded event emitter
│       ├── admin/      REST + WebSocket admin API
│       ├── fallback/   Staleness TTL fallback guard
│       └── config/     Configuration loader
├── mocks/backend/      Mock backend service (Phase 1)
├── controlplane/       Python package: bandit, trainer, API, OPE tools
├── loadgen/            Open-loop load generator (Phase 4)
├── infra/              docker-compose.yml, init.sql, redis.conf
├── experiments/        Scenario configs + analysis scripts (Phase 4+)
├── results/            Raw logs (gitignored), tables, figures (Phase 6)
├── portal/             React chaos + observability portal (Phase 8)
├── docs/               ARCHITECTURE, MODEL, DECISIONS, CLAIMS, EVALUATION,
│                       THREATS_TO_VALIDITY, RUNBOOK
├── Makefile
└── .github/workflows/ci.yml
```

---

## Architecture

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the full three-plane design.

**Three planes:**
1. **Data Plane (Go)**: Reverse proxy with atomic policy snapshot, bounded telemetry channel, per-backend connection pools.
2. **Control Plane (Python)**: LinUCB trainer, gRPC policy publisher, FastAPI portal API.
3. **UI Plane (React)**: Topology mesh, chaos panel, bandit introspection (Phase 8).

---

## Policies Implemented

| ID | Algorithm | Status |
|----|-----------|--------|
| B1 | Round-Robin | Phase 2 |
| B2 | Weighted Round-Robin | Phase 2 |
| B3 | Least-Connections | Phase 2 |
| B4 | Power-of-Two-Choices | Phase 2 |
| B5 | Peak-EWMA | Phase 2 |
| B6 | Heuristic-Score | Phase 2/5 |
| B7 | Reactive-Eject (consecutive 5xx) | Phase 2 |
| P1 | **LinUCB** (the proposal) | Phase 5 |
| P2a | Epsilon-Greedy linear | Phase 5 |
| P2b | Thompson Sampling linear | Phase 5 |

---

## Research Claims

See [docs/CLAIMS.md](docs/CLAIMS.md) for the full claims ledger (C1–C11).

> **Integrity note**: All results come from actual runs. Targets in the synopsis are hypotheses, not facts (R-1, R-2). If LinUCB shows no gain at low load, that is reported.

---

## Development Phases

| Phase | Description | Status |
|-------|-------------|--------|
| 0 | Scaffolding (this phase) | ✅ Done |
| 1 | Mock backends + fault injection | ⏳ Pending |
| 2 | Go data plane + classical baselines | ⏳ Pending |
| 3 | Telemetry pipeline + storage | ⏳ Pending |
| 4 | Load-test harness + baseline study | ⏳ Pending |
| 5 | ML control plane (Python) | ⏳ Pending |
| 6 | Full evaluation + ablations | ⏳ Pending |
| 7 | Hardening + claims audit | ⏳ Pending |
| 8 | React portal | ⏳ Pending |
| 9 | Docs + paper-ready output | ⏳ Pending |

---

## License

Academic use only. Not for commercial distribution.
