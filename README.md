# MyPA — Personal AI Assistant

An event-driven personal AI assistant that captures natural language requests via Telegram and autonomously interacts with Google Calendar using Gemini API tool calling. It supports full CRUD (Create, Read, Update, Delete) operations, conversational event summarization, and multi-turn tool execution.

## Architecture

```text
Telegram/WhatsApp Webhooks → ngrok (4040) → Proxy (8000) → Gateway (8080) → RabbitMQ → Orchestrator (8081) → Gemini API & Google Calendar API
Dashboard (GraphQL) →                           ↳ GraphQL API (8081)          ↳ Redis (State & Tokens)
                                                ↳ OAuth Callbacks (8081)      ↳ PostgreSQL (Audit Logs, Memory, & Users)
```

## Features
- **Web Dashboard**: Next.js-based admin dashboard powered by a GraphQL API for viewing audit logs, managing users and pairing codes, configuring tools, handling OAuth, and global settings.
- **Multi-Tenant Architecture**: Shared bot handle for multiple users (e.g., family members). Isolated personal data with shared family memory retrieval.
- **Multi-Channel Support**: Available on both Telegram and WhatsApp (via Twilio API).
- **Conversational Scheduling**: Create single or recurring Google Calendar events via natural language (infers dates, times, and recurrence rules).
- **Gmail Integration**: Ask the assistant to search, read, label, archive, and soft-delete emails, as well as draft replies directly from Telegram/WhatsApp.
- **Google Tasks Integration**: Create, complete, list, and delete Google Tasks across multiple task lists (e.g., Shopping, Work, Personal).
- **Web Search**: Answer real-time questions and summarize current events via the Tavily Search API.
- **Semantic Memory**: Infinite, searchable long-term memory of past conversations and user preferences using PostgreSQL `pgvector`.
- **Proactive Morning Briefings**: A background scheduler proactively wakes up and messages the user a summary of their daily schedule and pending TODO tasks.
- **Voice Commands**: Send voice messages via Telegram or WhatsApp, transcribed automatically using the Groq Whisper API.
- **Calendar Querying & Summarization**: Ask "What do I have tomorrow?" to retrieve and naturally summarize events.
- **Event Modifications**: Update or delete events easily through natural language.
- **Multi-turn Execution**: Capable of recursive reasoning, such as fetching events and then acting upon the retrieved list in a single user turn.
- **Custom Recurring Workflows (Macros)**: Allow users to define complex, multi-step cron jobs via natural language (e.g., weekly email summaries turned into task lists).
- **Multimodal Vision**: Process images sent via Telegram/WhatsApp (e.g., snap a flyer for calendar events or receipts for expenses).
- **Gmail Automation Flows**: Proactive agent behaviors to organize receipts, create events from emails, suggest responses, and autonomously unsubscribe from newsletters.
- **Durability & Rollback (`/undo`)**: Complete mutation audit logging with before/after state snapshots and one-click rollback support via `/undo` command and GraphQL `rollbackMutation`.
- **Topic-Isolated Memories & Living Entities**: Hierarchical topic memory trees with automatic conflict superseding and living entity profile cards.
- **Multi-Schedule & Contextual Calendars**: Virtual sub-calendars separating work and family schedules with schedule event overlays.
- **Baby & Family Care Tracker**: Real-time growth tracking, wonder weeks developmental leaps, pediatric medication cooldown safety enforcement, feeding/sleep logs, and supplies inventory.
- **House Maintenance, Contracts & Vehicle Vault**: Lease/mortgage/warranty contract manager with renewal notice deadlines, preventive upkeep schedules, and vehicle service mileage/inspection tracking.
- **Financial Tracking & Budget Intelligence**: Expense/income ledger with receipt vision parsing, monthly category budgets, alert thresholds, >30% historical spending anomaly detection, and recurring bills tracking.
- **On-Demand External APIs & Automations**: SSRF-protected on-demand REST/webhook API connector registry (AES-256-GCM encrypted headers), event-driven When→If→Then rules engine, and Personal CRM with anniversary/birthday alerts.
- **Shared Spaces & Partner Sync**: Collaborative spaces with cross-partner push notifications and claim/complete task hand-offs.
- **Expanded Web Control UI & GraphQL API**: Comprehensive Next.js Dashboard (`:3030`) with dedicated domain pages for all 9 expansion areas, KPI summary cards, and full GraphQL coverage.
- **System Commands**: Built-in commands for connecting accounts (`/connect`), on-demand database backups (`/backup` and `/restore`), rollback (`/undo`), and dynamically generated help (`/help`).
- **Microservice Architecture**: Decoupled ingestion and execution layers connected via RabbitMQ.

## V3 (Current Release)

MyPA has officially reached **V3 (The Expansion Release)**! 
While V1 (`v1.0.0`) established the core scheduling engine and V2 (`v2.0.0`) introduced semantic memory, reminders, and Google ecosystem integrations, **V3** transforms MyPA into a comprehensive, multi-modal personal assistant with web management, proactive automations, and deep life-domain intelligence.

The latest release includes:

- **Expanded Web Dashboard & GraphQL API**: Standalone Next.js control UI (`:3030`) with dedicated management consoles for Memories (`/memory`), Shared Spaces (`/shared`), Baby & Child Care (`/baby`), House & Vehicles (`/house`), Finance & Budgets (`/finance`), External Automations (`/automations`), and Audit/Rollback (`/audit`), backed by a comprehensive `gqlgen` GraphQL endpoint (`:8081`).
- **Pairing Codes & Multi-Tenant Onboarding**: Secure multi-user access control using 6-digit pairing codes with role assignments (Admin, Family) and family group memory isolation.
- **Multimodal Vision & Document Attachments**: Vision reasoning for event flyers, receipts, and photos, plus native document attachment support (`.txt`, `.md`, `.csv`, `.json`, `.pdf`) in WhatsApp with automatic outbound message chunking (≤1,500 chars) to bypass Twilio's length limitations.
- **Parallel Tool Execution & Multi-Turn Agent Loop**: Multi-turn execution capable of parallel tool calls (e.g. bulk tasks, batch email actions) and natural synthesis.
- **Custom Recurring Workflows (Macros)**: Define and manage automated cron jobs via natural language (`schedule_workflow`, `delete_workflow`) that run background workflows.
- **Gmail Automation Flows**: Proactive agent behaviors to organize receipts, autonomously unsubscribe from newsletters, draft responses, and extract calendar events from emails.
- **Docker-Managed ngrok Ingress**: Zero-terminal setup with a containerized ngrok service running directly in Docker Compose with real-time traffic inspection at `http://localhost:4040`.

The V3 engine is fully containerized, CI-verified, and production-ready.

👉 **See the [ROADMAP.md](docs/ROADMAP.md) for planned features and [architecture_v4.md](docs/architecture_v4.md) for the V4 system vision.**

## Prerequisites

- [Go 1.24+](https://go.dev/dl/)
- [Docker & Docker Compose](https://docs.docker.com/get-docker/)
- [ngrok Account & Auth Token](https://ngrok.com/) (for webhook ingress tunneling)
- Telegram Bot Token (from [@BotFather](https://t.me/BotFather))
- Twilio Account SID & Auth Token (for WhatsApp integration)
- Gemini API Key (from [Google AI Studio](https://aistudio.google.com/))
- Groq API Key (from [GroqCloud](https://console.groq.com/))
- Tavily API Key (from [Tavily](https://tavily.com/))
- Google Cloud OAuth credentials (for Calendar API)

## Quick Start

### Running with Docker (Recommended)

1. **Clone and configure:**
   ```bash
   cp .env.example .env
   # Edit .env with your API keys and tokens
   ```

2. **Start all services (including ngrok tunnel):**
   ```bash
   docker-compose up -d
   ```
   *(ngrok starts automatically inside Docker using `NGROK_AUTHTOKEN` and `NGROK_DOMAIN` from `.env`, exposing the tunnel dashboard at `http://localhost:4040`)*

3. **Set Webhooks:**

   - **Telegram:**
     ```bash
     curl "https://api.telegram.org/bot<YOUR_TOKEN>/setWebhook?url=<NGROK_URL>/webhook/telegram"
     ```
   - **WhatsApp:** Paste `<NGROK_URL>/webhook/twilio` (e.g., `https://your-ngrok-url.ngrok-free.dev/webhook/twilio`) into your Twilio WhatsApp Sandbox settings.

### Running Locally (Development)

1. **Clone and configure:**
   ```bash
   cp .env.example .env
   # Edit .env with your API keys and tokens
   ```

2. **Start infrastructure:**
   ```bash
   docker-compose up rabbitmq redis postgres pgadmin
   ```

3. **Run services:**
   ```bash
   go run ./cmd/gateway
   go run ./cmd/orchestrator
   go run ./cmd/proxy
   go run ./cmd/audit-worker
   ```

4. **Run dashboard (in a separate terminal):**
   ```bash
   cd dashboard
   npm install
   npm run dev
   ```

5. **Expose and set webhook:** Follow steps 3 & 4 from the Docker instructions above.

## Testing

The project utilizes robust unit and End-to-End (E2E) testing. E2E tests use [Testcontainers](https://golang.testcontainers.org/) to spin up ephemeral infrastructure (e.g., RabbitMQ).

**Prerequisites for E2E tests:**
- Docker must be running.

**Run all tests (Unit + E2E):**
```bash
go test -v ./...
```

**Run Unit tests only:**
```bash
go test -v ./internal/...
```

**Run E2E tests only:**
```bash
go test -v ./tests/...
```

## Services

| Service | Port | Description |
|---|---|---|
| Proxy | 8000 | Unified API Gateway (routes to Gateway and Orchestrator GraphQL API) |
| Gateway | 8080 | Telegram & WhatsApp webhook ingestion |
| Orchestrator | 8081 | LLM reasoning + API execution + OAuth + GraphQL API + Background Scheduler |
| Audit Worker | N/A | Asynchronously writes audit logs to PostgreSQL |
| Dashboard | 3030 | Next.js web application for administration and monitoring |
| RabbitMQ | 5672 | Message broker |
| Redis | 6379 | Conversation state + OAuth tokens |
| Database | 5432 | Audit logging & Semantic Memory storage (PostgreSQL) |
| pgadmin | 5050 | Web interface for PostgreSQL administration |
| ngrok | 4040 | Secure public ingress tunnel & traffic inspection UI |

## Project Structure

```text
dashboard/          # Next.js web application for administration and monitoring
cmd/
  audit-worker/     # Asynchronous audit logging worker
  gateway/          # Telegram & WhatsApp webhook ingestion
  orchestrator/     # LLM reasoning + execution service
  proxy/            # Unified API gateway routing
internal/
  config/           # Viper configuration
  models/           # Shared domain types
  broker/           # RabbitMQ publisher/consumer
  telegram/         # Telegram Bot API client
  twilio/           # Twilio API client and webhook handler (WhatsApp)
  audio/            # Groq Whisper API client for voice processing
  llm/              # Gemini API client + tool definitions
  calendar/         # Google Calendar API + OAuth
  gmail/            # Gmail API integration
  tasks/            # Google Tasks API integration
  tavily/           # Tavily Web Search API client
  state/            # Redis state management
  db/               # Database client for audit logging and memory
  scheduler/        # Cron jobs and background proactive messaging
  orchestrator/     # Core reasoning engine
```

## License

[MIT License](LICENSE)
