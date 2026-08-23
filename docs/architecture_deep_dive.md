# MyPA — Complete Architecture Deep Dive (Updated)

## High-Level Architecture

```mermaid
graph TB
    subgraph "External Clients"
        TG_USER["💬 Telegram User"]
        WA_USER["📱 WhatsApp User"]
    end

    subgraph "External APIs"
        TG_API["Telegram Bot API"]
        TWILIO_API["Twilio API"]
    end

    subgraph "MyPA Docker Compose Stack (8 containers)"
        subgraph "Proxy Service (:8000)"
            PROXY["Reverse Proxy\n(httputil.ReverseProxy)"]
        end

        subgraph "Gateway Service (:8080)"
            GW_TG["POST /webhook/telegram"]
            GW_TW["POST /webhook/twilio"]
        end

        subgraph "Message Broker"
            RABBIT_MSG["RabbitMQ\nQueue: telegram.inbound"]
            RABBIT_AUDIT["RabbitMQ\nQueue: audit.events"]
        end

        subgraph "Orchestrator Service (:8081)"
            ENGINE["Engine\n(processMessage)"]
            AUDIO_CLIENT["Groq Whisper\n(whisper-large-v3-turbo)"]
            LLM_CLIENT["Gemini 2.5 Flash\n(temp 0.2)"]
            TOOL_EXEC["Tool Execution\n(20 tools)"]
            OAUTH_HTTP["GET /auth/google/callback"]
            SCHED["Cron Scheduler\n(8AM Asia/Jerusalem)"]
        end

        subgraph "Audit Worker Service"
            AW_CONSUMER["ConsumeRaw()\n→ InsertAuditSession"]
            AW_ARCHIVER["Daily Archiver\n(30-day retention)"]
        end

        subgraph "Data Layer"
            REDIS["Redis 7\n• chat:{userID} (20 msgs, 1h TTL)\n• token:{userID} (OAuth, no TTL)"]
            PG["PostgreSQL 15 + pgvector\n• audit_sessions\n• audit_events\n• memories (3072-dim)"]
            PGADMIN["pgAdmin 4 (:5050)"]
        end
    end

    subgraph "External Services"
        GEMINI["Google Gemini API"]
        GROQ["Groq Whisper API"]
        GCAL["Google Calendar API"]
        GMAIL_API["Gmail API"]
        GTASKS["Google Tasks API"]
        TAVILY["Tavily Search API"]
        GOOGLE_OAUTH["Google OAuth 2.0"]
    end

    TG_USER --> TG_API
    WA_USER --> TWILIO_API

    TG_API -->|"POST webhook"| PROXY
    TWILIO_API -->|"POST webhook"| PROXY

    PROXY -->|"/webhook/*"| GW_TG
    PROXY -->|"/webhook/*"| GW_TW
    PROXY -->|"/auth/google/*"| OAUTH_HTTP

    GW_TG -->|"models.Message"| RABBIT_MSG
    GW_TW -->|"models.Message"| RABBIT_MSG

    RABBIT_MSG -->|"Consume()"| ENGINE

    ENGINE --> AUDIO_CLIENT
    AUDIO_CLIENT --> GROQ
    ENGINE --> LLM_CLIENT
    LLM_CLIENT --> GEMINI
    ENGINE --> TOOL_EXEC
    TOOL_EXEC --> GCAL
    TOOL_EXEC --> GMAIL_API
    TOOL_EXEC --> GTASKS
    TOOL_EXEC --> TAVILY

    ENGINE <--> REDIS
    ENGINE -->|"SaveMemory /\nSearchMemories"| PG

    ENGINE -->|"Publish(AuditSession)"| RABBIT_AUDIT
    RABBIT_AUDIT -->|"ConsumeRaw()"| AW_CONSUMER
    AW_CONSUMER -->|"InsertAuditSession"| PG
    AW_ARCHIVER -->|"archive + prune\n(>30 days)"| PG

    PGADMIN --> PG

    ENGINE -->|"response"| TG_API
    ENGINE -->|"response"| TWILIO_API
    TG_API --> TG_USER
    TWILIO_API --> WA_USER

    OAUTH_HTTP --> GOOGLE_OAUTH
    SCHED -->|"8AM daily"| ENGINE
```

---

## What Changed Since Last Analysis

> [!IMPORTANT]
> ### Summary of Changes
> 1. **New Audit Worker service** with daily archiver (30-day retention → JSONL export → DB prune)
> 2. **Restructured audit schema** — `AuditSession` + `AuditEvent` replacing flat `AuditLog`
> 3. **Generic broker** — `Publish(ctx, v any)` and `ConsumeRaw(func([]byte))` methods
> 4. **Unified Dockerfile** — single multi-target file with distroless runtime images
> 5. **New `EventPublisher` port interface** in orchestrator
> 6. **Healthcheck `start_period`** added to all infra containers

| Area | Before | After |
|---|---|---|
| **Audit model** | Flat `AuditLog` (user_message, llm_response, action_taken) | **`AuditSession`** (session-level: UUID, timing, tokens, status) + **`AuditEvent`** (span-level: event_type, payloads as JSONB, per-span timing & tokens) |
| **Audit persistence** | Orchestrator `defer` → direct DB write | Orchestrator → RabbitMQ `audit.events` → **Audit Worker** → DB |
| **Audit retention** | None | **30-day retention** — daily archiver exports to JSONL, prunes from DB |
| **Broker API** | `Publish(ctx, models.Message)`, `Consume(func(models.Message))` | **`Publish(ctx, v any)`**, `Consume(func(models.Message))`, **`ConsumeRaw(func([]byte))`** |
| **DB methods** | `LogInteraction(AuditLog)`, `GetLastAuditLogForUser()` | **`InsertAuditSession(AuditSession)`**, **`InsertAuditEvent(AuditEvent)`**, **`GetLastAuditSessionForUser()`** |
| **Port interfaces** | 9 interfaces | **10 interfaces** (added `EventPublisher`) |
| **Dockerfiles** | 3 separate files | **1 unified multi-target** with `gcr.io/distroless/static-debian12` runtimes |
| **Docker containers** | 7 | **8** (added audit-worker) |
| **Healthchecks** | No `start_period` | All infra: `start_period` (RabbitMQ 10s, Redis 5s, Postgres 10s) |

### What Did NOT Change
Gateway, Proxy, LLM client, tool declarations (20 tools), Calendar, Gmail, Tasks, Tavily, Scraper, Audio, Telegram, Twilio, Redis state store, PostgreSQL Memory schema, Scheduler, Markdown converters, Config structure.

---

## Infrastructure: 8-Container Docker Stack

| Container | Dockerfile Target | Runtime | Port | Role |
|---|---|---|---|---|
| **proxy** | `proxy` | `distroless/static-debian12` | `:8000` | Reverse proxy: webhooks → Gateway, OAuth → Orchestrator |
| **gateway** | `gateway` | `distroless/static-debian12` | `:8080` | Telegram & Twilio webhook ingestion → RabbitMQ |
| **orchestrator** | `orchestrator` | `distroless/static-debian12` | `:8081` | Brain: LLM reasoning, tool execution, audit publishing |
| **audit-worker** | `audit-worker` | `distroless/static-debian12` | — | Audit log persistence + 30-day retention archiver |
| **rabbitmq** | (image) | `rabbitmq:3.13-management-alpine` | `:5672`/`:15672` | 2 durable queues: `telegram.inbound` + `audit.events` |
| **redis** | (image) | `redis:7-alpine` | `:6379` | Session state + OAuth tokens |
| **postgres** | (image) | `pgvector/pgvector:pg15` | `:5432` | Audit sessions/events + semantic memory |
| **pgadmin** | (image) | `dpage/pgadmin4` | `:5050` | Database admin UI |

### Unified Dockerfile

```mermaid
graph TD
    subgraph "Stage 1: Shared deps"
        D["golang:1.26-alpine\ngo mod download (cached)"]
    end

    subgraph "Stage 2: Build targets"
        BG["build-gateway"]
        BO["build-orchestrator"]
        BP["build-proxy"]
        BA["build-audit-worker"]
    end

    subgraph "Stage 3: Runtime (distroless)"
        RG["gateway"]
        RO["orchestrator"]
        RP["proxy"]
        RA["audit-worker"]
    end

    D --> BG --> RG
    D --> BO --> RO
    D --> BP --> RP
    D --> BA --> RA
```

- Shared `deps` stage for `go mod download`
- Granular `COPY` per service (only `internal/`, `config/`, `cmd/<service>/`)
- BuildKit cache mounts for `/go/pkg/mod` and `/root/.cache/go-build`
- `ldflags='-w -s'` for stripped binaries
- `gcr.io/distroless/static-debian12` runtime (no shell, minimal attack surface)

---

## RabbitMQ: Two Queue Topology

```mermaid
graph LR
    subgraph "Publishers"
        GW["Gateway\n(Publish)"]
        ORCH["Orchestrator\n(Publish)"]
    end

    subgraph "RabbitMQ (durable, persistent, prefetch=1)"
        Q1["telegram.inbound"]
        Q2["audit.events"]
    end

    subgraph "Consumers"
        ENGINE["Orchestrator\n(Consume)"]
        AW["Audit Worker\n(ConsumeRaw)"]
    end

    GW -->|"models.Message"| Q1
    Q1 --> ENGINE

    ORCH -->|"models.AuditSession"| Q2
    Q2 --> AW
    AW -->|"InsertAuditSession"| PG["PostgreSQL"]
```

| Queue | Payload | Publisher | Consumer | Method | ACK Strategy |
|---|---|---|---|---|---|
| `telegram.inbound` | `models.Message` | Gateway | Orchestrator | `Consume(func(Message))` | ACK/NACK+requeue/Reject |
| `audit.events` | `models.AuditSession` | Orchestrator | Audit Worker | `ConsumeRaw(func([]byte))` | ACK on success, return `nil` on bad JSON (discard), return `error` on DB failure (requeue) |

The broker's `Publish(ctx, v any)` now accepts any JSON-serializable type, enabling reuse for both message types.

---

## Data Models

### Audit Schema (NEW — replaces flat AuditLog)

```mermaid
erDiagram
    AuditSession {
        uuid ID PK
        string UserID "indexed"
        string ChatID
        string Source "telegram/whatsapp"
        string UserPrompt
        string FinalResponse
        timestamp StartTime
        timestamp EndTime
        int64 TotalDurationMs
        int TotalTokens
        string Status "success/failed"
    }

    AuditEvent {
        uuid ID PK
        uuid SessionID FK "indexed"
        string EventType "llm_inference/tool_execution"
        string ActionName
        jsonb RequestPayload
        jsonb ResponsePayload
        string ErrorMessage
        timestamp StartTime
        timestamp EndTime
        int64 DurationMs
        int PromptTokens
        int CompletionTokens
    }

    AuditSession ||--o{ AuditEvent : "has many"
```

**Key improvements over flat `AuditLog`:**
- **Session-level** tracking: UUID session ID, total duration, total tokens, status
- **Event/span-level** granularity: individual LLM inferences and tool executions tracked separately
- **JSONB payloads**: full request/response data stored for debugging
- **Per-span token counts**: prompt and completion tokens tracked per event

### Other Models (unchanged)

| Model | Table | Purpose |
|---|---|---|
| `Message` | (RabbitMQ only) | Normalized cross-platform message |
| `ChatMessage` | (Redis only) | Conversation history turn |
| `Memory` | `memories` | Semantic facts with 3072-dim pgvector embeddings |
| `CalendarEvent` | (API only) | Google Calendar event structure |

---

## The Audit Worker (NEW)

```mermaid
graph TB
    subgraph "Audit Worker Service"
        CONSUMER["ConsumeRaw()\nfrom audit.events queue"]
        ARCHIVER["Daily Archiver\n(24h ticker)"]
    end

    CONSUMER -->|"Unmarshal AuditSession\n→ InsertAuditSession"| PG["PostgreSQL"]

    ARCHIVER -->|"Query sessions > 30 days"| PG
    ARCHIVER -->|"Export to JSONL"| FILE["audit_archive_<timestamp>.jsonl"]
    ARCHIVER -->|"DELETE from DB"| PG
```

**Two concurrent goroutines:**

1. **Consumer**: Reads from `audit.events` queue, deserializes `AuditSession`, persists to PostgreSQL. Poison messages (bad JSON) are discarded with `return nil`. DB errors cause requeue via `return error`.

2. **Daily Archiver** (`startArchiver`):
   - Runs on a `24h` ticker
   - Queries audit sessions older than 30 days
   - Exports them to timestamped JSONL files (`audit_archive_<unix_ts>.jsonl`)
   - Deletes archived records from PostgreSQL
   - Provides automatic data retention management

---

## All Flows

### Inbound (User-Initiated)
| # | Flow | Source | Trigger |
|---|---|---|---|
| 1 | **Text Message** | Telegram or WhatsApp | User sends text |
| 2 | **Voice Message** | Telegram or WhatsApp | User sends voice note |
| 3 | **`/connect` Command** | Telegram or WhatsApp | User types `/connect` |

### System-Initiated
| # | Flow | Trigger |
|---|---|---|
| 4 | **Morning Brief** | Cron at 8:00 AM Israel time |

---

## Flow 1: Text Message

```mermaid
sequenceDiagram
    participant User
    participant Platform as Telegram/Twilio API
    participant Proxy as Proxy (:8000)
    participant GW as Gateway (:8080)
    participant RMQ as RabbitMQ
    participant Engine as Orchestrator Engine
    participant Redis
    participant PG as PostgreSQL
    participant Gemini as Gemini 2.5 Flash
    participant Tools as External Services
    participant RMQ_A as RabbitMQ (audit.events)
    participant AW as Audit Worker

    User->>Platform: Send text message
    Platform->>Proxy: POST /webhook/*
    Proxy->>GW: Forward

    GW->>GW: Parse & normalize to models.Message
    GW->>RMQ: Publish(models.Message) → telegram.inbound
    GW-->>Platform: 200 OK (immediate)

    RMQ->>Engine: Consume() delivers message
    Note over Engine: Create AuditSession UUID<br/>Start timer (5-min timeout)

    Engine->>Redis: GetChatHistory(userID) → last 20 msgs
    Engine->>Gemini: GenerateEmbedding(text) → 3072-dim vector
    Engine->>PG: SearchMemories(userID, embedding, 5) → cosine ⇔
    PG-->>Engine: Top 5 relevant memories

    Note over Engine: Build system prompt:<br/>• Current RFC1123 datetime<br/>• Israel timezone (Sun-Thu workweek)<br/>• Tool instructions<br/>• Retrieved memories

    Engine->>Gemini: Chat(systemPrompt, history, msg, 20 tools)<br/>Temperature: 0.2

    alt Text response
        Gemini-->>Engine: Response{Text}
    else Tool call
        Gemini-->>Engine: Response{ToolCall}
        loop Recursive feedbackToLLM
            Engine->>Tools: Execute tool
            Tools-->>Engine: Result
            Engine->>Gemini: Continue with result
        end
    end

    Engine->>Engine: Format (ToTelegramHTML / ToWhatsApp)
    Engine->>Engine: Chunk ≤1500 chars (smart boundary)
    Engine->>Platform: Send response
    Platform->>User: Deliver message

    Engine->>Redis: AppendChatHistory (user + assistant)

    Note over Engine: defer: record EndTime,<br/>compute TotalDurationMs

    Engine-->>RMQ_A: Publish(AuditSession) [async defer]
    RMQ_A->>AW: ConsumeRaw()
    AW->>PG: InsertAuditSession
```

### Key Details
- Gateway responds instantly — no AI at the Gateway
- Redis: sliding 20-message window (RPUSH + LTRIM + EXPIRE 1h)
- Every message embedded via `gemini-embedding-001`, top 5 memories recalled via pgvector cosine distance
- LLM at temperature 0.2 for reliable tool calls
- **Session-based telemetry**: UUID session ID created at start, timing recorded, published as `AuditSession` on completion
- Poison pill defense: errors caught, user notified, message ACKed

---

## Flow 2: Voice / Audio Message

```mermaid
sequenceDiagram
    participant User
    participant Platform as Telegram/Twilio API
    participant GW as Gateway
    participant RMQ as RabbitMQ
    participant Engine as Orchestrator Engine
    participant Groq as Groq Whisper API

    User->>Platform: Send voice note 🎤
    Platform->>GW: POST /webhook/* (VoiceFileID or MediaURL)
    GW->>RMQ: Publish (Text empty, voice fields set)
    GW-->>Platform: 200 OK

    RMQ->>Engine: Deliver message

    alt Telegram voice
        Engine->>Platform: GetFile(fileID) → DownloadFile(path)
    else WhatsApp voice
        Engine->>Platform: DownloadMedia(mediaURL) [Basic Auth]
    end

    Engine->>Groq: TranscribeAudio(bytes, "voice.ogg")<br/>Model: whisper-large-v3-turbo
    Groq-->>Engine: Transcribed text

    Engine->>Platform: "🗣️ *Transcribed:* ..." (preview)
    Platform->>User: Shows transcription

    Note over Engine: msg.Text = transcribedText<br/>→ continues as Text Flow
```

- Audio download + transcription in Orchestrator (not Gateway)
- Groq Whisper `whisper-large-v3-turbo` — auto language detection
- User gets real-time transcription preview

---

## Flow 3: `/connect` Command (Google OAuth)

```mermaid
sequenceDiagram
    participant User
    participant Engine as Orchestrator Engine
    participant Google as Google OAuth 2.0
    participant OrcHTTP as Orchestrator HTTP (:8081)
    participant Redis

    User->>Engine: "/connect" (via Gateway → RabbitMQ)

    Engine->>Engine: oauthCfg.AuthCodeURL(userID)<br/>Scopes: Calendar + Gmail + Tasks
    Engine->>User: OAuth URL

    User->>Google: Grants permissions
    Google->>OrcHTTP: GET /auth/google/callback?code=xxx&state=userID

    OrcHTTP->>Google: Exchange(code) → tokens
    OrcHTTP->>Redis: SetOAuthToken(userID, tokenBytes)
    OrcHTTP->>User: "✅ Connected!"
```

- Single OAuth flow for Calendar + Gmail + Tasks
- Tokens in Redis without expiration, auto-refreshed

---

## Flow 4: Morning Brief (Scheduled)

```mermaid
sequenceDiagram
    participant Cron as Cron (8AM Asia/Jerusalem)
    participant Engine as Orchestrator
    participant PG as PostgreSQL

    Cron->>Engine: BroadcastProactiveMessage(prompt)
    Engine->>PG: GetUniqueUsers()
    PG-->>Engine: [user IDs]

    loop Each user
        Engine->>PG: GetLastAuditSessionForUser(userID)
        Note over Engine: Gets Source + ChatID<br/>from last interaction
        Engine->>Engine: processMessage(syntheticMsg)<br/>2-min timeout goroutine
    end
```

- Multi-tenant: broadcasts to all known users
- Platform-aware: sends on each user's last-used platform
- Each user in a separate goroutine (2-min timeout)

---

## Tool Catalog (20 Tools)

#### Calendar (4)
| Tool | Action |
|---|---|
| `create_calendar_event` | Create with title, start/end (ISO 8601), timezone, description, recurrence |
| `list_calendar_events` | List in date range with query |
| `update_calendar_event` | Partial update via Patch API |
| `delete_calendar_event` | Delete by ID |

#### Email (8)
| Tool | Action |
|---|---|
| `search_emails` | Gmail query syntax search |
| `read_email` | Full body (recursive MIME decode) |
| `draft_email_reply` | Draft with proper threading headers |
| `archive_emails` | Remove INBOX label (batch) |
| `soft_delete_emails` | Move to Trash (batch) |
| `list_email_labels` | Name → ID map |
| `apply_email_labels` | Apply label (batch) |
| `create_email_label` | Create new label |

#### Tasks (6)
| Tool | Action |
|---|---|
| `list_task_lists` | List all task lists |
| `create_task_list` | Create new list |
| `list_tasks` | Pending tasks (default `@default`) |
| `create_task` | Create with title, notes, due |
| `complete_task` | Mark completed |
| `delete_task` | Delete permanently |

#### Web (2)
| Tool | Action |
|---|---|
| `search_web` | Tavily — AI answer + top 3. Progress: "🔍 Searching..." |
| `fetch_webpage` | Readability → Markdown, 20k limit. Progress: "📖 Reading..." |

#### Memory (1)
| Tool | Action |
|---|---|
| `remember_fact` | Embed via gemini-embedding-001 → pgvector |

### Recursive Agent Loop

```mermaid
graph TD
    A["LLM returns ToolCall"] --> B["handleToolCall()"]
    B --> C["Execute service method"]
    C --> D["feedbackToLLM()"]
    D --> E["Append tool call + result to history"]
    E --> F["Re-prompt Gemini with continuation"]
    F --> G{"Response?"}
    G -->|"ToolCall"| B
    G -->|"Text"| H["Return final response"]
```

No hard iteration limit — recurses until LLM produces final text. Enables multi-step chains (e.g., search emails → read → create task → draft reply → summarize).

---

## Dual-Layer Persistence

```mermaid
graph TB
    subgraph "Redis (Hot)"
        R1["chat:{userID}\n20 msgs, 1h TTL"]
        R2["token:{userID}\nOAuth, no TTL"]
    end

    subgraph "PostgreSQL + pgvector (Cold)"
        P1["audit_sessions"]
        P2["audit_events"]
        P3["memories\n(3072-dim vectors)"]
    end

    ENGINE["Orchestrator"] -->|"Append/Get ChatHistory"| R1
    ENGINE -->|"Set/Get OAuthToken"| R2
    ENGINE -->|"Save/Search Memories"| P3

    ENGINE -->|"Publish(AuditSession)"| RMQ["RabbitMQ\naudit.events"]
    RMQ --> AW["Audit Worker"]
    AW -->|"InsertAuditSession"| P1
    AW -->|"Archive >30d → JSONL + DELETE"| P1
```

| Store | Data | Writer | Retention |
|---|---|---|---|
| **Redis** `chat:{userID}` | Last 20 turns | Orchestrator | 1 hour TTL |
| **Redis** `token:{userID}` | Google OAuth tokens | Orchestrator (OAuth callback) | Permanent |
| **PostgreSQL** `audit_sessions` | Session-level interaction logs | Audit Worker | **30 days** (archived to JSONL) |
| **PostgreSQL** `audit_events` | Per-span action traces | Audit Worker | Follows sessions |
| **PostgreSQL** `memories` | Semantic facts (3072-dim vectors) | Orchestrator (`remember_fact`) | Permanent |

---

## Clean Architecture: Port Interfaces

10 interfaces in [`ports.go`](file:///D:/MyPA/internal/orchestrator/ports.go):

| Interface | Methods |
|---|---|
| `TelegramClient` | `SendMessage`, `GetFile`, `DownloadFile` |
| `TwilioClient` | `SendMessage`, `DownloadMedia` |
| `AudioClient` | `TranscribeAudio` |
| `LLMClient` | `Chat`, `GenerateEmbedding` |
| `CalendarClient` | `CreateEvent`, `ListEvents`, `UpdateEvent`, `DeleteEvent` |
| `GmailClient` | `SearchEmails`, `ReadEmail`, `DraftReply`, `ArchiveEmail`, `SoftDeleteEmail`, `ListLabels`, `ApplyLabel`, `CreateLabel` |
| `TasksClient` | `ListTaskLists`, `CreateTaskList`, `ListTasks`, `CreateTask`, `CompleteTask`, `DeleteTask` |
| `DBClient` | `SaveMemory`, `SearchMemories`, `GetUniqueUsers`, `GetLastAuditSessionForUser` |
| **`EventPublisher`** | **`Publish(ctx, v any)`** ← NEW |
| `TavilyClient` | `Search` |

Unit tested with GoMock-generated mocks.

---

## Key Architecture Decisions

| Decision | Detail |
|---|---|
| **Async message processing** | Gateway responds instantly, offloads to RabbitMQ. Orchestrator has 5-min timeout. |
| **Gateway is dumb** | No AI, no downloads, no transcription. Only normalize and publish. |
| **RabbitMQ with manual ACK** | Durable queues, prefetch=1, manual ACK/NACK/Reject. Poison pill rejection. |
| **Decoupled audit pipeline** | Orchestrator → RabbitMQ `audit.events` → Audit Worker → PostgreSQL. Zero DB latency on request path. |
| **Structured audit telemetry** | Session + Event model with UUIDs, timing, token counts, JSONB payloads. |
| **30-day audit retention** | Daily archiver exports old sessions to JSONL, prunes from DB. |
| **Dual AI providers** | Gemini 2.5 Flash (reasoning), Groq Whisper (transcription), Gemini Embedding (memory). |
| **Semantic long-term memory** | pgvector 3072-dim cosine search on every message. `remember_fact` stores new facts. |
| **Redis for session state** | Sliding 20-msg window, 1h TTL. OAuth tokens permanent. |
| **Multi-tenant OAuth** | Per-user Google tokens in Redis. Calendar + Gmail + Tasks in one flow. Auto-refresh. |
| **Dual inbound channels** | Telegram + WhatsApp normalized to `models.Message`. |
| **Recursive tool loop** | No hard limit — recurses until LLM produces final text. |
| **Unified Dockerfile** | Multi-target with BuildKit caching. Distroless runtime images. Granular COPY. |
| **Generic broker** | `Publish(ctx, v any)` — reusable for any JSON-serializable payload type. |

---

## Complete End-to-End Flow

```
[User sends message on Telegram or WhatsApp]
        │
        ▼
[Platform API → Proxy (:8000) → Gateway (:8080)]
        │
        ▼
[Gateway: Normalize → Publish(models.Message) → RabbitMQ "telegram.inbound"]
        │
        ▼
[Orchestrator Engine: processMessage() — 5min timeout]
        │
        ├── [Create AuditSession UUID, start timer]
        │
        ├── [/connect?] → Return Google OAuth URL & exit
        │
        ├── [Voice?] → Download → Groq Whisper → preview → replace Text
        │
        ├── [Redis] → GetChatHistory (last 20)
        ├── [Gemini] → GenerateEmbedding (3072 dims)
        ├── [PostgreSQL] → SearchMemories (cosine ⇔, top 5)
        │
        ├── [Build system prompt] → datetime, timezone, tools, memories
        │
        ├── [Gemini LLM] → Chat @ temp 0.2, 20 tools
        │       ├── [Text] → Format & send
        │       └── [ToolCall] → Execute → feedbackToLLM → recurse
        │
        ├── [Format] → ToTelegramHTML() or ToWhatsApp()
        ├── [Chunk ≤1500] → smart boundary splitting
        ├── [Send] → Telegram/Twilio API
        ├── [Redis] → AppendChatHistory (user + assistant)
        │
        └── [defer] → Record EndTime, compute duration
                    → Publish(AuditSession) → RabbitMQ "audit.events"
                                                    │
                                                    ▼
                                          [Audit Worker]
                                          ├── InsertAuditSession → PostgreSQL
                                          └── Daily: Archive >30d → JSONL + DELETE
```
