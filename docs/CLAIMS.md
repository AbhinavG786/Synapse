# docs/CLAIMS.md — Claims Ledger

Every claim in the paper/synopsis maps to an experiment here.
Status: UNTESTED | SUPPORTED | REFUTED | INCONCLUSIVE

| ID  | Claim | Experiment(s) | Status | Evidence Path | Notes |
|-----|-------|---------------|--------|---------------|-------|
| C1  | Proxy adds < 1 ms overhead at P99 under moderate load | E1 | UNTESTED | — | Target is a hypothesis (R-2) |
| C2  | Decision (feature extract + route) is microsecond scale | A11 | UNTESTED | — | Go microbenchmark in Phase 2.8 |
| C3  | Synapse (LinUCB) lowers P99 vs RR and LC on mixed workloads | E2, E3 | UNTESTED | — | Primary result table |
| C4  | Gains grow with workload heterogeneity and load | E2, E3 | UNTESTED | — | Reported per utilisation level |
| C5  | Synapse shifts traffic away from degrading nodes before error/timeout thresholds | E4 | UNTESTED | — | Proactive vs reactive comparison |
| C6  | Detection/shift time is about 1–2 s (synopsis: 1.2 s, 95%) | E4 | UNTESTED | — | Synopsis value is a hypothesis |
| C7  | Data plane survives control-plane outage | E5 | UNTESTED | — | R-3 test |
| C8  | 500 ms sync interval is a sound trade-off | A3 | UNTESTED | — | Sweep: 100 ms / 500 ms / 2 s / 5 s |
| C9  | Bandit is adequate vs a full MDP at far lower cost | A9 | UNTESTED | — | Stretch; may be INCONCLUSIVE |
| C10 | Sublinear regret / learning converges in simulator | E7 | UNTESTED | — | Simulator in Phase 5.4 |
| C11 | Offline estimators agree with live results within CI | E7 | UNTESTED | — | OPE cross-check |

> **Protocol:** When a claim is REFUTED or INCONCLUSIVE, update the Status
> column, record the evidence path, and add a note proposing how the paper
> text should be modified. Do not hide unfavourable results (R-1, R-7).
