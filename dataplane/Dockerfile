# syntax=docker/dockerfile:1

# Stage 1: Build the Go proxy binary
FROM golang:1.25-alpine AS builder
WORKDIR /build

# Copy go.mod and go.sum first for layer caching
COPY go.mod go.sum* ./
RUN go mod download

# Copy source and build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/synapse-proxy ./cmd/proxy

# Stage 2: Minimal runtime image
FROM scratch
COPY --from=builder /out/synapse-proxy /synapse-proxy
EXPOSE 8080 9091
ENTRYPOINT ["/synapse-proxy"]
