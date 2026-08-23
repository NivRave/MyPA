# syntax=docker/dockerfile:1

# ── Shared dependency stage ──────────────────────────────────────
FROM golang:1.26-alpine AS deps
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

FROM gcr.io/distroless/static-debian12 AS gateway
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

FROM gcr.io/distroless/static-debian12 AS orchestrator
COPY --from=build-orchestrator /orchestrator /orchestrator
COPY config/config.yaml /config/config.yaml
EXPOSE 8081
ENTRYPOINT ["/orchestrator"]

# ── Proxy ────────────────────────────────────────────────────────
FROM deps AS build-proxy
COPY internal/ internal/
COPY config/  config/
COPY cmd/proxy/ cmd/proxy/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -ldflags='-w -s' -o /proxy ./cmd/proxy

FROM gcr.io/distroless/static-debian12 AS proxy
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

FROM gcr.io/distroless/static-debian12 AS audit-worker
COPY --from=build-audit-worker /audit-worker /audit-worker
COPY config/config.yaml /config/config.yaml
ENTRYPOINT ["/audit-worker"]
