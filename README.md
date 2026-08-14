# MyPA — Personal AI Assistant

An event-driven personal AI assistant that captures natural language requests via Telegram and autonomously interacts with Google Calendar using Gemini API tool calling. It supports full CRUD (Create, Read, Update, Delete) operations, conversational event summarization, and multi-turn tool execution.

## Architecture

```text
Telegram/WhatsApp Webhooks → Proxy (8000) → Gateway (8080) → RabbitMQ → Orchestrator (8081) → Gemini API & Google Calendar API
                                 ↳ OAuth Callbacks (8081)                      ↳ Redis (State & Tokens)
                                                                               ↳ PostgreSQL (Audit Logs & Vector Memory)
```

## Features
- **Multi-Channel Support**: Available on both Telegram and WhatsApp (via Twilio API).
- **Conversational Scheduling**: Create Google Calendar events via natural language (infers dates and times).
- **Gmail Integration**: Ask the assistant to read your unread emails and draft replies directly from Telegram/WhatsApp.
- **Google Tasks Integration**: Create, complete, list, and delete Google Tasks effortlessly.
- **Semantic Memory**: Infinite, searchable long-term memory of past conversations and user preferences using PostgreSQL `pgvector`.
- **Proactive Morning Briefings**: A background scheduler proactively wakes up and messages the user a summary of their daily schedule.
- **Voice Commands**: Send voice messages via Telegram or WhatsApp, transcribed automatically using the Groq Whisper API.
- **Calendar Querying & Summarization**: Ask "What do I have tomorrow?" to retrieve and naturally summarize events.
- **Event Modifications**: Update or delete events with a built-in safety confirmation flow to prevent accidental data loss.
- **Multi-turn Execution**: Capable of recursive reasoning, such as fetching events and then acting upon the retrieved list in a single user turn.
- **Audit Logging**: Asynchronously logs all user requests, LLM responses, and executed actions to a database.
- **Microservice Architecture**: Decoupled ingestion and execution layers connected via RabbitMQ.

## V1 (MVP) vs Future Roadmap

The features listed above comprise the **V1 Minimum Viable Product (MVP)**. The V1 core engine is fully built, containerized, and production-ready, effectively serving as an intelligent calendar and scheduling assistant.

We are currently in **Season 2** development. We have successfully rolled out infinite semantic memory and proactive daily briefings, and are now gearing up for new API integrations (like Gmail/Todoist) and cloud CI/CD deployment.

👉 **See the [ROADMAP.md](ROADMAP.md) for the full list of planned future features.**

## Prerequisites

- [Go 1.24+](https://go.dev/dl/)
- [Docker & Docker Compose](https://docs.docker.com/get-docker/)
- [ngrok](https://ngrok.com/) (for local Telegram webhook development)
- Telegram Bot Token (from [@BotFather](https://t.me/BotFather))
- Twilio Account SID & Auth Token (for WhatsApp integration)
- Gemini API Key (from [Google AI Studio](https://aistudio.google.com/))
- Groq API Key (from [GroqCloud](https://console.groq.com/))
- Google Cloud OAuth credentials (for Calendar API)

## Quick Start

### Running with Docker (Recommended)

1. **Clone and configure:**
   ```bash
   cp .env.example .env
   # Edit .env with your API keys and tokens
   ```

2. **Start all services:**
   ```bash
   docker-compose up -d
   ```

3. **Expose the Proxy via ngrok:**
   ```bash
   ngrok http 8000 --url https://your-ngrok-url.ngrok-free.dev
   ```

4. **Set Webhooks:**
   - **Telegram:**
     ```bash
     curl "https://api.telegram.org/bot<YOUR_TOKEN>/setWebhook?url=<NGROK_URL>/webhook/telegram"
     ```
   - **WhatsApp:** Paste `<NGROK_URL>/webhook/twilio` into your Twilio WhatsApp Sandbox settings.

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
   ```

4. **Expose and set webhook:** Follow steps 3 & 4 from the Docker instructions above.

## Services

| Service | Port | Description |
|---|---|---|
| Proxy | 8000 | Unified API Gateway (routes to Gateway and Orchestrator) |
| Gateway | 8080 | Telegram webhook ingestion |
| Orchestrator | 8081 | LLM reasoning + Calendar execution + OAuth |
| RabbitMQ | 5672 | Message broker |
| Redis | 6379 | Conversation state + OAuth tokens |
| Database | 5432 | Audit logging storage (PostgreSQL) |

## Project Structure

```
cmd/
  gateway/          # Telegram webhook service
  orchestrator/     # LLM reasoning + execution service
internal/
  config/           # Viper configuration
  models/           # Shared domain types
  broker/           # RabbitMQ publisher/consumer
  telegram/         # Telegram Bot API client
  twilio/           # Twilio API client and webhook handler (WhatsApp)
  audio/            # Groq Whisper API client for voice processing
  llm/              # Gemini API client + tool definitions
  calendar/         # Google Calendar API + OAuth
  state/            # Redis state management
  db/               # Database client for audit logging and memory
  scheduler/        # Cron jobs and background proactive messaging
  orchestrator/     # Core reasoning engine
```

## License

[MIT License](LICENSE)
