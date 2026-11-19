# syntax=docker/dockerfile:1

# ── Shared dependency stage ──────────────────────────────────────
FROM golang:1.26.11-alpine3.24@sha256:ecb67f987aa1a57794c91d19ade7ae0209f2f0f5496aaef9e6ae85ddf92d02ab AS deps
WORKDIR /app
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# ── App (V4-01: one binary with roles; replaces gateway, orchestrator and proxy) ──
# The debian base carries postgresql-client for pg_dump/pg_restore (backups and `mypa transfer`).
FROM deps AS build-app
COPY internal/ internal/
COPY config/  config/
COPY cmd/mypa/ cmd/mypa/
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -ldflags='-w -s' -o /mypa ./cmd/mypa

FROM debian:13.7-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2 AS app
RUN apt-get update && apt-get install -y postgresql-client ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=build-app /mypa /usr/local/bin/mypa
COPY config/config.yaml /config/config.yaml
EXPOSE 8000
ENTRYPOINT ["/usr/local/bin/mypa"]

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
