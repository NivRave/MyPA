# Backlog — side findings

Things noticed while doing other work, logged so they get fixed deliberately instead of forgotten.
Each item: where it was found, evidence, suggested fix. Fix an item on its own `fix/bl-<n>` branch off
`dev` (failing test first), then mark it done here with the commit hash.

Severity: **High** = wrong behaviour users can hit · **Medium** = security/privacy or ops risk ·
**Low** = hygiene.

## Code

| ID | Sev | Title | Status |
| :--- | :--- | :--- | :--- |
| BL-1 | High | Schedule module silently drops Google Calendar errors | done 2026-11-16 (`fix/bl-1`, DP-2) |
| BL-2 | Medium | GORM default logger: raw SQL with personal data, non-JSON, "record not found" as error | done 2026-11-16 (`fix/bl-2`, DP-5) |
| BL-3 | Low | `.gitignore` only ignores `.env`, not `.env.*` copies | done 2026-11-16 (`fix/bl-3`, DP-6) |
| BL-4 | Low | `migrate`/`audit-worker` inherit unused `localhost` URLs from `env_file` | done 2026-11-16 (`fix/bl-4`, DP-11) |
| BL-5 | Low | Queue `telegram.inbound` also carries WhatsApp traffic | done 2026-11-16 (`fix/bl-5`, DP-12); deploy = drain-and-switch |
| BL-6 | Low | Proxy logs a WARN for every browser `/favicon.ico` | done 2026-11-16 (`fix/bl-6`, DP-8) |
| BL-7 | Low | Go base images, `redis`, `pgvector` not digest-pinned | done 2026-11-16 (`fix/bl-7`, DP-9) |
| BL-8 | Medium | WhatsApp delivery failures are invisible (no Twilio status callback) | done 2026-11-16 (`fix/bl-8`, DP-4) |
| BL-9 | Low | Replies recap earlier turns' actions as if done in this turn | done 2026-11-16 (`fix/bl-9`, DP-10) — confirm in live use |
| BL-10 | Low | WhatsApp formatter nests `*` when a heading contains bold | done 2026-11-16 (`fix/bl-10`, DP-7) |
| BL-11 | Medium | Integration tests ran against the live compose database | done 2026-11-16 (`fix/bl-11`, DP-13); leftover fixtures → OP-9 |
| BL-12 | Low | `testutil` Redis test container uses the floating `redis:7-alpine` tag | open |

### BL-1 — Schedule module silently drops Google Calendar errors (High)
- **Found:** 2026-11-16, first deploy smoke test. "What's on my calendar today?" was answered via
  `list_topic_schedule`; no error was logged, but that is not proof Google was reached.
- **Evidence:** `internal/domains/schedule/handlers.go` lines 135, 164, 304, 502, 512, 585 all do
  `calClient, _ := m.resolveCalendarClient(...)`, and the Google `ListEvents`/insert/update/delete calls
  continue only `if err == nil`, with no log and nothing in the tool result.
- **Impact:** with a missing/expired/undecryptable Google token, the bot answers from local data only
  ("nothing scheduled") and can report an event as "Google Synced" when the sync failed. The user cannot
  tell, and the operator sees nothing in the logs.
- **Fix:** log each failure (`slog.Warn`, user id + operation, no event content) and add a line to the
  tool result such as "Google Calendar unavailable — local events only; reconnect with /connect", so the
  model tells the user. Distinguish "not connected" (expected, no warning) from "connected but failing".
  Tests: a fake calendar client returning errors → result mentions it, local events still listed.

### BL-2 — GORM default logger leaks personal data into logs (Medium)
- **Found:** 2026-11-16, reading orchestrator logs during the WhatsApp smoke test.
- **Evidence:** `internal/db/db.go:26` opens with `&gorm.Config{}`, i.e. GORM's default logger:
  colourised plain text (ANSI escapes) mixed into the JSON slog stream, full SQL **with bound values**
  (e.g. `WHERE user_id = '+9725…'`, phone numbers), and `record not found` printed as an error on normal
  lookups (`automation_repo.go:278`, `durability_repo.go:40`).
- **Impact:** personal data in container logs despite P3-3's rule (no message content/PII at INFO);
  log parsing breaks on non-JSON lines; real DB errors drown in not-found noise.
- **Fix:** a small `gorm/logger.Interface` adapter onto `slog` (JSON, same handler as the rest),
  `ParameterizedQueries: true` (SQL without values), `IgnoreRecordNotFoundError: true`,
  `Colorful: false`, slow-query threshold from config. Apply in `db.Open` (and `testutil` if wanted).
  Test: capture slog output for a query with a phone-number parameter → value absent, output is JSON.

### BL-3 — `.gitignore` only ignores `.env` (Low)
- **Found:** 2026-11-16 while backing up `.env` (backup was put outside the repo for this reason).
- **Evidence:** `.gitignore:16` is exactly `.env`; `.env.bak`, `.env.local`, `.env.prod` would be
  committable with secrets.
- **Fix:** `.env*` plus `!.env.example`; extend `TestEnvExample…`/hygiene tests to assert it.

### BL-4 — Unused `localhost` URLs inside `migrate`/`audit-worker` (Low)
- **Found:** 2026-11-16 while fixing DP-1 (`docs/DECISIONS_LOG.md`).
- **Evidence:** `env_file: .env` injects the host-side `REDIS_URL`/`RABBITMQ_URL` (localhost) into
  services that don't declare them in `environment:`. Harmless today (not read), but a trap if those
  services start using them.
- **Fix:** declare all three URLs on every Go service, or give `migrate` an explicit `environment:`
  instead of `env_file`. Extend `TestCompose_ContainerURLsTargetServiceNames` to require it.

### BL-5 — Queue name `telegram.inbound` is misleading (Low)
- **Evidence:** the Twilio handler publishes WhatsApp messages to the same queue.
- **Fix (optional):** rename to e.g. `inbound.messages` (+ DLQ). Needs a drain-and-switch like the
  RabbitMQ upgrade; only worth doing together with another broker change (see ADR 0001).

### BL-6 — Proxy WARN on `/favicon.ico` (Low)
- **Evidence:** `proxy route not found path=/favicon.ico` after every Google OAuth callback page.
- **Fix:** answer `/favicon.ico` with 204, or log unknown routes at DEBUG.

### BL-7 — Remaining images not digest-pinned (Low)
- **Evidence:** D5-4.5 in `docs/DECISIONS_LOG.md`: `golang:1.26-alpine`, `distroless/static-debian12`,
  `debian:12-slim`, `redis:7-alpine`, `pgvector/pgvector:pg15` are tag-pinned only.
- **Fix:** pin by digest like the others; Dependabot keeps them current.

### BL-8 — WhatsApp delivery failures are invisible (Medium)
- **Found:** 2026-11-16, "I sent a voice note and it doesn't do anything". The note was received,
  transcribed and acted on (`create_calendar_event`), and both replies were accepted by Twilio's REST API
  — then **failed asynchronously** with error 63015 (sandbox: recipient is no longer a sandbox
  participant). Our logs showed a perfectly successful turn; only Twilio's message log had the truth.
- **Evidence:** no `StatusCallback` anywhere in the code; `engine.sendMessage` treats "accepted" as
  "delivered"; diagnosis needed a manual query of the Twilio Messages API.
- **Impact:** any delivery problem (sandbox expiry, 24-hour session window, blocked number, daily
  limits) looks like the bot ignoring the user, with nothing for the operator to see.
- **Fix:** send `StatusCallback=<public URL>/webhook/twilio/status` with each outbound message; a
  signature-verified gateway handler logs `failed`/`undelivered` at WARN with `error_code` and
  message SID (no body). Optional: a small error-code → hint map (63015 → "re-join the sandbox",
  63016 → "outside the 24h window"). Tests: signature required, WARN on failed, nothing on delivered.
- **Also (owner):** move off the Twilio sandbox to a registered WhatsApp sender — sandbox membership
  expires and replies silently stop (OP-8).

### BL-9 — Replies claim actions from earlier turns (Low)
- **Found:** 2026-11-16, reviewing the reply to a resent voice note ("schedule a test event tomorrow 10:00").
- **Evidence:** the 07:17 turn made exactly one tool call (`create_calendar_event`), but the reply listed
  "*Calendar Checked:* Retrieved your schedule for today" **and** "*New Event Created*" as "what was done".
  The calendar read happened in an earlier turn (06:47); since P2-9 the history carries previous turns'
  tool calls and results, and the model summarised all of them as this turn's work.
- **Impact:** the user cannot trust the "what I did" summary; harmless here, misleading for writes
  (e.g. claiming a payment/reminder was created now when it was an older one).
- **Fix (to evaluate):** system-prompt rule "describe only actions taken in this turn"; and/or mark
  replayed tool history as earlier turns (e.g. a turn-boundary note before the current message). Test with
  a prompt-construction unit test (history from a previous turn is labelled as such).
- **Related:** the same resend created a **duplicate** event — correct for the model (the user asked
  again), but only because the first reply was never delivered (BL-8).

### BL-10 — WhatsApp formatter nests bold markers in headings (Low)
- **Found:** 2026-11-16, same review: a reply rendered as `*📅 *Event Details**`.
- **Evidence:** `internal/markdown/whatsapp.go` wraps `ast.Heading` in `*…*` and also emits `*` for any
  level-2 `ast.Emphasis` inside it; WhatsApp does not nest, so the asterisks show literally.
- **Fix:** while inside a heading, skip emphasis markers (track a `inHeading` flag); table test
  `### 📅 **Event Details**` → `*📅 Event Details*`.

### BL-11 — Integration tests ran against the live compose database (Medium)
- **Found:** 2026-11-16, while sizing DE-2: `users` held 156 rows, 154 of them `AttackerFamily`/`VictimFamily`
  fixtures created 2026-11-07…16 in 71 distinct minutes — one batch per local `go test ./...` run.
- **Evidence:** six test helpers (graph, scheduler ×2, orchestrator, automation, durability) defaulted to
  `postgres://mypa:mypa_password@localhost:5432/mypa` (or a host `DATABASE_URL`) — the compose Postgres,
  published on `127.0.0.1:5432` with the default credentials — and called `NewClientAutoMigrate`, so they
  also ran GORM AutoMigrate on the schema the `migrate` service owns. In CI there is no such server, so the
  same tests silently **skipped**. Deletes were all scoped to test identifiers; no real rows were removed.
- **Fix:** `internal/testutil/pgtest` (container per test binary, empty database per test, prod image) is
  the only DB source; `testutil.NewTestDB` and `internal/db` tests use it. Guard
  `TestNoTestReachesTheLiveDatabase` rejects the compose DSN and `os.Getenv("DATABASE_URL")` in any test.

### BL-12 — Redis test container not pinned (Low)
- **Found:** 2026-11-16, during BL-11: `internal/testutil/containers.go` starts `redis:7-alpine`, while
  compose runs `redis:7.4.11-alpine@sha256:d943…` (BL-7). Postgres tests now use the prod pin (DP-13).
- **Fix:** same pattern as `pgtest.Image` — a constant equal to the compose image plus a test that keeps
  the two in sync.

## Operations (owner)

| ID | Task | Status |
| :--- | :--- | :--- |
| OP-1 | Move `C:\Users\nivik\.mypa\backup-age.key` into a password manager, then delete the local copy (needed to restore encrypted backups — set `BACKUP_AGE_IDENTITY_FILE` only while restoring). Delete `C:\Users\nivik\.mypa\env-backup-*` once the new `.env` has proven itself, and `C:\Users\nivik\.mypa\db-backup-pre-op9-*.dump` (unencrypted pre-purge dump, owner-only ACL) once the purge has proven itself. | open |
| OP-2 | Second Google user (platform id ending `0357`) runs `/connect`; then delete both `legacy-token:*` Redis keys (tokens encrypted under the old public fallback key). The owner's own key can already go. | partly done — owner reconnected, owner's legacy key deleted 2026-11-16; second user pending |
| OP-3 | Old `pgadmin` container (pre-P3-5 image, unpinned, old credentials) is still running; stop/remove it. Start the new one with `docker compose --profile tools up -d pgadmin` when needed. | done 2026-11-16 (stopped and removed) |
| OP-4 | Push `dev`; watch the first CI run. A `-race` timing flake on runners → apply the `timingSlack` pattern (D5-3). | pushed 2026-11-16; first run: race tests, lint, dashboard, compose build green (no `-race` flakes); govulncheck red (go1.26.4 stdlib advisories) → fixed by DP-3; **done** — run 43824019172 on `9284c49` all 5 jobs green |
| OP-5 | Log in to the dashboard once (`JWT_SECRET` was rotated, old sessions are invalid). | open |
| OP-6 | Choose an off-machine backup target (backups are encrypted but stay on this disk). | open |
| OP-7 | After a few stable weeks: migration to drop the legacy `memories` table. | open |
| OP-9 | Purge the test fixtures BL-11 left in the live DB: 154 fixture users plus 722 audit sessions, 167 automation rules, 268 mutation logs, 243 reminders, 176 topic memories, 89 maintenance items, 2 preferences, 1 baby log — each predicate names a fixture identity (`AttackerFamily`/`VictimFamily` users, `*_IDOR`, `DurabilityTestFamily`, `durability_test_*`, `FamilyAlphaSnapshots`, `victim-usr`/`attacker-usr`). Dry run (rolled back) left exactly the MyFamily data. Take a backup first. | **done 2026-11-17** — owner approved; `pg_dump -Fc` backup first (see OP-1), purge in one transaction with the same counts as the dry run; afterwards every tenant table holds only `MyFamily` rows and every audit session belongs to a real user; dashboard GraphQL view unchanged |
| OP-8 | WhatsApp runs on the Twilio **sandbox**: membership expires and replies then fail with 63015 (hit 2026-11-16). Short term: re-send `join <word>` to the sandbox number. Long term: register a WhatsApp sender in Twilio. | open |

## Decisions pending

| ID | Decision | Where |
| :--- | :--- | :--- |
| DE-1 | Accept/reject the service-topology ADR | `docs/adr/0001-service-topology.md` |
| DE-2 | Users with an empty family group: GraphQL resolves them to a shared tenant `"default"` (two such users would see each other's data), while the orchestrator falls back to the user's own ID. Align on per-user isolation, or refuse users without a group? Changes visibility of existing rows. | **decided 2026-11-16** (`fix/de-2`, DP-14): per-user isolation. Worse than logged — `ActivePairingCodes`/`MutationLogs` treated `"default"` as *no filter*, so such a caller saw every family's codes and logs. Live data: 0 users without a group, 0 rows in `"default"` → no visible change |
