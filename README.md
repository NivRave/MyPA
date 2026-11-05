# MyPA — Multi-Channel Personal & Family AI Operating System

An event-driven, multi-tenant personal and family AI operating system accessible via **WhatsApp**, **Telegram**, and a **Next.js 15 Web Control Dashboard**. Built in Go and powered by Google Gemini tool-calling, PostgreSQL (`pgvector`), Redis, and RabbitMQ, MyPA combines core productivity integrations (Google Calendar, Gmail, Google Tasks, Web Search, Voice & Vision) with specialized life-domain intelligence across **Baby & Child Care**, **House & Vehicle Assets**, **Finance & Budgets**, **Shared Family Spaces**, **Topic-Mapped Memory & Living Entity Profiles**, **On-Demand External APIs & Event-Driven Automations**, and **Transactional Durability with Conversational Undo (`/undo`)**.

---

## Architecture

```text
Telegram / WhatsApp Webhooks ──► ngrok (4040) ──► Proxy (8000) ──► Gateway (8080) ──► RabbitMQ
                                                       │                                  │
Next.js 15 Dashboard (3030)  ──────────────────────────┤                                  ▼
                                                       ▼                        Orchestrator (8081)
                                              GraphQL API (8081) ◄────────────────────────┤
                                              OAuth Callbacks (8081)                      ├──► Gemini API (Multi-Turn + Parallel Tools)
                                                                                          ├──► Modular ToolRegistry & Domain Modules
                                                                                          ├──► Internal MutationEvent Bus (Undo / Partner Sync / Rules)
                                                                                          ├──► Google Calendar, Gmail, Tasks & Tavily APIs
                                                                                          ├──► Redis (Conversation State & Hot Tokens)
                                                                                          └──► PostgreSQL + pgvector (Encrypted Fallback, Audit, Domains)
```

### Core Architectural Pillars
- **Modular `ToolModule` Registry (`internal/orchestrator/registry.go`)**: Domain modules self-register their Gemini `FunctionDeclaration` schemas, execution handlers, and dynamic per-turn system prompt injections.
- **Internal `MutationEvent` Bus (`internal/events/bus.go`)**: Every domain state change emits a typed `MutationEvent` with `BeforeJSON` and `AfterJSON` snapshots, simultaneously powering:
  1. **Conversational Undo & Audit Trail** (`internal/domains/durability`)
  2. **Instant Partner Notifications** across WhatsApp/Telegram (`internal/domains/shared`)
  3. **Reactive `When -> If -> Then` User Automations** (`internal/domains/automation`)
- **AES-256-GCM Encryption at Rest (`internal/crypto/aes.go`)**: Protects PostgreSQL-persisted OAuth credentials and external API connector authorization headers using PBKDF2/SHA-256 key derivation (`ENCRYPTION_KEY`).

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

The Next.js 15 Dashboard communicates with the Orchestrator's `gqlgen` GraphQL API via the Proxy (`http://localhost:8000/graphql`) and provides dedicated interactive consoles:

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
   *(ngrok starts automatically inside Docker using `NGROK_AUTHTOKEN` and `NGROK_DOMAIN` from `.env`, exposing the tunnel inspection UI at `http://localhost:4040` and the Web Dashboard at `http://localhost:3030`)*

3. **Set Webhooks:**
   - **Telegram:**
     ```bash
     curl "https://api.telegram.org/bot<YOUR_TOKEN>/setWebhook?url=<NGROK_URL>/webhook/telegram&secret_token=<TELEGRAM_SECRET_TOKEN>"
     ```
   - **WhatsApp:** Set `<NGROK_URL>/webhook/twilio` (e.g., `https://your-domain.ngrok-free.dev/webhook/twilio`) in your Twilio WhatsApp Sandbox settings.

### Running Locally (Development)

1. **Start infrastructure containers:**
   ```bash
   docker-compose up -d rabbitmq redis postgres pgadmin
   ```

2. **Run Go microservices:**
   ```bash
   go run ./cmd/gateway
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

## Testing

Unit tests cover all core packages and domain modules (`crypto`, `events`, `orchestrator`, `db`, `durability`, `memory`, `shared`, `schedule`, `baby`, `house`, `finance`, `automation`). End-to-End (E2E) tests use [Testcontainers](https://golang.testcontainers.org/).

```bash
# Run all unit and domain tests
go test -v ./internal/...

# Run E2E tests (requires Docker running)
go test -v ./tests/...

# Verify full multi-stage container build
docker-compose build
```

---

## Services

| Service | Port | Description |
|---|---|---|
| `proxy` | `8000` | Unified API Gateway routing webhooks to `gateway` and `/graphql` to `orchestrator` |
| `gateway` | `8080` | Telegram & Twilio WhatsApp webhook ingestion and RabbitMQ publisher |
| `orchestrator` | `8081` | Gemini reasoning engine, `ToolModule` registry, `EventBus`, OAuth, GraphQL API, and Cron Scheduler |
| `audit-worker` | N/A | Asynchronous RabbitMQ consumer persisting system audit logs to PostgreSQL |
| `dashboard` | `3030` | Next.js 15 web application for multi-domain management and audit rollback |
| `rabbitmq` | `5672` | Message broker (`15672` management UI) |
| `redis` | `6379` | Hot conversation state and OAuth token cache |
| `postgres` | `5432` | PostgreSQL 15 with `pgvector` for domain tables, encrypted credentials, and semantic memory |
| `pgadmin` | `5050` | Web interface for PostgreSQL administration |
| `ngrok` | `4040` | Public webhook ingress tunnel and traffic inspection UI |

---

## Project Structure

```text
dashboard/                  # Next.js 15 Web Control Dashboard (App Router)
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
  crypto/                   # AES-256-GCM encryption at rest (PBKDF2/SHA-256)
  db/                       # PostgreSQL + pgvector client, dynamic migrations & domain repos
  domains/                  # Modular ToolModule implementations (Topics 1–9)
    automation/             # External REST connectors (SSRF-guarded), rules runner & CRM
    baby/                   # Baby tracker, Wonder Weeks milestones & medication guardrails
    durability/             # Encrypted token fallback, mutation log, undo & CSV/JSON export
    finance/                # Expense/income ledger, budgets & >30% anomaly detection
    house/                  # Contracts/warranties, preventive maintenance & vehicle vault
    memory/                 # Topic-mapped memory v2, living entities & consolidation
    schedule/               # Virtual [Topic] sub-calendars & partner Google Calendar sync
    shared/                 # Family shared spaces, shared reminders & partner notifier
  events/                   # Thread-safe internal MutationEvent pub/sub bus
  gmail/                    # Gmail API integration & automation flows
  graph/                    # gqlgen GraphQL schema, generated server & resolvers
  llm/                      # Gemini API client & base tool declarations
  models/                   # Shared domain & GORM database models
  orchestrator/             # Multi-turn agent engine & dynamic ToolModule Registry
  scheduler/                # Cron scheduler & proactive morning briefings
  state/                    # Redis state management
  tasks/                    # Google Tasks API integration
  tavily/                   # Tavily Web Search API client
  telegram/                 # Telegram Bot API client
  twilio/                   # Twilio WhatsApp API client, chunker & media handler
docs/
  FEATURES_EXPANSION.md     # Architectural specification for Topics 1–9
  PLAN.md                   # Parallel wave implementation plan
  ROADMAP.md                # Release history & V3/V4 roadmap
AGENTS.md                   # Global AI agent development rules & guardrails
SYSTEM.md                   # Canonical system architecture, DB/storage schema, API & feature playbook
REVIEW.md                   # Senior Principal Engineer full-stack code & security review report
```

---

## License

[MIT License](LICENSE)
