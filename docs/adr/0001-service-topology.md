# ADR 0001 — Service topology: consolidate binaries? replace RabbitMQ?

- **Status:** Proposed — **owner decides** (remediation plan item P4-4: write the ADR, do not implement)
- **Date:** 2026-11-16
- **Context owner:** MyPA maintainers

## 1. Context

MyPA is a single-family personal assistant. Throughput is a few messages per minute at peak, but a single turn can take up to **5 minutes** (multi-step Gemini tool calling). The deployment today (see `SYSTEM.md` §1.1) is:

| Container | Role | Code size (non-test) |
| --- | --- | --- |
| `proxy` | Reverse proxy: `/webhook/*` → gateway, `/graphql` + `/auth/google/*` → orchestrator | `cmd/proxy` ≈ 110 lines |
| `gateway` | Telegram/Twilio webhooks → verify → normalize → publish `telegram.inbound` | `cmd/gateway` ≈ 230 lines |
| `orchestrator` | Consumes `telegram.inbound`, LLM turns, tools, GraphQL, OAuth, cron scheduler, backups; publishes `audit.events` | the bulk of the codebase |
| `audit-worker` | Consumes `audit.events` → `audit_sessions`; 30-day archiver | small |
| `migrate` | One-shot schema migrations (P4-3) | reuses orchestrator image |
| `dashboard` | Next.js console (talks to `proxy:8000/graphql`) | — |
| `rabbitmq`, `postgres`, `redis` | Infrastructure | broker package ≈ 870 lines (consumer, publisher, topology, retry, keyed lanes) |

### What earlier remediation items already fixed (so they are *not* arguments for a change here)
- **P1-4 — duplicate side effects on redelivery.** Inbound messages are deduplicated (`SET NX` claim in Redis), and once any side-effecting tool has run, errors are non-retryable (ack, no requeue). The "RabbitMQ re-ran my turn and booked twice" class of bug is closed regardless of transport.
- **P2-8 — head-of-line blocking.** `Qos(N)` + an in-process keyed dispatcher (`internal/broker/keyed_dispatcher.go`) gives parallelism across users and strict FIFO per user. The dispatcher takes plain `func()` tasks, so it is **transport-agnostic** and survives any queue swap below.
- **P3-5 — compose hygiene** (digest-pinned images, opt-in tools), **P4-3 — explicit migrations.**

### Constraints that already exist
- **Single orchestrator replica.** The cron scheduler has no leader election, and P2-8's per-user ordering is in-process. Running two orchestrators would double-fire automations and break ordering. Horizontal scaling is not a goal at this scale.
- **Webhooks must return fast.** Telegram retries slow/failed webhooks; turns take minutes. Some durable hand-off between "webhook accepted" and "turn processed" is required whatever the topology.
- **Per-user FIFO** for inbound messages (chat history order) is a hard requirement.

## 2. Decision 1 — Merge `proxy` + `gateway` + `orchestrator` into one binary?

### Options
| | Option | Pros | Cons |
| --- | --- | --- | --- |
| 1a | **Status quo** (3 binaries) | Webhook ingress stays up while orchestrator restarts/crashes; isolation of the internet-facing surface | Three images/containers, a proxy hop, duplicated config/bootstrapping, more compose wiring; the isolation is weak in practice (same network, same `.env`) |
| 1b | **One binary, one HTTP server, packages unchanged** (`cmd/mypa`, mounting the gateway handlers, GraphQL and OAuth on one mux) | Removes `proxy` entirely and one network hop; one image to build/patch; one config load; simpler local dev (`go run ./cmd/mypa`) | Ingress is down during an orchestrator restart (seconds; Telegram retries webhooks, Twilio does not by default — configure Twilio's fallback URL or accept the window); a crash in turn processing takes ingress with it |
| 1c | **One binary with roles** (`mypa --role=all\|ingress\|worker`) | Same simplicity as 1b by default, but the old split is still a deploy-time flag if ingress isolation is ever needed | Slightly more bootstrapping code (role switch) |
| 1d | Merge `audit-worker` too | One fewer container | Audit becomes in-process; fine if the queue moves to Postgres (Decision 2), otherwise little gain |

### Challenge
- *Is ingress isolation worth a container?* The gateway only verifies, normalizes and enqueues. Its availability matters mostly during orchestrator deploys, which are seconds long and rare for a home system. Telegram covers that window with its own retries. The panic surface is already contained: P2-8 converts handler panics to errors.
- *Does merging couple the code?* Not if packages stay as they are. The merge is about `main`, not about the domain code.

### Recommendation
**1c**: one binary with `--role`, default `all`. Keep the gateway → queue → orchestrator hand-off through the durable queue, even in-process, so webhooks stay fast and crash-safe. Delete `proxy`; the dashboard points at the single service. `audit-worker` follows Decision 2.

## 3. Decision 2 — Replace RabbitMQ?

The broker carries two flows: `telegram.inbound` (needs durability, retry plus DLQ, and per-user FIFO via the keyed dispatcher) and `audit.events` (fire-and-forget telemetry, needs durability).

### Options
| | Option | Pros | Cons |
| --- | --- | --- | --- |
| 2a | **Keep RabbitMQ** | Works today, with the P1-4 and P2-8 hardening; publisher confirms and a DLQ topology are already written and tested | An extra stateful service (Erlang VM) to run, upgrade (P5-4: 3.x → 4.x) and back up separately; ~870 lines of broker plumbing (reconnects, confirms, topology) |
| 2b | **River (Postgres-backed jobs)** | No extra service; jobs live in the system of record, so P3-4 backups cover them; **transactional enqueue** (e.g. audit row and job in one tx = outbox for free); built-in retries/backoff, unique jobs, **periodic jobs with leader election** (would fix the scheduler's single-replica limit); job inspection via SQL or River UI | **No per-key FIFO in open-source River** (ordered "sequences" are a River Pro feature). Ordering would need a custom guard: per-user advisory lock plus "no earlier pending job for this user, else snooze". Workers need a `pgxpool` next to GORM. Polling/LISTEN adds Postgres load (negligible at this scale) |
| 2c | **Redis Streams** | Redis is already deployed with AOF; a stream is FIFO, so a single `XREADGROUP` consumer feeding the existing keyed dispatcher preserves P2-8 semantics **exactly**; tiny client code | No native DLQ or delayed retry: build it from `XPENDING` delivery counts, `XAUTOCLAIM` and a `*.dlq` stream. Durability is AOF `everysec` (up to ~1 s loss on a Redis crash), weaker than RabbitMQ confirms. Redis turns from a cache into a system of record, so its backups start to matter |
| 2d | Custom `SELECT … FOR UPDATE SKIP LOCKED` table queue | Full control, per-user FIFO by query design | Reinventing River; every edge case (stuck jobs, retries, cleanup) is ours |

### Challenge
- *Is replacing a working broker worth the risk?* The correctness problems that motivated this were fixed in P1-4 and P2-8 without changing transport. What remains is **operational weight**: one fewer stateful service, no RabbitMQ 4.x migration, less reconnect code. That is real but not urgent.
- *2b vs 2c.* River is the better long-term home: transactional, backed up, and it brings periodic-job leader election. Its missing per-user FIFO hits our one hard requirement, though. Redis Streams keeps ordering trivially, but it has to hand-build retries and the DLQ, and it moves durable state into Redis. Neither is a clear win, so the recommendation is conditional.
- *Message-loss tolerance.* P1-4 already chose at-most-once for side-effecting turns, where a crash mid-turn means "please resend". A ~1 s AOF loss window (2c) is consistent with that stance. Losing audit rows is acceptable; they are telemetry.

### Recommendation
1. **Short term: keep RabbitMQ (2a)**, but do the P5-4 upgrade to 4.x. Nothing is broken, and Decision 1 is independent of this one.
2. **If and when the broker is replaced, prefer River (2b)** with an explicit per-user ordering guard and a test equivalent to `parallel_consume_test.go`. Move `audit.events` first, since it has no ordering need and gains the transactional outbox. Then move the scheduler to River periodic jobs, which removes the single-replica constraint. Move `telegram.inbound` last.
3. Choose **Redis Streams (2c)** instead only if the owner prefers the smallest change, accepts Redis as durable state, and adds Redis to the backup plan.

## 4. Consequences (if 1c is accepted and 2a kept)
- Compose loses `proxy` (and `gateway` as a separate container); the `migrate` one-shot stays.
- `SYSTEM.md` §1.1, `README.md` (ports, webhooks URL path stays `/webhook/*`), `.env` (`GATEWAY_URL`, `ORCHESTRATOR_URL` become unused), and dashboard `GRAPHQL_ENDPOINT` change.
- Webhook signature/secret verification (`TELEGRAM_SECRET_TOKEN`, Twilio signature) moves unchanged with the gateway package.
- Scaling story unchanged: one replica. Revisit together with 2b (leader election) if that ever changes.

## 5. Not decided here
- Off-machine backups (P3-4 follow-up) — independent.
- Whether the dashboard should move into the Go binary (static export) — out of scope; pages such as `dashboard/src/app/audit/page.tsx` use server-side Next.js features that a static export would lose.
