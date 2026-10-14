# Synapse Makefile
# Phase 0: scaffolding targets only. Additional targets added in each phase.
#
# Requires: Go 1.22+, Python 3.11+, Docker, docker compose, protoc (Phase 2+)
# On Windows: use Git Bash or WSL. See docs/RUNBOOK.md.

.PHONY: all up up-infra down logs build-proxy build-cp \
        test test-go test-py lint lint-go lint-py \
        proto generate clean reproduce help

COMPOSE      := docker compose -f infra/docker-compose.yml
GO_MODULE    := ./dataplane
PY_CP        := ./controlplane
RESULTS_DIR  := ./results

# ------------------------------------------------------------------ #
# Default                                                             #
# ------------------------------------------------------------------ #
all: lint test

# ------------------------------------------------------------------ #
# Infrastructure                                                      #
# ------------------------------------------------------------------ #

## Start all services (builds images if needed)
up:
	$(COMPOSE) up -d --build
	@echo "✓ Synapse stack is up. Proxy: http://localhost:8080/healthz"

## Start only Redis + TimescaleDB (no proxy/cp build)
up-infra:
	$(COMPOSE) up -d redis timescaledb
	@echo "✓ Infrastructure only: Redis + TimescaleDB"

## Stop all services and remove volumes
down:
	$(COMPOSE) down -v

## Follow logs for all services
logs:
	$(COMPOSE) logs -f

## Start observability stack (Prometheus + Grafana)
up-obs:
	$(COMPOSE) --profile observability up -d prometheus grafana

# ------------------------------------------------------------------ #
# Build                                                               #
# ------------------------------------------------------------------ #

## Build the Go proxy binary locally (outside Docker)
build-proxy:
	cd $(GO_MODULE) && go build -trimpath -ldflags="-s -w" ./cmd/proxy

## Install Python control plane dependencies
install-cp:
	pip install -r $(PY_CP)/requirements.txt

# ------------------------------------------------------------------ #
# Tests                                                               #
# ------------------------------------------------------------------ #

## Run all tests (Go + Python)
test: test-go test-py

## Run Go tests with race detector
test-go:
	@echo "=== Go tests ==="
	cd $(GO_MODULE) && go test -race -count=1 -timeout=120s ./...

## Run Python tests
test-py:
	@echo "=== Python tests ==="
	cd $(PY_CP) && python -m pytest -v

# ------------------------------------------------------------------ #
# Lint                                                                #
# ------------------------------------------------------------------ #

## Run all linters
lint: lint-go lint-py

## Go: vet + staticcheck
lint-go:
	@echo "=== Go lint ==="
	cd $(GO_MODULE) && go vet ./...
	@command -v staticcheck >/dev/null 2>&1 && \
		cd $(GO_MODULE) && staticcheck ./... || \
		echo "WARNING: staticcheck not found; install with: go install honnef.co/go/tools/cmd/staticcheck@latest"

## Python: ruff + mypy
lint-py:
	@echo "=== Python lint ==="
	cd $(PY_CP) && python -m ruff check .
	cd $(PY_CP) && python -m mypy synapse_cp

# ------------------------------------------------------------------ #
# Proto code generation                                               #
# ------------------------------------------------------------------ #

## Generate Go and Python gRPC stubs from proto/synapse.proto
## Requires: protoc, protoc-gen-go, protoc-gen-go-grpc, grpcio-tools
proto:
	@echo "=== Generating proto stubs ==="
	mkdir -p dataplane/internal/synapsepb
	protoc \
		--go_out=dataplane/internal/synapsepb \
		--go_opt=paths=source_relative \
		--go-grpc_out=dataplane/internal/synapsepb \
		--go-grpc_opt=paths=source_relative \
		-I proto \
		proto/synapse.proto
	python -m grpc_tools.protoc \
		--python_out=controlplane/synapse_cp/grpc_server \
		--grpc_python_out=controlplane/synapse_cp/grpc_server \
		--pyi_out=controlplane/synapse_cp/grpc_server \
		-I proto \
		proto/synapse.proto
	@echo "✓ Proto stubs generated"

# ------------------------------------------------------------------ #
# Experiments                                                         #
# ------------------------------------------------------------------ #

## Reproduce headline results (Phase 6+)
## Documents the sequence of commands needed to reproduce results/ from scratch.
reproduce:
	@echo "reproduce target not yet implemented (Phase 6)"
	@echo "See docs/RUNBOOK.md for manual steps."

# ------------------------------------------------------------------ #
# Misc                                                                #
# ------------------------------------------------------------------ #

## Remove build artifacts
clean:
	rm -f $(GO_MODULE)/synapse-proxy
	find . -name "__pycache__" -type d -exec rm -rf {} + 2>/dev/null || true
	find . -name "*.pyc" -delete 2>/dev/null || true

## Print available targets
help:
	@echo "Synapse Makefile targets:"
	@grep -E '^## ' Makefile | sed 's/## /  /'
