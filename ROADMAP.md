# MyPA - Season 2 Roadmap

## Git Branching Strategy
We have transitioned to a structured branching workflow to ensure stability:
- **`master` branch:** Production-ready, stable code only.
- **`test` branch:** Staging environment for testing integrated features before they go to master.
- **`dev` branch:** The active development branch where new features are built.

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
- **Goal:** Expand the assistant's capabilities beyond calendar management.
- **Implementation:**
  - **Gmail API:** Read and summarize unread emails, draft replies.
  - **Task Management:** Integrate with Todoist, Notion, or Trello.
  - **Web Search:** Give the LLM a web-browsing tool for real-time information retrieval.

### Phase 17: Cloud Deployment & CI/CD
- **Goal:** Move from local development to a 24/7 cloud environment.
- **Implementation:** 
  - Set up GitHub Actions for automated linting, testing, and deployment.
  - Deploy the dockerized microservice stack (Proxy, Gateway, Orchestrator, Postgres, Redis, RabbitMQ) to a cloud provider (e.g., DigitalOcean, Railway, or GCP).
