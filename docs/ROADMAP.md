# MyPA Feature Roadmap (V3)

This document tracks upcoming features and functional improvements for the MyPA project, with a focus on the V3 "Expansion" release.

## V3: The Expansion Update
*Focus: Delivering immediate tangible value through new capabilities, improved UX, and a pluggable ecosystem without rewriting the core engine.*

### 1. Core Platform & UX
- [x] **Web-based Control UI / Dashboard**: Provide a graphical dashboard to manage sessions, tools, configurations, view audit logs, and manage OAuth connections.
- [x] **Pairing Codes & Stricter Onboarding**: Secure the multi-tenant architecture by requiring an admin to approve new users via a pairing code.
- **Pluggable Ecosystem (MCP / Plugins)**: Move from hardcoded integrations to a dynamic plugin system (e.g., using the Model Context Protocol) to easily load new tools.
- **Local Model Support**: Add abstraction to support local LLMs (e.g., via Ollama) for enhanced privacy and local inference.
- **Companion Nodes for Device Context**: Build a lightweight desktop daemon that connects to the orchestrator to execute local scripts or provide local context.

### 2. Advanced AI Capabilities
- [x] **Multimodal Vision (Image Processing)**: Process images sent via Telegram/WhatsApp (e.g., snap a flyer for calendar events, fridge photos for recipes, or receipts for expenses).
- [x] **Custom Recurring Workflows (Macros)**: Allow users to define complex, multi-step cron jobs via natural language (e.g., weekly email summaries turned into task lists).
- **Voice Outbound (Two-way Voice)**: Add Text-to-Speech (e.g., ElevenLabs) so the assistant can respond with voice notes, such as for the morning briefing.
- **Smart Home Orchestration**: Integrate with Home Assistant or Google Home for natural language control of the physical environment, synced with calendar events.

### 3. Expanded Tooling & QoL
- [x] **Gmail Automation Flows**: Organize receipts, create events based on emails, suggest responses, and manage subscriptions.
- **Enhanced WhatsApp Integration**: Support group chats and expanded messaging capabilities.
- **Project Management Integration**: Connect to remote files and platforms like GitHub to manage project tasks directly.
- **Granular Feature Configuration**: Add optional configuration settings so users can enable only specific features.
- **List Management**: Create, update, and manage various lists (e.g., shopping, packing) naturally through conversation.
- **Cooking & Recipes**: Manage saved recipes and provide cooking recommendations.

### 4. Expansion Waves (Wave 1 & Wave 2 — Completed)
- [x] **Durability & Rollback (Topic 1)**: Immutable mutation audit log with before/after state snapshots and one-click rollback (`/undo` and GraphQL `rollbackMutation`).
- [x] **Topic-Isolated Memories & Entity Profiles (Topic 2)**: Temporal memory trees with automatic conflict resolution, superseding engine, and living entity profile cards.
- [x] **Multi-Schedule & Contextual Time-Blocking (Topic 3)**: Work vs. personal calendars, virtual sub-calendars, and schedule event overlays.
- [x] **Baby & Family Care Tracker (Topic 4)**: Dynamic child age calculation, wonder weeks developmental leap tracking, pediatric medication cooldown safety, routine care logging, and supplies inventory.
- [x] **House Maintenance, Contracts & Vehicle Vault (Topic 5 & 8.1)**: Lease, mortgage, warranty and receipt registry with renewal notice period countdowns, preventive upkeep checklists, and vehicle asset odometer/inspection monitoring.
- [x] **Financial Tracking & Budget Intelligence (Topic 6)**: Transaction ledger, receipt vision parsing, monthly category budgets with alert thresholds, >30% historical anomaly detection, and recurring bills tracking.
- [x] **On-Demand External APIs, Automations & Personal CRM (Topics 7 & 8)**: SSRF-protected on-demand REST/webhook API connector registry with AES-256-GCM encrypted auth headers, event-driven When→If→Then rules engine, user prompt preferences/persona customization, and Personal CRM with upcoming date alerts.
- [x] **Shared Family Spaces & Partner Sync (Topic 9)**: Collaborative spaces, cross-user push notifications via Telegram/WhatsApp, and claim/complete task hand-offs.
- [x] **Full GraphQL API & Next.js Web UI Expansion**: Comprehensive GraphQL schema covering all 9 domain models, paired with 6 dedicated feature dashboard pages in Next.js.

### 5. Backlog (Future Feature Ideas)
- **Voice Outbound (Two-way Voice)**: Add Text-to-Speech (e.g., ElevenLabs) so the assistant can respond with voice notes, such as for the morning briefing.
- **Smart Home Orchestration**: Native integration with Home Assistant or Google Home for environmental triggers.
- **Unified "Universal Search"**: Cross-cutting semantic search combining vector memories, financial transactions, contracts, and email archives.
- **Geofenced Automations**: Mobile presence detection for location-triggered reminders.
- **Smart Travel & Itinerary Builder**: Multi-leg itinerary generation with live flight status updates.
- **Social Media & "Ghostwriter" Mode**: Draft content suggestions aligned with user persona.
