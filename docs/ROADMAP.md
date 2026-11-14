# MyPA Feature Roadmap

This document tracks completed milestones across the V3 Expansion & V3.1 Production Hardening releases, along with the upcoming feature backlog.

---

## 1. Core Platform & UX (V3.0 — Completed)
- [x] **Web-based Control UI / Dashboard**: Next.js 16 (React 19) graphical dashboard to manage sessions, tools, configurations, audit logs, and OAuth connections.
- [x] **Pairing Codes & Stricter Onboarding**: Multi-tenant onboarding requiring a 6-digit admin-issued pairing code with Redis rate-limiting and lockout protection.
- [x] **Modular `ToolModule` Plugin Registry**: Decoupled plugin architecture (`internal/orchestrator/registry.go`) hosting 70 tools across 13 domain packages with dynamic Two-Tier Intent Routing.
- [x] **Multimodal Vision (Image & PDF Processing)**: Process receipts, flyers, and contracts sent via Telegram/WhatsApp using Gemini Vision.
- [x] **Custom Recurring Workflows (Macros)**: User-defined multi-step cron jobs in natural language (`schedule_workflow` and `cron` automation rules).
- [x] **Gmail Automation Flows**: Search, read, draft replies, archive, apply labels, and execute SSRF-guarded `List-Unsubscribe` flows.

---

## 2. Life Domain Expansion Waves (V3.0 — Completed)
- [x] **Durability & Rollback (Topic 1)**: Immutable mutation audit log (`mutation_logs`) with `BeforeJSON`/`AfterJSON` snapshots, allowlisted ACID rollback (`/undo` and GraphQL `rollbackMutation`), and CSV/JSON export.
- [x] **Topic-Isolated Memories & Entity Profiles (Topic 2)**: Hierarchical topic memories (`pgvector` 3,072-dim) with automatic conflict superseding, living `EntityProfile` cards, and nightly consolidation.
- [x] **Multi-Schedule & Virtual Sub-Calendars (Topic 3)**: Virtual `[Topic]` sub-calendars, internal-only high-frequency schedules, and bidirectional partner Google Calendar sync.
- [x] **Baby & Family Care Tracker (Topic 4)**: Dynamic child age & Wonder Weeks leap engine, pharmacological class medication cooldown guardrails (Paracetamol 4h / Ibuprofen 6h), care logs, and supply inventory.
- [x] **House Maintenance, Contracts & Vehicle Vault (Topic 5 & 8.1)**: Lease, mortgage, warranty, and receipt vault with notice period countdowns, preventive home maintenance checklists, and vehicle asset monitoring.
- [x] **Financial Tracking & Budget Intelligence (Topic 6)**: Expense/income ledger, monthly category budgets with alert thresholds, >30% historical anomaly detection, and recurring bills tracking.
- [x] **On-Demand External APIs, Automations & Personal CRM (Topics 7 & 8)**: SSRF-protected REST/webhook connector registry with AES-256-GCM encrypted headers, `When -> If -> Then` `EventBus` rules engine, user persona preferences, and Personal CRM.
- [x] **Shared Family Spaces & Partner Sync (Topic 9)**: Per-topic family sharing permissions, cross-user push notifications via Telegram/WhatsApp, and shared reminder hand-offs.
- [x] **Full GraphQL API & Decoupled Next.js Dashboard**: 22 queries and 28 mutations routed through domain repositories, paired with 14 Next.js App Router views.

---

## 3. Production Security, Scalability & Test Hardening (V3.1 — Completed)
- [x] **Defense-in-Depth Ingress & Pre-Auth (`SEC-01`–`SEC-03`, `SEC-10`, `SEC-11`)**: Pre-auth user verification, Admin RBAC on `/backup` & `/restore`, Telegram secret token & Twilio HMAC-SHA1 signature verification, constant-time GraphQL Bearer/JWT middleware, localhost port bindings, and `http.MaxBytesReader` limits.
- [x] **Positive Multi-Tenant Authorization & Anti-IDOR (`SEC-04`)**: Compound tenant scoping across all 8 domain repositories and GraphQL resolvers with zero tool-argument tenant overrides.
- [x] **Centralized SSRF NetGuard (`SEC-05`)**: Custom dialer (`internal/netguard`) blocking loopback, RFC1918 private networks, link-local/cloud metadata (`169.254.169.254`), and internal single-label Docker hostnames.
- [x] **HKDF-SHA256 Salted AES-256-GCM & OAuth CSRF Nonces (`SEC-07`, `SEC-08`)**: Salted key derivation for encrypted Redis/PostgreSQL credentials and 32-byte single-use `GETDEL` OAuth state nonces.
- [x] **Prompt Injection Boundaries & Confirmation Gates (`SEC-09`)**: `<untrusted_external_content>` XML tagging and interactive `CONFIRM <TOOL>` staging for privileged meta-tools.
- [x] **Engine & Prompt Scalability (`SCALE-01`–`SCALE-03`)**: Redis `PromptCache` with `EventBus` invalidation, bilingual (English/Hebrew) two-tier tool intent matching (~80% token savings), `PhotoData` stripping from Redis history, 8-turn recursion ceiling, and concurrent `errgroup` tool execution.
- [x] **Broker Resilience, Dual-Lane EventBus & DB Tuning (`SCALE-04`, `SCALE-05`)**: Mutex-serialized `SafePublisher` with publisher confirms, `mypa.dlx` dead-letter queues with 3-attempt retries, deterministic dual-lane `EventBus` (sync durability + 4-worker async pool), PostgreSQL connection pool sizing (`25/10`), and `halfvec(3072)` HNSW + composite B-Tree indexes.
- [x] **Full-Stack Automated Test Suite**: Singleton Testcontainers harness (PostgreSQL 16 + Redis 7) covering all 8 repositories and GraphQL resolvers, plus 31 Vitest + React Testing Library dashboard tests.

---

## 4. Future Backlog (V4 Horizon)
- [ ] **Voice Outbound (Two-Way Voice)**: Text-to-Speech integration (e.g., ElevenLabs / Gemini Live Audio) for spoken morning briefings and voice replies.
- [ ] **Local Model Support**: Pluggable LLM provider abstraction supporting local inference (e.g., Ollama / vLLM) for offline or privacy-sensitive turns.
- [ ] **Smart Home Orchestration**: Native Home Assistant / Matter integration for natural-language environmental control synced with family schedules.
- [ ] **Companion Desktop / Mobile Nodes**: Lightweight daemon providing local device context and script execution.
- [ ] **Unified Universal Search**: Cross-domain hybrid search combining vector memories, financial transactions, contracts, and email archives in a single query.
- [ ] **OpenTelemetry Distributed Tracing**: End-to-end span visualization across Proxy, Gateway, RabbitMQ, Orchestrator, and Gemini API calls.
