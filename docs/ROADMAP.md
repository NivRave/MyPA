# MyPA Roadmap

> **The single source of truth for where MyPA is going.**
> Last reviewed: 2026-11-18 (verified against the code) · Current release: v3.4.0 (the pre-V4 baseline) · Next milestone: V4.0

---

## 1. How to use this file

- **One file.** Planned work lives here and nowhere else. Bugs and side findings go to [`BACKLOG.md`](./BACKLOG.md), architectural choices to an ADR in [`adr/`](./adr/), decisions made while implementing to [`DECISIONS_LOG.md`](./DECISIONS_LOG.md). How the system works today is in [`SYSTEM.md`](../SYSTEM.md).
- **Stable IDs.** Every item has an ID (`V4-xx` committed, `EV-xx` under evaluation, `DE-xx` decisions). Use it in commits and pull requests. IDs are never reused; V4-42 was never assigned.
- **Status values:** `Planned`, `In progress`, `Done`, `Parked`.
- **Size** is relative effort (S / M / L), not a schedule.
- **Versions.** Milestones map to Semantic Versioning ([`AGENTS.md`](../AGENTS.md) §5): V4.0 is `v4.0.0` (breaking topology, configuration and data-model changes), V4.1 is `v4.1.0`, V4.2 is `v4.2.0`. V4.2 changes topology again (V4-23 removes RabbitMQ and Redis); it keeps a minor version only if the upgrade from V4.1 is automatic, and is otherwise tagged as the next major version.
- **When an item ships:** set it to `Done` with the release tag. At the release, move its row to §10 Delivered.
- **Done on every channel.** An item is `Done` only when it works on every supported channel, or degrades in a documented way, proven by the conformance suite (V4-34).
- **Adding an item:** it needs a one-line "why" and a "done when". Anything that adds a new way to execute code, or a new unauthenticated surface, needs an ADR first. The ADRs already known to be needed are listed in §7.
- **Changing the plan while it is being built.** The plan is read again before every item, so a change takes effect at the next item. A new idea becomes a new item with an ID, a why and a "done when". A changed decision is recorded in §7 as a new decision that replaces the old one; the old line stays. A finished item is never edited: rework is a new item. The row of the item being built is not edited. Findings outside the current item go to [`BACKLOG.md`](./BACKLOG.md). A fix for the live system follows the hotfix rule in [`AGENTS.md`](../AGENTS.md) §5.
- **Review** this file at every minor release.

---

## 2. Product principles (strengths to protect)

These are the areas where MyPA is ahead of comparable open-source assistants (§11). New work must not weaken them.

1. **Family-first and multi-tenant.** Every feature works for several people, with tenant isolation.
2. **Every write is undoable.** New write tools emit a `MutationEvent` and support `/undo` and dashboard rollback.
3. **Safe by default.** Fail-closed ingress, SSRF guard, encryption at rest, confirmation gates and untrusted-content boundaries. Every new surface gets adversarial tests.
4. **Privacy in logs.** No message content or personal data at INFO level, enforced by tests.
5. **Low idle cost.** Event-driven and schedule-driven. No always-on model heartbeat. Two-tier tool routing keeps prompts small.
6. **Domain depth over breadth.** Guardrails such as medication cooldowns, notice-period countdowns and budget anomaly alerts.
7. **Release discipline.** Semantic versions, versioned migrations, CI gates and generated docs.
8. **Channel-neutral.** The same outcome on every supported channel, not necessarily the same interface. Every flow can be completed with plain typed replies; buttons and links are enhancements.

---

## 3. V4 at a glance

| Milestone | Theme | Outcome |
| :--- | :--- | :--- |
| **V4.0** | Foundations | Simpler to run; households, channels, sign-in and packs become proper building blocks; not tied to one model vendor; less legacy code |
| **V4.1** | Daily use | A setup wizard and guided onboarding, a reorganised dashboard, easier approvals, better recall, family group chat, voice replies, the Household pack, Discord |
| **V4.2** | Reach | More integrations without hand-written modules, RabbitMQ and Redis removed, care templates |
| **Later** | Under evaluation | Decided after V4.2, each with a stated trigger |

V4.0 comes first because its building blocks (one binary, the channel layer, households, sign-in, packs and the provider layer) make every later item cheaper to build. It is the largest milestone and changes configuration and the data model, so it is built in stages, each of which leaves the app working. Items are listed in build order. V4.2 changes topology once more (V4-23). Both upgrades go through V4-43.

1. **Safety net and cleanup:** V4-34 (pinning down today's behaviour first), V4-17, V4-43 (so every later stage can be rolled back), V4-06, V4-09.
2. **Structure:** V4-01, V4-31, V4-40, V4-30, V4-32.
3. **Access and scope:** V4-35, V4-27, V4-05, V4-08, V4-41, V4-33.
4. **Models, cost and legacy:** V4-03, V4-02, V4-04, V4-07 (last, because it waits on OP-7 and on the re-encryption pass).

**Build order after V4.0**

- **V4.1:** V4-39, V4-10, V4-11, V4-24, V4-36, V4-37, V4-38, V4-44, then the state-store ADR, then V4-12, V4-13, V4-14, V4-15, V4-16, V4-18, V4-28.
- **V4.2:** V4-23, V4-26, V4-29, then the MCP ADR, then V4-19, V4-20, V4-25, V4-21, V4-22.

**Before V4.0 starts**

- **Done 2026-11-18:** baseline release cut from `dev` (OP-10) as tag `v3.4.0`, with backup `backup_2026-11-18_21-52-46.dump.age` taken on it. It is the rollback point and the baseline that V4-43 restores from.
- Stage 1 needs no ADR and can start as soon as the baseline exists. Write the identity ADR and the channel ADR (§7) before stage 2.
- Owner tasks that gate later items: OP-7 (V4-07) and OP-8 (V4-41). Neither blocks stage 1. OP-1 is done.

---

## 4. V4.0 — Foundations

| ID | Item | What, and done when | Origin | Size | Status |
| :--- | :--- | :--- | :--- | :---: | :--- |
| V4-01 | **Service consolidation** | Merge `proxy`, `gateway` and `orchestrator` into one binary with `--role` (default `all`), keeping the durable queue hand-off, as recommended in ADR 0001 option 1c. The `audit-worker` stays a separate container until its queue moves to Postgres (V4-23). **Done when** `docker compose up` runs one app container plus the audit worker, the dashboard and infrastructure, and the dashboard talks to the app directly. Accepted in DE-1. | ADR 0001; comparison (NanoClaw and ZeroClaw run as one process) | L | Done |
| V4-02 | **LLM provider layer** | A provider interface in `internal/llm` supporting Gemini, OpenAI, Claude, Grok and local Ollama (DE-3), planned as three adapters: Gemini, Anthropic and OpenAI-compatible. A provider conformance suite runs the same scenarios (tool calls, images, Hebrew, untrusted content) against each provider, with a generated capability matrix. Automatic failover on quota or outage; optional local routing for privacy-sensitive turns. Resolves BL-29. **Done when** switching provider is a config change and a failover test passes with the primary disabled. | Old roadmap ("Local Model Support") and V4 architecture doc ("Multi-Provider LLM Failover"), merged; comparison (ZeroClaw, Hermes) | L | Done |
| V4-03 | **Pinned embeddings** | Configure the embedding model separately from the chat model. The vector size (3,072) is fixed in the schema, so a different embedding model means a schema migration and a full re-embed; this item documents and tests that path. Until a second embedding model is supported, every install needs the Gemini embedding key, including one that chats through a local model. **Done when** changing the chat provider leaves the `halfvec(3072)` data untouched and the re-embedding path has been run once on a copy of real data. Prerequisite for V4-02. | Comparison follow-up | M | In progress |
| V4-04 | **Usage tracking and cost caps** | Record token usage per user and per turn; daily caps per user and per install; show usage on the dashboard. **Done when** a capped user gets a clear message and no model call is made. | Comparison (ZeroClaw) | M | Done |
| V4-05 | **Quiet hours and time zones enforced in code** | The scheduler honours each user's quiet hours and time zone for reminders, briefings and automations. Today quiet hours are only prompt text and the scheduler uses one install-wide zone. **Done when** a non-urgent alert due in quiet hours is deferred, proven by a test. | Code review in the comparison | M | Done |
| V4-06 | **Remove `remember_fact`** | It duplicates `save_topic_memory` and both are always offered. **Done when** the tool is gone and the generated catalogue is updated. | Code review in the comparison | S | Done |
| V4-07 | **Retire legacy paths** | Three independent removals, each with its own precondition. (1) Drop the legacy `memories` table once the waiting period in OP-7 has passed. (2) Fold the legacy `workflows` loader into cron automation rules. (3) Remove the unsalted-ciphertext fallback, but only after a re-encryption pass (BL-16): no such code exists today, and old-format values can still sit in the Google tokens in Redis, the credential fallback table and the API connector headers. The pass rewrites each value in the current format and reports how many remain. OP-2 (the `legacy-token:*` keys) is a separate clean-up and does not unblock this. **Done when** no legacy branches remain in `internal/db`, `internal/scheduler` and `internal/crypto`, and the re-encryption report showed zero old-format values before the fallback was deleted. | Code review in the comparison | M | Planned |
| V4-08 | **Merge the calendar tools** | One calendar tool set instead of `workspace_calendar` (4 tools) plus `schedule` (5 tools), with Google sync as a flag. **Done when** the calendar domain offers at most five tools and existing behaviour passes its tests. | Code review in the comparison | M | Done |
| V4-09 | **Align the default model** | The code default and `.env.example` still name `gemini-2.5-flash`. **Done when** one current default is defined in one place. | Code review in the comparison | S | Done |
| V4-27 | **Feature packs** | Core is always on: memory, reminders, calendar, tasks and contacts, web search, undo, family sharing, user preferences and the personal CRM. Opt-in packs per household: Baby, House and vehicles, Finance, Gmail, and Automations (external APIs, rules and workflows). Two things must move first. Topics become data instead of hard-coded names (today "baby" appears in 25 files outside its own module). User preferences and the personal CRM move out of the Automations module into Core: today language, tone, quiet hours and standing instructions reach the model only through that module's prompt section, so switching the pack off would silently drop them. A disabled pack contributes no tools, prompt section, routing keywords, dashboard page or briefing lines; its data is kept. Guardrails inside an enabled pack are not optional, and there are no per-tool switches (use V4-11 for that). Pack choices are stored on the household (V4-30) and changed from the dashboard. New households start with Core only (DE-6); on upgrade, every pack that already holds data is switched on, so nothing disappears. **Done when** a household with Baby disabled sees no baby tool, prompt text or page; a household with Automations disabled still has its preferences applied; an upgraded install keeps every feature it was using; each of these is proven by a test; and the placeholder switches on the dashboard Tools page are replaced by real pack switches. | Owner request (2026-11-18) | L | Done |
| V4-30 | **Household entity** | A family becomes a first-class record instead of a text label on each user. It holds name, language, time zone, currency and enabled packs. The data model allows many households per install; the experience is built for one first (DE-7). On upgrade, each existing family label becomes a household and a user without a label gets a household of their own. On a fresh install, the first household and its owner come from the existing bootstrap setting (`ALLOWED_USERS`) until the wizard (V4-24) exists. Needs the identity ADR (§7). **Done when** family-level settings live on the household, every tenant check uses its ID, and the existing tenant-isolation tests still pass. Prerequisite for V4-27, V4-32, V4-35 and V4-36. | Planning session (2026-11-18) | L | Done |
| V4-31 | **Channel layer** | One adapter per channel behind a single interface. Incoming messages become a neutral format; outgoing messages are text plus optional choices, attachments or voice, rendered by the adapter. Each adapter declares its capabilities (buttons and how many, invite links, groups, voice, message length, formatting) and features degrade in a documented way. Telegram and WhatsApp are ported first. Needs the channel ADR (§7). **Done when** no code outside the adapters names a platform and V4-34 passes on both. | Planning session (2026-11-18) | L | Done |
| V4-32 | **People and channel accounts** | A person is separate from their channel accounts. One person can link several accounts and choose a preferred channel for notifications. **Done when** one person can use Telegram and WhatsApp as the same user with the same data. Requires V4-30 and V4-31. Needs the identity ADR (§7), because linking accounts must never let one person take over another. | Planning session (2026-11-18) | M | Done |
| V4-33 | **Proactive delivery policy** | A rule per channel for messages the assistant starts itself (reminders, briefings, partner notifications). When a channel refuses delivery, for example outside WhatsApp's 24-hour window, use an approved template, fall back to the person's other channel, or hold until they next write. The outcome is recorded. **Done when** a reminder due outside the WhatsApp window is delivered by one of these routes and never silently lost. Covers both Twilio modes (V4-41). Requires V4-31, and V4-32 for the other-channel fallback. Related to BL-8. | Planning session (2026-11-18) | M | Done |
| V4-34 | **Channel conformance suite** | One shared set of scenarios (pairing, approval, reminder, undo, media, long reply) run against every adapter with a fake transport, in CI, plus a generated capability matrix in `SYSTEM.md`. It starts in stage 1 as tests that pin down today's Telegram and WhatsApp behaviour, and is completed when the adapters exist (V4-31). **Done when** both existing channels pass and a new adapter cannot be registered without passing. | Planning session (2026-11-18) | M | Done |
| V4-35 | **Dashboard sign-in and roles** | Per-person sign-in through the bot: `/dashboard` in chat returns a short-lived, single-use link. This replaces the shared dashboard secret. Roles become owner, adult, and child or guest, with a separate operator role for the install; today's `admin` becomes owner and operator, and `family` becomes adult. Needs the identity ADR (§7), because the link is a new way in. **Done when** the shared secret no longer grants access and each role sees and changes only what it may, proven by tests. Requires V4-30 and V4-32. | Planning session (2026-11-18) | L | Done |
| V4-40 | **Run locally or deployed** | The same release runs on a home PC or on a server, chosen by a compose profile and settings, never by editing files. Local: everything in Docker, as today. Single server: the identical stack plus a reverse proxy with automatic TLS (Caddy). Managed services: optional, by pointing the database, cache and queue addresses at external services (compose stops hard-coding them). An ingress setting chooses how MyPA is reached: none (Telegram by polling, Discord), Cloudflare Tunnel, Tailscale Funnel, own reverse proxy, or ngrok; each channel adapter declares whether it needs a public address. One public address setting replaces the four that must agree today (Telegram webhook, Twilio callback, Google redirect, tunnel domain); on a domain the owner controls, that address stays the same in both modes, so a move changes nothing at Twilio or Google. Moving between modes uses V4-43. The dashboard is not exposed publicly before V4-35. **Done when** one install is moved from local to a server and back, losing nothing except the short-lived items V4-43 lists, and a Telegram-only install runs with no tunnel. A single server is the supported deployed path (DE-9). | Owner request (2026-11-18) | L | In progress |
| V4-41 | **Twilio sandbox and registered sender** | Both are supported through a mode setting (DE-8). Sandbox mode: onboarding includes the join step, the three-day membership expiry is detected and explained instead of failing silently, and only Twilio's stock templates exist, so reminders outside the 24-hour window fall back under V4-33. Sender mode: MyPA provides the wording for its reminder and briefing templates and a guided submission; each owner gets them approved in their own Twilio account. `/doctor` (V4-17) reports the mode and its health. **Done when** one install switches modes by configuration alone and V4-34 passes in both. | Owner request (2026-11-18) | M | Done |
| V4-43 | **Restore and migrate** | One mechanism for three jobs (DE-10): upgrading from the pre-V4 baseline release (OP-10), moving between local and deployed, and disaster recovery. Backups stay on the local machine for now, so disaster recovery covers lost or damaged data on a working machine, not the loss of the machine itself (OP-11). A backup is the database dump plus the archive files (audit archives today, conversation archives after V4-12). It is restored into any V4 install and the versioned migrations bring it up to date; the old backup is the rollback. Two keys travel with it through an explicit, protected export: the data encryption key and the backup key (OP-1). The procedure stops the old install first, so only one is ever live on the same Telegram bot and WhatsApp number. Until V4-23, RabbitMQ and Redis still hold state: the procedure drains the queue before the backup and documents what is not carried (the 24-hour chat window, pending approvals and other short-lived keys); Google sign-ins are restored from their encrypted copy in the database. On upgrade, packs that hold data are switched on (V4-27). The mechanism is built first and the item is marked done at the V4.0 release. **Done when** a baseline backup restores into V4.0 on a different machine with Google sign-ins intact, every feature in use still available, and `/doctor` (V4-17) reporting healthy. | Owner decision (2026-11-18) | L | In progress |
| V4-17 | **`/doctor`** | An admin command, with its dashboard page arriving in V4-37, that checks webhook registration, Google token health per user, the WhatsApp mode and sender status (V4-41), queue and failed-message depth, last backup age and migration version, and gives a next step for each failure. Addresses the failures behind BL-1 and BL-8 and makes OP-8 and OP-11 visible. **Done when** a broken webhook, an expired Google token and a stale backup are each detected in tests, and the report reads correctly on every supported channel. | Comparison (OpenClaw and ZeroClaw `doctor`) | M | Done |

---

## 5. V4.1 — Daily use

| ID | Item | What, and done when | Origin | Size | Status |
| :--- | :--- | :--- | :--- | :---: | :--- |
| V4-10 | **Approval buttons** | Replace typed `CONFIRM <TOOL>` with inline buttons on Telegram and quick-reply buttons on WhatsApp; the typed form stays as a fallback everywhere. WhatsApp allows three quick-reply buttons inside the 24-hour window without template approval, and they are defined as content templates in the owner's Twilio account (V4-41). Each channel adapter (V4-31) renders the choice in its own way. **Done when** a gated tool can be approved or rejected with one tap on channels that have buttons, and with a typed reply everywhere. | Comparison (NanoClaw V2 approval cards, OpenClaw Telegram widgets) | M | Planned |
| V4-11 | **Risk tiers** | Each tool gets a tier: automatic, needs approval, or blocked. Tiers are configurable per family member and default from the roles in V4-35 (owner, adult, child or guest). Extends the current untrusted-turn gate. **Done when** tiers are set from the dashboard and enforced in `executeSingleTool`. Builds on V4-10. | Comparison (ZeroClaw autonomy levels) | M | Planned |
| V4-12 | **Conversation recall** | Keep conversations beyond the 30-message, 24-hour Redis window: tenant-scoped storage in Postgres with full-text search, a summary on trim, and a search tool. Everything is kept, in tiers (DE-4). Hot: recent conversations in full in Postgres. Warm: older ones as summaries and facts, which are the only parts that get embeddings. Cold: full transcripts compressed and encrypted in monthly archive files per household, on disk or object storage, reusing the audit archive sink, and fetched on demand. Media is stored once and archived immediately. The hot tier is designed as the replacement for the Redis chat window, so V4-23 does not rebuild it. Archive files are part of every backup (V4-43), and a cold tier on object storage needs the off-machine target deferred in OP-11. Tier lengths are set per household, and a person can delete their own history, archives included. **Done when** "what did we decide about X last month?" is answered from history. | Comparison (Hermes session search) | M | Planned |
| V4-13 | **Unified search** | One query across memories, transactions, contracts, email and conversations (V4-12). Results come only from Core and the household's enabled packs (V4-27). **Done when** a single tool returns ranked, source-labelled results across domains. | Old roadmap ("Unified Universal Search") | L | Planned |
| V4-14 | **Memory change log** | Memory consolidation is implemented but is not scheduled and emits no undo events today (BL-14). This item first schedules it nightly and makes each change it applies an undoable mutation, then adds a per-run report (merged, superseded, profile updates), shown by a command and on the dashboard. **Done when** a nightly run on test data produces a report and one reported change is reverted with `/undo`. | Comparison (nanobot `/dream-log`, Hermes Curator reports) | M | Planned |
| V4-15 | **Intent-routing fallback** | When no keyword matches, pick tool domains by embedding similarity to the tool descriptions; keywords stay as the fast path. **Done when** a paraphrase test set reaches the right domain tools. | Code review in the comparison | M | Planned |
| V4-16 | **Family group chat** | MyPA joins a family group chat, answers when mentioned, attributes each action to its sender and respects per-topic sharing. The pre-auth gate applies per sender. Offered on channels whose adapter declares group support: Telegram first, Discord with V4-39. Not offered on WhatsApp at first, because Twilio's group option relays messages through the business number and is not a native group (DE-8); that is the documented degradation. Needs the channel ADR (§7). **Done when** two members of one household use the assistant in the same Telegram group, each action is recorded under the right person, and a non-member in the group gets no access. | Comparison (Hermes group bots, NanoClaw trigger word) | M | Planned |
| V4-18 | **Voice replies** | Text-to-speech for briefings and replies, opt-in per user, on channels whose adapter declares voice; other channels get text. **Done when** a user who opted in receives a spoken morning briefing on Telegram and on WhatsApp, and a user who did not opt in gets text. | Old roadmap ("Voice Outbound"); comparison (Hermes voice mode) | M | Planned |
| V4-28 | **Household pack** (shopping and food) | Shared shopping lists, pantry stock with low-stock alerts, a meal plan, and recipes saved from links. The stock engine generalises the baby supply inventory and is the same one V4-29 uses. **Done when** two family members can add to and tick off one list from chat, and a meal plan can produce a shopping list. Requires V4-27. | Owner request (2026-11-18) | M | Planned |
| V4-36 | **Guided onboarding** | Three layers. Install: the setup wizard (V4-24). Household setup by the owner: name, language, time zone, currency, packs, invitations. Member onboarding in chat: an invite link where the channel supports one and a typed code everywhere, then a short conversation (name, language, quiet hours, one example per enabled pack). Every step can be skipped, the flow is resumable and can be repeated with `/setup`, and permissions are asked for late (Google access on the first calendar request). **Done when** a new member goes from invitation to a first useful answer in under two minutes on every supported channel, and no user is stored as "Unknown". Requires V4-24, V4-27, V4-30, V4-31 and V4-32. Invite links are covered by the identity ADR (§7). | Planning session (2026-11-18) | L | Planned |
| V4-24 | **Browser setup wizard** | A first-run page in the dashboard that generates secrets, validates keys, creates the first household and its owner (V4-30), lets them choose packs (V4-27) and writes the configuration, instead of hand-editing about 40 environment keys. It is the first layer of onboarding (V4-36). **Done when** a fresh install reaches its first answered message without the settings file being edited by hand. | Comparison (ZeroClaw `/onboard`) | M | Planned |
| V4-37 | **Dashboard reorganisation** | Two areas instead of one. Household admin: members, invitations, packs, sharing, connected accounts, data export. System admin: health, usage and cost, logs, backups, model provider. Pages for disabled packs are hidden. **Done when** a household owner never sees system pages unless they are also the operator. Requires V4-27 and V4-35. | Planning session (2026-11-18) | M | Planned |
| V4-38 | **Admin actions in chat** | Inviting a member, enabling a pack and changing a role can be done from chat by the owner, confirmed through V4-10, because most people will not open the dashboard. **Done when** each of the three works on every supported channel and is refused for anyone who is not the owner. | Planning session (2026-11-18) | M | Planned |
| V4-39 | **Discord adapter** | The first new channel, built only on the channel layer (V4-31), as the proof that the layer works. Discord bots connect outward, so no public address should be needed (V4-40); confirm this when building. **Done when** it passes V4-34 without changes to the engine. | Planning session (2026-11-18) | M | Planned |
| V4-44 | **Drop the old identity columns** | The contract step of ADR 0002 (Decision 2, step 7): drop `family_group`, the old person columns, `users` and `pairing_codes`. Why: through V4.0 every write goes to both the old and the new keys, and the old columns are what the new ones are checked against, so they go only once real use has confirmed the new ones. **Done when** one release of V4.0 has been in use; a comparison report on the live data shows the old and new keys agree on every row (each label maps to its household, each old person key to its person, and the only rows without a person are the ones Migration B counted); a versioned migration drops the columns and tables, with the tenant-isolation tests passing; and the release notes and [`RESTORE_AND_MIGRATE.md`](./RESTORE_AND_MIGRATE.md) state that the rollback point moves to the backup taken before that release. Requires V4-30, V4-32, V4-35 and V4-36 (V4-35, because roles are held in `users` until it moves them). | ADR 0002 (2026-11-19) | M | Planned |

---

## 6. V4.2 — Reach

| ID | Item | What, and done when | Origin | Size | Status |
| :--- | :--- | :--- | :--- | :---: | :--- |
| V4-19 | **MCP client** | Connect to MCP servers. An admin approves each server and its tool allowlist. MCP tools pass through NetGuard, untrusted-output wrapping and risk tiers (V4-11). Needs its own ADR under the rule in §1, written when V4.2 is planned. **Done when** one MCP server is usable end to end with adversarial tests. | Comparison (nanobot, Hermes) | L | Planned |
| V4-20 | **OpenAPI auto-discovery** | Extend `register_external_api` to parse OpenAPI 3.1 and build typed function declarations at runtime, behind the same NetGuard and confirmation gates as today. Covered by the same ADR as V4-19. **Done when** an API registered from its OpenAPI file can be called with typed arguments, and a malformed or hostile file is rejected in tests. | V4 architecture doc | M | Planned |
| V4-21 | **Suggested workflows** | Detect a repeated multi-step request and offer to save it as a workflow or automation rule. Nothing is saved without consent. A periodic review flags unused ones for pruning. **Done when** a request repeated three times in test data produces one offer, accepting it creates a rule, and declining it creates nothing. | Comparison (Hermes learned skills and Curator) | M | Planned |
| V4-22 | **Sustained goals** | A `/goal` tracked across weeks with check-ins through reminders and the morning briefing, for example "service the car before the lease ends". **Done when** a goal set in chat produces a check-in on schedule, survives a restart, and can be completed or dropped from any supported channel. | Comparison (nanobot and Hermes `/goal`) | M | Planned |
| V4-23 | **Postgres as the only state store** | RabbitMQ and Redis are removed from every install; there is no separate lite profile (DE-5). The queue moves to a Postgres-backed job queue (River, per ADR 0001), in stages: audit events first, at which point the `audit-worker` merges into the app; then the scheduler; then inbound messages. Open-source River has no per-key ordering and no dead-letter queue, so both are built here: a per-user ordering guard, and a replacement for today's dead-letter handling. Every Redis key gets a home in Postgres or in an in-memory cache: the chat window (already replaced by V4-12), Google tokens, sign-in nonces, pending approvals, sticky tool domains, pairing rate limits and lockouts, the duplicate-message claims and the prompt cache. Needs the state-store ADR (§7) first. The upgrade from V4.1 must be automatic, including draining the old queue, and one command starts everything. **Done when** an install runs as the app, the dashboard and Postgres (plus the reverse proxy when deployed); the ordering, retry and duplicate-protection tests still pass; and a backup, meaning the database plus the archive files, captures all state. Depends on V4-01 and V4-12. | Comparison (OpenClaw `onboard`, NanoClaw installer, ZeroClaw single binary); Owner decision (2026-11-18) | L | Planned |
| V4-25 | **Smart home** | Home Assistant / Matter control in natural language, tied to family schedules. Prefer delivering it through V4-19. All actions are tiered (V4-11). **Done when** one Home Assistant action can be run from chat, is refused for a role whose tier blocks it, and appears in the audit log. | Old roadmap ("Smart Home Orchestration") | M | Planned |
| V4-26 | **OpenTelemetry tracing** | Spans across ingress, queue, orchestrator, model calls and tool calls. No message content in spans. **Done when** one message can be followed from arrival to reply in a trace viewer, and a test confirms that no message text appears in any span. | Old roadmap and V4 architecture doc | M | Planned |
| V4-29 | **Care templates** (baby, child, pet) | Generalise the Baby pack into one Care engine with templates: care logs, schedules, medication cooldowns and supplies for any care subject; the child template adds school and activities. Supplies use the stock engine from V4-28. **Done when** a pet or an older child can be tracked without baby-specific wording, and existing baby data migrates unchanged. Requires V4-27 and V4-28. | Owner request (2026-11-18) | L | Planned |

---

## 7. Open decisions

None at present. New ones are added here with an ID and what they block.

### Decided

| ID | Decision | Date |
| :--- | :--- | :--- |
| DE-1 | ADR 0001 accepted. One binary with roles (option 1c). The queue moves from RabbitMQ to Postgres (River) for every install, in stages, by V4.2. | 2026-11-18 |
| DE-3 | Supported providers: Gemini, OpenAI, Claude, Grok and local Ollama. | 2026-11-18 |
| DE-4 | Conversation history is kept in full, in hot, warm and cold tiers designed to minimise storage and cost. | 2026-11-18 |
| DE-5 | Postgres is the only state store for every install. No SQLite and no separate lite profile. | 2026-11-18 |
| DE-6 | Primary audience is any household. Core is on by default; every pack is opt-in. On upgrade, packs that already hold data are switched on. | 2026-11-18 |
| DE-7 | The data model supports many households per install; the experience is built for one household first. | 2026-11-18 |
| DE-8 | WhatsApp stays on Twilio, supporting both the sandbox and a registered sender. Checked on 2026-11-18: Twilio offers no native WhatsApp group, only a relay through the business number, so group chat is not offered on WhatsApp at first. | 2026-11-18 |
| DE-9 | A single server running the same stack is the supported deployed path, with one public address on a domain the owner controls. Managed services remain possible through settings. | 2026-11-18 |
| DE-10 | One upgrade and move path: restore a backup into any V4 install and let the migrations bring it up to date. | 2026-11-18 |
| DE-11 | "Prerequisite for V4-02" in the V4-03 row means V4-03's merged code (the embedding model is pinned apart from the chat model, proven by its tests), not its check on real data. V4-02 may be built while V4-03 is still "In progress". V4-03 stays "In progress" until the re-embed has been run once on a copy of real data, which is done together with the dry run in OP-12. The V4.0 release still requires V4-03 to be Done. | 2026-11-20 |

### ADRs required before build

An ADR is written only for a decision that is hard to undo and touches data or security. ADR 0001 exists; these three are written and accepted before the items they cover are started.

| ADR | Covers | Needed before |
| :--- | :--- | :--- |
| [Identity and access](./adr/0002-identity-and-access.md) (ADR 0002, accepted 2026-11-19) | Households, people, linking channel accounts, roles, the dashboard sign-in link, invite links | Stage 2 of V4.0: V4-30, V4-32, V4-35, V4-36 |
| [Channel layer (short)](./adr/0003-channel-layer.md) (ADR 0003, accepted 2026-11-19) | The adapter interface, capability declaration, proactive delivery, group chats | Stage 2 of V4.0: V4-31, V4-33, V4-16 |
| State store | Moving the queue and every Redis key into Postgres: the per-user ordering guard, the dead-letter replacement, duplicate protection, single-use nonces and expiry | V4-23; write it during V4.1, because it informs V4-12 and V4-43 |

Model providers (V4-02, V4-03) and deployment and ingress (V4-40) do not need an ADR: their choices are easy to change and are recorded in [`DECISIONS_LOG.md`](./DECISIONS_LOG.md) as the items are built. The MCP client (V4-19) runs third-party tools, so it gets an ADR under the rule in §1 when V4.2 is planned.

---

## 8. Later — under evaluation

Not committed. Each has a trigger that would promote it to a milestone.

| ID | Item | Trigger to revisit | Origin |
| :--- | :--- | :--- | :--- |
| EV-01 | **More channels** (Signal, Matrix, email) | A family member needs one. Each is a new adapter on the channel layer (V4-31) and must pass V4-34. Discord is committed as V4-39. | Comparison (all five support more channels) |
| EV-02 | **Sandboxed execution** (container or WebAssembly) | Before any user-supplied tool or code execution is allowed. Not needed while every tool is first-party Go. | Comparison (NanoClaw, IronClaw) |
| EV-03 | **Multi-agent specialist sub-graphs** | Single-agent turns demonstrably fail at cross-domain planning, and V4-04 shows the cost is acceptable | V4 architecture doc |
| EV-04 | **Cross-encoder memory re-ranking** | Measured recall precision is poor; measure first | V4 architecture doc |
| EV-05 | **Managed ingress profile** (Traefik or Caddy with automatic TLS) | Promoted to V4-40 on 2026-11-18 | V4 architecture doc |
| EV-06 | **Companion device context, read-only** | A concrete need for local device context; anything beyond read-only depends on EV-02 | Old roadmap ("Companion Desktop / Mobile Nodes"), reduced |
| EV-07 | **Health records pack** (adult template for Care) | V4-29 has shipped, health fields are encrypted at field level, and the scope is agreed: record-keeping only (appointments, medication schedules, vaccinations, documents), never diagnosis or treatment advice | Owner request (2026-11-18) |
| EV-08 | **Workout pack** | V4-19 is available and there is a fitness data source worth connecting; manual logging alone does not justify a pack | Owner request (2026-11-18) |

---

## 9. Not planned

| Item | Why | Origin |
| :--- | :--- | :--- |
| **Companion nodes with script execution** | Host-level execution is the main source of security incidents in comparable projects. Revisit only after EV-02. | Old roadmap, removed |
| **Edge webhook workers** | Adds infrastructure while V4-01 removes it; a family-scale install has no latency need. | V4 architecture doc, removed |
| **Public skill or plugin marketplace** | Unreviewed third-party code. OpenClaw's marketplace was used to distribute malware. | Comparison |
| **Host shell or browser automation by default** | Contradicts principle 3. | Comparison |
| **Always-on model heartbeat** | Idle cost; contradicts principle 5. | Comparison |
| **General-purpose agent features** (coding agents, agent swarms, task boards) | Outside a family assistant's scope. | Comparison |
| **Channel count as a goal** | Channels are added on need (EV-01), not to match others. | Comparison |

---

## 10. Delivered

Remediation (2026-11) and rollout fixes are recorded in [`REMEDIATION_PLAN.md`](./archive/REMEDIATION_PLAN.md), [`BACKLOG.md`](./BACKLOG.md) and [`DECISIONS_LOG.md`](./DECISIONS_LOG.md).

The entries below are kept as they were written at release. Corrections found when this file was verified on 2026-11-18: the tool count is now 68, tests run on the production Postgres 15 image, and memory consolidation is implemented but was never scheduled (BL-14, V4-14).

### Core Platform & UX (V3.0)
- [x] **Web-based Control UI / Dashboard**: Next.js 16 (React 19) graphical dashboard to manage sessions, tools, configurations, audit logs, and OAuth connections.
- [x] **Pairing Codes & Stricter Onboarding**: Multi-tenant onboarding requiring a 6-digit admin-issued pairing code with Redis rate-limiting and lockout protection.
- [x] **Modular `ToolModule` Plugin Registry**: Decoupled plugin architecture (`internal/orchestrator/registry.go`) hosting 70 tools across 13 domain packages with dynamic Two-Tier Intent Routing.
- [x] **Multimodal Vision (Image & PDF Processing)**: Process receipts, flyers, and contracts sent via Telegram/WhatsApp using Gemini Vision.
- [x] **Custom Recurring Workflows (Macros)**: User-defined multi-step cron jobs in natural language (`schedule_workflow` and `cron` automation rules).
- [x] **Gmail Automation Flows**: Search, read, draft replies, archive, apply labels, and execute SSRF-guarded `List-Unsubscribe` flows.

### Life Domain Expansion Waves (V3.0)
- [x] **Durability & Rollback (Topic 1)**: Immutable mutation audit log (`mutation_logs`) with `BeforeJSON`/`AfterJSON` snapshots, allowlisted ACID rollback (`/undo` and GraphQL `rollbackMutation`), and CSV/JSON export.
- [x] **Topic-Isolated Memories & Entity Profiles (Topic 2)**: Hierarchical topic memories (`pgvector` 3,072-dim) with automatic conflict superseding, living `EntityProfile` cards, and nightly consolidation.
- [x] **Multi-Schedule & Virtual Sub-Calendars (Topic 3)**: Virtual `[Topic]` sub-calendars, internal-only high-frequency schedules, and bidirectional partner Google Calendar sync.
- [x] **Baby & Family Care Tracker (Topic 4)**: Dynamic child age & Wonder Weeks leap engine, pharmacological class medication cooldown guardrails (Paracetamol 4h / Ibuprofen 6h), care logs, and supply inventory.
- [x] **House Maintenance, Contracts & Vehicle Vault (Topic 5 & 8.1)**: Lease, mortgage, warranty, and receipt vault with notice period countdowns, preventive home maintenance checklists, and vehicle asset monitoring.
- [x] **Financial Tracking & Budget Intelligence (Topic 6)**: Expense/income ledger, monthly category budgets with alert thresholds, >30% historical anomaly detection, and recurring bills tracking.
- [x] **On-Demand External APIs, Automations & Personal CRM (Topics 7 & 8)**: SSRF-protected REST/webhook connector registry with AES-256-GCM encrypted headers, `When -> If -> Then` `EventBus` rules engine, user persona preferences, and Personal CRM.
- [x] **Shared Family Spaces & Partner Sync (Topic 9)**: Per-topic family sharing permissions, cross-user push notifications via Telegram/WhatsApp, and shared reminder hand-offs.
- [x] **Full GraphQL API & Decoupled Next.js Dashboard**: 22 queries and 28 mutations routed through domain repositories, paired with 14 Next.js App Router views.

### Production Security, Scalability & Test Hardening (V3.1)
- [x] **Defense-in-Depth Ingress & Pre-Auth (`SEC-01`–`SEC-03`, `SEC-10`, `SEC-11`)**: Pre-auth user verification, Admin RBAC on `/backup` & `/restore`, Telegram secret token & Twilio HMAC-SHA1 signature verification, constant-time GraphQL Bearer/JWT middleware, localhost port bindings, and `http.MaxBytesReader` limits.
- [x] **Positive Multi-Tenant Authorization & Anti-IDOR (`SEC-04`)**: Compound tenant scoping across all 8 domain repositories and GraphQL resolvers with zero tool-argument tenant overrides.
- [x] **Centralized SSRF NetGuard (`SEC-05`)**: Custom dialer (`internal/netguard`) blocking loopback, RFC1918 private networks, link-local/cloud metadata (`169.254.169.254`), and internal single-label Docker hostnames.
- [x] **HKDF-SHA256 Salted AES-256-GCM & OAuth CSRF Nonces (`SEC-07`, `SEC-08`)**: Salted key derivation for encrypted Redis/PostgreSQL credentials and 32-byte single-use `GETDEL` OAuth state nonces.
- [x] **Prompt Injection Boundaries & Confirmation Gates (`SEC-09`)**: `<untrusted_external_content>` XML tagging and interactive `CONFIRM <TOOL>` staging for privileged meta-tools.
- [x] **Engine & Prompt Scalability (`SCALE-01`–`SCALE-03`)**: Redis `PromptCache` with `EventBus` invalidation, bilingual (English/Hebrew) two-tier tool intent matching (~80% token savings), `PhotoData` stripping from Redis history, 8-turn recursion ceiling, and concurrent `errgroup` tool execution.
- [x] **Broker Resilience, Dual-Lane EventBus & DB Tuning (`SCALE-04`, `SCALE-05`)**: Mutex-serialized `SafePublisher` with publisher confirms, `mypa.dlx` dead-letter queues with 3-attempt retries, deterministic dual-lane `EventBus` (sync durability + 4-worker async pool), PostgreSQL connection pool sizing (`25/10`), and `halfvec(3072)` HNSW + composite B-Tree indexes.
- [x] **Full-Stack Automated Test Suite**: Singleton Testcontainers harness (PostgreSQL 16 + Redis 7) covering all 8 repositories and GraphQL resolvers, plus 31 Vitest + React Testing Library dashboard tests.

---

## 11. Comparison snapshot (2026-11-18)

Five open-source personal assistants were reviewed from their public docs, release notes and security advisories, not from their source code. This space changes weekly, so re-check before relying on it.

| Project | Worth learning from | Worth avoiding |
| :--- | :--- | :--- |
| **OpenClaw** | Channel breadth, companion apps, `doctor`, pairing for unknown senders | Marketplace malware, host execution by default, cost of a 30-minute heartbeat |
| **Hermes Agent** | Learned skills with a Curator, search over past sessions, voice mode, wide model support | Host execution by default, very high change volume |
| **nanobot** | Memory with a visible change log, `/goal`, MCP, web UI | Sandbox off by default, repeated high-severity advisories |
| **ZeroClaw** | Autonomy levels, rate and cost limits, single binary, browser onboarding, `doctor` | Pre-1.0 configuration churn |
| **NanoClaw** | A container per session, credentials kept away from the agent, approval cards, one process | Frequent breaking changes, Docker required, built around one model vendor |

---

## 12. Where the old items went

This file replaces the previous `ROADMAP.md` backlog and `architecture_v4.md`.

| Old item | Old location | Now |
| :--- | :--- | :--- |
| Voice Outbound | ROADMAP §4 | V4-18 |
| Local Model Support | ROADMAP §4 | V4-02 |
| Smart Home Orchestration | ROADMAP §4 | V4-25 |
| Companion Desktop / Mobile Nodes | ROADMAP §4 | EV-06 (read-only); script execution not planned |
| Unified Universal Search | ROADMAP §4 | V4-13 |
| OpenTelemetry Distributed Tracing | ROADMAP §4 and architecture_v4 §2 | V4-26 |
| Edge Webhook Workers | architecture_v4 §2 | Not planned |
| Managed Ingress Controller | architecture_v4 §2 | V4-40 (was EV-05) |
| Multi-Agent Specialist Sub-Graphs | architecture_v4 §3 | EV-03 |
| Zero-Shot OpenAPI Auto-Discovery | architecture_v4 §3 | V4-20 |
| Cross-Encoder Memory Re-Ranking | architecture_v4 §3 | EV-04 |
| Multi-Provider LLM Failover | architecture_v4 §4 | V4-02 |
| Foundation pillars delivered in V3.1 | architecture_v4 §1 | §10 Delivered |
