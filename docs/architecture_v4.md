# MyPA V4: Enterprise Architecture & Next-Generation Vision

This document outlines the next-generation architectural roadmap for **MyPA V4**.

> [!TIP]
> Several foundational security, memory, and reliability capabilities originally envisioned for V4 were pulled forward and **delivered in V3.1** (see Section 1 below). Sections 2–4 detail the remaining V4 horizon.

---

## 1. Foundation Pillars Already Delivered in V3.1

- [x] **Transactional Snapshot Rollback & Audit Trail (`internal/domains/durability`)**: Immutable `mutation_logs` recording `BeforeJSON` and `AfterJSON` state snapshots for every tool and GraphQL mutation, enabling atomic point-in-time rollback via `/undo` and `rollbackMutation`.
- [x] **Encrypted State & Credentials at Rest (`internal/crypto/aes.go`)**: Application-level AES-256-GCM encryption with HKDF-SHA256 salted key derivation for OAuth tokens in Redis (`token:<userID>`), PostgreSQL fallback (`encrypted_credentials`), and external API headers.
- [x] **Prompt Injection Defense & Confirmation Gates (`internal/orchestrator/security.go`)**: Semantic `<untrusted_external_content>` XML boundary tagging for external data combined with interactive `CONFIRM <TOOL>` staging for privileged meta-tools.
- [x] **Circuit Breakers & Token Cost Controls (`internal/orchestrator/engine.go`, `registry.go`)**: Bounded 8-turn recursion ceiling (`MaxToolRecursionDepth = 8`), concurrent `errgroup` execution, Redis dynamic prompt caching, and Two-Tier Intent Routing (~80% prompt token reduction).
- [x] **Living Entity Knowledge Graph & Memory Consolidation (`internal/domains/memory`)**: Structured `EntityProfile` JSONB cards, temporal memory superseding (`SupersededByID`, `ValidUntil`), and background memory consolidation (`ConsolidateMemories`) indexed with `pgvector` `halfvec(3072)` HNSW indexes.

---

## 2. Cloud-Native Ingestion & Observability (Planned for V4)

### Serverless Edge Ingestion & Ingress Controller
- **Edge Webhook Workers**: Optional deployment of webhook signature verification (`Telegram` and `Twilio`) to serverless edge functions (e.g., Cloudflare Workers) for global low-latency ingestion.
- **Managed Ingress Controller**: Optional Traefik / Caddy ingress profile alongside the lightweight Go `proxy` service for automated ACME TLS termination in cloud deployments.

### OpenTelemetry Distributed Tracing
- Instrument `proxy`, `gateway`, `orchestrator`, and `audit-worker` with **OpenTelemetry (OTLP)** spans propagated across RabbitMQ AMQP headers to visualize end-to-end latency breakdowns (Webhook -> Queue -> Redis/Postgres -> Gemini API -> Tool Execution) in Jaeger/Grafana Tempo.

---

## 3. Advanced Multi-Agent Reasoning (Planned for V4)

### Multi-Agent Specialist Sub-Graphs
- Evolve complex cross-domain queries (e.g., *"Plan a family vacation within our monthly budget and clear our shared calendar"*) into coordinated specialist sub-agent turns (Finance Planner + Schedule Coordinator) that synthesize a unified proposal.

### Zero-Shot OpenAPI Auto-Discovery
- Extend the on-demand External API Connector (`register_external_api`) to automatically parse OpenAPI 3.1 / Swagger specifications and dynamically synthesize typed `FunctionDeclaration` schemas at runtime.

### Cross-Encoder Memory Re-Ranking
- Complement `pgvector` HNSW cosine retrieval (`Top-20` candidates) with a lightweight cross-encoder re-ranking pass to select the `Top-5` highest-precision memories for long-horizon reasoning.

---

## 4. Resilience & Graceful Degradation (Planned for V4)

### Multi-Provider LLM Failover
- Extend `internal/llm/` to support automatic fallback across secondary model providers or local Ollama endpoints when primary cloud LLM quotas or regional endpoints experience downtime.
