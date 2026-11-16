# Backlog — side findings

Things noticed while doing other work, logged so they get fixed deliberately instead of forgotten.
Each item: where it was found, evidence, suggested fix. Fix an item on its own `fix/bl-<n>` branch off
`dev` (failing test first), then mark it done here with the commit hash.

Severity: **High** = wrong behaviour users can hit · **Medium** = security/privacy or ops risk ·
**Low** = hygiene.

## Code

| ID | Sev | Title | Status |
| :--- | :--- | :--- | :--- |
| BL-1 | High | Schedule module silently drops Google Calendar errors | open |
| BL-2 | Medium | GORM default logger: raw SQL with personal data, non-JSON, "record not found" as error | open |
| BL-3 | Low | `.gitignore` only ignores `.env`, not `.env.*` copies | open |
| BL-4 | Low | `migrate`/`audit-worker` inherit unused `localhost` URLs from `env_file` | open |
| BL-5 | Low | Queue `telegram.inbound` also carries WhatsApp traffic | open |
| BL-6 | Low | Proxy logs a WARN for every browser `/favicon.ico` | open |
| BL-7 | Low | Go base images, `redis`, `pgvector` not digest-pinned | open |

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

## Operations (owner)

| ID | Task | Status |
| :--- | :--- | :--- |
| OP-1 | Move `C:\Users\nivik\.mypa\backup-age.key` into a password manager, then delete the local copy (needed to restore encrypted backups — set `BACKUP_AGE_IDENTITY_FILE` only while restoring). Delete `C:\Users\nivik\.mypa\env-backup-*` once the new `.env` has proven itself. | open |
| OP-2 | Second Google user (platform id ending `0357`) runs `/connect`; then delete both `legacy-token:*` Redis keys (tokens encrypted under the old public fallback key). The owner's own key can already go. | open |
| OP-3 | Old `pgadmin` container (pre-P3-5 image, unpinned, old credentials) is still running; stop/remove it. Start the new one with `docker compose --profile tools up -d pgadmin` when needed. | open |
| OP-4 | Push `dev`; watch the first CI run. A `-race` timing flake on runners → apply the `timingSlack` pattern (D5-3). | open |
| OP-5 | Log in to the dashboard once (`JWT_SECRET` was rotated, old sessions are invalid). | open |
| OP-6 | Choose an off-machine backup target (backups are encrypted but stay on this disk). | open |
| OP-7 | After a few stable weeks: migration to drop the legacy `memories` table. | open |

## Decisions pending

| ID | Decision | Where |
| :--- | :--- | :--- |
| DE-1 | Accept/reject the service-topology ADR | `docs/adr/0001-service-topology.md` |
| DE-2 | Users with an empty family group: GraphQL resolves them to a shared tenant `"default"` (two such users would see each other's data), while the orchestrator falls back to the user's own ID. Align on per-user isolation, or refuse users without a group? Changes visibility of existing rows. | `docs/DECISIONS_LOG.md` (P3-2 notes) |
