# MyPA V4: Architecture & System Vision

This document outlines the core architectural overhaul, security guardrails, advanced reasoning models, and infrastructure transformations planned for **MyPA V4 (The Enterprise Foundation)**. 

While V3 focuses on feature expansion, V4 focuses on rewriting the foundational data layer, execution models, and scaling capabilities.

## 1. Architecture & Infrastructure Overhaul

### Event Sourcing & CQRS
Move toward a true Event Sourcing architecture to ensure every state change is recorded as an immutable event log. This allows for perfect point-in-time recovery—if the AI makes an error, the system state can be "rewound."

### Serverless Ingestion & Cloud Deployment
- **Webhook Gateway**: Decouple the webhook ingestion layer into serverless edge functions (e.g., Cloudflare Workers) for high reliability and zero idle cost.
- **Proxy Replacement**: Replace the custom Go proxy service with an industry-standard ingress controller (Nginx, Traefik, or Caddy) for built-in rate limiting, SSL termination, and load balancing.
- **CI/CD**: Move from local development to a 24/7 cloud environment with automated linting, testing, and deployment via GitHub Actions.

### Observability & Distributed Tracing
Implement OpenTelemetry across the Proxy, Gateway, and Orchestrator for visual distributed tracing to monitor exact latency breakdowns (e.g., Gemini API vs. PostgreSQL vs. RabbitMQ).

## 2. Security & Guardrails

### Prompt Injection Defense ("The Guardian")
Pass incoming prompts through a fast, specialized local model or heuristic filter to detect and block prompt injection attacks before reaching the main orchestrator.

### Encrypted State & Tokens
Enforce application-level encryption at rest for all sensitive data. OAuth tokens (in Redis) and personal conversation memories (in PostgreSQL) must be cryptographically locked.

### Cost Controls & Circuit Breakers
Enforce strict token budgets per user per day. Implement circuit breakers to detect and halt recursive tool-calling loops.

## 3. Advanced AI Reasoning & Brain Management

### Multi-Agent Swarm Architecture
Transition from a single monolithic orchestrator to specialized sub-agents (e.g., Travel Agent, Finance Agent). These agents will debate and synthesize final answers for complex tasks.

### "Measure Twice, Cut Once" Eval Loops
Distinguish between non-destructive and destructive actions. Destructive actions (deleting events, sending emails) require the orchestrator to generate a plan that is validated by a secondary "Reviewer" agent before execution.

### Explicit Reflection & Tracing
Require the orchestrator to output its internal Chain of Thought in a structured format, stored in the Audit Log, ensuring deterministic explanations for AI decisions.

### API Auto-Discovery (Zero-Shot Integrations)
Enable the assistant to autonomously read OpenAPI/Swagger docs and figure out how to call new third-party services without hardcoded integrations.

## 4. Memory Architecture

### Memory Consolidation ("Sleep Cycles")
Implement a nightly "sleep cycle" cron job where a background agent processes the day's logs, extracts permanent facts, updates a compressed Knowledge Graph, and deletes stale vectors.

### Entity-based Knowledge Graph
Supplement vector search with a structured Knowledge Graph to deterministically map relationships, preferences, and entities.

### Cross-Encoder Re-ranking
Enhance memory retrieval by fetching a broad set of memories from PostgreSQL and using a cross-encoder model to re-rank them, passing only the most relevant context to the orchestrator.

## 5. Scope & Limitations (The Constitution)

### Hard Boundaries
Establish a core "Constitution" defining forbidden actions (e.g., never send emails without explicit user confirmation, never initiate contact with unauthorized numbers).

### Graceful Degradation
Design the system to handle LLM API outages gracefully by falling back to a "dumb" mode where hardcoded slash commands continue to function via direct API calls.
