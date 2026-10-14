# docs/DECISIONS.md — Architecture Decision Record

All significant decisions are logged here: date, decision, alternatives considered,
and reason. Required by R-12: never silently change reward definition, feature set,
or evaluation protocol.

Format: each entry starts with a level-2 heading `## D-NNN: Title (YYYY-MM-DD)`.

---

## D-001: gRPC Transport for Policy Snapshots (2026-10-14)

**Decision:** Use gRPC server-streaming (PolicyService.StreamPolicy) as the
primary transport from the control plane to the proxy for policy snapshots.
Redis is used as a hot backup copy and for pub/sub to the React portal.

**Alternatives considered:**
- Redis Pub/Sub only: simpler, but no back-pressure and no structured typing.
- HTTP polling: simpler but adds latency and isn't push-based.
- gRPC only: chosen as primary. Redis retained for the portal and as a fallback.

**Reason:** gRPC gives structured, versioned, strongly-typed messages and natural
server-push semantics. The proxy reconnects automatically on stream failure and
uses the last-good snapshot (R-3).

---

## D-002: Telemetry Transport (2026-10-14)

**Decision:** Phase 0/2 — proxy emits to a bounded in-process channel.
Phase 3 — batcher writes to Redis Streams (XADD). Control plane reads
via XREAD. Optional gRPC client-stream (TelemetryService.StreamTelemetry)
added as an alternative if Redis becomes a bottleneck.

**Reason:** Redis Streams provide fan-in from multiple proxy instances,
replay capability, and persistence — useful for OPE (Section 6.7).

---

## D-003: Feature Dimension d = 16 (feature_spec_version = 1) (2026-10-14)

**Decision:** Initial context vector dimension is d = 16 (see features.go layout).
To be confirmed/adjusted in Phase 2.3 when the full feature set is finalised.
Any change requires bumping feature_spec_version and updating this record.

**Current layout:**
- [0..5]  endpoint class one-hot (6 classes)
- [6..10] method class one-hot (5 classes)
- [11]    log1p(payload_bytes)
- [12]    auth_tier
- [13]    cpu_util
- [14]    in_flight (normalised)
- [15]    queue_depth (normalised)

**Deferred to Phase 2.3:**
- latency EWMA short/long and their ratio
- error_rate
- metric_age (staleness)
These may push d to 20. Golden test vectors must be regenerated.

---

## D-004: Single Postgres Instance with TimescaleDB Extension (2026-10-14)

**Decision:** Use a single Postgres instance (timescaledb/timescaledb Docker image)
with two schemas: `config` (relational) and `events` (hypertable). Avoids
running two separate Postgres containers.

**Alternatives:** Separate Postgres + separate TimescaleDB. Adds operational
complexity for no benefit at this scale.

---

## D-005: Disjoint LinUCB as Default; Hybrid as Ablation A10 (2026-10-14)

**Decision:** Start with disjoint LinUCB (separate theta_a per arm, context =
request features + that arm's own system features). Hybrid LinUCB (shared + per-arm
parameters) is implemented only if time permits, as ablation A10.

**Reason:** Spec Section 6.2 explicitly states this as the decision to make in
Phase 5. Disjoint is simpler to implement correctly, and the system features
differ per arm, making it a natural fit. Hybrid may improve sample efficiency
when arms share structure; this hypothesis is testable via A10.

---

## D-006: Windows + WSL for make targets (2026-10-14)

**Decision:** The Makefile targets assume WSL or Git Bash are available for shell
commands on Windows. CI runs on ubuntu-latest GitHub Actions runners.
PowerShell alternatives are noted in the README.

**Reason:** The team's dev environment is Windows. GNU make is available via
Git Bash / WSL. CI is Linux-only per convention.
