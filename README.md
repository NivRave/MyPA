# MyPA — Multi-Channel Personal & Family AI Operating System

An event-driven, multi-tenant personal and family AI operating system accessible via **WhatsApp**, **Telegram**, and a **Next.js 16 (React 19) Web Control Dashboard**. Built in Go and powered by Google Gemini tool-calling, PostgreSQL (`pgvector`), Redis, and RabbitMQ, MyPA combines core productivity integrations (Google Calendar, Gmail, Google Tasks, Web Search, Voice & Vision) with specialized life-domain intelligence across **Baby & Child Care**, **House & Vehicle Assets**, **Finance & Budgets**, **Shared Family Spaces**, **Topic-Mapped Memory & Living Entity Profiles**, **On-Demand External APIs & Event-Driven Automations**, and **Transactional Durability with Conversational Undo (`/undo`)**.

---

## Highlights

- **Channels:** Telegram and WhatsApp (Twilio), voice notes (Groq Whisper) and images/documents (Gemini vision); a Next.js web dashboard for browsing, editing and rolling back data.
- **Tools:** <!-- gen:tool-summary -->68 tools across 13 modules<!-- /gen:tool-summary -->, offered in two tiers (always-on core tools + domain groups selected per message) to keep prompts small. Full catalogue: [SYSTEM.md §6](./SYSTEM.md#6-complete-ai-agent-tool-catalog).
- **Google Workspace:** Calendar, Gmail, Tasks and Contacts; live web search (Tavily).
- **Life domains:** baby care (with medication-cooldown guardrails), house contracts and maintenance, vehicles, finance and budgets, a personal CRM, topic-scoped memory and entity profiles, shared family spaces with partner notifications.
- **Automations:** event-driven rules, scheduled natural-language workflows, and runtime-registered external APIs behind SSRF protection.
- **Durability:** every tool write is snapshotted for `/undo` and dashboard rollback; encrypted, age-protected nightly backups; versioned schema migrations.
- **Security:** fail-closed webhook verification, tenant isolation, prompt-injection boundaries with confirmation for sensitive tools, AES-256-GCM encryption at rest.

## Documentation

| Topic | Where |
| :--- | :--- |
| Architecture, services & ports | [SYSTEM.md §1](./SYSTEM.md#1-system-overview--high-level-architecture) |
| Repository layout | [SYSTEM.md §2](./SYSTEM.md#2-repository--package-structure) |
| Security model | [SYSTEM.md §3.4](./SYSTEM.md#34-security-architecture--defense-in-depth) |
| Database schema & migrations | [SYSTEM.md §4](./SYSTEM.md#4-postgresql--pgvector-database-schema-reference) |
| Tool catalogue (generated) | [SYSTEM.md §6](./SYSTEM.md#6-complete-ai-agent-tool-catalog) |
| GraphQL API & dashboard routes | [SYSTEM.md §7](./SYSTEM.md#7-graphql-api--nextjs-dashboard-reference) |
| Environment variables & cron jobs | [SYSTEM.md §8](./SYSTEM.md#8-configuration-environment-variables--cron-schedules) |
| Adding a feature | [SYSTEM.md §9](./SYSTEM.md#9-step-by-step-feature-development-playbook-8-architectural-sections) and [AGENTS.md](./AGENTS.md) |
| Design decisions | [docs/DECISIONS_LOG.md](./docs/DECISIONS_LOG.md), [docs/adr/](./docs/adr/) |
| Known issues & follow-ups | [docs/BACKLOG.md](./docs/BACKLOG.md) |

---

## Prerequisites

- [Go 1.26+](https://go.dev/dl/) and [Node.js 24](https://nodejs.org/) (only for running services or the dashboard outside Docker)
- [Docker & Docker Compose](https://docs.docker.com/get-docker/)
- Gemini API key ([Google AI Studio](https://aistudio.google.com/))
- Telegram bot token ([@BotFather](https://t.me/BotFather)) and/or Twilio account (WhatsApp)
- Optional: ngrok account (webhook ingress), Google Cloud OAuth credentials (Workspace tools), Groq (voice), Tavily (web search)

---

## Quick Start

### Running with Docker (Recommended)

1. **Clone and configure environment:**
   ```bash
   cp .env.example .env
   # Edit .env with your API keys, OAuth credentials, and security secrets
   ```
   Required: `ENCRYPTION_KEY` and `JWT_SECRET` (two *different* `openssl rand -hex 32` values), `DASHBOARD_API_SECRET`, `DASHBOARD_FAMILY_GROUP`, `GEMINI_API_KEY`, and the infrastructure credentials `RABBITMQ_DEFAULT_USER`/`RABBITMQ_DEFAULT_PASS` and `POSTGRES_USER`/`POSTGRES_PASSWORD`/`POSTGRES_DB`; plus `TELEGRAM_SECRET_TOKEN` when Telegram is configured and `TWILIO_AUTH_TOKEN` when `TWILIO_ACCOUNT_SID` is set. Recommended: `BACKUP_AGE_RECIPIENTS`. Every service validates its keys at startup and lists anything missing. Full reference: [SYSTEM.md §8.1](./SYSTEM.md#81-environment-variables-internalconfigconfiggo--internalcryptoaesgo).

   Docker builds each container's broker/Redis/database URL from those credentials and the service names (`rabbitmq`, `redis`, `postgres`); the `RABBITMQ_URL`, `REDIS_URL` and `DATABASE_URL` keys in `.env` are **ignored by Docker** and only used when you run a service on the host (below).

2. **Build and start all services (including containerized ngrok):**
   ```bash
   docker-compose up -d --build
   ```
   *(the one-shot `migrate` service applies database migrations first; the orchestrator and audit-worker start only after it succeeds)*
   *(ngrok starts automatically inside Docker using `NGROK_AUTHTOKEN` and `NGROK_DOMAIN` from `.env`, exposing the tunnel inspection UI at `http://localhost:4040` and the Web Dashboard at `http://localhost:3030`)*

3. **Set Webhooks:**
   - **Telegram:** Automatic. When `TELEGRAM_WEBHOOK_URL` is set, the gateway calls `setWebhook` with `TELEGRAM_SECRET_TOKEN` on startup (look for `telegram webhook registered with secret token` in `docker-compose logs gateway`). To register manually instead:
     ```bash
     curl "https://api.telegram.org/bot<YOUR_TOKEN>/setWebhook?url=<NGROK_URL>/webhook/telegram&secret_token=<TELEGRAM_SECRET_TOKEN>"
     ```
   - **WhatsApp:** Set `<NGROK_URL>/webhook/twilio` (e.g., `https://your-domain.ngrok-free.dev/webhook/twilio`) in your Twilio WhatsApp Sandbox settings. Also set `TWILIO_STATUS_CALLBACK_URL=<NGROK_URL>/webhook/twilio/status` in `.env`, so replies Twilio accepts but cannot deliver show up in `docker-compose logs gateway` as `whatsapp message not delivered` (with the error code and a hint, e.g. 63015 = re-join the sandbox).

### Running Locally (Development)

1. **Start infrastructure containers:**
   ```bash
   docker-compose up -d rabbitmq redis postgres
   # optional DB admin UI (opt-in profile; set PGADMIN_DEFAULT_PASSWORD first):
   docker-compose --profile tools up -d pgadmin
   ```

2. **Run Go microservices:** each reads `.env` from the working directory, so there `RABBITMQ_URL`, `REDIS_URL` and `DATABASE_URL` must point at `localhost` (the ports are published on `127.0.0.1`), using the same credentials as above — see `.env.example`.
   ```bash
   go run ./cmd/gateway
   go run ./cmd/orchestrator -migrate-only   # apply schema migrations first (P4-3)
   go run ./cmd/orchestrator
   go run ./cmd/proxy
   go run ./cmd/audit-worker
   ```

3. **Run Next.js Web Dashboard:**
   ```bash
   cd dashboard
   npm install
   npm run dev
   ```

### Upgrade note: RabbitMQ 3.13 → 4.3 (P5-4)

The broker now runs `rabbitmq:4.3.7` with a fixed `hostname: rabbitmq`. Previously the node name came from the random container ID, so the first start after this change is a **fresh, empty node** (exchanges/queues are re-declared by the services on startup). Messages still sitting in the old node are not migrated:

1. Stop ingestion: `docker compose stop gateway`.
2. Let the orchestrator drain `telegram.inbound`; check `telegram.inbound.dlq` / `audit.events.dlq` at `http://localhost:15672` and export anything you want to keep.
3. `docker compose up -d` — the new broker starts; from now on its data survives container recreation.

### Upgrade note: inbound queue renamed (BL-5)

The inbound queue is now `inbound.messages` (it was `telegram.inbound`, but it also carries WhatsApp). Before upgrading a running stack: `docker compose stop gateway`, wait until `telegram.inbound` shows 0 messages at `http://localhost:15672`, then `docker compose up -d`. Afterwards the empty `telegram.inbound` and `telegram.inbound.dlq` can be deleted in the management UI.

---

## Testing & Quality Verification

MyPA includes a comprehensive, multi-tier automated test suite covering unit logic, data races, database repository integration, GraphQL resolvers, frontend React components, and multi-stage container builds:

```bash
# 1. Run all unit tests with Go data race detector
go test -v -race ./internal/...

# 2. Run database repository and GraphQL resolver integration tests
go test -v ./internal/db/... ./internal/graph/...

# 3. Run Next.js Dashboard Vitest component tests (33 tests across 4 test suites)
npm test --prefix dashboard

# 4. Verify full multi-stage container build
docker-compose build

# 5. Run end-to-end integration tests (requires Docker daemon)
go test -v ./tests/...
```

Database tests need a running Docker daemon: each one gets its own empty database in a throwaway testcontainers Postgres (`internal/testutil/pgtest`, same image as production). They never connect to the compose database, and a test that tries to (compose DSN or `DATABASE_URL`) fails `TestNoTestReachesTheLiveDatabase`. Test containers must use the exact `docker-compose.yml` images (`TestTestContainerImagesMatchCompose`).

Phone numbers in committed files (examples, docs, fixtures) must be fictional: use `+1 NXX 555 0100–0199`, e.g. `+12025550123`. `TestNoRealPhoneNumbersCommitted` fails on any tracked number that could reach a person, and prints it masked.

**Continuous integration** (`.github/workflows/ci.yml`, on push/PR to `dev` and `master`): Go build + vet + `go test -race -p 1 ./...`, golangci-lint v2.15.0 (config in `.golangci.yml`), `govulncheck`, the dashboard (`npm ci`, lint, test, build, `npm audit --omit=dev --audit-level=high`), and `docker compose build` (with `.env.example` copied to `.env`). Run the linter locally with `docker run --rm -v "$PWD:/app" -w /app golangci/golangci-lint:v2.15.0 golangci-lint run ./...`.

To remove local build artefacts (root `*.exe`/`*.test`, `bin/`, coverage files): `powershell -File scripts/clean.ps1` (Windows) or `sh scripts/clean.sh`. Build outputs belong in `bin/` (gitignored).

When you change a tool declaration, regenerate the catalogue: `go run ./cmd/devtools/gendocs` (CI fails if the generated sections of `SYSTEM.md`/`README.md` are stale).

---

## License

[MIT License](LICENSE)
