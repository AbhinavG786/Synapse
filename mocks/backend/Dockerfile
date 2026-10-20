# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS builder
WORKDIR /build
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/backend ./cmd/backend

FROM scratch
COPY --from=builder /out/backend /backend
EXPOSE 9000
ENTRYPOINT ["/backend"]
