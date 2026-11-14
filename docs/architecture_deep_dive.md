# MyPA — Complete Architecture Deep Dive

> [!NOTE]
> For the exhaustive database table definitions (29 GORM models), complete 70-tool parameter catalog, and 8-section Feature Development Playbook, see [`SYSTEM.md`](../SYSTEM.md).

---

## 1. High-Level System Architecture

```mermaid
graph TB
    subgraph "External Clients"
        TG_USER["💬 Telegram User"]
        WA_USER["📱 WhatsApp User"]
        WEB_USER["🌐 Web Browser (:3030)"]
    end

    subgraph "External Messaging APIs"
        TG_API["Telegram Bot API"]
        TWILIO_API["Twilio WhatsApp API"]
    end

    subgraph "MyPA Docker Compose Stack (10 Containers)"
        subgraph "Ingress & Edge"
            NGROK["ngrok (:4040)"]
            PROXY["Proxy Service (:8000)\n• /webhook/* → Gateway\n• /graphql (Auth Guard) → Orchestrator\n• /auth/google/* → Orchestrator"]
            DASH["Next.js 16 Dashboard (:3030)\n• 14 App Router Consoles\n• Typed graphqlFetch Client"]
        end

        subgraph "Gateway Service (:8080)"
            GW_TG["POST /webhook/telegram\n(Secret Token Verify)"]
            GW_TW["POST /webhook/twilio\n(HMAC-SHA1 Verify)"]
        end

        subgraph "RabbitMQ Broker (:5672 / :15672)"
            RMQ_IN["Queue: telegram.inbound\n(+ telegram.inbound.dlq)"]
            RMQ_AUD["Queue: audit.events\n(+ audit.events.dlq)"]
            RMQ_DLX["Dead-Letter Exchange\nmypa.dlx"]
        end

        subgraph "Orchestrator Service (:8081)"
            ENGINE["Engine (ProcessMessage)\n• Pre-Auth & Admin RBAC\n• Prompt Injection Gate"]
            INTENT["Two-Tier Intent Matcher\n(8 Core + 9 Domain Groups)"]
            REGISTRY["ToolModule Registry\n(70 Tools / 13 Packages)"]
            BUS["Dual-Lane EventBus\n• Sync: Durability & Cache\n• Async: 4-Worker Pool"]
            GQL["gqlgen GraphQL Server\n(22 Queries / 28 Mutations)"]
            OAUTH_HTTP["GET /auth/google/callback\n(32B CSRF Nonce GETDEL)"]
            SCHED["Cron Scheduler\n(Backups, Briefings, Reminders, Rules)"]
        end

        subgraph "Audit Worker Service"
            AW_CONSUMER["Resilient Consumer\n→ InsertAuditSession"]
            AW_ARCHIVER["Daily Archiver\n(30-day JSONL retention)"]
        end

        subgraph "Persistence Layer"
            REDIS["Redis 7 (:6379)\n• chat:<userID> (stripped photos)\n• token:<userID> (AES-256-GCM)\n• ctx_prompt:<fg>:<mod> (10m TTL)\n• oauth_state & rate_limit keys"]
            PG["PostgreSQL 15 + pgvector (:5432)\n• 29 Multi-Tenant GORM Tables\n• halfvec(3072) HNSW Indexes\n• 6 Composite B-Tree Indexes"]
            PGADMIN["pgAdmin 4 (:5050)"]
        end
    end

    subgraph "External AI & Workspace Services"
        GEMINI["Google Gemini 2.5 Flash\n& gemini-embedding-001"]
        GROQ["Groq Whisper API\n(whisper-large-v3)"]
        GSUITE["Google Calendar, Gmail,\nTasks & People APIs"]
        TAVILY["Tavily Search API\n& NetGuard Scraper"]
    end

    TG_USER <--> TG_API
    WA_USER <--> TWILIO_API
    WEB_USER <--> DASH

    TG_API & TWILIO_API --> NGROK --> PROXY
    DASH -->|"POST /graphql (Bearer)"| PROXY

    PROXY --> GW_TG & GW_TW
    PROXY --> GQL & OAUTH_HTTP

    GW_TG & GW_TW -->|"SafePublisher (Confirms)"| RMQ_IN
    RMQ_IN -.->|"3 Failed Retries"| RMQ_DLX
    RMQ_AUD -.->|"3 Failed Retries"| RMQ_DLX

    RMQ_IN -->|"Consumer"| ENGINE
    ENGINE <--> INTENT & REGISTRY
    REGISTRY -->|"Publish(MutationEvent)"| BUS
    GQL -->|"Publish(MutationEvent)"| BUS

    ENGINE & REGISTRY & GQL <--> REDIS
    ENGINE & REGISTRY & GQL <--> PG
    BUS -->|"Sync: MutationLog"| PG
    BUS -->|"Async: Partner Sync"| TG_API & TWILIO_API

    ENGINE -->|"SafePublisher"| RMQ_AUD --> AW_CONSUMER --> PG
    AW_ARCHIVER --> PG

    ENGINE <--> GEMINI & GROQ
    REGISTRY <--> GSUITE & TAVILY
    SCHED --> ENGINE
    PGADMIN --> PG
```

---

## 2. Container Stack & Unified Multi-Target Dockerfile

All four Go microservices compile from a single root [`Dockerfile`](../Dockerfile) using BuildKit cache mounts (`/go/pkg/mod` and `/root/.cache/go-build`) and granular `COPY` directives (`internal/`, `config/`, `cmd/<service>/`):

| Container | Target / Image | Runtime Image | Host Port | Role |
| :--- | :--- | :--- | :---: | :--- |
| **`proxy`** | `proxy` | `gcr.io/distroless/static-debian12` | `127.0.0.1:8000` | Edge reverse proxy: `/webhook/*` → Gateway, `/graphql` (with auth header check) & `/auth/google/*` → Orchestrator |
| **`gateway`** | `gateway` | `gcr.io/distroless/static-debian12` | `127.0.0.1:8080` | Validates Telegram secret token & Twilio HMAC-SHA1 signature, normalizes payloads, publishes to RabbitMQ |
| **`orchestrator`** | `orchestrator` | `debian:12-slim` (`postgresql-client`) | `127.0.0.1:8081` | Reasoning engine, 70-tool `Registry`, dual-lane `EventBus`, OAuth2, GraphQL API, Cron scheduler |
| **`audit-worker`** | `audit-worker` | `gcr.io/distroless/static-debian12` | — | Consumes `audit.events`, writes `AuditSession` records, archives sessions >30 days to `/app/archives` JSONL |
| **`dashboard`** | `dashboard/Dockerfile` | `node:20-alpine` (Next.js standalone) | `127.0.0.1:3030` | Next.js 16 (React 19) Web Control Dashboard communicating via `http://proxy:8000/graphql` |
| **`rabbitmq`** | Official Image | `rabbitmq:3.13-management-alpine` | `127.0.0.1:5672`, `15672` | AMQP 0-9-1 broker with `mypa.dlx` dead-letter exchange and `.dlq` queues |
| **`redis`** | Official Image | `redis:7-alpine` (`--appendonly yes`) | `127.0.0.1:6379` | Hot conversation state, encrypted OAuth tokens, prompt cache, CSRF nonces, rate limiters |
| **`postgres`** | Official Image | `pgvector/pgvector:pg15` | `127.0.0.1:5432` | Relational + `pgvector` database (29 tables, HNSW `halfvec(3072)` & composite indexes) |
| **`pgadmin`** | Official Image | `dpage/pgadmin4` | `127.0.0.1:5050` | PostgreSQL administration console |
| **`ngrok`** | Official Image | `ngrok/ngrok:latest` | `127.0.0.1:4040` | Public HTTPS webhook tunnel to `proxy:8000` |

---

## 3. Core Execution Flows

### Flow 1: Conversational Turn & Two-Tier Tool Execution

```mermaid
sequenceDiagram
    participant User
    participant Platform as Telegram / Twilio
    participant Proxy as Proxy (:8000)
    participant GW as Gateway (:8080)
    participant RMQ as RabbitMQ (telegram.inbound)
    participant Engine as Orchestrator Engine
    participant Redis
    participant PG as PostgreSQL (pgvector)
    participant Gemini as Gemini 2.5 Flash
    participant Registry as ToolModule Registry
    participant Bus as Dual-Lane EventBus

    User->>Platform: Send message (text / photo / PDF)
    Platform->>Proxy: POST /webhook/telegram or /webhook/twilio
    Proxy->>GW: Forward (10MB MaxBytesReader)
    GW->>GW: Verify X-Telegram-Bot-Api-Secret-Token or X-Twilio-Signature
    GW->>RMQ: SafePublisher.Publish(models.Message) [Publisher Confirm]
    GW-->>Platform: 200 OK

    RMQ->>Engine: Deliver models.Message (prefetch=1, 3-retry DLQ guard)
    Engine->>PG: Pre-Auth Check: GetUser(msg.UserID)
    alt Unauthenticated User
        Engine->>Redis: Check/Increment Pairing Rate Limit (5 tries / 15m → 1h lockout)
        Engine-->>Platform: Prompt for 6-digit pairing code or redeem code
    else Authenticated User
        Engine->>Redis: GetChatHistory(userID) + MGET ctx_prompt:<familyGroup>:*
        Engine->>Gemini: GenerateEmbedding(text) → 3072-dim vector
        Engine->>PG: SearchSimilarMemories (halfvec(3072) HNSW cosine)
        Engine->>Engine: KeywordIntentMatcher → Select Tier 1 (8 Core) + Active Tier 2 Tools

        loop Bounded Recursion (MaxToolRecursionDepth = 8)
            Engine->>Gemini: Chat(systemPrompt, history, activeTools)
            alt ToolCalls returned
                Engine->>Engine: Check SEC-09 Meta-Tool Confirmation Gate
                Engine->>Registry: Execute tools concurrently (errgroup, max 8)
                Registry->>PG: Tenant-scoped repository read/write
                Registry->>Bus: Publish(MutationEvent)
                Bus->>PG: Sync Lane: Insert MutationLog + Invalidate PromptCache
                Bus-->>Platform: Async Lane: Partner SharedSpace notification & Automation rules
                Registry-->>Engine: Tool responses
            else Final Text returned
                Gemini-->>Engine: Response text
            end
        end

        Engine->>Engine: Format (SafeChunkTelegramHTML / ToWhatsApp)
        Engine->>Platform: Deliver response
        Engine->>Redis: AppendChatHistory (strip raw PhotoData bytes → metadata tag)
        Engine-->>RMQ: Async defer: Publish(AuditSession) → audit.events
    end
```

### Flow 2: Voice Note Transcription (`whisper-large-v3`)
1. After passing the pre-auth check in `Engine.ProcessMessage`, voice notes (`VoiceFileID` on Telegram or `audio/*` `MediaURL` on WhatsApp) are downloaded in the Orchestrator.
2. WhatsApp media downloads validate the URL against `internal/twilio`'s HTTPS `*.twilio.com` / `*.twiliocdn.com` domain allowlist (capped at 25MB) before attaching Twilio BasicAuth credentials.
3. Audio bytes are sent to Groq Whisper (`whisper-large-v3`), a real-time `"🗣️ Transcribed: ..."` preview is sent to the user, and `msg.Text` proceeds through the standard conversational flow.

### Flow 3: Hardened Google OAuth2 (`/connect`)
1. User sends `/connect`.
2. Orchestrator generates a 32-byte cryptographically random hex nonce (`internal/crypto`), stores `oauth_state:<nonce> -> userID` in Redis with a 10-minute TTL, and sends the Google OAuth2 authorization URL.
3. On `GET /auth/google/callback`, Orchestrator atomically validates and consumes the nonce via Redis `GETDEL`, exchanges the authorization code for tokens, encrypts the token payload with HKDF-SHA256 salted AES-256-GCM, and dual-writes it to Redis (`token:<userID>`) and PostgreSQL (`encrypted_credentials`).

### Flow 4: Unified Snapshot Rollback (`/undo` & GraphQL `rollbackMutation`)
1. Every state-mutating tool or GraphQL mutation emits a `MutationEvent` containing `BeforeJSON` and `AfterJSON` snapshots, persisted synchronously into `mutation_logs`.
2. When a user sends `/undo` in chat, invokes `undo_last_action`, or clicks **Rollback** in the Web Dashboard (`/audit`), `durability.Module.Rollback` verifies positive tenant ownership, checks the entity against the 19-table allowlist (`ResolveRollbackTable`), restores the snapshot inside an atomic GORM transaction, and marks `is_undone = true`.

---

## 4. Modular `ToolModule` Catalog (70 Tools / 13 Packages)

Rather than a monolithic switch statement, all 70 tools are encapsulated in 13 `orchestrator.ToolModule` packages under [`internal/domains/`](../internal/domains):

| Package | Domain Group | Tool Count | Registered Tools |
| :--- | :--- | :---: | :--- |
| `registry` (built-in) | Tier 1 Core | 1 | `get_current_time` |
| `workspace_calendar` | `DomainCalendar` | 4 | `create_calendar_event`, `list_calendar_events`, `update_calendar_event`, `delete_calendar_event` |
| `workspace_gmail` | `DomainGmail` | 9 | `search_emails`, `read_email`, `draft_email_reply`, `archive_emails`, `soft_delete_emails`, `list_email_labels`, `apply_email_labels`, `create_email_label`, `unsubscribe_email` |
| `workspace_tasks` | `DomainTasks` | 6 | `list_task_lists`, `create_task_list`, `list_tasks`, `create_task`, `complete_task`, `delete_task` |
| `workspace_contacts` | `DomainContacts` | 2 | `search_contacts`, `create_contact` |
| `web_research` | Tier 1 Core | 2 | `search_web`, `fetch_webpage` |
| `durability` | Tier 1 Core / Audit | 3 | `undo_last_action`, `list_recent_changes`, `export_topic_data` |
| `memory` | Tier 1 Core / Memory | 5 | `remember_fact`, `save_topic_memory`, `search_topic_memories`, `upsert_entity_profile`, `get_entity_profile` |
| `shared` | `DomainShared` + Core | 5 | `schedule_reminder`, `configure_shared_topic`, `create_shared_reminder`, `complete_shared_reminder`, `list_shared_activity` |
| `schedule` | `DomainCalendar` | 5 | `schedule_topic_event`, `list_topic_schedule`, `configure_topic_calendar`, `delete_topic_event`, `update_topic_event` |
| `baby` | `DomainBaby` | 5 | `upsert_baby_profile`, `log_baby_care`, `query_baby_logs_and_trends`, `get_baby_milestone_suggestions`, `manage_baby_inventory` |
| `house` | `DomainHouse` | 4 | `save_house_contract_or_receipt`, `list_house_contracts`, `manage_home_maintenance`, `manage_vehicle_asset` |
| `finance` | `DomainFinance` | 4 | `log_financial_transaction`, `get_financial_summary`, `set_category_budget`, `manage_recurring_bills` |
| `automation` | `DomainAutomation` & `DomainContacts` | 15 | `register_external_api`, `list_external_apis`, `call_external_api`, `create_automation_rule`, `list_automation_rules`, `delete_automation_rule`, `update_user_preferences`, `get_user_preferences`, `upsert_personal_crm`, `query_personal_crm`, `schedule_workflow`, `list_workflows`, `delete_workflow` *(plus 2 internal helpers)* |

---

## 5. Dual-Layer Storage & Indexing Summary

| Store | Key / Table Group | Count | Highlights |
| :--- | :--- | :---: | :--- |
| **Redis 7** | `chat:*`, `token:*`, `ctx_prompt:*`, `oauth_state:*`, `pending_meta_tool:*`, `rate_limit:pairing:*` | 7 patterns | Stripped `PhotoData` history (<10KB/user), HKDF AES-256-GCM encrypted tokens, 10m dynamic prompt cache, 10m `GETDEL` OAuth nonces |
| **PostgreSQL 15 + `pgvector`** | Core, Durability, Memory v2, Shared, Schedule, Baby, House, Finance, Automation | 29 tables | Pool tuned (`25 max open / 10 idle`), `halfvec(3072)` HNSW cosine indexes on `memories` & `topic_memories`, 6 composite multi-tenant B-Tree indexes |
| **RabbitMQ 3.13** | `telegram.inbound`, `audit.events` + `.dlq` queues on `mypa.dlx` | 4 queues | Mutex-serialized `SafePublisher` with broker confirms, 3-attempt exponential backoff consumer retries, poison-pill isolation |
