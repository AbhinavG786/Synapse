# docs/EVALUATION.md — Evaluation Protocol

## Metrics

Primary:
- P50, P90, P95, P99, P99.9 end-to-end latency (overall and per endpoint class)
- Error rate, goodput, timeout rate

Secondary:
- Per-node load share and imbalance (coefficient of variation, Jain's fairness index)
- Fault detection time (first time degraded node's traffic share < 50% of prior share)
- Recovery time
- Proxy-added latency (overhead vs direct-to-backend)
- Proxy CPU and memory
- Policy sync lag

Bandit-specific:
- Cumulative regret vs oracle (simulator)
- Per-arm pull counts and exploration share over time
- Theta drift

## Statistical Protocol (R-7)

- Minimum **10 independent runs** per condition (different seeds)
- Report mean, median, and **95% bootstrap confidence intervals**
- Paired comparison or **Mann-Whitney U** for significance
- Report **effect sizes**
- Discard nothing silently; warmup period logged separately

## Load Generation (R-8)

- **Primary** (latency measurements): open-loop Poisson arrivals (constant-arrival-rate or custom Go generator)
- **Secondary** (throughput): closed-loop VUs (k6/Locust), clearly labelled
- Never use coordinated omission (closed-loop) for latency claims

## Experiments

| ID | Purpose | Phase |
|----|---------|-------|
| E1 | Proxy overhead: direct vs proxy (RR) | 2 |
| E2 | Main comparison: all policies on W1 at 60/80/90% load | 4+5 |
| E3 | Heterogeneous endpoints (W2/W4) | 4+5 |
| E4 | Fault scenarios F1–F7 | 4+5 |
| E5 | Control-plane outage | 5 |
| E6 | Scalability: 4/8/16/32 nodes | 6 |
| E7 | Learning curve / regret + OPE cross-check | 5+6 |

## Ablations

| ID | Variable | Values |
|----|----------|--------|
| A1 | Context features | request-only / system-only / both / +L_hat |
| A2 | Exploration | alpha sweep; epsilon floor sweep |
| A3 | Sync interval | 100 ms / 500 ms / 2 s / 5 s |
| A4 | Forgetting | factor / window / change-detection on vs off |
| A5 | Reward shape | latency-only / +penalty; lambda sweep |
| A6 | Safety nets | circuit-breaker on vs off |
| A7 | Telemetry staleness | delayed / dropped metrics |
| A8 | Cold start | new node joining mid-run |
| A9 | Coupling test | bandit vs DQN/MDP in simulator |
| A10 | Disjoint vs hybrid LinUCB | — |
| A11 | Decision cost | LinUCB scoring time vs d and N |
