# MyPA — Multi-Channel Personal & Family AI Operating System

An event-driven, multi-tenant personal and family AI operating system accessible via **WhatsApp**, **Telegram**, and a **Next.js 16 (React 19) Web Control Dashboard**. Built in Go and powered by Google Gemini tool-calling, PostgreSQL (`pgvector`), Redis, and RabbitMQ, MyPA combines core productivity integrations (Google Calendar, Gmail, Google Tasks, Web Search, Voice & Vision) with specialized life-domain intelligence across **Baby & Child Care**, **House & Vehicle Assets**, **Finance & Budgets**, **Shared Family Spaces**, **Topic-Mapped Memory & Living Entity Profiles**, **On-Demand External APIs & Event-Driven Automations**, and **Transactional Durability with Conversational Undo (`/undo`)**.

---

## Highlights

- **Channels:** Telegram and WhatsApp (Twilio), voice notes (Groq Whisper) and images/documents (Gemini vision); a Next.js web dashboard for browsing, editing and rolling back data, which each owner or adult signs in to with `/dashboard` in chat (a link valid 10 minutes, once). One person can use several apps as the same user with the same data: `/link` in chat adds another account (confirmed from the first), `/accounts` lists them and `/notify N` picks where notifications go.
- **Tools:** <!-- gen:tool-summary -->63 tools across 13 modules<!-- /gen:tool-summary -->, offered in two tiers (always-on core tools + domain groups selected per message) to keep prompts small. Full catalogue: [SYSTEM.md §6](./SYSTEM.md#6-complete-ai-agent-tool-catalog).
- **Google Workspace:** Calendar, Gmail, Tasks and Contacts; live web search (Tavily).
- **Life domains:** baby care (with medication-cooldown guardrails), house contracts and maintenance, vehicles, finance and budgets, a personal CRM, topic-scoped memory and entity profiles, shared family spaces with partner notifications.
- **Automations:** event-driven rules, scheduled natural-language workflows, and runtime-registered external APIs behind SSRF protection.
- **Quiet hours and time zones:** reminders, the morning briefing and scheduled automations follow each household's time zone, and wait until a person's quiet hours end; a reminder marked urgent goes out anyway.
- **Feature packs:** Core (memory, reminders, calendar, tasks and contacts, web search, undo, family sharing, preferences and the personal CRM) is always on. Baby, House and vehicles, Finance, Gmail and Automations are packs an owner switches on or off per household on the dashboard's **Tools & Packs** page; a pack that is off adds no tools, chat guidance, briefing lines or pages, and keeps its data. A new household starts with Core only; an upgraded one keeps every pack it holds data in.
- **Durability:** every tool write is snapshotted for `/undo` and dashboard rollback; encrypted, age-protected nightly backups (database plus archive files); versioned schema migrations; one documented path to upgrade, move or restore an install.
- **Roles:** owner, adult, child and guest, plus the install's operator. Owners manage the household and its members and see each member's activity, never another member's chats or personal data; adults see the shared data and their own; children chat only. Pairing codes give adult or child. Guests (only the topics shared with them by name) wait for sharing by name (BL-46).
- **Health check:** the operator sends `/doctor` in chat and gets the state of the Telegram webhook, each family member's Google sign-in, the WhatsApp mode and its health, the queues, the newest backup and the database schema, with a next step for every problem.
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
| Upgrading, moving and restoring an install | [docs/RESTORE_AND_MIGRATE.md](./docs/RESTORE_AND_MIGRATE.md) |
| Roadmap | [docs/ROADMAP.md](./docs/ROADMAP.md) |
| Design decisions | [docs/DECISIONS_LOG.md](./docs/DECISIONS_LOG.md), [docs/adr/](./docs/adr/) |
| Known issues & follow-ups | [docs/BACKLOG.md](./docs/BACKLOG.md) |

---

## Prerequisites

- [Go 1.26+](https://go.dev/dl/) and [Node.js 24](https://nodejs.org/) (only for running services or the dashboard outside Docker)
- [Docker & Docker Compose](https://docs.docker.com/get-docker/)
- Gemini API key ([Google AI Studio](https://aistudio.google.com/))
- Telegram bot token ([@BotFather](https://t.me/BotFather)) and/or Twilio account (WhatsApp)
- Optional: a way to be reached from the internet, needed for WhatsApp but not for Telegram (a domain for a server with Caddy, Cloudflare Tunnel, Tailscale Funnel, your own reverse proxy, or ngrok), Google Cloud OAuth credentials (Workspace tools), Groq (voice), Tavily (web search)

---

## Quick Start

### Running with Docker (Recommended)

1. **Clone and configure environment:**
   ```bash
   cp .env.example .env
   # Edit .env with your API keys, OAuth credentials, and security secrets
   ```
   Required: `ENCRYPTION_KEY` (an `openssl rand -hex 32` value), `GEMINI_API_KEY`, and the infrastructure credentials `RABBITMQ_DEFAULT_USER`/`RABBITMQ_DEFAULT_PASS` and `POSTGRES_USER`/`POSTGRES_PASSWORD`/`POSTGRES_DB`; plus `TELEGRAM_SECRET_TOKEN` when Telegram receives webhooks and `TWILIO_AUTH_TOKEN` when `TWILIO_ACCOUNT_SID` is set. Recommended: `BACKUP_AGE_RECIPIENTS`, and `LINK_CODE_SECRET` (another `openssl rand -hex 32` value) to let people link a second app with `/link`. There is no dashboard secret any more (V4-35): `DASHBOARD_API_SECRET`, `DASHBOARD_FAMILY_GROUP` and `JWT_SECRET` are refused if still set, so remove them when you upgrade. The app and the audit worker validate their keys at startup and list anything missing. To create the first household and its owner, set `ALLOWED_USERS` before the first start; it is read only while no household exists, and after that people join with a pairing code (V4-30). Full reference: [SYSTEM.md §8.1](./SYSTEM.md#81-environment-variables-internalconfigconfiggo--internalcryptoaesgo).

   Docker builds each container's broker/Redis/database URL from those credentials and the service names (`rabbitmq`, `redis`, `postgres`); the `RABBITMQ_URL`, `REDIS_URL` and `DATABASE_URL` keys in `.env` are **ignored by Docker** and only used when you run a service on the host (below). To use managed services instead, set `EXTERNAL_DATABASE_URL`, `EXTERNAL_REDIS_URL` and `EXTERNAL_RABBITMQ_URL`.

   **Choose how MyPA is reached** (V4-40). The same files run on a home PC or a server; only `.env` changes:

   | You run | `INGRESS` | `PUBLIC_URL` | `COMPOSE_PROFILES` |
   | :--- | :--- | :--- | :--- |
   | Telegram only, at home, no tunnel | `none` (default) | *(empty)* | *(empty)* |
   | A server with your own domain (DNS pointing at it, ports 80 and 443 open) | `caddy` | `https://mypa.example.com` | `caddy` |
   | A Cloudflare Tunnel (`CLOUDFLARE_TUNNEL_TOKEN`; hostname and `http://app:8000` set in Cloudflare) | `cloudflare` | the tunnel's hostname | `cloudflare` |
   | Tailscale Funnel (`TAILSCALE_AUTHKEY`) | `tailscale` | `https://<TAILSCALE_HOSTNAME>.<tailnet>.ts.net` | `tailscale` |
   | Your own reverse proxy, forwarding to `127.0.0.1:8000` | `proxy` | your proxy's address | *(empty)* |
   | ngrok (`NGROK_AUTHTOKEN`) | `ngrok` | your reserved ngrok domain | `ngrok` |

   With `INGRESS=none` Telegram fetches messages itself (polling), and WhatsApp, which needs a public address, refuses to start. `PUBLIC_URL` is the one public address: the Telegram webhook, the WhatsApp webhooks and the Google sign-in redirect are all derived from it. On a domain you control it stays the same when you move between a home PC and a server, so nothing changes at Twilio or Google. The dashboard is never behind any of these.

2. **Build and start all services:**
   ```bash
   docker-compose up -d --build
   ```
   *(this starts one `app` container — webhooks, conversation turns, `/graphql` and the Google sign-in on port 8000 — plus the `audit-worker`, the `dashboard` and the infrastructure; the one-shot `migrate` service applies database migrations first, and the app and audit-worker start only after it succeeds)*
   *(the tunnel or proxy of your `INGRESS` starts with it, through `COMPOSE_PROFILES`; with ngrok its inspection UI is at `http://localhost:4040`. The Web Dashboard is at `http://localhost:3030`)*

   **Sign in to the dashboard** (V4-35): send `/dashboard` to the assistant in a direct chat and open the link on the computer MyPA runs on. It works once, for 10 minutes; the page signs you in when you press its button. A session lasts 24 hours without use and 7 days at most; `/signout` in chat ends every session. After an upgrade every former admin is an owner and the operator; if `/doctor` reports a household without an owner, run `docker compose run --rm app access grant owner <Telegram ID or phone number>`.

3. **Set Webhooks:**
   - **Telegram:** Automatic. With `INGRESS=none` the app polls (look for `telegram polling for updates` in `docker compose logs app`). With a public address it calls `setWebhook` for `PUBLIC_URL/webhook/telegram` with `TELEGRAM_SECRET_TOKEN` on startup (look for `telegram webhook registered with secret token`).
   - **WhatsApp:** Set `PUBLIC_URL/webhook/twilio` (e.g. `https://mypa.example.com/webhook/twilio`) in your Twilio WhatsApp Sandbox settings. Delivery reports go to `PUBLIC_URL/webhook/twilio/status` automatically, so replies Twilio accepts but cannot deliver show up in `docker compose logs app` as `whatsapp message not delivered` (with the error code and a hint, e.g. 63015 = re-join the sandbox).
     WhatsApp runs in one of two modes (`TWILIO_WHATSAPP_MODE`, V4-41). **Sandbox** (the default for Twilio's sandbox number): set `TWILIO_SANDBOX_JOIN_CODE` to the join code from the Twilio Console, so invitations tell new people to send `join <code>` first; Twilio drops a person 72 hours after they last wrote, and `/doctor` names who and what to send. **Sender** (any number you registered in Twilio): run `docker compose run --rm app channel-setup whatsapp` to see MyPA's reminder and briefing templates, add `--submit` to create and submit them in your Twilio account, then put the two `TWILIO_TEMPLATE_*_SID` lines it prints in `.env`; `/doctor` shows when WhatsApp has approved them. Switching is a settings change and a restart.
   - **Google:** register `PUBLIC_URL/auth/google/callback` as the OAuth redirect URI (or `http://localhost:8000/auth/google/callback` with no public address, which only works in a browser on the same machine).

### Running Locally (Development)

1. **Start infrastructure containers:**
   ```bash
   docker-compose up -d rabbitmq redis postgres
   # optional DB admin UI (opt-in profile; set PGADMIN_DEFAULT_PASSWORD first):
   docker-compose --profile tools up -d pgadmin
   ```

2. **Run the Go services:** each reads `.env` from the working directory, so there `RABBITMQ_URL`, `REDIS_URL` and `DATABASE_URL` must point at `localhost` (the ports are published on `127.0.0.1`), using the same credentials as above — see `.env.example`.
   ```bash
   go run ./cmd/mypa -migrate-only   # apply schema migrations first (P4-3)
   go run ./cmd/mypa                 # the app on :8000 (HTTP_PORT); default role: all
   go run ./cmd/audit-worker
   ```
   The app is one binary with roles (V4-01): `--role=all` (the default) runs everything in one process; `--role=ingress` only receives the webhooks and queues them, and `--role=worker` only processes the queue and serves `/graphql` and the Google sign-in. Split roles need your own reverse proxy in front: `/webhook/*` to the ingress, `/graphql` and `/auth/google/*` to the worker. Run one worker only (the scheduler is not leader-elected).

3. **Run Next.js Web Dashboard:**
   ```bash
   cd dashboard
   npm install
   npm run dev
   ```

### Upgrade note: one app container (V4-01)

`proxy`, `gateway` and `orchestrator` are now one `app` service (`cmd/mypa`), on the old proxy port 8000, so the public webhook and sign-in addresses do not change. When upgrading, follow [docs/RESTORE_AND_MIGRATE.md](./docs/RESTORE_AND_MIGRATE.md) section A: it stops the old services and starts V4 with `--remove-orphans`, so the old containers do not linger. In `.env`, `GATEWAY_URL` and `ORCHESTRATOR_URL` are no longer read and can be removed. Logs are now `docker compose logs app`.

### Upgrade note: one public address (V4-40)

`TELEGRAM_WEBHOOK_URL`, `TWILIO_STATUS_CALLBACK_URL`, `GOOGLE_REDIRECT_URL` and `NGROK_DOMAIN` are replaced by `INGRESS` and `PUBLIC_URL`, and the app refuses to start while one of them is still in `.env`. For an install that used ngrok: remove the four, and set `INGRESS=ngrok`, `PUBLIC_URL=https://<your ngrok domain>` and `COMPOSE_PROFILES=ngrok` (ngrok no longer starts without its profile). The addresses Telegram, Twilio and Google know do not change. Details: [docs/RESTORE_AND_MIGRATE.md](./docs/RESTORE_AND_MIGRATE.md) section D.

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

# 3. Run Next.js Dashboard Vitest component tests (63 tests across 8 test suites)
npm test --prefix dashboard

# 4. Verify full multi-stage container build
docker-compose build

# 5. Run end-to-end integration tests (requires Docker daemon)
go test -v ./tests/...
```

Database tests need a running Docker daemon: each one gets its own empty database in a throwaway testcontainers Postgres (`internal/testutil/pgtest`, same image as production). They never connect to the compose database, and a test that tries to (compose DSN or `DATABASE_URL`) fails `TestNoTestReachesTheLiveDatabase`. Test containers must use the exact `docker-compose.yml` images (`TestTestContainerImagesMatchCompose`).

The channel conformance suite (`internal/conformance`, part of `go test ./...`) runs the same scenarios on Telegram and on WhatsApp in both its modes over a fake transport and compares what each platform would receive with pinned transcripts in `testdata/golden`. If a change is meant to alter what users receive, regenerate them with `go test ./internal/conformance -update` and review the diff.

Phone numbers in committed files (examples, docs, fixtures) must be fictional: use `+1 NXX 555 0100–0199`, e.g. `+12025550123`. `TestNoRealPhoneNumbersCommitted` fails on any tracked number that could reach a person, and prints it masked.

**Continuous integration** (`.github/workflows/ci.yml`, on push/PR to `dev` and `master`): Go build + vet + `go test -race -p 1 ./...`, golangci-lint v2.15.0 (config in `.golangci.yml`), `govulncheck`, the dashboard (`npm ci`, lint, test, build, `npm audit --omit=dev --audit-level=high`), and `docker compose build` (with `.env.example` copied to `.env`). Run the linter locally with `docker run --rm -v "$PWD:/app" -w /app golangci/golangci-lint:v2.15.0 golangci-lint run ./...`.

To remove local build artefacts (root `*.exe`/`*.test`, `bin/`, coverage files): `powershell -File scripts/clean.ps1` (Windows) or `sh scripts/clean.sh`. Build outputs belong in `bin/` (gitignored).

When you change a tool declaration or a channel adapter's capabilities, regenerate the docs: `go run ./cmd/devtools/gendocs` (CI fails if the generated sections of `SYSTEM.md`/`README.md` are stale).

---

## License

[MIT License](LICENSE)
