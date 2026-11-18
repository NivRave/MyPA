# syntax=docker/dockerfile:1

# ── Shared dependency stage ──────────────────────────────────────
FROM golang:1.26.11-alpine3.24@sha256:ecb67f987aa1a57794c91d19ade7ae0209f2f0f5496aaef9e6ae85ddf92d02ab AS deps
WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# ── Gateway ──────────────────────────────────────────────────────
FROM deps AS build-gateway
COPY internal/ internal/
COPY config/  config/
COPY cmd/gateway/ cmd/gateway/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -ldflags='-w -s' -o /gateway ./cmd/gateway

FROM gcr.io/distroless/static-debian12@sha256:d75cdd72874d4790092fcb1b058493ecf6bb5bf2b2b897045b00ff01d91843f2 AS gateway
COPY --from=build-gateway /gateway /gateway
COPY config/config.yaml /config/config.yaml
EXPOSE 8080
ENTRYPOINT ["/gateway"]

# ── Orchestrator ─────────────────────────────────────────────────
FROM deps AS build-orchestrator
COPY internal/ internal/
COPY config/  config/
COPY cmd/orchestrator/ cmd/orchestrator/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -ldflags='-w -s' -o /orchestrator ./cmd/orchestrator

FROM debian:12.15-slim@sha256:734a72b3d6142a71993b4bc580ca01d8ca687f77a2c422d96a961d3376b179bc AS orchestrator
RUN apt-get update && apt-get install -y postgresql-client ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=build-orchestrator /orchestrator /usr/local/bin/orchestrator
COPY config/config.yaml /config/config.yaml
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/orchestrator"]

# ── Proxy ────────────────────────────────────────────────────────
FROM deps AS build-proxy
COPY internal/ internal/
COPY config/  config/
COPY cmd/proxy/ cmd/proxy/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -ldflags='-w -s' -o /proxy ./cmd/proxy

FROM gcr.io/distroless/static-debian12@sha256:d75cdd72874d4790092fcb1b058493ecf6bb5bf2b2b897045b00ff01d91843f2 AS proxy
COPY --from=build-proxy /proxy /proxy
COPY config/config.yaml /config/config.yaml
EXPOSE 8000
ENTRYPOINT ["/proxy"]

# ── Audit Worker ───────────────────────────────────────────────────
FROM deps AS build-audit-worker
COPY internal/ internal/
COPY config/  config/
COPY cmd/audit-worker/ cmd/audit-worker/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -ldflags='-w -s' -o /audit-worker ./cmd/audit-worker

FROM gcr.io/distroless/static-debian12@sha256:d75cdd72874d4790092fcb1b058493ecf6bb5bf2b2b897045b00ff01d91843f2 AS audit-worker
COPY --from=build-audit-worker /audit-worker /audit-worker
COPY config/config.yaml /config/config.yaml
ENTRYPOINT ["/audit-worker"]
