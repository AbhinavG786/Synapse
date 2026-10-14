# docs/MODEL.md — Bandit Model Specification

## LinUCB Algorithm (Li et al., WWW 2010)

**Reference:** Li, L., Chu, W., Langford, J., & Schapire, R. E. (2010).
A contextual-bandit approach to personalized news article recommendation.
*Proceedings of the 19th International Conference on World Wide Web* (WWW 2010).

### Notation

| Symbol | Description |
|--------|-------------|
| `a` | Arm (backend node N_i) |
| `x_{t,a}` | Context vector for arm `a` at time `t` (length `d+1` with bias) |
| `A_a` | d×d regularised matrix, initialised to `λ_reg * I_d` |
| `b_a` | d-vector, initialised to 0 |
| `θ_a` | `A_a^{-1} b_a` — estimated reward coefficients |
| `alpha` | Exploration bonus coefficient (UCB width) |
| `λ_reg` | L2 regularisation strength |

### Selection

```
a* = argmax_a [ x_{t,a}^T θ_a  +  alpha * sqrt(x_{t,a}^T A_a^{-1} x_{t,a}) ]
```

The second term is the uncertainty bonus (exploration). `theta_a = A_a^{-1} b_a`.

### Update (on observed reward r)

```
A_a  ←  A_a + x x^T
b_a  ←  b_a + r * x
θ_a  =  A_a^{-1} b_a
```

`A_a^{-1}` is maintained directly via Sherman-Morrison (Phase 5.1):

```
A^{-1} ← A^{-1} - (A^{-1} x x^T A^{-1}) / (1 + x^T A^{-1} x)
```

Periodically re-inverted from `A` to bound numerical drift.

### Non-stationarity (Section 6.5)

Exponential forgetting:
```
A ← γ_f * A + x x^T + (1 - γ_f) * λ_reg * I
b ← γ_f * b + r * x
```

γ_f ∈ (0, 1]. γ_f = 1 recovers the stationary case.

## Reward Definition (Section 6.4)

```
r = -(t_exec_ms / scale_k)  -  λ * 1[error or timeout]
```

- `scale_k` = baseline median latency for endpoint class k (stored in `config.route_classes.baseline_median_ms`)
- `λ` = configurable error penalty (ablation A5)
- Clipped to `[-10, 0]`

**Reward uses proxy-observed end-to-end latency**, not backend self-reported time,
to avoid reporting bias.

## Heuristic-Score Policy (B6)

```
Score(N_i) = alpha * L_hat(E_k, P_s, N_i)  +  beta * C_i  +  gamma * Q_i
```

- `L_hat` = per-(endpoint, node) EWMA latency estimate
- `C_i` = CPU utilisation of node i
- `Q_i` = queue depth of node i
- Lower score = preferred node

## Go–Python Score Equivalence

The context vector layout, normaliser (mean/scale), and scoring formula must
produce identical floating-point scores in Go and Python (within 1e-5 tolerance).
This is verified by golden test vectors — see Phase 5.3.

The `feature_spec_version` field in `PolicySnapshot` is the contract version.
Any change to the vector layout requires:
1. Bumping `feature_spec_version`
2. Updating this document
3. Regenerating golden test vectors
4. Adding a D-XXX entry in `DECISIONS.md`
