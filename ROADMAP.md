# MyPA - Season 2 Roadmap

## Git Branching Strategy
We have transitioned to a structured branching workflow to ensure stability:
- **`master` branch:** Production-ready, stable code only.
- **`dev` branch:** The active development branch where new features are built and tested.

## Planned Features

### Phase 14: Long-Term Semantic Memory (Vector Database)
- **Goal:** Give the assistant infinite, searchable memory of past conversations and user preferences.
- **Implementation:** Upgrade PostgreSQL to use the `pgvector` extension. 
- **Mechanism:** Convert user messages into text embeddings via Google Gemini API and store them in Postgres. Retrieve relevant memories dynamically using vector similarity search during conversations.

### Phase 15: Morning Briefings & Proactive Messaging
- **Goal:** The assistant reaches out proactively rather than just responding.
- **Implementation:** Build a background chron scheduler within the Orchestrator.
- **Mechanism:** At a designated time (e.g., 8:00 AM), the bot pulls calendar events and weather, formulates a summary via LLM, and dispatches a message via Telegram or WhatsApp.

### Phase 16: Extended Tool Integrations
- [x] **Phase 16a: Gmail API Integration**: Let the LLM read unread emails and draft replies on the user's behalf.
- [x] **Phase 16b: Google Tasks Integration**: Build functionality to create and manage the user's Google Tasks.
- [ ] **Phase 16c: Web Search Integration (Tavily)**: Grant the LLM the ability to search the web for real-time answers.

### Phase 17: Cloud Deployment & CI/CD
- **Goal:** Move from local development to a 24/7 cloud environment.
- **Implementation:** 
  - Set up GitHub Actions for automated linting, testing, and deployment.
  - Deploy the dockerized microservice stack (Proxy, Gateway, Orchestrator, Postgres, Redis, RabbitMQ) to a cloud provider (e.g., DigitalOcean, Railway, or GCP).
