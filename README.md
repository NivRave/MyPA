# MyPA — Personal AI Assistant

An event-driven personal AI assistant that captures natural language requests via Telegram and autonomously creates Google Calendar events using Gemini API tool calling.

## Architecture

```
Telegram Webhooks → Proxy (8000) → Gateway (8080) → RabbitMQ → Orchestrator (8081) → Gemini API & Google Calendar API
                                 ↳ OAuth Callbacks (8081)                      ↳ Redis (State & Tokens)
```

## Prerequisites

- [Go 1.24+](https://go.dev/dl/)
- [Docker & Docker Compose](https://docs.docker.com/get-docker/)
- [ngrok](https://ngrok.com/) (for local Telegram webhook development)
- Telegram Bot Token (from [@BotFather](https://t.me/BotFather))
- Gemini API Key (from [Google AI Studio](https://aistudio.google.com/))
- Google Cloud OAuth credentials (for Calendar API)

## Quick Start

1. **Clone and configure:**
   ```bash
   cp .env.example .env
   # Edit .env with your API keys and tokens
   ```

2. **Start infrastructure:**
   ```bash
   docker-compose up rabbitmq redis
   ```

3. **Run services (development):**
   ```bash
   go run ./cmd/gateway
   go run ./cmd/orchestrator
   go run ./cmd/proxy
   ```

4. **Expose the Proxy via ngrok:**
   ```bash
   ngrok http 8000 --url https://your-ngrok-url.ngrok-free.dev
   ```

5. **Set Telegram webhook:**
   ```bash
   curl "https://api.telegram.org/bot<YOUR_TOKEN>/setWebhook?url=<NGROK_URL>/webhook/telegram"
   ```

## Services

| Service | Port | Description |
|---|---|---|
| Proxy | 8000 | Unified API Gateway (routes to Gateway and Orchestrator) |
| Gateway | 8080 | Telegram webhook ingestion |
| Orchestrator | 8081 | LLM reasoning + Calendar execution + OAuth |
| RabbitMQ | 5672 | Message broker |
| Redis | 6379 | Conversation state + OAuth tokens |

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
  llm/              # Gemini API client + tool definitions
  calendar/         # Google Calendar API + OAuth
  state/            # Redis state management
  orchestrator/     # Core reasoning engine
```

## License

[MIT License](LICENSE)
