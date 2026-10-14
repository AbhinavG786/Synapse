# docs/RUNBOOK.md — Operations and Troubleshooting

## Quick Start

```bash
# Start all infrastructure
make up

# Run all tests
make test

# Run linters
make lint

# Start only infra (no proxy/cp build needed)
make up-infra

# Tear down everything (removes volumes)
make down
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `SYNAPSE_LISTEN` | `:8080` | Proxy listen address |
| `SYNAPSE_CP_ADDR` | `localhost:9090` | Control plane gRPC address |
| `REDIS_ADDR` | `localhost:6379` | Redis address |
| `POSTGRES_DSN` | — | PostgreSQL connection string |
| `TIMESCALE_DSN` | — | TimescaleDB connection string (same or different DB) |
| `SYNAPSE_RUN_ID` | `local` | Run identifier for telemetry tagging |
| `SYNAPSE_DEFAULT_ALGO` | `least-conn` | Initial routing algorithm |
| `SYNAPSE_SNAPSHOT_TTL` | `30s` | Snapshot staleness TTL |
| `SYNAPSE_TELEMETRY_CHAN` | `4096` | Telemetry channel buffer size |

## Known Operational Issues

### Socket exhaustion / TIME_WAIT growth
- Symptom: `dial tcp: lookup ... connection refused` or EADDRINUSE
- Cause: ephemeral port starvation from high connection churn
- Mitigation: tune `net.ipv4.tcp_tw_reuse=1`, use persistent keep-alive pools,
  set `MaxIdleConnsPerHost` appropriately (default: 100)

### Telemetry drops
- Symptom: `telemetry events dropped` WARNING in proxy logs
- Cause: control plane or Redis is slow; channel is full
- Mitigation: increase `SYNAPSE_TELEMETRY_CHAN`; check control plane latency

### Stale snapshot fallback
- Symptom: proxy logs `falling back to least-connections`
- Cause: control plane down or not pushing updates within TTL
- Expected behaviour per R-3; E5 tests this deliberately

## TCP Tuning Notes (Linux)

```bash
# Allow TIME_WAIT socket reuse
sysctl -w net.ipv4.tcp_tw_reuse=1

# Increase local port range
sysctl -w net.ipv4.ip_local_port_range="1024 65535"

# Increase listen backlog
sysctl -w net.core.somaxconn=65535
```
