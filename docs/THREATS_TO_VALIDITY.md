# docs/THREATS_TO_VALIDITY.md — Threats to Validity

This document catalogues the known threats to the validity of Synapse's
experimental results, as required by Section 8 of the implementation spec.

## Internal Validity

**T1 — Single-machine testbed**: All components (proxy, backends, control plane,
load generator) run on the same physical host. CPU contention between them is a
confound. Mitigation: pin CPUs with taskset/cpuset; run load generator on
dedicated cores; record host specs with every result.

**T2 — Container CPU noise**: Docker cgroup CPU limits are soft limits at low
utilisation; neighbouring containers can interfere. Mitigation: CPU pinning,
repeated runs (≥10), report variance.

**T3 — Go GC effects**: The Go garbage collector can introduce STW pauses,
inflating tail latency. This affects all Go-based policies equally, but may
interact with allocation patterns. Mitigation: disable GC in microbenchmarks;
report GC stats; keep hot path allocation-free (R-4).

## External Validity

**T4 — Synthetic workloads**: Endpoint classes and service times are configurable
synthetic models (lognormal), not real production traces. The findings may not
generalise to real applications with different cost distributions.
Mitigation: W5 (trace replay) if a real trace is available; document limitations.

**T5 — Simulator fidelity**: The discrete-event simulator in Phase 5.4 is
calibrated against Phase 1/4 measurements but remains an approximation.
Mitigation: validate simulator vs live measurements before trusting it (Phase 5.4);
OPE cross-check (E7).

**T6 — Limited node counts**: We test up to 32 nodes (E6). Real microservice
meshes may have hundreds. The LinUCB arm set scales linearly; very large arm
counts are not evaluated.

**T7 — No real production traces**: We use synthetic traffic profiles (W1–W4)
and optionally a recorded synthetic trace (W5). Findings should be replicated
with real traces before drawing production conclusions.

## Construct Validity

**T8 — Reward signal proxy**: The bandit's reward is proxy-observed end-to-end
latency, not user-perceived quality. These are strongly correlated but not
identical (e.g., client-side retries, CDN caching).

**T9 — Bandit independence assumption**: The bandit treats routing decisions as
independent, but queue depth at one node is influenced by past routing decisions.
This is an acknowledged limitation (Section 2); ablation A9 probes it.
