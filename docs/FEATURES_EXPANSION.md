# MyPA Expansion Specification: Durability, Topic Memory, Life Domains & Shared Spaces

> [!NOTE]
> **Implementation Status: 100% Complete (Delivered in V3.0 & V3.1)**  
> All 9 feature topics and 4 rollout milestones defined in this specification are fully implemented in production across `internal/domains/`, `internal/db/`, `internal/graph/`, and `dashboard/`. See [`SYSTEM.md`](../SYSTEM.md) for the canonical implementation reference.

This document defines the approved feature topics and phased rollout milestones for upgrading **MyPA** with enterprise-grade durability, topic-mapped memory, multi-schedule management, life-domain trackers (Baby, House, Finance), dynamic external APIs/automations, and multi-user shared spaces.

---

## Topic 1: Durability, Persistence & Data Integrity
*Goal: Ensure no state, credentials, or user records are ever lost or corrupted, with full auditability and rollback capability.*

- **1.1 Persistent Token & State Fallback**: Dual-write OAuth tokens and critical user session metadata to PostgreSQL (encrypted at rest) so a Redis restart or flush never disconnects Google accounts or wipes active context.
- **1.2 Immutable Mutation Log & Undo/Rollback**: Track all AI-initiated data mutations (memories updated, events created/deleted, expenses logged) with previous-state snapshots, allowing conversational commands like *"Undo that last change"* or Web Dashboard rollbacks.
- **1.3 Idempotency & Deduplication Guardrails**: Prevent duplicate calendar events, double-logged receipts, or repeated reminders when webhooks retry or multi-turn tool calls loop.
- **1.4 Automated Schema Validation & Dead-Letter Queue (DLQ)**: Strict validation on structured data writes plus a RabbitMQ DLQ so failed messages/tasks are persisted and retried safely rather than dropped.
- **1.5 Granular Data Export & Automated Cloud/Local Backups**: Extend `/backup` to support structured JSON/CSV exports per topic (e.g., export all financial records or house contracts) alongside full PostgreSQL + vector snapshots.

---

## Topic 2: Topic-Mapped Memory & Knowledge Graph
*Goal: Move beyond flat vector search to structured, topic-isolated, conflict-free long-term memory.*

- **2.1 Hierarchical Topic & Tag Taxonomy**: Upgrade `Memory` with `Topic` (e.g., `baby`, `house`, `finance`, `health`, `work`), `Subtopic`, and `Tags[]` (`jsonb`), enabling hybrid filtering (Topic + Vector similarity) so queries about one domain do not pull in unrelated facts.
- **2.2 Structured Entity Profiles ("Living Documents")**: Alongside unstructured vector facts, maintain structured JSONB entity cards (e.g., `BabyProfile`, `Property`, `Vehicle`) that get deterministically updated when facts change (e.g., weight, address, rent amount) rather than accumulating contradictory vector rows.
- **2.3 Memory Lifecycle, Superseding & Conflict Resolution**: When a new fact contradicts an old one (e.g., *"Rent increased to $2,200"*), automatically mark the older memory as `superseded_by` with temporal validity (`valid_from` / `valid_until`) instead of returning both to the LLM.
- **2.4 Nightly Memory Consolidation ("Sleep Cycle")**: A scheduled background job that reviews the day's conversation logs and raw memories, merges duplicates, assigns/refines topics, and updates structured entity profiles.
- **2.5 Dashboard Memory Explorer**: A visual Topic Tree & Memory Editor in the Next.js Dashboard (`:3030`) to browse, edit, pin, re-tag, or delete memories by topic.

---

## Topic 3: Multi-Schedule & Virtual Sub-Calendars (By Topic/Tags)
*Goal: Support distinct schedules per topic whether using a single Google Calendar, multiple sub-calendars, or internal MyPA schedules.*

- **3.1 Virtual Topic Calendars on a Single Google Calendar**: Support multiple logical schedules (`[Baby]`, `[House]`, `[Finance]`, `[Work]`, `[Personal]`) mapped onto a single Google Calendar via consistent prefix/tag metadata, color-coding (`colorId`), and extended properties—or optionally routed to separate Google sub-calendars.
- **3.2 Internal MyPA Native Schedule Store**: Maintain an internal PostgreSQL schedule table synced with Google Calendar (or standalone for high-frequency internal schedules like baby feeding intervals or medication logs that shouldn't clutter the main Google Calendar view).
- **3.3 Topic-Filtered Queries & Briefings**: Query schedules by topic (*"What's on the house maintenance schedule this month?"*, *"Show only the baby's medical schedule"*) and customize morning briefings by topic per user preference.

---

## Topic 4: Baby & Family Care Tracker (Lifecycle-Aware)
*Goal: Maintain baby data, routines, medical schedules, and proactive developmental suggestions based on age/DOB.*

- **4.1 Dynamic Profile & Age/Milestone Engine**: Store key attributes (Name, Date of Birth, due date/gestational age, gender, pediatrician, allergies, current weight/height) and automatically compute exact age in days/weeks/months on every turn.
- **4.2 Developmental & Medical Schedule Generator**: Proactively suggest and schedule age-appropriate vaccinations, pediatric checkups, developmental leaps, sleep window adjustments, and feeding transitions based on DOB.
- **4.3 Daily Care & Routine Logs**: Quick conversational logging via text or voice (*"Fed 120ml at 14:30"*, *"Diaper change"*, *"Fever 38.2°C given paracetamol"*) stored in structured time-series tables with trend summaries (*"How much did the baby eat over the last 3 days?"*).
- **4.4 Smart Care Reminders & Inventory Alerts**: Track recurring baby supplies (formula, diapers, vitamin D drops) and medication cooldowns (e.g., *"When can we give the next dose of fever reducer?"*).

---

## Topic 5: House Maintenance, Contracts & Document Vault
*Goal: Centralize home ownership/rental contracts, receipts, warranties, and preventive maintenance.*

- **5.1 Lease/Mortgage & Contract Registry**: Track property details (rent vs. buy, landlord/HOA contacts, lease renewal dates, notice periods, warranty expirations, insurance policies) with multi-stage advance reminders (e.g., 60 days and 30 days before lease renewal).
- **5.2 Multimodal Receipt & Contract Ingestion**: Send a photo or PDF of a contract, utility bill, or repair receipt via Telegram/WhatsApp to extract vendor, amount, warranty period, and key clauses, storing the metadata and linking it to the `house` topic and `finance` tracker.
- **5.3 Preventive Home Maintenance Engine**: Seasonal and interval-based maintenance schedules and suggestions (e.g., AC filter cleaning, boiler servicing, pest control, appliance descaling) with last-completed tracking and contractor contact memory.

---

## Topic 6: Financial Tracking & Budget Intelligence
*Goal: Provide durable expense, bill, and budget tracking integrated with chat, vision receipts, and email flows.*

- **6.1 Conversational & Vision Expense/Income Ledger**: Dedicated PostgreSQL financial tables (`transactions`, `budgets`, `subscriptions`) populated via chat (*"Spent 180 NIS on groceries"*), receipt photos, or Gmail receipt automation flows.
- **6.2 Recurring Bills, Subscriptions & Anomaly Detection**: Track recurring charges (rent, utilities, streaming, daycare), alert before renewal dates, and flag unusual bill spikes (e.g., *"Your electricity bill is 40% higher than average"*).
- **6.3 Category Budgets & Periodic Financial Briefings**: Set monthly budgets by topic/category (`baby`, `house`, `groceries`, `dining`) with weekly/monthly spend breakdowns in chat and visual charts in the Web Dashboard.

---

## Topic 7: On-Demand External APIs & User-Defined Automations
*Goal: Allow users to connect external APIs and build rich event-driven automations at runtime.*

- **7.1 Dynamic On-Demand API Connector Registry**: Register external REST/GraphQL APIs or Webhooks at runtime (via Dashboard or chat) with custom headers/auth tokens stored securely, plus an `http_request` / OpenAPI tool so the agent can query external services on demand (weather, currency rates, municipal APIs, smart home webhooks, package tracking).
- **7.2 Event-Driven & Condition-Based Automations (`When -> If -> Then`)**: Expand `Workflow` beyond simple cron expressions to support **triggers** (Cron time, incoming Gmail matching a filter, new expense over `$X`, baby age milestone reached, webhook received) + **conditions** + **actions** (send message, call external API, create task/calendar event, update memory).
- **7.3 Runtime Customizability (Personas, Custom Prompts & Preferences)**: Per-user and per-topic customization in chat or Dashboard—configure briefing times, quiet hours, preferred language/tone, active topics, default currency, and custom "standing instructions".

---

## Topic 8: Additional High-Value Capabilities
*Goal: Extend structured life management to vehicles, personal relationships, and interactive confirmations.*

- **8.1 Vehicle & Asset Tracker**: Track car insurance, annual licensing/test dates, mileage, service history, and tire/oil change intervals.
- **8.2 Personal & Family CRM (Important Dates & Gift/Medical Memory)**: Track birthdays, anniversaries, clothing sizes, gift ideas, and family medical history/prescriptions with advance preparation reminders.
- **8.3 Interactive Action Confirmation Buttons**: Use Telegram inline keyboards / WhatsApp quick replies for destructive or financial confirmations (`[✅ Confirm Expense] [✏️ Edit Category] [❌ Cancel]`) to guarantee data integrity.

---

## Topic 9: Shared Multi-User Spaces, Sync & Cross-User Notifications
*Goal: Seamlessly share topic memories, schedules, tasks, and real-time notifications between defined family/partner users.*

- **9.1 Topic-Level Sharing & Access Control (`SharedSpaces`)**:
  - Define sharing rules per topic or entity between specific users (e.g., `baby`, `house`, and `family_finance` are automatically shared between partners, while `work` or `personal` remain private by default).
  - Granular permissions (`view`, `edit`, `notify`) configurable via chat (*"Share the baby and house topics with my wife"*) or the Web Dashboard.
- **9.2 Automatic Cross-User Notifications & Activity Feed**:
  - When a user logs data, creates an event, or updates an entity in a shared topic (e.g., *"Baby fed 120ml at 14:00"*, *"Scheduled pediatrician visit for Tuesday"*, or *"Paid electricity bill"*), automatically notify the other subscribed member(s) via their preferred channel (Telegram/WhatsApp).
  - Configurable notification modes per topic: **Instant push** (e.g., medication given, urgent schedule changes), **Smart digest** (batched summary), or **Silent sync** (available when queried, no push alert).
- **9.3 Shared & Cross-Synced Calendars/Schedules**:
  - When creating a schedule item on a shared topic (e.g., `[Baby]` or `[House]`), automatically:
    1. Save it to the shared internal MyPA schedule so both users see it when querying schedules.
    2. Sync it to **both** users' connected Google Calendars (or invite the partner's email as an attendee / write to a designated shared family Google Calendar) without creating duplicates.
- **9.4 Shared Reminders, Tasks & "Claim/Complete" Hand-offs**:
  - Support shared reminders and to-dos (e.g., *"Remind us to give Vitamin D at 19:00"* or *"Buy diapers"*).
  - When one partner marks a shared reminder or task as done (*"I gave the drops"* or clicks `[✅ Done]`), log **who** completed it (`completed_by`), cancel the pending reminder for the other partner, and send a confirmation notice.
- **9.5 Attribution & Collaborative Audit Trail**:
  - Store `created_by_user_id` and `updated_by_user_id` on every shared memory, baby log, financial transaction, or house record so either partner can query who logged or completed an item and when.

---

## Phased Rollout Milestones

1. **Milestone 1: Core Foundation — Data Integrity, Topic Memory & Shared Spaces (Topics 1, 2 & 9)**
   - Persistent encrypted OAuth/state fallback in PostgreSQL, mutation audit log + Undo support, hierarchical `Topic`/`Subtopic`/`Tags` memory schema, structured Entity Profiles (`jsonb`), memory superseding, and `SharedSpace` cross-user attribution + notification routing.
2. **Milestone 2: Multi-Schedule Engine & Virtual Sub-Calendars (Topic 3 + Topic 9 Calendar Sync)**
   - Internal PostgreSQL schedule table + single/multi Google Calendar topic tagging (`[Baby]`, `[House]`, etc.), color mapping, topic-filtered queries/briefings, and automatic partner calendar sync.
3. **Milestone 3: Life Domain Modules — Baby Tracker, House Vault & Financial Ledger (Topics 4, 5 & 6)**
   - Dedicated structured schemas and LLM tools for Baby care logs/milestones, House contracts/warranties/maintenance schedules, and Expense/Budget tracking (integrated with vision receipt parsing and shared partner notifications).
4. **Milestone 4: Dynamic External APIs, Event-Driven Automations & Customizability (Topics 7 & 8)**
   - On-demand API connector registry, `When -> If -> Then` event-driven automations, per-user runtime preferences/standing instructions, Vehicle/CRM trackers, and interactive confirmation buttons.
