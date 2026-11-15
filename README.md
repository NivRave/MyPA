# MyPA — Multi-Channel Personal & Family AI Operating System

An event-driven, multi-tenant personal and family AI operating system accessible via **WhatsApp**, **Telegram**, and a **Next.js 16 (React 19) Web Control Dashboard**. Built in Go and powered by Google Gemini tool-calling, PostgreSQL (`pgvector`), Redis, and RabbitMQ, MyPA combines core productivity integrations (Google Calendar, Gmail, Google Tasks, Web Search, Voice & Vision) with specialized life-domain intelligence across **Baby & Child Care**, **House & Vehicle Assets**, **Finance & Budgets**, **Shared Family Spaces**, **Topic-Mapped Memory & Living Entity Profiles**, **On-Demand External APIs & Event-Driven Automations**, and **Transactional Durability with Conversational Undo (`/undo`)**.

---

## Architecture

```text
+-----------------------+     +------------------------+
| Telegram Bot Webhook  |     | Twilio WhatsApp Webhook|
+-----------------------+     +------------------------+
            \                             /
             \                           /
              v                         v
       +---------------------------------------+
       |             ngrok (:4040)             |
       +---------------------------------------+
                           |
                           v
       +---------------------------------------+
       |             Proxy (:8000)             |
       |  - Unified reverse proxy edge gateway |
       +---------------------------------------+
             /                         \
            /                           \  /graphql (Auth Guard)
           v                             v
+-----------------------+      +-----------------------------------------------------------+
|    Gateway (:8080)    |      |                   Orchestrator (:8081)                    |
| - Secret token check  |      | - Pre-Auth Rejection & Admin RBAC (/backup, /restore)     |
| - Twilio HMAC verify  |      | - 70 Modular ToolModules across 13 Domain Packages        |
| - MaxBytesReader limit|      | - Two-Tier Intent Tool Grouping (8 Core + Active Tier 2)  |
+-----------------------+      | - Bounded Multi-Turn Recursion (max 8) & Concurrent Tools |
           |                   | - Dual-Lane EventBus (Sync Durability + 4 Async Workers)  |
           |                   | - GraphQL Server (28 Queries/Mutations via Domain Repos)  |
           v                   +-----------------------------------------------------------+
+-----------------------+             |                            |                       ^
|   RabbitMQ (:5672)    |             |                            |                       |
| - SafePublisher mutex |             v                            v                       |
| - mypa.dlx & DLQs     |     +-------------------+       +--------------------+           |
| - 3 bounded retries   |     |   Redis 7 (:6379) |       | PostgreSQL (:5432) |           |
+-----------------------+     | - Encrypted tokens|       | - MaxOpenConns(25) |           |
      |         |             | - 32B CSRF nonces |       | - halfvec(3072)    |           |
      |         |             | - Stripped photos |       |   HNSW indexes     |           |
      v         v             | - Prompt cache    |       | - 6 Composite      |           |
+-----------------------+     +-------------------+       |   B-Tree indexes   |           |
| Audit Worker          |                                 +--------------------+           |
| - 30-day archiving    |                                                                  |
+-----------------------+                                                                  |
                                                                                           |
                                       +-----------------------------------+               |
                                       |      Next.js Dashboard (:3030)    |               |
                                       | - Zero direct pg.Pool connections |               |
                                       | - Centralized graphql.ts client   |---------------+
                                       | - Clean empty states, 0 mock data |
                                       | - Vitest test suite (31 tests)    |
                                       +-----------------------------------+
```

### Core Architectural Pillars
- **70 Modular `ToolModule` Plugins (`internal/orchestrator/registry.go`)**: 28 migrated base workspace tools + 42 domain tools across 8 life domains with dynamic Two-Tier Intent Routing (8 Tier 1 Core Tools + Tier 2 matched domain groups based on intent classification, reducing prompt tokens by ~75%).
- **Bounded Recursion & Concurrent Tool Execution (`internal/orchestrator/engine.go`)**: Multi-turn tool calls capped at `MaxToolRecursionDepth = 8` iterations with Gemini natural-language summary fallback, and parallel tool executions run concurrently via `errgroup.Group` bounded to `MaxConcurrentTools = 8`.
- **Dual-Lane `EventBus` (`internal/events/bus.go`)**: Synchronous lane for ACID durability logging and instant prompt cache eviction, paired with a 4-worker asynchronous pool (1,024-event queue) for cross-user partner notifications and external automation webhooks, with deterministic FIFO ordering and race-free `closeMu sync.RWMutex` draining.
- **Production PostgreSQL Connection Pool & pgvector (`internal/db/`)**: Sized at `MaxOpenConns(25)` and `MaxIdleConns(10)`, featuring `((embedding::halfvec(3072)) halfvec_cosine_ops)` HNSW vector indexes and 6 high-volume composite multi-tenant B-Tree indexes.
- **Enterprise RabbitMQ Topology (`internal/broker/`)**: Mutex-serialized `SafePublisher` with publisher confirms (`ch.Confirm(false)`), auto-reconnect supervisor, declarative DLQ topology (`mypa.dlx`), and 3-attempt bounded retries with poison-pill rejection.
- **Decoupled Next.js Dashboard**: Direct database access (`pg.Pool`) completely eliminated in favor of a centralized `graphql.ts` client routed through `http://proxy:8000/graphql`, clean empty states without mock data, client error boundaries, and 31 Vitest component tests.


---

## Features & Life Domains

### 1. Core Productivity & Multi-Modal Channels
- **Multi-Channel Messaging**: Native support for **WhatsApp** (via Twilio with automatic ≤1,500-char smart chunking and document attachments) and **Telegram**.
- **Voice & Multimodal Vision**: Automatic voice note transcription via Groq Whisper API and Gemini vision analysis for receipts, flyers, and documents.
- **Google Workspace Suite**: Full CRUD for **Google Calendar** (single/recurring events, conflict checks), **Gmail** (search, read, draft, label, archive, unsubscribe flows), and **Google Tasks** (multi-list task management).
- **Real-Time Web Search**: Live web search and summarization via the **Tavily Search API**.
- **Proactive Morning Briefings & Custom Macros**: Cron-scheduled daily briefings and user-defined recurring natural language workflows (`schedule_workflow`).

### 2. Durability, State Fallback & Conversational Rollback (Topic 1)
- **Encrypted OAuth & State Fallback**: Dual-write persistence wrapping Redis state with AES-256-GCM encrypted PostgreSQL storage (`EncryptedCredential`) so sessions survive cache restarts.
- **Snapshot Mutation Log & `/undo`**: Records `BeforeJSON` and `AfterJSON` snapshots for every AI tool action. Revert mistakes via chat (`undo_last_action` / `/undo`) or one-click rollback in the Web Dashboard (`rollbackMutation`).
- **Multi-Format Data Export**: Export any topic's records (`baby`, `house`, `finance`, `memory`, `schedule`) as formatted JSON or RFC 4180 CSV (`export_topic_data`).

### 3. Topic-Mapped Memory & Living Knowledge Graph (Topic 2)
- **Hierarchical Topic Memories**: Organizes facts by `topic` (`baby`, `house`, `finance`, `work`, `personal`, `family`), `subtopic`, and `tags` (`save_topic_memory`, `search_topic_memories`).
- **Automatic Conflict Superseding**: Updating an existing fact automatically versions history via `SupersededByID` and `ValidUntil` timestamps rather than overwriting or duplicating facts.
- **Living Entity Profiles**: Structured JSONB profile cards (`EntityProfile`) for key entities (`upsert_entity_profile`, `get_entity_profile`) plus background memory consolidation (`ConsolidateMemories`).

### 4. Multi-Schedule Engine & Virtual Sub-Calendars (Topic 3)
- **Virtual `[Topic]` Sub-Calendars**: Maps life topics to color-coded `[Topic]` prefixes on a single primary Google Calendar (`schedule_topic_event`, `list_topic_schedule`, `configure_topic_calendar`).
- **Internal-Only High-Frequency Schedules**: Supports `sync_to_google = false` for high-frequency routines (e.g., infant feeding intervals) so the primary Google Calendar remains uncluttered.
- **Bidirectional Partner Calendar Sync**: Shared topic events automatically sync to connected partner Google Calendars (`PartnerGoogleEventID`).

### 5. Baby & Family Care Tracker (Topic 4)
- **Growth & Care Logging**: Track feedings (breast/bottle/solids), sleep windows, diapers, temperature, measurements, and developmental milestones (`upsert_baby_profile`, `log_baby_care`, `query_baby_logs_and_trends`).
- **Dynamic Age & Wonder Weeks Engine**: Automatically injects exact baby age (days/weeks/months with prematurity adjustment) and upcoming developmental leaps/checkups into the system prompt (`get_baby_milestone_suggestions`).
- **Pediatric Medication Safety Guardrails**: Enforces minimum cooldown intervals (4h for Paracetamol, 6h for Ibuprofen), warns against Ibuprofen under 6 months, and flags urgent pediatric fever thresholds under 3 months.
- **Supply Inventory**: Monitor formula, diapers, and medication stock with low-supply alerts (`manage_baby_inventory`).

### 6. House Maintenance, Contracts & Vehicle Vault (Topic 5 & 8.1)
- **Contract, Lease & Warranty Vault**: Store leases, mortgages, warranties, insurance policies, and repair receipts with advance renewal/cancellation notice alerts (`save_house_contract_or_receipt`, `list_house_contracts`).
- **Preventive Home Maintenance**: Interval-based and seasonal maintenance scheduling for HVAC, plumbing, appliances, and pest control (`manage_home_maintenance`).
- **Vehicle Asset Management**: Track license plates, annual test/inspection dates, insurance expiries, mileage, and service intervals (`manage_vehicle_asset`).

### 7. Financial Tracking & Budget Intelligence (Topic 6)
- **Expense & Income Ledger**: Log transactions from chat, receipt photos, or emails (`log_financial_transaction`, `get_financial_summary`).
- **Category Budgets & Proactive Alerts**: Configure monthly category limits with customizable warning thresholds (`set_category_budget`).
- **Historical Anomaly Detection**: Automatically flags transactions that exceed the historical merchant/category average by >30%.
- **Recurring Bills & Subscriptions**: Track monthly and annual bills with auto-renew status (`manage_recurring_bills`).

### 8. On-Demand External APIs, Automations & Personal CRM (Topics 7, 8.2 & 8.3)
- **On-Demand REST/Webhook Connectors**: Register custom external APIs at runtime with AES-256-GCM encrypted auth headers and multi-layer SSRF protection blocking private CIDRs, localhost, and cloud metadata endpoints (`register_external_api`, `list_external_apis`, `call_external_api`).
- **Event-Driven `When -> If -> Then` Automations**: Reactive rules engine triggered by `MutationEvent` bus publications (`create_automation_rule`, `list_automation_rules`, `delete_automation_rule`).
- **Runtime Persona & Preference Customization**: Configure preferred language, tone/persona, default currency, quiet hours, and standing instructions (`update_user_preferences`, `get_user_preferences`).
- **Personal CRM & Gift Tracker**: Store relationships, birthdays/anniversaries (with 30-day lookahead alerts), clothing sizes, and gift ideas (`upsert_personal_crm`, `query_personal_crm`).

### 9. Shared Multi-User Spaces & Partner Sync (Topic 9)
- **Granular Topic Sharing**: Configure per-topic family permissions (`CanView`, `CanEdit`) and notification modes (`instant`, `digest`, `silent`) (`configure_shared_topic`).
- **Cross-User Notification Router**: Instant partner notifications on WhatsApp/Telegram whenever a shared topic (`baby`, `house`, `finance`) is updated (`"📢 [Shared • Baby] ..."`).
- **Shared Reminder Hand-Offs**: Create and claim/complete family tasks (`create_shared_reminder`, `complete_shared_reminder`, `list_shared_activity`)—completing a task automatically notifies the partner and prevents duplicate alerts.

---

## Web Control Dashboard (`:3030`)

The Next.js 16 Dashboard communicates with the Orchestrator's `gqlgen` GraphQL API via the Proxy (`http://localhost:8000/graphql`) and provides dedicated interactive consoles:

| Route | Console Description |
|---|---|
| `/` | Overview KPIs, system health, user pairing codes, OAuth status, and global settings |
| `/memory` | **Memory Explorer**: Browse/filter topic-isolated memories and Living Entity Profile cards |
| `/shared` | **Shared Spaces**: Manage family topic permissions, notification modes, and shared tasks |
| `/baby` | **Baby Tracker**: Feeding/sleep/diaper logs, pediatric milestones, and supply inventory |
| `/house` | **House & Assets**: Active contracts/warranties, home maintenance tasks, and vehicle vault |
| `/finance` | **Finance & Budgets**: Transaction ledger, monthly category budget bars, and recurring bills |
| `/automations` | **Automations & CRM**: External API connectors, event-driven rules, and Personal CRM |
| `/audit` | **Audit & Rollback**: Full mutation log with `BeforeJSON`/`AfterJSON` inspection and one-click Rollback |

---

## Security Architecture & Defense-in-Depth

MyPA enforces defense-in-depth security across all edge ingress, compute, data persistence, and outbound integration layers:

1. **NetGuard SSRF Defense (`internal/netguard`):**
   - All outbound HTTP requests (`fetch_webpage`, `unsubscribe_email`, `call_external_api`, `register_external_api`) execute through `netguard.SafeClient(timeout)`.
   - Pre-connection DNS resolution blocks IPv4/IPv6 loopback (`127.0.0.0/8`, `::1`), RFC1918 private subnets (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`), cloud metadata IP (`169.254.169.254`), carrier-grade NAT (`100.64.0.0/10`), link-local multicast, and internal Docker single-label container names.
   - Redirect targets are inspected and validated prior to each hop.

2. **Webhook Ingress Signatures & Edge Protection (`cmd/gateway`, `internal/twilio`):**
   - **Telegram:** Enforces `X-Telegram-Bot-Api-Secret-Token` matching `TELEGRAM_SECRET_TOKEN` using constant-time comparison (`subtle.ConstantTimeCompare`). Fails closed: the gateway refuses to start if Telegram is configured without a secret, and rejects every update when no secret is set. On startup the gateway registers `TELEGRAM_WEBHOOK_URL` with Telegram via `setWebhook` (including `secret_token`), retrying with backoff.
   - **Twilio WhatsApp:** Calculates and verifies HMAC-SHA1 signatures (`X-Twilio-Signature`) across full request URLs and sorted parameters using `TWILIO_AUTH_TOKEN`. Fails closed: the gateway refuses to start if `TWILIO_ACCOUNT_SID` is set without `TWILIO_AUTH_TOKEN`, and rejects every WhatsApp webhook when no auth token is set.
   - **Edge Proxy:** Rejects unauthenticated `/graphql` calls at the reverse proxy layer, restricts `/playground` in production, and bounds HTTP request bodies with `http.MaxBytesReader` (10MB gateway, 1MB GraphQL).

3. **GraphQL Authentication & Tenant Authorization (`internal/graph/auth_middleware.go`):**
   - Rejects unauthenticated `/graphql` requests with HTTP 401.
   - Validates `Authorization: Bearer <token>` via constant-time `DASHBOARD_API_SECRET` check (an admin scoped to `DASHBOARD_FAMILY_GROUP` only) or HS256 Bearer JWT claims (`user_id`, `role`, `family_group`, mandatory `exp` ≤ 24 h, `alg` must be `HS256`).
   - Resolvers strictly enforce positive tenant authorization (`resolveTenantAndActor`) preventing cross-tenant IDOR access.

4. **Cryptographic Key Management & Storage Encryption (`internal/crypto/aes.go`):**
   - Encrypts OAuth credentials in Redis (`token:<userID>`) and PostgreSQL fallback (`encrypted_credentials`), as well as API connector authorization headers, using AES-256-GCM.
   - Employs HKDF-SHA256 salted key derivation (`[16-byte Salt][12-byte Nonce][Ciphertext + 16-byte Tag]`) with automatic backward compatibility for legacy unsalted payloads.
   - Fails fast on application startup: every service runs `cfg.Validate(<service>)` and exits with a list of all missing or placeholder secrets (e.g. `ENCRYPTION_KEY`, `JWT_SECRET`). The dev fallback encryption key exists only inside `go test` binaries.

5. **OAuth CSRF State Defense:**
   - Generates 32-byte cryptographically random hex nonces stored in Redis (`oauth_state:<nonce> -> userID`) with a 10-minute TTL.
   - The `/auth/google/callback` endpoint atomically validates and deletes the nonce via `GETDEL` before exchanging authorization codes.

6. **Positive Tenant Scoping & Anti-IDOR Defense:**
   - Multi-tenant database queries enforce compound scoping: `WHERE ((family_group = ? AND family_group != '') OR user_id = ?)`.
   - Modifying and deleting records verifies ownership positively:
     `isAuthorized := record != nil && ((familyGroup != "" && record.FamilyGroup == familyGroup) || (record.UserID != "" && record.UserID == userID))`
   - Prohibits tool arguments from overriding caller tenant boundaries (`ActorID()` and `TenantFamilyGroup()`).

7. **Prompt Injection Boundaries & Sensitive-Tool Gates:**
   - Content from emails (read and search), web pages and web search, external APIs, images/PDFs and documents is demarcated as untrusted (`<untrusted_external_content …>`); attacker-supplied closing tags are neutralized.
   - After untrusted content is ingested in a turn, sensitive tools (preferences, automations, workflows, external APIs, deletions, unsubscribes, shared reminders, memory writes) require explicit confirmation (`CONFIRM <TOOL_NAME>`).
   - Stored standing instructions and memories are presented to the model as user data, never as system-level requirements.

---

## Environment Variables Reference

| Variable | Description | Required? | Default / Example |
| :--- | :--- | :---: | :--- |
| **`ENCRYPTION_KEY`** | Primary secret for HKDF-SHA256 salted AES-256-GCM encryption at rest; the orchestrator refuses to start if it is missing. | **Yes** | `openssl rand -hex 32` |
| **`JWT_SECRET`** | HMAC-SHA256 secret for dashboard JWTs on `/graphql`. Must differ from `ENCRYPTION_KEY`. | **Yes** | `openssl rand -hex 32` |
| **`DASHBOARD_API_SECRET`** | Secret bearer token used by the Next.js Web Dashboard and Proxy to authenticate requests to `/graphql`. | **Yes** | `super-secret-dashboard-key` |
| **`DASHBOARD_FAMILY_GROUP`** | Family group the dashboard manages; the dashboard secret is an admin of this family only. Use the family group name from `ALLOWED_USERS`. | **Yes** | `TheFamily` |
| **`TELEGRAM_SECRET_TOKEN`** | Webhook secret token verified via `X-Telegram-Bot-Api-Secret-Token` header on Telegram ingress. 1–256 chars of `A-Z a-z 0-9 _ -` (e.g. `openssl rand -hex 32`). Gateway refuses to start without it when Telegram is configured. | **Yes** | `random-webhook-secret-token` |
| `TELEGRAM_WEBHOOK_URL` | Public URL of `/webhook/telegram`. When set, the gateway calls `setWebhook` with this URL and `TELEGRAM_SECRET_TOKEN` on startup. | If TG active | `https://your-domain.ngrok-free.dev/webhook/telegram` |
| **`TWILIO_AUTH_TOKEN`** | Twilio Auth Token used to calculate and verify HMAC-SHA1 signatures (`X-Twilio-Signature`) on WhatsApp webhooks. Gateway refuses to start without it when `TWILIO_ACCOUNT_SID` is set. | **Yes** | `twilio-account-auth-token` |
| **`RABBITMQ_URL`** | AMQP 0-9-1 connection string for RabbitMQ broker. | **Yes** | `amqp://user:password@rabbitmq:5672/` |
| **`DATABASE_URL`** | PostgreSQL connection string (`postgres://...`). | **Yes** | `postgres://mypa:mypa@postgres:5432/mypa?sslmode=disable` |
| **`REDIS_URL`** | Redis connection string (`redis://...`). | **Yes** | `redis://redis:6379/0` |
| `ALLOWED_USERS` | Bootstrap allowed users formatted as `"platform_id:Name:Role:FamilyGroup"`. | No | `123456789:Admin:admin:TheFamily` |
| `DEFAULT_TIMEZONE` | Default IANA timezone used for system prompts and localized datetime parsing. | No | `"Asia/Jerusalem"` / `"UTC"` |
| `LOG_LEVEL` | `debug` / `info` / `warn` / `error`. Message text is only logged at `debug`. | No | `info` |
| `BACKUP_AGE_RECIPIENTS` | age public key(s) (`age1…`, comma-separated) used to encrypt nightly and `/backup` dumps. Generate a pair with `age-keygen -o backup.key`. | Recommended | `age1…` |
| `BACKUP_AGE_IDENTITY_FILE` | Path to the matching age private key, only needed to restore an encrypted backup. Keep it off the server. | For restore | `/run/secrets/backup.key` |
| `WEEK_START_DAY` | First day of the week used when the assistant reasons about "this/next week". | No | `Sunday` |
| `WEEKEND_DAYS` | Comma-separated weekend days. | No | `Friday,Saturday` |
| `GRAPHQL_ENDPOINT` | Upstream GraphQL URL used by Next.js server actions and SSR data fetching. | No | `http://proxy:8000/graphql` |
| `GEMINI_API_KEY` | Google AI Studio API key for Gemini chat and tool-calling models. | **Yes** | `AIzaSy...` |
| `GEMINI_MODEL` | Primary Gemini model identifier. | No | `"gemini-2.5-flash"` |
| `TELEGRAM_BOT_TOKEN` | Telegram Bot token obtained from [@BotFather](https://t.me/BotFather). | If TG active | `123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11` |
| `TWILIO_ACCOUNT_SID` | Twilio Account SID for WhatsApp messaging. | If WA active | `ACXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX` |
| `TWILIO_FROM_NUMBER` | Twilio WhatsApp sender number. | If WA active | `whatsapp:+14155238886` |
| `GOOGLE_CLIENT_ID` | Google OAuth2 Client ID for Workspace tools (Calendar, Gmail, Tasks). | If OAuth active | `...apps.googleusercontent.com` |
| `GOOGLE_CLIENT_SECRET` | Google OAuth2 Client Secret. | If OAuth active | `GOCSPX-...` |
| `GOOGLE_REDIRECT_URL` | OAuth2 callback URL for Google integration. | If OAuth active | `https://your-domain/auth/google/callback` |
| `GROQ_API_KEY` | Groq API key for Groq Whisper (`whisper-large-v3`) voice note transcription. | No | `gsk_...` |
| `TAVILY_API_KEY` | Tavily API key for real-time web research (`search_web`). | No | `tvly-...` |
| `AUDIT_ARCHIVE_DIR` | Directory path for persisting 30-day session archive JSONL files in `audit-worker`. | No | `"/app/archives"` |
| `NGROK_AUTHTOKEN` | Ngrok authentication token for public ingress tunneling. | No | `ngrok-token` |
| `NGROK_DOMAIN` | Reserved static Ngrok domain tunneling to `proxy:8000`. No default — ngrok won't start without it. | **Yes** (with ngrok) | `your-domain.ngrok-free.dev` |

---

## Prerequisites

- [Go 1.24+](https://go.dev/dl/)
- [Docker & Docker Compose](https://docs.docker.com/get-docker/)
- [ngrok Account & Auth Token](https://ngrok.com/) (for webhook ingress tunneling)
- Telegram Bot Token (from [@BotFather](https://t.me/BotFather))
- Twilio Account SID & Auth Token (for WhatsApp integration)
- Gemini API Key (from [Google AI Studio](https://aistudio.google.com/))
- Groq API Key (from [GroqCloud](https://console.groq.com/))
- Tavily API Key (from [Tavily](https://tavily.com/))
- Google Cloud OAuth credentials (for Calendar, Gmail, and Tasks APIs)

---

## Quick Start

### Running with Docker (Recommended)

1. **Clone and configure environment:**
   ```bash
   cp .env.example .env
   # Edit .env with your API keys, OAuth credentials, and security secrets (ENCRYPTION_KEY, TELEGRAM_SECRET_TOKEN, DASHBOARD_API_SECRET)
   ```

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
   - **WhatsApp:** Set `<NGROK_URL>/webhook/twilio` (e.g., `https://your-domain.ngrok-free.dev/webhook/twilio`) in your Twilio WhatsApp Sandbox settings.

### Running Locally (Development)

1. **Start infrastructure containers:**
   ```bash
   docker-compose up -d rabbitmq redis postgres
   # optional DB admin UI (opt-in profile; set PGADMIN_DEFAULT_PASSWORD first):
   docker-compose --profile tools up -d pgadmin
   ```

2. **Run Go microservices:**
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

---

## Testing & Quality Verification

MyPA includes a comprehensive, multi-tier automated test suite covering unit logic, data races, database repository integration, GraphQL resolvers, frontend React components, and multi-stage container builds:

```bash
# 1. Run all unit tests with Go data race detector
go test -v -race ./internal/...

# 2. Run database repository and GraphQL resolver integration tests
go test -v ./internal/db/... ./internal/graph/...

# 3. Run Next.js Dashboard Vitest component tests (31 tests across 4 test suites)
npm test --prefix dashboard

# 4. Verify full multi-stage container build
docker-compose build

# 5. Run end-to-end integration tests (requires Docker daemon)
go test -v ./tests/...
```

---

## Services

| Service | Port | Description |
|---|---|---|
| `proxy` | `8000` | Unified API Gateway routing webhooks to `gateway` and `/graphql` to `orchestrator` |
| `gateway` | `8080` | Telegram & Twilio WhatsApp webhook ingestion and RabbitMQ publisher |
| `orchestrator` | `8081` | Gemini reasoning engine, `ToolModule` registry, `EventBus`, OAuth, GraphQL API, and Cron Scheduler |
| `audit-worker` | N/A | Asynchronous RabbitMQ consumer persisting system audit logs to PostgreSQL |
| `dashboard` | `3030` | Next.js 16 (React 19) web application for multi-domain management and audit rollback |
| `rabbitmq` | `5672` | Message broker (`15672` management UI) |
| `redis` | `6379` | Hot conversation state and OAuth token cache |
| `postgres` | `5432` | PostgreSQL 15 with `pgvector` for domain tables, encrypted credentials, and semantic memory |
| `pgadmin` | `5050` | Web interface for PostgreSQL administration (opt-in: `--profile tools`) |
| `ngrok` | `4040` | Public webhook ingress tunnel and traffic inspection UI |

---

## Project Structure

```text
dashboard/                  # Next.js 16 (React 19) Web Control Dashboard (App Router)
  src/app/
    audit/                  # Mutation audit log & one-click rollback console
    automations/            # External APIs, EventBus rules & Personal CRM console
    baby/                   # Baby care logs, milestone leaps & inventory console
    finance/                # Transactions, category budgets & anomalies console
    house/                  # Contracts/warranties, home maintenance & vehicles console
    memory/                 # Topic memory explorer & living entity profiles console
    shared/                 # Multi-user family spaces & shared reminders console
cmd/
  audit-worker/             # Asynchronous audit logging worker
  gateway/                  # Telegram & WhatsApp webhook ingestion service
  orchestrator/             # LLM reasoning, domain modules, GraphQL & scheduler service
  proxy/                    # Unified reverse proxy & API gateway
internal/
  audio/                    # Groq Whisper API client for voice transcription
  broker/                   # RabbitMQ publisher/consumer
  calendar/                 # Google Calendar API + OAuth
  config/                   # Viper configuration loader
  crypto/                   # AES-256-GCM encryption at rest (HKDF-SHA256 salted derivation)
  db/                       # PostgreSQL + pgvector client, dynamic migrations & domain repos
  domains/                  # Modular ToolModule implementations (70 tools / 13 modules)
    automation/             # External REST connectors (SSRF-guarded), rules runner & CRM
    baby/                   # Baby tracker, Wonder Weeks milestones & medication guardrails
    durability/             # Encrypted token fallback, mutation log, undo & CSV/JSON export
    finance/                # Expense/income ledger, budgets & >30% anomaly detection
    house/                  # Contracts/warranties, preventive maintenance & vehicle vault
    memory/                 # Topic-mapped memory v2, living entities & consolidation
    schedule/               # Virtual [Topic] sub-calendars & partner Google Calendar sync
    shared/                 # Family shared spaces, shared reminders & partner notifier
    toolutil/               # Centralized typed argument parsing & timezone normalization
    web_research/           # Web search (Tavily) & SSRF-guarded webpage scraper
    workspace_calendar/     # Google Calendar workspace CRUD tools
    workspace_contacts/     # Google Contacts workspace CRUD tools
    workspace_gmail/        # Gmail workspace CRUD & unsubscribe tools
    workspace_tasks/        # Google Tasks workspace CRUD tools
  events/                   # Thread-safe dual-lane MutationEvent pub/sub bus
  gmail/                    # Gmail API integration & automation flows
  graph/                    # gqlgen GraphQL schema, generated server & resolvers
  llm/                      # Gemini API client & base tool declarations
  models/                   # Shared domain & GORM database models
  netguard/                 # SSRF defense validator blocking private CIDRs & metadata
  orchestrator/             # Multi-turn agent engine & dynamic ToolModule Registry
  scheduler/                # Cron scheduler & proactive morning briefings
  state/                    # Redis state management
  tasks/                    # Google Tasks API integration
  tavily/                   # Tavily Web Search API client
  telegram/                 # Telegram Bot API client
  twilio/                   # Twilio WhatsApp API client, chunker & media handler
docs/
  FEATURES_EXPANSION.md     # Architectural specification for Topics 1–9
  ROADMAP.md                # Release history & V3/V4 roadmap
  architecture_deep_dive.md # End-to-end architecture & data-flow reference
  architecture_v4.md        # V4 enterprise architecture vision
AGENTS.md                   # Global AI agent development rules & guardrails
SYSTEM.md                   # Canonical system architecture, DB/storage schema, API & feature playbook
```

---

## License

[MIT License](LICENSE)
