# MyPA Remediation Plan

Source: full code review of `dev` @ `9f08725` (2026-11-15). Every item below was traced in code; file/line references are approximate starting points — re-locate before editing.

## How to work this plan (for Claude Code)

- Follow `AGENTS.md`: verify branch, work on `dev` (or a `fix/<id>` branch off it), read `SYSTEM.md` before design changes, update `SYSTEM.md` for any model/Redis key/tool/GraphQL change.
- **One item = one commit.** Use the repo's conventional-commit style, e.g. `fix(gateway): fail closed on missing Telegram secret (P1-1)`.
- For each item: write/adjust a failing test first, implement, run `go test -p 1 ./...`, then `docker-compose build` before proposing the commit.
- Do phases in order. Phase 1 items are security/data-integrity bugs — do not batch them with refactors.
- Items marked **[DECIDE]** need the owner's sign-off before implementation — stop and ask.

---

## Phase 0 — Manual prep (owner, not Claude Code)

- [ ] Generate secrets (e.g. `openssl rand -hex 32` each) and add to `.env`: `ENCRYPTION_KEY`, `TELEGRAM_SECRET_TOKEN`, `DASHBOARD_API_SECRET`, `JWT_SECRET` (must differ from `ENCRYPTION_KEY`).
- [ ] Before changing `ENCRYPTION_KEY`: check what key the running orchestrator uses today. If OAuth tokens / API headers are already encrypted with some key, keep that value or plan to re-run `/connect` for each user.
- [ ] Check whether the GitHub repo `NivRave/MyPA` is public. If yes, treat the ngrok domain and any historical secrets as exposed.
- [ ] Note which container images are currently running vs. the code on `dev` (`docker compose images`) — the current `.env` would crash the current orchestrator build at startup.

---

## Phase 1 — Critical (security & data integrity)

### P1-1 Telegram webhook must fail closed + register the secret
**Problem:** `cmd/gateway/main.go` (`setupRouter`, `/webhook/telegram`) only checks `X-Telegram-Bot-Api-Secret-Token` if `TELEGRAM_SECRET_TOKEN` is set; it isn't, and nothing calls `setWebhook` with `secret_token`. User identity comes from `from.id`, replies go to `chat.id` (`internal/telegram/handler.go` ~L94), so a forged update with the admin's user ID and the attacker's chat ID gives full admin access with replies to the attacker. Also allows bypassing pairing lockout by rotating forged user IDs.
**Change:**
1. Gateway refuses to start if `TELEGRAM_BOT_TOKEN` is set and `TELEGRAM_SECRET_TOKEN` is empty.
2. Remove the `if cfg.Telegram.SecretToken != ""` bypass — always verify.
3. Add `telegram.Client.SetWebhook(ctx, url, secretToken)` and call it on gateway startup when `TELEGRAM_WEBHOOK_URL` is set (log success/failure; don't crash on network failure, retry with backoff).
4. Same fail-closed rule for Twilio: `internal/twilio/handler.go` ~L56 skips signature check when auth token is empty → refuse to start instead.
**Accept:** unit tests — missing header → 401; wrong token → 401; empty configured token → startup error. Existing adversarial tests still pass.

### P1-2 Central config validation
**Problem:** `internal/config/config.go` `Load()` never validates required secrets; services discover missing values at runtime (or fail open).
**Change:** add `func (c *Config) Validate(service string) error` with per-service required keys (gateway: Telegram secret, Twilio token if Twilio enabled, RabbitMQ; orchestrator: `ENCRYPTION_KEY`, `JWT_SECRET`, `DASHBOARD_API_SECRET`, DB, Redis, Gemini). Call from each `cmd/*/main.go`. Replace the panic in `internal/crypto/aes.go` `getRawSecret` with a returned error so `ValidateKeyConfigured()` actually returns one (it currently panics instead).
Also: `ENV=test` / `GO_ENV=test` currently switches encryption to a hard-coded key (`aes.go` `isTestMode`). Restrict the fallback to `flag.Lookup("test.v") != nil` only.
**Accept:** table test for `Validate`; `.env.example` lists every required key with a comment.

### P1-3 Per-user system-prompt cache keys (privacy leak)
**Problem:** `internal/orchestrator/registry.go` `CombinedSystemPrompt` (~L211) caches each module's `SystemPromptSection` in Redis keyed by `prompt:<familyGroup>:<module>` (`internal/state/store.go` ~L281, TTL 10 min). The `memory` section includes the *user's personal* pinned memories and the `automation` section includes that user's persona/standing instructions (`schedule`, `shared` also read `callCtx.User`). The next family member gets them.
**Change:** key by `<familyGroup>:<userID>:<module>` (simplest, safe default). Update `InvalidatePromptSections` and every caller (EventBus sync lane) to invalidate all users in the family — use `SCAN` on `prompt:<fg>:*` rather than `KEYS`. Update `SYSTEM.md` Redis key inventory.
**Accept:** test — two users in same family, user A has a personal pinned memory + standing instruction; user B's combined prompt must not contain either, with cache warm.

### P1-4 Message idempotency + no retries after side effects
**Problem:** `Engine.Start` (`engine.go` ~L221) returns any error to the consumer, which requeues up to 3 times (`internal/broker/consumer.go` `handleFailure`). The whole turn re-runs, including tool calls → duplicate calendar events, emails, ledger rows. The user also gets an error message per attempt. `IdempotencyRecord` exists (`internal/db/durability_repo.go`) but isn't used on inbound messages.
**Change:**
1. Dedup key `inbound:<source>:<chatID>:<messageID>` via Redis `SET NX EX 86400` at the top of `ProcessMessage`; skip if already present. (Telegram `message_id` is per-chat; Twilio `MessageSid` is global.)
2. Track in `TurnSecurityState` (or a new turn state) whether any tool executed. Define `broker.ErrNonRetryable`; return it (wrapped) once a tool has run, or after a reply was sent. Consumer: on `errors.Is(err, ErrNonRetryable)` → ack, no requeue.
3. Only send the "internal error" message on the final attempt or non-retryable error.
4. Skip messages with empty IDs from dedup but log a warning.
**Accept:** tests — same message delivered twice → tools execute once; tool ran then send failed → no requeue; LLM call failed before tools → requeued.

### P1-5 `/restore` needs confirmation
**Problem:** `engine.go` (~L455) runs `pg_restore -c` immediately on `/restore` from any admin message.
**Change:** reuse the existing pending-action/`CONFIRM` mechanism: `/restore` stages a pending action (5 min TTL) showing the backup filename and timestamp; only `CONFIRM RESTORE` executes. Take an automatic `/backup` immediately before restoring. Same for any future destructive admin commands.
**Accept:** test — `/restore` alone never calls `RestoreDatabase`; `CONFIRM RESTORE` within TTL does; expired → no-op message.

---

## Phase 2 — Correctness & efficiency

### P2-1 Unreachable tools
**Problem:** `ModuleDomainMapping` (`registry.go` ~L347) has no entry for `memory` or `durability`, so `upsert_entity_profile`, `get_entity_profile`, `list_recent_changes`, `export_topic_data` are never offered to the model.
**Change:** add a `DomainMemory` group (keywords EN/HE: remember, profile, who is, מה אתה זוכר, פרופיל…) and map `durability` non-core tools to a `DomainHistory` group (undo, changes, history, export, היסטוריה, ייצוא…) — or promote them to core if cheap. Add a test asserting **every registered tool is reachable** from at least one domain or core.

### P2-2 Sticky domains for follow-ups
**Problem:** routing uses only the current message text; "yes, delete the second one" loses the previous turn's tools.
**Change:** store matched domains for the last turn in Redis (`turn_domains:<userID>`, TTL ~15 min) and union them into this turn's selection. Also union domains of tools actually called last turn.
**Accept:** test — turn 1 "show my emails" → gmail; turn 2 "archive the first one" → gmail tools present.

### P2-3 Vector index is never used
**Problem:** indexes are on `(embedding::halfvec(3072))` (`internal/db/migrations.go` ~L160) but queries order by `embedding <=> ?` (`db.go` ~L148, `memory_v2_repo.go` ~L148/162) → sequential scan.
**Change:** order by `embedding::halfvec(3072) <=> ?::halfvec(3072)` (or bind a `pgvector.HalfVector`). Verify with `EXPLAIN` in a testcontainers test that the plan uses the HNSW index (set `enable_seqscan=off` in the test session if the table is tiny).
**[DECIDE] optional:** switch embeddings to 768 dims via Gemini `output_dimensionality` — 4× smaller, plain `vector(768)` HNSW. Requires a re-embedding migration.

### P2-4 Unify the two memory systems
**Problem:** `remember_fact` writes to legacy `memories` (`memory/module.go` ~L313 → `db.SaveMemory`), which the engine searches every message (`engine.go` ~L650, extra embedding call). `save_topic_memory` writes to `topic_memories`. Two tools, two stores, two retrieval paths.
**Change:** make `remember_fact` an alias that writes a `TopicMemory` (topic `personal`/`family`), migrate existing `memories` rows into `topic_memories` (one-off migration, keep old table until verified), switch the engine's per-message retrieval to the v2 repo, then drop the v1 path. Update `SYSTEM.md` tables.

### P2-5 Skip embedding + retrieval for trivial messages
Skip `GenerateEmbedding` + memory search when the message is short and has no content words (e.g. ≤ 3 tokens, "thanks", "ok", "תודה", "כן") or is a confirmation. Make the threshold a constant with a test.

### P2-6 Prompt ordering for Gemini implicit caching
**Problem:** the system prompt is built in `ProcessMessage` (~L616) with `time.Now().Format(time.RFC1123)` near the top, so no prefix is stable.
**Change:** move the static instruction block into a package-level constant/template (e.g. `internal/orchestrator/prompt.go`). Order: static instructions → module sections (stable per user) → memories → current date (minute precision) + timezone at the very end. Remove instructions that reference tools not loaded this turn (generate tool-usage guidance from the selected declarations, or keep it per-module in `SystemPromptSection`). Move "user is in Israel / week starts Sunday" into user preferences/config.

### P2-7 Remove repeated per-tool user lookup
`executeSingleTool` (~L943) calls `e.db.GetUser` for every tool call. Resolve the user once per turn and carry it in the turn context/CallContext.

### P2-8 Process users in parallel, each user in order
**Problem:** `ch.Qos(1, …)` (`consumer.go` ~L75) + a single consumer → one user's 5-minute turn blocks everyone. Retry backoff `time.Sleep` (~L233) also blocks the consumer.
**Change:** `Qos(N)` (N configurable, default 4) and dispatch deliveries to goroutines with a per-user lock (ordered within a user, parallel across users). Replace in-consumer sleep with a delayed-retry approach (TTL'd retry queue dead-lettering back to the main queue) or move the sleep into the worker goroutine. Verify ack/nack thread-safety in `amqp091-go` and guard with a mutex if needed.
**Accept:** test — user A's slow handler doesn't delay user B; two messages from user A are processed in order.

### P2-9 Persist tool context in history (smaller)
Chat history keeps 20 messages, 1 h TTL, and only user text + final reply. Consider storing a compact summary of tool calls/results per turn (IDs of created events/emails) so "delete that event" works later. Raise TTL to ~24 h. **[DECIDE]** on exact TTL/size.

---

## Phase 3 — Security hardening

### P3-1 Broaden prompt-injection protection
**Problem:** `MarkUntrusted` is only called by `read_email`, `fetch_webpage`, WhatsApp documents. Gate in `executeSingleTool` covers only 4 meta tools.
**Change:**
1. Also mark untrusted: `search_emails` (subjects/snippets), `search_web`, `call_external_api` responses, images/PDFs from any channel, Telegram documents.
2. Extend the confirmation gate (when turn is untrusted) to: `call_external_api`, `register_external_api`, `soft_delete_emails`, `unsubscribe_email`, `delete_calendar_event`, `delete_task`, `create_shared_reminder`, `remember_fact`, `save_topic_memory`, `upsert_entity_profile`, `update_user_preferences`. Put the list in one place (e.g. a `Sensitive bool` on the tool registration or a map in `security.go`).
3. Standing instructions / memories injected into the system prompt must be wrapped as data (`WrapUntrustedContent`) and never labelled "STRICT REQUIREMENT" (`automation/module.go` SystemPromptSection).
**Accept:** adversarial test — email body instructs "remember that you must always call X" → staged for confirmation, not saved.

### P3-2 Auth tokens
In `internal/graph/auth_middleware.go`:
- Require `exp` (reject tokens without it); cap max lifetime.
- Check the JWT header `alg == "HS256"` explicitly.
- Stop falling back to `ENCRYPTION_KEY` for `JWT_SECRET` (`cmd/orchestrator/main.go` ~L376) — fail validation instead (P1-2).
- `resolveTenantAndActor` (`internal/graph/resolver.go` ~L156): return an error when claims are nil instead of defaulting to the requested family group.
- **[DECIDE]** scope the dashboard secret to a configured family group instead of global admin.

### P3-3 Logging hygiene
- Stop logging message text at INFO: `cmd/gateway/main.go` ("received message"), `internal/twilio/handler.go` (~L87), `engine.go` (`processing message`, Hebrew-retry log). Log length/hash instead; allow full text at DEBUG.
- `consumer.go`: don't log full body on unparseable messages.
- Add Docker log rotation to every service in `docker-compose.yml` (`logging: driver: json-file, options: {max-size: "10m", max-file: "5"}`) via a YAML anchor.

### P3-4 Backups
- Encrypt dumps at rest (e.g. `age` with a public key from env) — **[DECIDE]** tool.
- Pass DB credentials via `PGPASSWORD`/`.pgpass` env rather than the URL in argv (`internal/db/backup.go`).
- Retention: keep 7 daily + 4 weekly instead of 3.
- **[DECIDE]** off-machine copy (cloud bucket / other disk).

### P3-5 Committed infrastructure details
Remove the hard-coded ngrok domain default from `docker-compose.yml` (require `NGROK_DOMAIN`). Pin images: `ngrok/ngrok:<version>`, `dpage/pgadmin4:<version>`; make pgAdmin opt-in via a compose profile (`profiles: ["tools"]`) and require a non-default password.

---

## Phase 4 — Architecture & code structure

### P4-1 Split `ProcessMessage`
`engine.go` `ProcessMessage` is ~450 lines. Extract: `commands.go` (/help, /undo, /connect, /backup, /restore as a command table), `media.go` (Telegram/WhatsApp photo/voice/document ingestion → normalized attachment), `prompt.go` (P2-6), `turn.go` (LLM loop). No behaviour change; tests must stay green.

### P4-2 Split large domain modules
`house` (1.4k lines), `automation` (1.3k), `schedule`, `baby`, `finance` (~1k each): split each into `declarations.go`, `handlers.go`, `service.go` (business logic, no genai types). No behaviour change.

### P4-3 Versioned migrations **[DECIDE]**
Replace startup `AutoMigrate` (`internal/db/db.go` ~L64) with versioned SQL migrations (goose or Atlas). Generate a baseline from the current schema, keep `AutoMigrate` only in tests. Run migrations as an explicit step/flag.

### P4-4 Service consolidation **[DECIDE — write an ADR first, don't implement]**
Write `docs/adr/0001-service-topology.md` weighing: merge `proxy` + `gateway` + `orchestrator` into one binary (keep packages), replace RabbitMQ with a Postgres-backed queue (River) or Redis Streams, keep Postgres/Redis/dashboard. Note what P1-4 and P2-8 already fix. Owner decides.

---

## Phase 5 — Repo hygiene, CI, dependencies

### P5-1 Remove stray files (all are gitignored or untracked — local deletes)
- Root: `tmp_orch.exe`, `graph.test.exe`, `test_db.exe`, `ngrok.exe` (~143 MB)
- `bin/` (stale Aug builds, 56 MB)
- `coverage` (Aug)
- `_list_models.go`, `_test_oauth.go` → delete, or move to `cmd/devtools/` if still useful
- Move `REVIEW.md` → `docs/reviews/2026-11-03-review.md`; this file → `docs/REMEDIATION_PLAN.md`
- **[DECIDE]** archive `.agents/teamwork/` (141 folders) outside the repo
- Add a `make clean` / script that removes build artefacts; build outputs go to `bin/` only.

### P5-2 Test organization
Rename/merge test files named after agent iterations (`adversarial_m2a_challenger2_test.go`, `cooldown_adversarial_iter2_test.go`, `m1b_challenge1_adversarial_test.go`, `empirical_*`) into behaviour-named files (`*_security_test.go`, `*_cooldown_test.go`). Keep every test case; no logic changes. Delete exact duplicates only.

### P5-3 CI (`.github/workflows/ci.yml`)
- Trigger on `push` to `dev` and `master`, and PRs to both.
- Pin `golangci-lint` to a version (use `golangci/golangci-lint-action`).
- Add `govulncheck ./...`.
- Add a dashboard job: `npm ci`, `npm run lint`, `npm test`, `npm run build`, `npm audit --audit-level=high`.
- Add `docker compose build` (or `docker buildx bake`) job.

### P5-4 Dependency updates
- `dashboard/Dockerfile`: `node:20-alpine` (EOL Apr 2026) → `node:22-alpine` or `node:24-alpine`.
- `rabbitmq:3.13-management-alpine` → current 4.x (check classic-queue/DLX compatibility) — skip if P4-4 removes RabbitMQ.
- Bump `gqlgen` (v0.17.49) and regenerate; run full tests.
- Add Dependabot/Renovate config for Go modules, npm, Docker, GitHub Actions.

### P5-5 Documentation drift
Generate the tool catalog section of `SYSTEM.md`/`README.md` from the registry (`go run ./cmd/devtools/gendocs`) instead of hand-maintained counts. Trim README to overview + quick start; link to `SYSTEM.md`.

---

## Tracking

| ID | Title | Phase | Status |
|---|---|---|---|
| P1-1 | Telegram/Twilio webhooks fail closed + setWebhook | 1 | ☑ |
| P1-2 | Central config validation, no test-key fallback | 1 | ☑ |
| P1-3 | Per-user prompt cache keys | 1 | ☑ |
| P1-4 | Inbound idempotency + non-retryable after side effects | 1 | ☑ |
| P1-5 | `/restore` confirmation + pre-restore backup | 1 | ☑ |
| P2-1 | Make all tools reachable | 2 | ☑ |
| P2-2 | Sticky domains | 2 | ☑ |
| P2-3 | Use the HNSW index | 2 | ☑ |
| P2-4 | Unify memory systems | 2 | ☑ |
| P2-5 | Skip embedding for trivial messages | 2 | ☑ |
| P2-6 | Cache-friendly prompt ordering | 2 | ☐ |
| P2-7 | Resolve user once per turn | 2 | ☐ |
| P2-8 | Parallel users, ordered per user | 2 | ☐ |
| P2-9 | Persist tool context in history | 2 | ☐ |
| P3-1 | Broader prompt-injection gate | 3 | ☐ |
| P3-2 | JWT/claims hardening | 3 | ☐ |
| P3-3 | Logging hygiene + rotation | 3 | ☐ |
| P3-4 | Backup encryption & retention | 3 | ☐ |
| P3-5 | Compose: no committed domain, pinned images | 3 | ☐ |
| P4-1 | Split ProcessMessage | 4 | ☐ |
| P4-2 | Split domain modules | 4 | ☐ |
| P4-3 | Versioned migrations | 4 | ☐ |
| P4-4 | Service topology ADR | 4 | ☐ |
| P5-1 | Remove stray files | 5 | ☐ |
| P5-2 | Test file organization | 5 | ☐ |
| P5-3 | CI coverage | 5 | ☐ |
| P5-4 | Dependency updates | 5 | ☐ |
| P5-5 | Generated docs | 5 | ☐ |
