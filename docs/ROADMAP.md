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
- **Multimodal Vision (Image Processing)**: Process images sent via Telegram/WhatsApp (e.g., snap a flyer for calendar events, fridge photos for recipes, or receipts for expenses).
- **Custom Recurring Workflows (Macros)**: Allow users to define complex, multi-step cron jobs via natural language (e.g., weekly email summaries turned into task lists).
- **Voice Outbound (Two-way Voice)**: Add Text-to-Speech (e.g., ElevenLabs) so the assistant can respond with voice notes, such as for the morning briefing.
- **Smart Home Orchestration**: Integrate with Home Assistant or Google Home for natural language control of the physical environment, synced with calendar events.

### 3. Expanded Tooling & QoL
- **Gmail Automation Flows**: Organize receipts, create events based on emails, suggest responses, and manage subscriptions.
- **Enhanced WhatsApp Integration**: Support group chats and expanded messaging capabilities.
- **Project Management Integration**: Connect to remote files and platforms like GitHub to manage project tasks directly.
- **Granular Feature Configuration**: Add optional configuration settings so users can enable only specific features.
- **List Management**: Create, update, and manage various lists (e.g., shopping, packing) naturally through conversation.
- **Cooking & Recipes**: Manage saved recipes and provide cooking recommendations.

### 4. Backlog (Future Feature Ideas)
- **Automated Expense & Budget Tracking**
- **Proactive Relationship Management (Personal CRM)**
- **Smart Scheduling & Contextual Time-Blocking**
- **Unified "Universal Search"**
- **Geofenced Automations**
- **Health & Habit Check-ins**
- **Smart Travel & Itinerary Builder**
- **Shared Family Dashboard**
- **Social Media & "Ghostwriter" Mode**
- **Personal "Data Takeout" & Full Ownership**
