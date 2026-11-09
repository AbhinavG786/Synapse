# Synapse Makefile
# Phase 2: data plane core + classical baselines.
#
# Requires: Go 1.26+, Python 3.13+, Docker, docker compose
# On Windows: use Git Bash or WSL. See docs/RUNBOOK.md.

.PHONY: all up up-infra down logs build-proxy build-mock build-cp \
        test test-go test-go-mock test-go-dataplane test-py \
        bench bench-decision \
        lint lint-go lint-py \
        proto generate clean reproduce help

COMPOSE        := docker compose -f infra/docker-compose.yml
GO_DATAPLANE   := ./dataplane
GO_MOCK        := ./mocks/backend
PY_CP          := ./controlplane
RESULTS_DIR    := ./results

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
	cd $(GO_DATAPLANE) && go build -trimpath -ldflags="-s -w" -o synapse-proxy ./cmd/proxy

## Build the mock backend binary locally
build-mock:
	cd $(GO_MOCK) && go build -trimpath -ldflags="-s -w" -o backend ./cmd/backend

## Install Python control plane dependencies
install-cp:
	pip install -r $(PY_CP)/requirements.txt

# ------------------------------------------------------------------ #
# Tests                                                               #
# ------------------------------------------------------------------ #

## Run all tests (Go + Python)
test: test-go test-py

## Run all Go tests with race detector (dataplane + mock backend)
test-go: test-go-mock test-go-dataplane

## Run mock backend Go tests
test-go-mock:
	@echo "=== Mock backend tests ==="
	cd $(GO_MOCK) && go test -race -count=1 -timeout=120s ./...

## Run dataplane Go tests
test-go-dataplane:
	@echo "=== Dataplane tests ==="
	cd $(GO_DATAPLANE) && go test -race -count=1 -timeout=120s ./internal/...

## Run Python tests
test-py:
	@echo "=== Python tests ==="
	cd $(PY_CP) && python -m pytest -v

# ------------------------------------------------------------------ #
# Benchmarks (Phase 2.8)                                             #
# ------------------------------------------------------------------ #

## Run all Go microbenchmarks (save output to results/bench_phase2.txt)
bench: bench-decision

## Benchmark routing decision pipeline (feature extract + pick + emit)
bench-decision:
	@mkdir -p $(RESULTS_DIR)
	cd $(GO_DATAPLANE) && go test -bench=. -benchmem -count=5 -timeout=120s ./bench/ \
	  | tee ../../$(RESULTS_DIR)/bench_phase2_$(shell date +%Y%m%d_%H%M%S).txt
	@echo "✓ Benchmark results saved to $(RESULTS_DIR)/"

# ------------------------------------------------------------------ #
# Lint                                                                #
# ------------------------------------------------------------------ #

## Run all linters
lint: lint-go lint-py

## Go: vet on both modules
lint-go:
	@echo "=== Go lint: mock backend ==="
	cd $(GO_MOCK) && go vet ./...
	@echo "=== Go lint: dataplane ==="
	cd $(GO_DATAPLANE) && go vet ./...
	@command -v staticcheck >/dev/null 2>&1 && \
		cd $(GO_DATAPLANE) && staticcheck ./... || \
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
reproduce:
	@echo "reproduce target not yet implemented (Phase 6)"
	@echo "See docs/RUNBOOK.md for manual steps."

# ------------------------------------------------------------------ #
# Misc                                                                #
# ------------------------------------------------------------------ #

## Remove build artifacts
clean:
	rm -f $(GO_DATAPLANE)/synapse-proxy $(GO_MOCK)/backend
	find . -name "__pycache__" -type d -exec rm -rf {} + 2>/dev/null || true
	find . -name "*.pyc" -delete 2>/dev/null || true

## Print available targets
help:
	@echo "Synapse Makefile targets:"
	@grep -E '^## ' Makefile | sed 's/## /  /'
