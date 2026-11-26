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
| BL-12 | Low | `testutil` Redis test container uses the floating `redis:7-alpine` tag | done 2026-11-17 (`fix/bl-12`, DP-15) — also the RabbitMQ test images (broker, e2e) |
| BL-13 | Low | `.env.example` `ALLOWED_USERS` example comment uses a real-format `+972 50…` number (not the owner's); switch to the reserved fictional `+1555010xxxx` range like the SQL-logging test | done 2026-11-17 (`fix/bl-13`, DP-16) — now `+1 202 555 01xx` (the real fictional range; `+1555010xxxx` is not it), plus a tree-wide guard |
| BL-14 | Medium | Memory consolidation is implemented but never runs: nothing schedules or calls it | open — tracked as V4-14 |
| BL-15 | Low | Two code comments point to `docs/PLAN.md`, which does not exist | open |
| BL-16 | Medium | No re-encryption path for old-format (unsalted) ciphertexts, so the legacy decryption fallback cannot be removed safely | pass built — `mypa reencrypt` (V4-07); the removal is V4-45, after the pass has reported zero on the live V4.0 install (DE-12) |
| BL-17 | Low | WhatsApp message splitter cuts by bytes and can split a character in unbroken non-Latin text | open |
| BL-18 | Low | A document sent on Telegram gets no answer at all | open — the channel layer (V4-31) is the natural place |
| BL-19 | Low | Reminder and transcription labels render as italics, probably meant as bold | open |
| BL-20 | Low | `/doctor` shows the same host twice when the webhook addresses differ only in path | open |
| BL-21 | Medium | `/restore` in chat restores over the live schema and does not migrate, which breaks once V4 adds a migration | done 2026-11-19 (`chore/bl-21-retire-chat-restore`): option A, the chat restore is retired |
| BL-22 | Low | Restore tool: the rollback test cannot fail yet, and the piped dump check runs only against a fake | open |
| BL-23 | Low | A failing archive step makes the nightly backup discard its database dump | open |
| BL-24 | Medium | Memories saved with `save_topic_memory` before V4-06 have no embedding, so they are not recalled by meaning | open: the backfill exists since V4-03 (`mypa embeddings reembed --missing`); running it on the live install is the owner's |
| BL-25 | Low | Undo snapshots of saved memories carry the full 3,072-number embedding | done 2026-11-24 (`fix/undo-batch`, with BL-57) |
| BL-26 | Medium | `save_topic_memory` stores `scope` as given, so a value such as `Family` is saved but never found | done 2026-11-24 (`fix/access-memory-batch`, migration 13) |
| BL-27 | High | The real-phone-number guard and the `.gitignore` guard are skipped in every full test run, including CI | done 2026-11-19 (`chore/bl-27-guards`) |
| BL-28 | Low | The one-default-model guard misses a model alias with no version digit (`gemini-flash-latest`) and does not search `.mjs`, `.sh`, `.ps1` or `.sql` files | open |
| BL-29 | Medium | The LLM client sends `Temperature: 0.2`; Gemini 3.6 Flash ignores sampling parameters and later model generations are to reject them with HTTP 400 | done 2026-11-20 (V4-02, DP-35) |
| BL-30 | Low | Tests in `cmd/mypa` (formerly `cmd/orchestrator`) and `internal/crypto` change environment variables and do not put them back exactly | open |
| BL-31 | Medium | The dashboard `users` query returns the users of every family, not only the caller's | done 2026-11-20 by V4-35 (`feat/v4-35-dashboard-signin`): an owner gets the household's members, an adult only themselves |
| BL-32 | Low | A pairing code can be redeemed by two senders at the same moment | open: ADR 0002 requires a one-statement claim for every code |
| BL-33 | Low | WhatsApp and Telegram message splitting counts bytes, not characters, so non-Latin text is split at about half the allowed length | open: fix after the channel port (V4-31), not during it |
| BL-34 | Low | The dashboard home page still shows the label "Orchestrator V3 • Active" | open |
| BL-35 | Medium | A numbered list loses its numbers on both channels, so the typed fallback for choices cannot be answered "with the number" | open: before buttons (V4-10) rely on the fallback |
| BL-36 | Low | With managed services (`EXTERNAL_*`), the bundled Postgres, Redis and RabbitMQ containers still start and sit idle | open |
| BL-37 | Low | `go test ./...` on the Windows host, without `-p 1`, fails the Postgres-backed tests after about 3 s | open |
| BL-38 | Low | The dashboard's `logBabyCare` does not check that the baby ID belongs to the caller's household | done 2026-11-24 (`fix/access-memory-batch`) |
| BL-39 | Low | Deleting an automation rule that is not in the caller's household reports success | done 2026-11-24 (`fix/access-memory-batch`) |
| BL-40 | Low | `/doctor` does not say that linking is off when `LINK_CODE_SECRET` is not set | open |
| BL-41 | Medium | An owner cannot yet remove a member's channel account | done 2026-11-24 (`fix/access-memory-batch`, dashboard; chat with V4-38) |
| BL-42 | Low | Wrong pairing codes do not count toward the install-wide limit on typed codes | open — pairing codes end with V4-36 |
| BL-43 | High | A `false` is stored as `true` in every boolean column whose model says `default:true` (`is_shared` on transactions, `can_view` and `can_edit` on shared spaces, `is_active`, `auto_renew`, `sync_to_google`, `include_in_morning_briefing`) | done 2026-11-24 (`fix/bl-43-stored-booleans`): no non-zero tag default, migration 12; rows stored before are for the owner to review (docs/RESTORE_AND_MIGRATE.md) |
| BL-44 | Medium | In chat, the finance tools list the household's unshared transactions of every member, while the dashboard shows each member only their own | done 2026-11-24 (`fix/bl-43-stored-booleans`): owner's decision, the dashboard's rule on every finance read |
| BL-45 | Low | `dashboard/README.md` documents `NEXT_PUBLIC_GRAPHQL_API_KEY`, which nothing reads | open |
| BL-46 | Medium | Guests cannot be used yet: a guest sees only the topics shared with them by name, and nothing shares a topic with a guest by name | open — needs a roadmap item (proposal: with V4-36 invites or V4-11 tiers) |
| BL-47 | Medium | A household settings change goes to the mutation log, so `/undo` stops working until the next undoable change | done 2026-11-24 (`fix/undo-batch`): off the log, and `/undo` skips entity types it cannot undo |
| BL-48 | Low | The Shared Spaces dashboard page shows two made-up members ("Dana (Admin)", "Omer (Family)") | open |
| BL-49 | Medium | Partner notifications and event-triggered automation messages ignore quiet hours | done 2026-11-24 (`fix/time-automation-batch`) |
| BL-50 | Medium | The reminder tools read a due time without an offset as UTC and confirm it in the server's zone | done 2026-11-24 (`fix/time-automation-batch`) |
| BL-51 | Low | Topic calendar colours never reach Google: no real client implements `CreateEventWithColor` | open |
| BL-52 | Low | `internal/llm/tools.go` keeps a static copy of 27 module tools whose schemas have drifted from the modules | open |
| BL-53 | Low | `update_calendar_event` and `list_calendar_events` read a time without an offset as UTC when no zone is given | open |
| BL-54 | Low | WhatsApp templates exist in English only | open |
| BL-55 | Medium | A legacy workflow run counts as the person writing, and its answer bypasses the delivery policy | done 2026-11-24 (`fix/time-automation-batch`) |
| BL-56 | Low | Telegram's "bot was blocked by the user" is an error, not a "not reachable" refusal | open |
| BL-57 | Medium | Undo restores a memory's old embedding, which after a re-embed into another model is in the wrong vector space | done 2026-11-24 (`fix/undo-batch`, with BL-25) |
| BL-58 | Medium | A turn kept local (`LLM_LOCAL_DOMAINS`) is in the history later cloud turns send; voice and tools reach their own services | open |
| BL-59 | Low | `TestReembed_ToAnotherSizeAfterTheDocumentedMigration` fails on a CRLF checkout of docs/EMBEDDINGS.md | done 2026-11-25 (`fix/pre-test-batch`) |
| BL-60 | Low | `audit_events.prompt_tokens` and `completion_tokens` are never written | open |
| BL-61 | Low | The dashboard's Sessions page shows "View History" and "Revoke" buttons that do nothing | open |
| BL-62 | Medium | An automation rule created inactive is stored active: the column's default replaces a false `IsActive` | done 2026-11-24 (`fix/bl-43-stored-booleans`, with BL-43) |
| BL-63 | Medium | A cron rule whose action is a plain instruction does nothing when it fires | done 2026-11-24 (`fix/time-automation-batch`) |
| BL-64 | Low | The Redis token cache still accepts a Google sign-in stored unencrypted, a legacy branch outside V4-07 and V4-45 | open |
| BL-65 | Low | `/undo` and the dashboard rollback act on the household's newest change, so one member can undo another person's new workflow | done 2026-11-24 (`fix/undo-batch`): chat undoes one's own; the dashboard by role |
| BL-66 | Medium | A financial transaction cannot be changed or deleted after it is logged, so a person cannot make a wrongly shared one private | done 2026-11-25 (`fix/undo-complete-batch`): its own person changes or deletes it, in chat and on the dashboard |
| BL-67 | Medium | A household automation rule fires on another member's unshared transaction, and a webhook rule sends the whole row to its maker's URL | done 2026-11-24 (`fix/time-automation-batch`) |
| BL-68 | Medium | Many chat writes are logged with an action undo cannot roll back (`transaction_logged`, `rule_created`, `upsert`, `supersede` ...), so `/undo` after one fails, and keeps failing | done 2026-11-25 (`fix/undo-complete-batch`): logged as create/update/delete, with an undo guard |
| BL-69 | Low | `schedule_reminder` (a reminder to oneself) cannot be undone: `ScheduledReminder` is not on the undo allowlist and its event names no row | done 2026-11-25 (`fix/pre-test-batch`): `/undo` cancels it, unless it went out |
| BL-70 | Medium | In chat, `list_recent_changes` and `export_topic_data` show every member's change summaries, a private transaction's included | done 2026-11-25 (`fix/pre-test-batch`): the dashboard's rule |
| BL-71 | Low | `TestReembed_ToAnotherSizeAfterTheDocumentedMigration` fails on a Windows checkout: its regex expects LF after the doc's code fence | duplicate of BL-59, done with it 2026-11-25 |
| BL-72 | Low | Making a private transaction shared (`log_financial_transaction` `update`) is not gated in an untrusted turn, so injected text could reveal it | done 2026-11-25 (`fix/pre-test-batch`): every widening call is gated |
| BL-73 | Low | The dashboard's rollback of another member's change that is already undone answers with that change's summary | open |
| BL-74 | Medium | A dashboard sign-in link sometimes reaches the person broken: the channel's markdown conversion reads `_abc_` or `__abc__` in the random token as emphasis | done 2026-11-25 (`fix/ci-flake-93`): a bare URL is read as one piece |

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
- **Done:** a general guard instead of one constant per image (DP-15); it also found
  `rabbitmq:4.3.7-management-alpine` without digest in `internal/broker` and `tests/e2e_test.go`.

### BL-13 — Real-format phone number in `.env.example` (Low)
- **Found:** 2026-11-17, during the pre-push secret scan: the `ALLOWED_USERS` example comment carries two
  dialable `+972` numbers (not the owner's). Already in pushed history, including `master`; no rewrite
  (not personal data of the owner, and a rewrite of shared history costs more than it protects).
- **Done:** the example uses `+12025550101` / `+12025550102` (NANP 555-0100–0199, reserved for fiction).
  Guard `TestNoRealPhoneNumbersCommitted` (`internal/config`) scans every git-tracked file and fails on
  any number that could reach a person, printing it masked (DP-16).

### BL-14 — Memory consolidation never runs (Medium)
- **Found:** 2026-11-18, while verifying the V4 roadmap against the code.
- **Evidence:** `ConsolidateMemories` (`internal/domains/memory/consolidation.go:48`) has no caller outside
  `module_test.go`. The scheduler registers four jobs (backup 02:00, briefing 08:00, reminders every minute,
  workflow reload every 5 minutes) and none of them is consolidation. The function also emits no
  `MutationEvent`, so its changes would not be undoable. The roadmap listed "nightly consolidation" as delivered.
- **Impact:** duplicates and outdated facts are not merged and entity profiles are not refreshed from
  memories in the background. No error is shown; memory quality degrades slowly.
- **Fix:** tracked as V4-14: schedule it nightly, make each applied change an undoable mutation, add a
  per-run report. Test: a nightly run on fixture data produces a report and one change is reverted with `/undo`.

### BL-15 — Comments point to a missing `docs/PLAN.md` (Low)
- **Found:** 2026-11-18, during the docs clean-up.
- **Evidence:** `internal/domains/shared/notifier.go:80` and `internal/models/shared.go:53` cite
  "docs/PLAN.md" for the default sharing policy. No such file exists in the repository.
- **Fix:** point the comments at where the policy is actually described (`SYSTEM.md`), or state the rule inline.

### BL-16 — No re-encryption path for old-format ciphertexts (Medium)
- **Found:** 2026-11-18, while verifying V4-07.
- **Evidence:** `crypto.Decrypt` (`internal/crypto/aes.go`) falls back to the legacy unsalted format when the
  salted format fails. Nothing rewrites a value after a successful legacy decrypt, and there is no migration.
  Values are encrypted in three places: Google tokens in Redis (`internal/state/store.go:140`), the credential
  fallback table (`internal/domains/durability/module.go:150`) and API connector headers
  (`internal/domains/automation/runner.go:109`). Any of them written before the salted format was introduced
  is still in the old format.
- **Impact:** the fallback cannot be removed without first knowing that no old-format value remains; removing
  it blind would make those Google sign-ins and API headers unreadable. This is separate from OP-2, which is
  about tokens encrypted under the old public fallback key.
- **Fix:** a one-off re-encryption pass over the three stores that rewrites each value in the current format
  and reports how many old-format values remain; delete the fallback only when the report shows zero. Split by DE-12:
  the pass ships in V4-07, and V4-45 deletes the fallback after the pass has run on the live V4.0 install.

### BL-17 — WhatsApp splitter can cut inside a character (Low)
- **Found:** 2026-11-19, while writing the channel conformance suite (V4-34).
- **Evidence:** `twilio.SplitMessage` (`internal/twilio/client.go`) measures and cuts by bytes. It prefers a
  paragraph break, a newline or a space, but when the first 1,500 bytes contain none it cuts at byte 1,500.
  Probe: `SplitMessage("a"+strings.Repeat("א", 1200), 1500)` returns two chunks that are both invalid UTF-8.
- **Impact:** only text with more than 1,500 bytes and no space or newline, in a multi-byte script (about 750
  Hebrew letters, 500 CJK characters, or a long run of emoji). Ordinary Hebrew or English prose is not
  affected, and the conformance suite's long-reply scenario (mixed Hebrew and English) passes. When it does
  happen, Twilio receives a broken character at the cut.
- **Fix:** cut on a rune boundary (back up while `!utf8.RuneStart`). Test: the probe above yields only valid
  UTF-8 and loses no character. If the splitter moves into a channel adapter (V4-31), fix it there.

### BL-18 — A document sent on Telegram gets no answer (Low)
- **Found:** 2026-11-19, pinned by the conformance scenario `media_document`.
- **Evidence:** `telegram.ParseUpdate` (`internal/telegram/handler.go`) returns nothing for an update that has
  neither text, a voice note nor a photo, so a file sent as a document never reaches the engine and the sender
  gets silence. With a caption, the caption is answered and the file is ignored without a word. WhatsApp reads
  text files and PDFs and explains what it cannot read (`internal/orchestrator/media.go`).
- **Impact:** the same action has a different outcome per channel, and on Telegram the person cannot tell
  whether the file arrived (ROADMAP §2, principle 8).
- **Fix:** at minimum answer that documents are not supported on Telegram yet; better, ingest them as on
  WhatsApp. Either belongs in the Telegram adapter (V4-31). Update the pinned transcript when it changes.

### BL-19 — Reminder and transcription labels render as italics (Low)
- **Found:** 2026-11-19, visible in the conformance transcripts `reminder.*` and `media_voice.*`.
- **Evidence:** `engine.go` sends `🔔 *Reminder:*` and `🔔 *Shared Task Reminder:*`, and `media.go` sends
  `🗣️ *Transcribed:*`. A single asterisk is Markdown emphasis, so Telegram shows `<i>Reminder:</i>` and
  WhatsApp `_Reminder:_`. Other headings in the code use `**…**` and render bold.
- **Impact:** cosmetic. The single asterisk looks like a leftover from Telegram's old Markdown mode, where it
  meant bold.
- **Fix:** if bold is intended, use `**…**` and regenerate the transcripts
  (`go test ./internal/conformance -update`).

### BL-20 — `/doctor` webhook mismatch can print the same host twice (Low)
- **Found:** 2026-11-19, in the review of V4-17 (non-blocking, after the item passed).
- **Evidence:** `TelegramWebhook.Run` (`internal/doctor/checks.go`) reports a mismatch as "a different address
  (X) than the configured one (Y)" and shortens both with `hostOf`. When Telegram's registered address and
  `TELEGRAM_WEBHOOK_URL` share a host and differ only in path, X and Y are identical.
- **Impact:** cosmetic. The failure is still detected and the next step is still right; the line just does not
  show what differs.
- **Fix:** when the hosts are equal, show host and path. Test: a mismatch in path only prints two different
  strings. The pinned `doctor` transcripts are not affected (their fixture has matching addresses).

### BL-21 — `/restore` in chat does not reset the schema or migrate (Medium)
- **Found:** 2026-11-19, while building restore and migrate (V4-43).
- **Evidence:** `executeConfirmedRestore` (`internal/orchestrator/restore.go`) calls `db.RestoreDatabase` with
  `pg_restore -c --if-exists` into the live database of the running orchestrator, and nothing afterwards runs
  the migrations. `-c` drops only the objects the dump contains.
- **Impact:** none today, because the schema on `dev` is still the one every existing backup was taken with.
  From the first V4 migration on, `/restore` of an older backup leaves the newer tables and columns in place
  next to restored older data, with goose's version table rolled back to the backup's version. The running
  app then works against a schema it does not expect, and the next start either refuses to run (pending
  migrations) or fails migrating (objects already exist). The new `transfer import` path does not have this
  problem: it restores into an emptied schema and then migrates, with the app stopped.
- **Decision prepared 2026-11-19 (nothing is fixed yet).** Three facts bear on it beyond the evidence above:
  - `/restore` takes the newest dump in the backup directory (`internal/orchestrator/restore.go:56`,
    `:91-92`) and restores it into the database the app is using, with no check that anything is stopped.
    The tool refuses a running install (`internal/transfer/transfer.go:326-346`), takes a safety backup
    (`:355-368`), empties the schema (`:372`), restores, then migrates (`:387-407`).
  - Restoring an encrypted backup from chat needs the private backup key readable by the running app
    (`internal/orchestrator/restore.go:49-54`, `internal/db/backup.go:95-110`). Since OP-1 that key is kept
    off the machine and mounted only while restoring, so a chat restore already starts at the terminal.
  - A backup taken since V4-43 has a manifest with its schema version (`internal/db/backup_set.go:88-89`,
    `internal/transfer/live.go:36`). Older dumps, the v3.4.0 baseline backup among them, have none.
- **Options:**

  | | Option | What it takes | Cost | Risk |
  | :--- | :--- | :--- | :---: | :--- |
  | A | **Retire the chat restore.** `/restore` stays as a command, but it only names the newest backup and replies with the steps for `transfer import --replace`. `CONFIRM RESTORE` goes. | Remove the staging and `executeConfirmedRestore` (`internal/orchestrator/restore.go:77-162`), rewrite `restore_confirm_test.go`, update the `/help` line (`internal/orchestrator/commands.go:100`), `SYSTEM.md` and `docs/RESTORE_AND_MIGRATE.md` | S | Lowest. A restore can no longer be started from the phone. One restore path remains to keep correct at every migration |
  | B | **Make it call the same mechanism.** | The import needs the app stopped and empties the schema under it, so the app cannot run it on itself. It would have to record the request and exit, and something outside it (an entrypoint wrapper or a small supervisor) would run the import and start the app again. The key must be on the machine for that | L | Highest. A chat message makes the app take down its own database; a failure half-way leaves the install down with nobody at the keyboard; and the process layout this hand-off is built on changes in V4-01 and again in V4-23 |
  | C | **Refuse when the schema version differs.** | Before staging and again before restoring, read the manifest beside the dump and compare it with the database (`db.SchemaVersion`, `internal/db/doctor_probes.go:19`); refuse on any difference or a missing manifest, pointing to the tool | S | Medium. It closes this bug but keeps the restore under a live app: in-flight turns, the scheduler and the audit worker hold connections while tables are dropped and recreated, and Redis still holds chat windows and approvals for data that is gone. After each migration every older backup is refused, so the command only reaches back to the last migration. Two restore paths stay to test |

- **Recommendation: A, before V4-30 ships the first V4 migration.** The one thing A gives up, restoring from
  the phone, is already not possible for an encrypted backup without a terminal step. The tool does every
  safeguard `/restore` has (confirmation by flag, a safety backup first) and the two it lacks (a stopped
  app, a clean schema, then the migrations). If a restore from chat is still wanted, C is the smallest safe
  step. B is worth another look only after V4-23, when the app is one process with one state store and
  could offer a maintenance mode; not before.
- **Decided 2026-11-19 by the owner: option A.** The chat restore is retired as described in row A. It is
  to be built before V4-30 ships the first V4 migration.
- **Done:** 2026-11-19 (`chore/bl-21-retire-chat-restore`). The line references above describe the code
  before the change. `/restore` is still admin-only; it names the newest backup (the manifest of a set,
  or a bare dump) and replies with the steps of `docs/RESTORE_AND_MIGRATE.md` section C for
  `transfer import <name> --replace`. The backup is named by file name only; the one path shown is the
  fixed key location of the documented command (`/backups/backup-key.txt`, for an encrypted backup), and
  no error text is shown. With no backup it says so. The staging, `executeConfirmedRestore`, `isDestructiveAction`, the
  restore seam of `backupOps` and the `DATABASE_URL` check of `/restore` are gone, so `CONFIRM RESTORE`
  is an ordinary confirmation. A `restore` action staged before the upgrade is dropped when confirmed and
  answered as if nothing were pending. `db.GetLatestBackup` now wraps `db.ErrNoBackup`, so "no backup" is
  told apart from an unreadable directory. Tests: `restore_confirm_test.go` (refusal, instructions on
  both channels, bare dump, no backup, unreadable directory, `CONFIRM RESTORE`, the leftover staged
  record) and `TestOrchestrator_HasNoRestorePath`, which fails if the orchestrator package calls a
  restore function of `internal/db` or `internal/transfer` again. No conformance scenario covered
  `/restore`.

### BL-22 — Two restore-tool paths are tested less directly than the rest (Low)
- **Found:** 2026-11-19, in the review of V4-43 (non-blocking, after the item passed).
- **Evidence:** (1) `TestTransfer_RollbackRestoresWithoutMigrating` (`cmd/mypa/transfer_test.go`, formerly `cmd/orchestrator`)
  restores a set at schema version 2 into a build whose latest version is also 2, so it would pass even if
  `--no-migrate` migrated; today it only proves the printed text. (2) `db.CheckDump` runs `pg_restore -f -`
  reading the archive from a pipe, but `pgtest.ToolRunner` copies the dump into the container and runs
  `pg_restore -f /dev/null <file>`, so the streamed form that gates every import runs only against the fake
  runner.
- **Impact:** low. If (2) misbehaved, the import would refuse before changing anything. (1) starts to matter
  with the first V4 migration.
- **Fix:** (1) a unit test in `internal/transfer` with `SkipMigrate` and a `Migrate` function that fails the
  test when called, and, once a V4 migration exists, an assertion in the end-to-end test that the schema stays
  at the backup's version. (2) let the test runner feed stdin to `pg_restore` inside the container, so the
  piped form is exercised with the real tool. Also covered by the owner's first real restore.

### BL-23 — A failing archive step now costs the nightly database backup (Low)
- **Found:** 2026-11-19, in the review of V4-43 (non-blocking, after the item passed).
- **Evidence:** the nightly backup and `/backup` go through `db.CreateBackupSet` (`internal/db/backup_set.go`).
  When listing or bundling the archive files fails, its `fail` path removes the database dump that was already
  written. Before V4-43 the database backup did not depend on the archive volume.
- **Impact:** low today (the orchestrator reads the volume through a read-only mount, half-written `.tmp`
  files are skipped), but a database backup should not be lost because archives could not be read. `/doctor`
  would report the stale backup the next day.
- **Fix:** for live backups, keep the dump when the archive step fails, write the manifest without archives
  and with a note, and log an error. An export should keep failing outright: a set made to travel must be
  complete. Test: a failing bundle step during a live backup leaves a verified database-only set.

### BL-24 — Memories saved with `save_topic_memory` before V4-06 have no embedding (Medium)
- **Found:** 2026-11-19, while removing `remember_fact` (V4-06).
- **Evidence:** until V4-06, `executeSaveTopicMemory` (`internal/domains/memory/module.go`) wrote a
  `TopicMemory` without an embedding, and the model's `BeforeCreate` hook stored a zero vector. Only
  `remember_fact` called `GenerateEmbedding`. The turn's memory retrieval (`SearchSimilarMemories`,
  `internal/db/memory_v2_repo.go`) orders by vector distance and takes the nearest five, so a zero-vector
  memory is only returned when fewer than five embedded ones exist.
- **Impact:** every fact the assistant saved through `save_topic_memory` (the richer tool, with topic and
  tags) is invisible to automatic recall unless it is pinned or looked up with `search_topic_memories`. New
  saves are embedded from V4-06 on; the existing rows are not. How many rows are affected on the live system
  is not known (it is a count of `topic_memories` whose embedding is all zeros).
- **Fix:** a one-off backfill that embeds every `topic_memories` row with a zero vector, reporting how many it
  did. It is the same job as the re-embedding path V4-03 has to build and test, so do it there or reuse its
  mechanism. Test: a zero-vector memory is found by a similar query after the backfill.
- **Mechanism (V4-03, 2026-11-20):** `mypa embeddings reembed --missing` embeds every `topic_memories` row
  holding a zero vector with the recorded model, in one transaction (`db.Reembed`, `TestReembed_MissingOnly`).
  `mypa embeddings status` shows how many rows are affected, and the worker logs the count at start. Not done:
  running it on the live install, which follows the V4.0 upgrade (docs/EMBEDDINGS.md).

### BL-25 — Undo snapshots of memories carry the full embedding (Low)
- **Found:** 2026-11-19, in the review of V4-06.
- **Evidence:** `executeSaveTopicMemory` (`internal/domains/memory/module.go`) publishes a mutation event whose
  `AfterJSON` is the whole `TopicMemory`, and `pgvector.Vector` serialises to its 3,072 numbers. The same
  happens to `BeforeJSON` when the memory is undone or superseded. Before V4-06 these were 3,072 zeros; now
  they are real values, tens of kilobytes of JSON per saved memory in `mutation_logs`.
- **Impact:** storage growth in `mutation_logs` proportional to the number of saved memories, and a larger
  payload in the dashboard's mutation list. No functional problem; a restored memory keeps its embedding.
- **Fix:** keep the embedding out of the snapshot and recompute it on restore, or store it once and reference
  it. Decide together with BL-24 and V4-03, which settle how embeddings are (re)computed. Test: the snapshot
  of a saved memory stays small, and an undone-then-restored memory is still found by meaning.
- **Decided (owner, 2026-11-20):** fixed together with BL-57 in one change, after V4-07 and before the V4.0 release, not part of any stage-4 item. The
  direction is in BL-57.
- **Done:** 2026-11-24 (`fix/undo-batch`, with BL-57). `TopicMemory.Embedding` is `json:"-"`, so no snapshot written
  by any path carries it (`TestSaveTopicMemory_SnapshotHoldsNoVector`: no vector, under 1 KB). Existing snapshots
  are not rewritten.

### BL-26 — `save_topic_memory` does not normalise `scope` (Medium)
- **Found:** 2026-11-19, in the second review of V4-06 (non-blocking, after the item passed).
- **Evidence:** `executeSaveTopicMemory` (`internal/domains/memory/module.go`) defaults an empty `scope` to
  `personal` and otherwise stores the value as the model sent it. Every read path matches the two literals
  exactly (`scope = 'personal'`, `scope = 'family'` in `internal/db/memory_v2_repo.go`). The removed
  `remember_fact` lower-cased the value and mapped anything that was not `family` to `personal`.
- **Impact:** a save with `"Family"`, `"FAMILY"` or any other spelling answers `"status":"saved"`, and the
  memory is then returned by neither search nor recall, and is not visible to the rest of the family. This was
  already true of `save_topic_memory`; since V4-06 it is the only way to save a fact.
- **Fix:** normalise in `executeSaveTopicMemory` (trim, lower-case, `family` or else `personal`), as the
  removed tool did. Test: a save with `"Family"` is stored as `family` and found by a family member's search.
  Consider a one-off correction of existing rows whose scope is neither literal.
- **Done:** 2026-11-24 (`fix/access-memory-batch`). `models.NormalizeMemoryScope` (trim, lower-case, `family` or
  else `personal`) is applied by `save_topic_memory` and by the dashboard's `saveTopicMemory`
  (`TestSaveTopicMemory_ScopeIsNormalised`: `"Family"` is stored as `family` and found by another member).
  Go migration 13 corrects existing rows by the same rule and logs only the counts
  (`TestMigration13_NormalisesMemoryScopes`, on the v3.4.0 baseline dump plus added rows).

### BL-27 — Two repository guards are skipped in every full test run, including CI (High)
- **Found:** 2026-11-19, in the review of V4-09.
- **Evidence:** `clearEnvKeepTemp` in `internal/config/config_test.go` calls `os.Clearenv()` and restores only
  `TMP`, `TEMP` and `USERPROFILE`. It runs in tests that come first in the package (file-name order), and
  `PATH` is never restored. `TestNoRealPhoneNumbersCommitted` (`phone_numbers_test.go`) and
  `TestGitignore_IgnoresEveryEnvCopyButTheExample` (`gitignore_env_test.go`) then call
  `exec.LookPath("git")`, do not find it, and `t.Skip("git not available")`. The package still prints `ok`.
  Reproduce: `go test -v ./internal/config/` shows both as `--- SKIP`; each passes when run alone with `-run`.
  CI runs the whole package (`go test -race -count=1 -p 1 ./...`), so both have been skipped there.
- **Impact:** the guard against committing a real person's phone number (BL-13) and the guard that `.env`
  copies are ignored by git (BL-3) have not been enforcing anything in CI. Nothing is known to have slipped
  through: both pass today when run alone. But a skip looks exactly like a pass, which is the failure this
  project already decided not to accept for database tests (DP-13).
- **Fix:** (1) make `clearEnvKeepTemp` restore the whole environment when the test ends (snapshot
  `os.Environ()`, restore in `t.Cleanup`), or use `t.Setenv` for the keys it means to clear; (2) make both
  guards fail, not skip, when git is missing (CI always has it), or walk the tree as
  `TestDefaultChatModelIsDefinedInOnePlace` now does. Test: a full `go test -v ./internal/config/` shows no
  `SKIP`. Consider a repository-wide check that no guard test reports SKIP in CI.
- **Done:** 2026-11-19 (`chore/bl-27-guards`). `clearEnvKeepTemp` takes the test, snapshots `os.Environ()`
  and restores it in `t.Cleanup`; `TestClearEnvKeepTemp_RestoresTheEnvironment` holds it to that. Both
  guards now fail when git is not on `PATH` instead of skipping. A full `go test -v ./internal/config/`
  shows no `SKIP`, and both guards pass on the tracked tree: nothing had slipped through while they were
  off. Not done: the repository-wide check that no guard reports SKIP in CI. The other unrestored
  environment changes found in the same search are BL-30.

### BL-28 — The one-default-model guard has two blind spots (Low)
- **Found:** 2026-11-19, in the independent review of V4-09 (non-blocking).
- **Evidence:** `chatModelName` in `internal/config/default_model_test.go` is
  `(?i)\bgemini[- ]?[0-9][0-9A-Za-z.\-]*`: a digit must follow `gemini`. That is deliberate, so that
  `GEMINI_MODEL`, `DefaultGeminiModel` and `gemini-embedding-001` do not match, but it also lets through an
  alias with no version digit, such as `gemini-flash-latest`. Separately, `decidesTheDefault` searches only
  `.env.example`, `Dockerfile*`, `.go`, `.yaml`, `.yml`, `.ts`, `.tsx` and `.json`. The tree also holds `.mjs`
  (`tests/dashboard_graphql_test.mjs`, the dashboard config files), `.sh` and `.ps1` (`scripts/`) and `.sql`
  (`internal/db/migrations/`), and none of them is searched.
- **Impact:** `TestDefaultChatModelIsDefinedInOnePlace` would still pass if `docker-compose.yml` or a
  Dockerfile `ENV` set `GEMINI_MODEL=gemini-flash-latest`, or if a script or a migration named a model. That
  is a second place deciding the model, which is what V4-09 removed. None exists today.
- **Fix:** match the known alias forms as well (`gemini-<family>-latest`, with `<family>` one of `flash`,
  `flash-lite`, `pro`), keeping the embedding models excluded, and add the four file types to
  `decidesTheDefault`. Test: add `gemini-flash-latest` to the cases of `TestChatModelNamePattern`, and a
  compose line, a Dockerfile `ENV` and one file of each new type to `TestDecidesTheDefault`.

### BL-29 — The LLM client sends a sampling parameter that newer Gemini models do not accept (Medium)
- **Found:** 2026-11-19, in the independent review of V4-09 (non-blocking).
- **Evidence:** `internal/llm/client.go` builds every chat request with
  `Temperature: genai.Ptr[float32](0.2)` ("Low temp for more deterministic tool calling"). It is the only
  sampling parameter the client sets. As reported in the review: Gemini 3.6 Flash, the default since V4-09,
  ignores sampling parameters, and Google says later model generations will reject them with HTTP 400. The
  provider's behaviour was not re-checked when this entry was written.
- **Impact:** none on replies today, but the setting no longer does what its comment says, so tool calling
  is not made more deterministic by it. Once `DefaultGeminiModel` (or an install's `GEMINI_MODEL`) names a
  later generation, every chat request would fail with HTTP 400, which means no replies at all.
- **Fix:** resolve in V4-02, the provider work, before the default moves past 3.6: send no sampling
  parameters, or send them only to models that accept them, decided per provider and model. Test: the
  request built for the default model carries no `Temperature`.
- **Fixed in V4-02 (2026-11-20, DP-35):** no adapter sends a sampling parameter, for any provider or model.
  `TestGeminiRequest_CarriesNoSamplingParameter` (`internal/llm`) checks the Gemini request, and the conformance
  scenario `no-sampling` checks the request every provider's fake receives.

### BL-58 — A turn kept local can still reach cloud services in three ways (Medium)
- **Found:** 2026-11-20, while building V4-02.
- **Evidence:** V4-02 keeps a turn in a domain of `LLM_LOCAL_DOMAINS` on Ollama and skips its embedding
  (`internal/orchestrator/turn.go`, §3.19 of SYSTEM.md). Three paths are outside that: (1) the turn is stored in
  the conversation history (`chat:person:<id>`), and the next turn that is not local sends that history to the
  cloud provider; (2) a voice note is transcribed by Groq before the turn's domains are known; (3) a tool the
  model calls (web search, Gmail) reaches its own service.
- **Impact:** an owner who sets `LLM_LOCAL_DOMAINS=finance` may expect finance conversations never to leave the
  machine, and (1) breaks that one turn later. (2) and (3) follow from what the person asked for.
- **Fix:** decide whether the history of a local turn is withheld from later cloud turns (for example stored
  with a local mark and dropped from a cloud request), and say in the docs what local routing covers. Test: a
  cloud turn after a local one carries none of the local turn's text.

### BL-59 — A doc-reading test fails when the doc has Windows line endings (Low)
- **Found:** 2026-11-20, while running the race suite for V4-02 in the golang container, which mounts the Windows
  working tree.
- **Evidence:** `sizeChangeMigration` (`internal/db/embedding_space_test.go`) finds the migration in
  docs/EMBEDDINGS.md with the pattern ``<!-- size-change-migration -->\s*```sql\n(.*?)```  ``. With CRLF line endings
  in the working copy (`core.autocrlf=true`, or an editor that writes CRLF) the `\n` after `sql` does not match and
  the test fails with "must hold the size-change migration in a marked sql block". The committed file is LF, so CI
  passes; a local run against a CRLF working copy does not.
- **Impact:** a false failure of the full local check on Windows. No effect on the product.
- **Fix:** match `\r?\n` in the pattern (the function already removes `\r` from the match), or add
  `docs/*.md text eol=lf` to `.gitattributes`. Test: the function finds the block in a CRLF copy of the doc.
- **Done:** 2026-11-25 (`fix/pre-test-batch`), the owner's decision: the pattern matches ```` ```sql\r?\n ````, in a
  function of the doc's text (`sizeChangeMigrationIn`). Test: `TestSizeChangeMigration_IsFoundWithEitherLineEnding`
  (an LF and a CRLF copy give the same SQL). BL-71 was a duplicate.

### BL-60 — The audit events' token columns are never written (Low)
- **Found:** 2026-11-21, while building V4-04.
- **Evidence:** `models.AuditEvent` has `PromptTokens` and `CompletionTokens` (`internal/models/models.go`), and no code
  writes or reads them. V4-04 records each model call's tokens in `model_usage` and fills the turn's total in
  `audit_sessions.total_tokens`, which was also never written before.
- **Impact:** none on behaviour; two columns that look meaningful are always 0, and a reader of the schema may
  trust them.
- **Fix:** drop the two columns in a versioned migration, or fill them if audit events ever get one row per
  model call. `model_usage` already holds that per call, so dropping them is the simpler choice.

### BL-61 — Dead buttons on the dashboard's Sessions page (Low)
- **Found:** 2026-11-21, while adding the Usage page next to it (V4-04).
- **Evidence:** `dashboard/src/app/sessions/page.tsx` renders "View History" and "Revoke" on every row with no
  handler and no route behind them.
- **Impact:** an owner who presses "Revoke" may believe a member's access was taken away; nothing happens.
- **Fix:** remove both buttons, or wire "View History" to `/logs` filtered by the person; revoking access belongs
  to the household admin area (V4-37). Test: the page has no button without an action.

### BL-62 — An automation rule created inactive is stored active (Medium)
- **Found:** 2026-11-24, while tightening `TestReloadWorkflows_WithPostgres` (V4-07).
- **Evidence:** `models.AutomationRule.IsActive` is tagged `gorm:"default:true"`. GORM leaves a zero value out of the
  insert when the field has a default, so `Create` with `IsActive: false` stores `true`. `create_automation_rule`
  with `is_active: false` (`internal/domains/automation/handlers.go`, `executeCreateAutomationRule`) therefore
  creates an active rule, and the scheduler and the event runner run it. The test now switches its rule off with an
  update after creating it.
- **Impact:** a rule the person asked to create switched off runs anyway, on its schedule or on matching events.
- **Fix:** write `is_active` explicitly on create (`Select("*")`, or an update after the insert in the same
  transaction), or drop the default from the tag with a migration that keeps the column default. Test: a rule
  created with `is_active: false` is stored false and is not scheduled.
- **Done:** 2026-11-24 with BL-43 (`fix/bl-43-stored-booleans`). `Select("*")` would not have helped: GORM puts the
  default in place of a zero value even for a selected column. The tag has no default, and migration 12 drops the
  column's. Tests: `TestStoredValues_AFalseIsStoredAsAsked`, `TestDashboard_AFalseIsStoredAsAsked`,
  `TestChat_ARuleCreatedInactiveIsStoredInactive`, and `TestReloadWorkflows_WithPostgres` without its workaround.

### BL-63 — A cron rule whose action is a plain instruction does nothing (Medium)
- **Found:** 2026-11-24, while folding the workflows into cron rules (V4-07).
- **Evidence:** `create_automation_rule` describes `action_instruction` as a webhook URL, `notify: ...` "or task
  instruction". `Runner.ExecuteAction` acts on a webhook and on `notify:` only; anything else publishes
  `rule_executed` and does nothing. The scheduler runs a plain instruction as a turn only when it has no runner,
  which production always has. V4-07 gave scheduled workflows an explicit `prompt:` action instead of changing what
  existing plain rules do.
- **Impact:** a person who creates a scheduled rule with a task instruction is told it was created, and it never
  does anything.
- **Fix:** decide what a plain instruction means: either treat it as `prompt:` (a turn for the rule's person; this
  changes what existing rules do on the next fire, so check the live rules first) or refuse it when the rule is
  created, and say so in the tool's description. Test: a cron rule with a plain instruction does what the
  description says.
- **Done:** 2026-11-24 (`fix/time-automation-batch`), the owner's decision: a new rule created with a plain instruction on a
  cron trigger is stored as `prompt:` and runs as a turn for its person (`automation.NormalizeAction`, used by
  `create_automation_rule` and the `saveAutomationRule` mutation; the tool's description says so). On an event
  trigger such a rule would do nothing, so it is refused when it is created. Existing rules are not changed;
  `/doctor` (Automation rules, `db.Client.IdleRuleCount`) counts the household's rules whose action does nothing.
  Tests: `TestCronRule_APlainInstructionRunsAsATurn`, `TestCreateRule_APlainInstructionIsAPromptOnASchedule`,
  `TestSchemaResolvers_SaveAutomationRule_APlainInstruction`, `TestIdleRules`.

### BL-64 — The token cache still accepts an unencrypted Google sign-in (Low)
- **Found:** 2026-11-24, while building the re-encryption pass (V4-07).
- **Evidence:** `state.Store.getOAuthToken` (`internal/state/store.go`) treats a value that does not decrypt as an
  unencrypted token when it parses as JSON with an `access_token`, and encrypts it on that read. The pass reports
  such a value as "not readable with this key" and leaves it. V4-45 removes only the unsalted fallback in
  `internal/crypto`.
- **Impact:** a plaintext token written by a very old release stays readable in Redis until it is next read; the
  branch is never removed. No current code writes one.
- **Fix:** with V4-45 or V4-23 (which moves the tokens into Postgres): count such values in the pass or at start,
  then remove the branch once none is left. Test: a plaintext token is refused.

### BL-65 — One member's undo can remove another person's new workflow (Low)
- **Found:** 2026-11-24, by the V4-07 review.
- **Evidence:** `RollbackLast` (`internal/domains/durability/module.go`) takes the household's newest mutation, and
  `RollbackSnapshot` (`internal/db/durability_repo.go`) deletes the created row with a household filter only. Since
  V4-07 a workflow is an automation rule, which is on the undo allowlist; before, `Workflow` was not, so a workflow
  could not be undone at all. Every scoped rule read and write hides another person's workflow
  (`workflowsOfOthersHidden`); undo does not go through it.
- **Impact:** when the household's newest change is a person's `schedule_workflow`, another member's `/undo` deletes
  that workflow. The same holds for every person-owned row in a household table today.
- **Fix:** decide whether undo of a person's own row is that person's: either filter `RollbackLast` to the caller's
  own mutations for person-owned entities, or check the owner in `RollbackSnapshot`. Test: another member's `/undo`
  leaves a person's new workflow in place.
- **Done:** 2026-11-24 (`fix/undo-batch`), the owner's decision: `/undo` and `undo_last_action` act on the caller's
  own newest undoable change. In the dashboard an owner (`identity.UndoOthersChanges`) may roll back any change to
  shared household data; a change to a person-owned row only that person; a change with no known person only an
  owner from the dashboard, and only for shared data. `db.MayUndo` decides, in the module and again inside
  `RollbackSnapshot` on the locked log and the row (`models.UndoVia`). Tests: `TestUndo_ChatUndoesTheCallersOwnNewestChange`,
  `TestUndo_TheRollbackItselfChecksTheOwner`, `TestUndo_DashboardRulesPerRole`.

### BL-66 — A transaction cannot be changed or deleted after it is logged (Medium)
- **Found:** 2026-11-24, while writing the BL-43 upgrade notes.
- **Evidence:** the finance tools (`log_financial_transaction`, `get_financial_summary`, `set_category_budget`,
  `manage_recurring_bills`) and the GraphQL schema (`logFinancialTransaction`) create and read transactions; no tool,
  mutation or repository method updates or deletes one (`FinanceRepository` has no `UpdateTransaction`).
- **Impact:** a transaction stored shared that its person meant as private (every one logged before BL-43's fix was
  stored shared) stays visible to the household. The only correction is an `UPDATE` in the database by the owner.
  A wrong amount or category cannot be corrected either.
- **Fix:** an update (at least `is_shared`, amount, category, merchant) and a delete, for the transaction's own person
  only, in chat and on the dashboard, announced and undoable like other changes. Test: another member cannot change
  or delete it; its person can make it private, and it then leaves the other members' lists and totals.
- **Done:** 2026-11-25 (`fix/undo-complete-batch`), the owner's decision: `log_financial_transaction` takes `action`
  `update` or `delete` with `transaction_id` (sharing, amount, category, merchant, date, type, currency, notes); the
  dashboard has `updateFinancialTransaction` and `deleteFinancialTransaction` (the finance page edits the viewer's own,
  `FinancialTransaction.isMine`). Both go through `db.FinanceRepository.UpdateOwnTransaction`/`DeleteOwnTransaction`,
  which find the caller's own transactions only, so another member and an owner are refused (`ErrNotOwnTransaction`).
  A chat delete is gated in an untrusted turn like the other destructive tools (`destructiveActionsWhenUntrusted`); the
  dashboard's deletes have no confirmation step, and this one follows them. Both are logged as `update`/`delete` with
  their snapshot, announced, and undoable; the budget and anomaly checks run on the new values; one made private is
  skipped by the partner notifier and by another member's rule (`MutationEvent.Unshared`). Tests:
  `TestChat_APersonChangesAndDeletesTheirOwnTransaction`, `TestChat_AChangedTransactionCountsWithItsNewValues`,
  `TestChat_OnlyItsPersonMayChangeOrDeleteATransaction`, `TestChat_APrivateTransactionIsNotRevealedByTheNotifierOrARule`,
  `TestDashboard_APersonChangesAndDeletesTheirOwnTransaction`, `TestDeletingATransactionIsGatedInAnUntrustedTurn`.
  Gating an update that shares a private transaction is left to the owner (BL-72).

### BL-67 — Household automation rules see every member's unshared rows (Medium)
- **Found:** 2026-11-24, while fixing BL-44 (review of the partner notifier).
- **Evidence:** `automation.Runner` matches every mutation event of the household against its active rules
  (`HandleMutationEvent`; `EvaluateCondition` reads `Summary` and `AfterJSON`). A rule one member made
  (`trigger_topic` `finance`, `min_amount` 500) fires on another member's unshared transaction: `notify:` tells its
  maker (the rule's own text, so only that it fired), and a webhook rule POSTs the whole event, merchant and amount
  included, to the maker's URL (`ExecuteAction`, `internal/domains/automation/runner.go`).
- **Impact:** a person's private transaction (or unshared calendar event) reaches another member through a rule,
  the one path left after BL-44.
- **Also:** the partner notifier skips an unshared row by its after-image only. A delete carries only `BeforeJSON`
  (chat `delete_calendar_event`, dashboard `deleteScheduleEvent`), so deleting an unshared calendar event in a topic
  with a notifying shared space announces its title. Fix with the same change: `unshared` also reads `BeforeJSON`
  when there is no after-image.
- **Fix:** decide whether a household rule acts on another person's unshared rows. If not, the runner skips an event
  whose after-image says `"is_shared": false` unless the rule's person is the event's actor. Test: Omer's rule does
  not fire on Dana's private transaction and fires on her shared one and on his own private one.
- **Done:** 2026-11-24 (`fix/time-automation-batch`), the owner's decision: a household rule does not act on another person's
  unshared rows. The runner skips an event whose row is `"is_shared": false` unless the rule's person is the event's
  actor; the row is read from the after-image, or the before-image on a delete (`events.MutationEvent.Unshared`,
  which the partner notifier uses too, so deleting an unshared calendar event announces nothing). Tests:
  `TestRunner_ARuleSkipsAnotherMembersUnsharedRow`, `TestRunner_AnActorNamedByPersonIDIsTheRulesPerson` (an event names
  its actor by platform ID or `users.id`; the rule's `user_id` and `person_id` both count), `TestNotifier_DeletingAnUnsharedRowIsNotAnnounced`.

### BL-68 — Many chat writes cannot be undone, and then block `/undo` (Medium)
- **Found:** 2026-11-24, while fixing BL-47.
- **Evidence:** `RollbackSnapshot` (`internal/db/durability_repo.go`) rolls back the actions `create`, `update` and
  `delete` only, and answers `unsupported mutation action` for anything else. Chat tools log other actions on entity
  types that are on the undo allowlist: `transaction_logged` (`FinancialTransaction`, `internal/domains/finance/handlers.go`),
  `rule_created` (`AutomationRule`), `maintenance_created`, `vehicle_created` and the other `vehicle_*` and
  `maintenance_*` actions (`internal/domains/house/handlers.go`), `upsert` (`EntityProfile`), `complete`
  (`SharedTaskReminder`), `recurring_bill_*`, `budget_set`, `preferences_updated`, and `supersede` (`TopicMemory`,
  a memory saved in place of an older one).
- **Impact:** after one of these, `/undo` fails with "unsupported mutation action". It is the person's newest change
  of an undoable type, so every later `/undo` fails the same way until they make a change undo can roll back. BL-47's
  fix skips entity types, as decided, not actions.
- **Fix:** decide between (a) logging these as `create`/`update`/`delete` with the snapshots undo needs (`upsert` and
  `supersede` need both rows), or (b) having `/undo` skip actions it cannot roll back as well, which then undoes an
  older change without saying so. Test: `/undo` right after logging a transaction in chat removes it.
- **Done:** 2026-11-25 (`fix/undo-complete-batch`), the owner's decision (a): every chat and dashboard write to an
  allowlisted entity is logged as `create`, `update` or `delete` with the snapshots undo needs (an upsert by whether the
  row existed; completing a reminder is an update). The old name stays in `kind` (`MutationEvent.Kind`,
  `mutation_logs.kind`, migration 14), and every reader (the partner notifier's verb, a rule's `action` condition, the
  audit page, `list_recent_changes`, the CSV export) reads `Label()`, so what people see and what rules match is
  unchanged. A memory saved in place of an older one, and a baby measurement that updates the profile, record the other
  row in `also_json`; one undo puts both back. Snapshots keep empty fields (`models.Snapshot`). Old entries: undone
  where the stored snapshot is enough (`db.UndoPlan`), otherwise `/undo` names the change and changes nothing
  (`db.CannotUndoMessage`). The dashboard's new budget and first preferences were logged as updates with no earlier
  state; they are creates now. The guard: `cmd/mypa/undo_guard_test.go` walks every tool the worker registers
  (`toolModules`) and proves `/undo` reverses each write, or names why it cannot (`notUndoable`). Tests:
  `TestUndoGuard_EveryToolIsClassified`, `TestUndoGuard_UndoReversesEveryWrite` (one subtest per write, the
  transaction and the replaced memory among them), `TestUndoGuard_CatchesAWriteToolWithNoUndo`,
  `TestUndo_AnOldEntryWithAUsableSnapshotIsUndone`, `TestUndo_AnOldEntryWithoutASnapshotIsNamedAndNothingChanges`,
  `TestDashboard_ANewBudgetAndFirstPreferencesAreUndone`. After review: a rule's record that it ran no longer stands
  in front of the change that fired it, a webhook receives the old action name, an owner is never shown another
  member's summary, and an old whole-row snapshot's empty fields are cleared (`TestUndo_ARulesRecordThatItRanDoesNotBlockUndo`,
  `TestWebhook_TheReceiverSeesTheChangeUnderItsOwnName`, `TestUndoGuard_PeopleAndRulesSeeTheSameNames`,
  `TestUndo_AnOwnerIsNotShownAnotherMembersSummary`, `TestUndo_AnOldWholeRowSnapshotClearsTheFieldsItLeftOut`).

### BL-69 — A reminder to oneself cannot be undone (Low)
- **Found:** 2026-11-25, by the undo guard written for BL-68.
- **Evidence:** `schedule_reminder` (`internal/domains/shared/module.go`) saves a `ScheduledReminder` and logs it with
  the entity ID `<person>:<unix time>`, not the row's ID (`DBClient.SaveReminder` returns none), and
  `ScheduledReminder` is not on the undo allowlist (`db.UndoTables`). The guard lists it in `notUndoable`.
- **Impact:** `/undo` right after "remind me at 7" skips it and undoes the person's previous undoable change, if any.
- **Fix:** return the new row's ID from `SaveReminder`, log it as a `create` of a person-keyed table, and add
  `scheduled_reminders` to the allowlist (undo confines a table without `household_id` to the log's own user); then
  move its proof into `undoProofs`.
- **Done:** 2026-11-25 (`fix/pre-test-batch`), the owner's decision: `schedule_reminder` saves the reminder with
  `db.Client.CreateReminder` and logs it by its row ID; `ScheduledReminder` is on the allowlist as a person-keyed table.
  `/undo` right after it cancels it; once it has gone out (`is_sent`), `/undo` says so and changes nothing
  (`db.ErrReminderSent`). The poller claims a reminder before sending it (`db.Client.ClaimReminder`), so the claim and
  an undo, which lock the same row, never both win. An entry written before named no row and gets the plain cannot-undo answer. Its proof moved
  into `undoProofs`. Tests: `TestUndo_AReminderToOneself`, `TestUndo_AnOldReminderEntryIsNamedAndNothingChanges`,
  `TestUndoGuard_UndoReversesEveryWrite/schedule_reminder`.

### BL-70 — Chat's change history shows other members' private changes (Medium)
- **Found:** 2026-11-25, while fixing BL-66.
- **Evidence:** `list_recent_changes` and `export_topic_data` read `GetMutationLogsByTopic`
  (`internal/db/durability_repo.go`), which filters by household only, and print each entry's summary (and, in the
  export, its snapshots). The dashboard's `mutationLogs` shows an adult only their own entries and an owner the others'
  without summaries or snapshots.
- **Impact:** another member can read in chat that a person logged, changed or deleted a private transaction, with its
  amount and merchant.
- **Fix:** give the chat tools the dashboard's rule: the caller's own entries in full; others' only for an owner, as
  "action entity type" without summaries or snapshots. Test: Omer's private transaction is not in Dana's chat history.
- **Done:** 2026-11-25 (`fix/pre-test-batch`), the owner's decision: `db.Client.GetMutationLogsByTopic`, which both
  chat tools read, applies the dashboard's rule (`identity.ViewMemberActivity`; another member's entry as `<label>
  <entity type>`, no snapshots). The sweep found no other chat tool or export reading the mutation log or audit data;
  the dashboard's `mutationLogs` and `auditSessions` already had the rule, and `transfer export` is the operator's
  copy of the whole install. Test: `TestChatHistory_AnotherMembersPrivateChangeIsNotShown`.

### BL-71 — A docs test fails on a Windows checkout (Low)
- **Found:** 2026-11-25, in the full test run for BL-68.
- **Evidence:** `sizeChangeMigration` (`internal/db/embedding_space_test.go`) finds the SQL block in `docs/EMBEDDINGS.md`
  with ```` ```sql\n ````; with `core.autocrlf=true` the working copy has CRLF, so the block is not found and
  `TestReembed_ToAnotherSizeAfterTheDocumentedMigration` fails. The race run in a container mounts the same working
  copy. The file's content in git is unchanged.
- **Impact:** a false failure on Windows; none in CI (Linux checkout).
- **Fix:** match `\r?\n` in the regex (the test already strips CRs from the SQL it reads), or pin the doc to LF in
  `.gitattributes`. Test: the test passes on a CRLF copy of the doc.
- **Duplicate of BL-59**, done with it 2026-11-25.

### BL-72 — Sharing a private transaction is not gated in an untrusted turn (Low)
- **Found:** 2026-11-25, by the review of BL-66.
- **Evidence:** the owner's decision for BL-66 gates only the delete (`destructiveActionsWhenUntrusted` in
  `internal/orchestrator/security.go`). `log_financial_transaction` with `action: "update"` and `is_shared: true`
  runs in a turn that read an email or a web page.
- **Impact:** injected text could make a person's private transaction shared; the partner notifier then announces
  it to the household. Before BL-66 no path changed a transaction at all.
- **Fix:** decide whether an update that sets `is_shared: true` is gated too (the gate reads the call's arguments,
  so it can be gated for that case only). Test: after untrusted content it is staged for CONFIRM.
- **Done:** 2026-11-25 (`fix/pre-test-batch`), the owner's decision: after untrusted content every call that widens
  who sees something is staged for CONFIRM (`sharingCallsWhenUntrusted`): `log_financial_transaction` `update` with
  `is_shared: true`, `configure_shared_topic` unless `can_view` is false, and `create_calendar_event` that makes a shared event (`is_shared:
  true`, or no yes/no `is_shared` on a topic shared by default, as the tool reads it).
  Sharing a memory (`save_topic_memory`) or an entity profile was already gated as a whole tool; no tool changes an
  existing calendar event's sharing. A call that makes something more private runs. Test:
  `TestSharingIsGatedInAnUntrustedTurn` (staged, then run once on CONFIRM).

### BL-73 — An owner's rollback of an already-undone change shows its summary (Low)
- **Found:** 2026-11-25, by the review of the BL-70 batch.
- **Evidence:** `executeRollbackInternal` (`internal/domains/durability/module.go`) answers an entry that is already
  undone with `log.Summary` in `Summary` and `Detail`. It runs after `MayUndo` without row states, so an owner calling
  `rollbackMutation` on another member's undone entry gets that member's summary.
- **Impact:** an owner can read another member's change summary (a private transaction's merchant and amount) that the
  audit page and chat history (BL-70) show them only as action and entity type.
- **Fix:** name another member's change by its label and entity type there too, as `notUndoable` does. Test: an owner's
  rollback of a member's undone private entry does not show its summary.

### BL-74 — A dashboard sign-in link sometimes reaches the person broken (Medium)
- **Found:** 2026-11-25, CI run 45569046251 on `3fc388c`: `TestDashboard_OnlyOwnersAdultsAndTheOperator/adult`
  (`internal/conformance`) failed its assertion (no token in the reply), not a data race. The runs before and after
  passed.
- **Evidence:** the reply held `signin#<i>WkILRIM5Uov</i>-bef6...`: the token was `_WkILRIM5Uov_-bef6...`, and
  `telegram.ToTelegramHTML` read `#_..._-` as emphasis. `twilio.ToWhatsApp` turned `__abc__` into `*abc*` the same way.
  On 20,000 tokens from a fixed seed, 7 Telegram links changed (about 1 in 3,000). The conformance test was right: a
  real person would have had a link that signs no one in.
- **Fix:** `internal/channel/bareurl`, an inline parser both converters add: a bare http(s) URL, any host (localhost, a
  LAN name, an IP), is read as one piece before emphasis and written back as it was. Tests:
  `TestToTelegramHTML_BareURLIsLiteral`, `TestToWhatsApp_BareURLIsLiteral` (the CI token and its `__` form), the
  `..._SignInTokensSurvive` runs over 20,000 seeded tokens, and `..._URLsLeaveTheRestAlone`.

### BL-30 — Other tests change the process environment and do not restore it exactly (Low)
- **Found:** 2026-11-19, in the search of every `_test.go` file done for BL-27.
- **Evidence:** `cmd/mypa/playground_security_test.go:18-19` and `:44-45` (formerly `cmd/orchestrator`) set `ENV` and
  `DASHBOARD_API_SECRET` with `os.Setenv`. `ENV` is put back with a `defer`, but as an empty value when it
  was unset before; `DASHBOARD_API_SECRET` is never put back. `internal/crypto/aes_test.go:31-42` and
  `:105-109` set and unset `ENCRYPTION_KEY` and restore it the same way, so an unset variable comes back
  set to an empty string. No other `_test.go` file calls `os.Clearenv`, `os.Setenv`, `os.Unsetenv` or
  `os.Chdir` (the `os.Chdir` calls in `internal/config/config_test.go` are restored with a `defer`).
  Separately, the only other `t.Skip` in the repository is `internal/llm/llm_test.go:17`
  (`TestLLMFunctionCalling`, skipped when `GEMINI_API_KEY` is not set). It is a call to the real provider,
  not a guard, and it is skipped in CI.
- **Impact:** none known. Each test binary is one process, so the leftover values reach only later tests
  of the same package, and no test there is known to depend on them. It is the same mechanism as BL-27:
  a test added later that reads one of these variables would get a value another test left behind.
- **Fix:** replace the `os.Setenv`/`os.Unsetenv` pairs with `t.Setenv`, which restores the exact earlier
  state (unset stays unset) and refuses to run in a parallel test. For the `os.Unsetenv` step in
  `aes_test.go`, call `t.Setenv` first and then `os.Unsetenv`. Decide whether `TestLLMFunctionCalling`
  should stay a skipped test in the unit suite or move behind a build tag.

### BL-31 — The dashboard `users` query returns every family's users (Medium)
- **Found:** 2026-11-19, while reading the code for ADR 0002.
- **Evidence:** `Users` (`internal/graph/schema.resolvers.go:1409-1427`) runs `Find(&dbUsers)` with no
  filter and never calls `resolveTenantAndActor`. The listing next to it does (`ActivePairingCodes`,
  `:1430-1448`). The dashboard signs in as an admin bound to one family
  (`internal/graph/auth_middleware.go:114-125`), which is meant to stop exactly this (P3-2). No test in
  `internal/graph` calls the query.
- **Impact:** none visible today: since OP-9 the live database holds one family. With a second family, its
  dashboard would list the names, platform IDs (phone numbers among them), roles and family labels of
  everyone on the install.
- **Fix:** scope the query to the caller's family unless `wantsAllTenants` allows otherwise, as the other
  listings do. Test: a tenant-bound admin sees only its own family's users. ADR 0002 (Decision 3) removes
  the pattern altogether; this fix is small enough to make before that.

### BL-32 — A pairing code can be redeemed twice at the same moment (Low)
- **Found:** 2026-11-19, while reading the code for ADR 0002.
- **Evidence:** `handleUnauthenticatedUser` reads the code with `GetPairingCode`
  (`internal/orchestrator/engine.go:283-286`; `internal/db/db.go:232-239`, which requires `is_used = false`)
  and then calls `RedeemPairingCode` (`internal/db/db.go:242-254`). That sets `is_used` with `WHERE id = ?`
  only: it does not require the code to be unused still, and does not look at how many rows changed, before
  creating the user. Messages from two different senders are handled in parallel
  (`internal/broker/consumer.go:149-150`), so both can pass the read and both become users.
- **Impact:** low. It needs the same code from two accounts within milliseconds, and someone who knows a
  code can simply use it first. But "works once" is the property the invite and link codes of ADR 0002
  depend on, and the `pairing` conformance scenario only proves it for two attempts in sequence.
- **Fix:** claim the code in one statement (`UPDATE ... WHERE id = ? AND is_used = false AND expires_at >
  now()`) and create the user only when exactly one row changed. Test: two redemptions of one code started
  together against a real database produce exactly one user.

### BL-33 — Message splitting counts bytes, not characters (Low)
- **Found:** 2026-11-19, while building BL-21.
- **Evidence:** both splitters measure with `len()` of a Go string, which counts UTF-8 bytes:
  `twilio.SplitMessage` (`internal/twilio/client.go:103`, called with 1,500) and
  `markdown.SafeChunkTelegramHTML` (`internal/markdown/telegram.go:148`, called with 4,000). The platforms
  limit by characters (WhatsApp through Twilio 1,600, Telegram 4,096). A Hebrew, Arabic or Cyrillic letter
  is two bytes and most CJK characters three.
- **Impact:** a reply in Hebrew, or any non-Latin script, is split at about half the length the platform
  allows (about 750 Hebrew letters on WhatsApp, 2,000 on Telegram), so a long answer arrives as more
  messages than needed. Nothing is lost. Separate from BL-17, which is about cutting inside a character.
- **Fix:** after the channel port (V4-31), not during it: ADR 0003 has the port keep today's split functions
  and their byte counting unchanged (ADR 0003, the `MaxTextLen` row). Then measure in
  characters (runes, or UTF-16 units for Telegram), keep the margin below the limit, and fix BL-17 in the
  same change. Test: a Hebrew reply of 1,000 letters goes out on WhatsApp as one message, and every chunk
  stays within the platform's limit in characters.

### BL-34 — The dashboard home page still names the orchestrator (Low)
- **Found:** 2026-11-19, while building V4-01.
- **Evidence:** `dashboard/src/app/page.tsx:81` renders the status label `Orchestrator V3 • Active`. Since
  V4-01 there is no orchestrator service (it is the worker of the one `app`), and the label is a fixed
  string: it reports neither the running version nor whether the app is reachable.
- **Impact:** cosmetic; the label is wrong on V4 and says "Active" even when the app is down.
- **Fix:** with the dashboard reorganisation (V4-37), or on its own: show the app and its version, taken
  from the app instead of hard-coded (for example from `/doctor`'s data once V4-37 gives it a page), or
  drop the label. Test: the home page does not contain "Orchestrator".

### BL-35 — A numbered list loses its numbers on both channels (Medium)
- **Found:** 2026-11-19, by the new conformance scenario `fallbacks` (V4-34).
- **Evidence:** `channel.ChoiceList` (`internal/channel/render.go`) renders choices for a channel without
  buttons as `1. Book it (reply yes)`, `2. …`, and ADR 0003 Decision 2 says the person replies "with the
  number or the words". Both adapters then convert the text from Markdown, and both converters write every
  list item with a fixed bullet whether the list is ordered or not: `internal/telegram/markdown.go:97-103`
  writes `• `, the WhatsApp converter in `internal/twilio/markdown.go` writes `- `. What arrives is pinned in
  `internal/conformance/testdata/golden/fallbacks.*.txt`: `• Book it (reply yes)` on Telegram and
  `- Book it (reply yes)` on WhatsApp. The same happens to any numbered list the model writes in a reply.
- **Impact:** no user hits it through choices yet, because nothing in the engine sends choices before V4-10.
  Once it does, a person on either channel sees an unnumbered list and is told elsewhere to answer with a
  number. Numbered steps in ordinary replies (a recipe, a plan) already lose their numbers today.
- **Fix:** in both converters, render an item of an ordered list (`ast.List.IsOrdered()`) with its number
  (`Start` plus its position) instead of the bullet. Tests: a converter test per channel, and in the
  conformance scenario `fallbacks` assert `1. Book it` … `3. Another day` and regenerate the two transcripts.
  The `plain_reply` transcripts stay as they are (their list is unordered).

### BL-36 — Bundled infrastructure still starts with managed services (Low)
- **Found:** 2026-11-20, while building local-or-deployed (V4-40).
- **Evidence:** `EXTERNAL_DATABASE_URL`, `EXTERNAL_REDIS_URL` and `EXTERNAL_RABBITMQ_URL` replace the
  addresses the containers use (`docker-compose.yml`), but `postgres`, `redis` and `rabbitmq` have no
  profile and the app, `migrate` and `audit-worker` depend on them with `service_healthy`, so `up` starts
  them anyway. Compose profiles only add services; making the bundled ones a profile would make the
  default install's database opt-in.
- **Impact:** an install on managed services runs three idle containers (memory, and a second database
  that is never written). Nothing is wrong functionally.
- **Fix:** options are `depends_on: required: false` with the bundled services in a default-on profile
  set through `COMPOSE_PROFILES` in `.env.example`, or a documented `--scale <service>=0`. Either needs
  the compose topology tests (`TestCompose_UpRunsOneAppPlusAuditWorkerDashboardAndInfrastructure`) to say
  what a default `up` starts, and a run against a real managed Postgres.

### BL-37 — Postgres-backed tests time out in a parallel host run (Low)
- **Found:** 2026-11-20, while building V4-40 (unrelated packages).
- **Evidence:** `go test ./... -count=1` on the Windows host fails most tests in `internal/db` and some in
  `internal/domains/automation` and `internal/domains/memory` after about 3 s each; the same packages pass
  when run alone, and CI and the documented full run use `-p 1` in the golang container. The failures look
  like a too-short wait for the throwaway Postgres container when many packages start one at once.
- **Impact:** a developer who runs the plain command locally sees dozens of failures that are not real.
- **Fix:** find the 3-second limit in the shared Postgres test helper and raise it, or document `-p 1`
  next to `go test ./...` in the README's test instructions.

### BL-38 — `logBabyCare` accepts another household's baby ID (Low)
- **Found:** 2026-11-20, while building V4-30 (the GraphQL slice).
- **Evidence:** the `logBabyCare` mutation (`internal/graph/schema.resolvers.go`) writes the care log into the
  caller's household without first loading the baby profile through the scope, so a log can name a
  `baby_id` that belongs to another household. The chat tool resolves the baby through the scope first.
- **Impact:** no data leaks: the log is in the caller's household and nothing of the other household is read.
  It is a dangling reference that the baby summaries of the caller's household ignore.
- **Fix:** load the profile with `GetBabyProfile(scope, id)` first and refuse when it is not found.
- **Done:** 2026-11-24 (`fix/access-memory-batch`). `TestLogBabyCare_BabyOfAnotherHouseholdIsRefused`: another
  household's baby and a missing one are refused and nothing is written.

### BL-39 — Deleting a foreign automation rule reports success (Low)
- **Found:** 2026-11-20, while building V4-30 (the finance and automation slice).
- **Evidence:** `DeleteAutomationRule(scope, id)` filters by household and deletes nothing for a rule of another
  household, but returns no error when no row matched, because an existing test asserts that deleting a missing
  rule succeeds. The dashboard path checks the rule's household first; the repository answer is the same
  "success" for a missing rule and for a rule elsewhere. Both callers (the chat tool and the dashboard) look the
  rule up in the household first and refuse, so no caller is told a deletion happened; only a new caller of the
  repository method could be.
- **Impact:** none today; a trap for a future caller.
- **Fix:** return not-found on zero rows, as every other delete by ID does since V4-30, and change that test's
  expectation deliberately.
- **Done:** 2026-11-24 (`fix/access-memory-batch`). `TestDeleteAutomationRule_NoRowIsNotFound`. Two old
  expectations changed deliberately: `TestAutomationRepo_AutomationRules_CRUDAndIsolation` (another household's
  delete) and `TestAutomationRules_AWorkflowIsSeenOnlyByItsPerson` (another member's delete of a workflow) now
  expect not-found instead of success; both still check the rule is left in place.

### BL-40 — `/doctor` does not say that linking is off when `LINK_CODE_SECRET` is not set (Low)
- **Found:** 2026-11-20, while building V4-32.
- **Evidence:** without `LINK_CODE_SECRET` the app starts, logs a warning once, and answers `/link` with
  "Linking accounts is not set up on this install". `/doctor` has no line for it.
- **Impact:** an owner who never reads the start-up log finds out only when someone tries to link.
- **Fix:** an Info line in the `/doctor` report ("linking accounts is off: set LINK_CODE_SECRET"), pinned by
  the `doctor` conformance transcript.

### BL-41 — An owner cannot yet remove a member's channel account (Medium)
- **Found:** 2026-11-20, while building V4-32.
- **Evidence:** ADR 0002 Decision 4 lets "an owner or any older account" remove an account that a stolen,
  unlocked phone added. V4-32 builds the older-account path (`/unlink N`, `db.RemoveAccount`). Owners do not
  exist until V4-35 (today's admins become owners there), and the dashboard has no member page that could
  do it.
- **Impact:** if the person has lost every older account, only an operator with database access can remove
  the thief's account until V4-35.
- **Fix:** with V4-35, an owner action (dashboard, and later chat) that removes a member's account under the
  same last-account rule, announced like every other change.
- **Done:** 2026-11-24 (`fix/access-memory-batch`), dashboard only; chat comes with V4-38. `memberAccounts` and
  `removeMemberAccount` (owners of the member's household only), `db.RemoveMemberAccount` (last account
  refused; an `account_remove` audit session in the same transaction), announced to the person's remaining
  accounts and the owners. `TestRemoveMemberAccount_OwnerRemovesAMembersSecondAccount` (end to end) and
  `TestRemoveMemberAccount_OwnerOfTheHouseholdOnly`.

### BL-42 — Wrong pairing codes do not count toward the install-wide limit on typed codes (Low)
- **Found:** 2026-11-20, while building V4-32.
- **Evidence:** V4-32 adds an install-wide limit on wrong link codes (`rate_limit:codes:global`, ADR 0002
  Decision 4). The 6-digit pairing codes keep only the per-sender lockout, so many senders can still share
  the guessing of a pairing code (ADR 0002 §1).
- **Impact:** a 6-digit code has 20 bits; spread across senders it is guessable within its 24 hours.
- **Fix:** count wrong pairing codes against the same limit, or end pairing codes as V4-36 plans (invites in
  `access_tokens`, keyed hash, owner approval).

### BL-43 — A `false` is stored as `true` where the model's default is `true` (High)
- **Found:** 2026-11-20, while building V4-35 (`TestSessionAuth_EachRoleSeesAndChangesOnlyWhatItMay`).
- **Evidence:** `logFinancialTransaction(isShared: false)` stores `is_shared = true`. GORM leaves a zero-valued
  field out of the INSERT when the field has a `default`, so the column's `DEFAULT true` applies. The same
  holds for every `bool` with `gorm:"default:true"`: `FinancialTransaction.IsShared`,
  `SharedSpace.CanView` and `CanEdit`, `AutomationRule.IsActive`, `RecurringBill.AutoRenew`,
  `ScheduleEvent.SyncToGoogle`, `TopicCalendarConfig.IncludeInMorningBriefing`. Chat writes are affected
  too (`internal/domains/finance/handlers.go` passes `is_shared` through).
- **Impact:** a transaction a person marked private is shown to the household as shared; a shared space
  set to "no view" or "no edit" keeps both; a rule created inactive runs. V4-35's dashboard rule
  (an unshared row is its person's own) is right, but the rows say "shared".
- **Fix:** write these fields explicitly (`Select("*")` or `*bool` with the default) and add a test per
  model that a `false` round-trips; decide whether rows written so far can be corrected (they cannot be
  told apart from rows meant as `true`).
- **Done:** 2026-11-24 (`fix/bl-43-stored-booleans`). The seven fields carry no tag default (GORM puts a tag default
  in place of a zero value on insert, `Select("*")` included); migration `00012_no_true_defaults.sql` drops the
  columns' `DEFAULT true` and changes no row. Every constructor already set the fields, so no caller changed.
  `TestModels_NoDefaultReplacesAChosenZeroValue` fails on any model field whose default differs from its zero value
  (two string defaults whose empty value means "not given" are listed). Round trips on create and on update from
  true to false, through the repositories (on the AutoMigrate and the migrated schema), the dashboard mutations
  and the chat tools. Rows written before are not rewritten: docs/RESTORE_AND_MIGRATE.md lists them for the owner
  to review. A transaction had no edit path to correct one; its person has one since BL-66.

### BL-44 — Chat shows other members' unshared transactions (Medium)
- **Found:** 2026-11-20, while building V4-35.
- **Evidence:** `FinanceRepo.ListTransactions` filters the household only; the chat finance tools list every
  member's transactions. Since V4-35 the dashboard shows an unshared transaction to its person only
  (ADR 0002 Decision 1: an owner does not see another adult's personal data).
- **Impact:** the two surfaces disagree about the same row; chat is the wider one.
- **Fix:** apply the same rule in the finance repository (`is_shared OR` the person's own), after BL-43, so
  the rows mean what they say.
- **Done:** 2026-11-24 (`fix/bl-43-stored-booleans`), the owner's decision: in chat a member sees their own unshared
  transactions and the household's shared ones. `FinanceRepo.transactions` applies it to every transaction read:
  the list, one transaction, the summary (its per-member totals included), the month's category totals (the prompt
  and the budget check) and the anomaly average. The dashboard query relies on it (its limit now counts visible
  rows only). The partner notifier announced every finance change, an unshared transaction's amount and merchant
  included; it no longer announces a row marked `"is_shared": false` (a transaction or a calendar event). Tests:
  `TestFinanceRepo_AMemberReadsSharedAndTheirOwnTransactions`, `TestChat_AMemberSeesSharedAndTheirOwnTransactions`
  (two members of one household), `TestNotifier_AnUnsharedRowIsNotAnnounced`. Household automation rules still
  see every member's rows: BL-67.

### BL-46 — Guests cannot be used yet (Medium)
- **Found:** 2026-11-20, V4-35 review.
- **Evidence:** ADR 0002 Decision 1: a guest chats "only [in] the topics shared with them by name" (a
  babysitter and the baby log). `shared_spaces` shares a topic with a member, but nothing limits what a
  guest's turn reads to those topics, and no item on the roadmap builds it (V4-11 is per-tool tiers).
- **What V4-35 does meanwhile:** fails closed. The guest row of the role table is empty, the engine refuses
  a guest's every message before commands and the model, and no pairing code makes a guest
  (`identity.ParseInviteRole`, `generatePairingCode`, the dashboard form). Migration C makes no guest.
- **Fix:** a roadmap item (the owner's to add) that scopes a guest's turn to the topics shared with them by
  name, in every module and in recall; then give the guest row `UseAssistant` and allow guest codes.

### BL-45 — The dashboard README documents a setting nothing reads (Low)
- **Found:** 2026-11-20, while building V4-35.
- **Evidence:** `dashboard/README.md` lists `NEXT_PUBLIC_GRAPHQL_API_KEY` ("client-side Bearer token for
  browser mutations"); no code reads it, and since V4-35 no page calls `/graphql` from the browser.
- **Fix:** remove the row.

### BL-47 — A household settings change blocks `/undo` (Medium)
- **Found:** 2026-11-20, while building V4-27.
- **Evidence:** `updateHousehold` publishes a `MutationEvent` (topic `household`), which the durability
  module records in `mutation_logs`. `/undo` and `undo_last_action` take the household's newest entry
  (`GetLastMutation`), and `households` is not undoable (`ResolveRollbackTable`), so after a settings change
  every undo fails until something undoable is written. V4-35 kept sign-ins off the log for this reason, and
  V4-27's `setPackEnabled` keeps pack switches off it.
- **Fix:** keep household settings off the mutation log (log them at INFO, as sign-ins and pack switches
  are), or make `GetLastMutation` skip entity types that cannot be undone.
- **Done:** 2026-11-24 (`fix/undo-batch`), both: `updateHousehold` publishes nothing and logs at INFO (household,
  person, names of the settings changed); `RollbackLast` reads `GetLastUndoableMutation`, which skips entity types
  not on the allowlist. Tests: `TestUndo_HouseholdSettingsChangeDoesNotBlockUndo`, `TestUndo_SkipsEntriesThatCannotBeUndone`.
  An action undo cannot roll back on an undoable entity type still blocked it: fixed by BL-68.

### BL-48 — Made-up members on the Shared Spaces page (Low)
- **Found:** 2026-11-20, while building V4-27.
- **Evidence:** `dashboard/src/app/shared/page.tsx` shows "● 2 Active Members Linked", "Dana (Admin)" and
  "Omer (Family)" for every household; the names are hard-coded.
- **Fix:** list the household's members from the `users` query (V4-35), or drop the card until V4-37.

### BL-49 — Partner notifications and event-triggered automation messages ignore quiet hours (Medium)
- **Found:** 2026-11-20, while building V4-05.
- **Evidence:** V4-05 holds what the scheduler sends (reminders, the briefing, cron rules and workflows). A
  partner notification (`shared.Notifier`, `CallContext.SendNotification`) and the `notify:` message of a
  rule triggered by an event (`automation.Runner.ExecuteAction` from the event bus) go out at once, at any
  hour. Neither has a schedule to work the hold out from, so holding them needs stored messages.
- **Fix:** hold them in a small outbox released when the person's quiet hours end, or decide they are
  exempt. Fits V4-33, which adds holding a message until a channel can take it.
- **Done:** 2026-11-24 (`fix/time-automation-batch`), the owner's decision: both respect the recipient's quiet hours. They are
  held in their `proactive_deliveries` row (pending until the hours end, reason `quiet_hours`;
  `proactive.Request.HoldInQuietHours`, `proactive.WithQuietHours`), not a second outbox, and the minute tick sends
  them. Urgent ones go at once: a rule whose condition says `"urgent": true` (`automation.RuleUrgent`), and a
  partner notification about a row marked urgent. Tests: `TestPartnerAndRuleNotifications_WaitForTheRecipientsQuietHours`,
  `TestDeliver_ANotificationInQuietHoursWaitsUntilTheyEnd`, `TestDeliver_AnUrgentNotificationIsNotHeld`.

### BL-50 — Reminder tools read a due time without an offset as UTC (Medium)
- **Found:** 2026-11-20, while building V4-05.
- **Evidence:** `schedule_reminder` parses `due_time` with `toolutil.ParseTime` (a naive time is UTC) and
  `create_shared_reminder` with `time.Parse` (the same), so "2026-11-20T23:00:00" from a person in
  Jerusalem fires at 01:00 their time. The confirmation prints the time with `time.Local`, the server's
  zone. The prompt gives the model the household's zone, so it often sends an offset, but nothing
  enforces it. The scheduler side (V4-05) uses the person's zone.
- **Fix:** parse with `toolutil.ParseTimeInLocation` in the household's zone (the engine's `location`) and
  confirm in that zone; this needs the install default zone in `CallContext`.
- **Done:** 2026-11-24 (`fix/time-automation-batch`), the owner's decision: both reminder tools read a due time without an
  offset in the person's zone (`CallContext.Loc()`: the household's, else `DEFAULT_TIMEZONE`, which the engine sets
  as `CallContext.Location`) and confirm it in that zone; a time with an offset is read as before. Tests:
  `TestScheduleReminder_ANaiveDueTimeIsThePersonsLocalTime`, `TestCreateSharedReminder_ANaiveDueTimeIsThePersonsLocalTime`,
  `TestScheduleReminder_FallsBackToTheInstallZone`, `TestCallContext_CarriesThePersonsZone`.

### BL-51 — Topic calendar colours never reach Google (Low)
- **Found:** 2026-11-20, while building V4-08.
- **Evidence:** `configure_topic_calendar` stores a `color_id`, and the create path sends it only when the
  client satisfies `schedule.ColoredCalendarClient`. Only the test fake does; `internal/calendar.Client`
  has no `CreateEventWithColor`, so every event is created in the calendar's default colour.
- **Fix:** add `CreateEventWithColor` to `calendar.Client` (set `Event.ColorId`) and a client test.

### BL-52 — Static tool list repeats module tools with old schemas (Low)
- **Found:** 2026-11-20, while building V4-08.
- **Evidence:** `llm.CalendarEventTool` declares 27 tools that modules also declare. `GenerateResponse`
  adds it when a call carries no scoped tool list, after the modules' declarations, so a module's schema
  wins by name; but its `update_calendar_event`/`delete_calendar_event` still take `id`, which the
  calendar module no longer reads.
- **Fix:** drop the static list and fall back to the registry's declarations, or keep only names no
  module declares.

### BL-53 — Calendar update and list read a time without an offset as UTC (Low)
- **Found:** 2026-11-20, while building V4-08.
- **Evidence:** `create_calendar_event` requires `timezone`; `update_calendar_event` and
  `list_calendar_events` take it optionally (list not at all) and read a time without an offset as UTC.
  The model usually sends an offset or the zone. Same cause as BL-50: `CallContext` carries no zone.
- **Fix:** with BL-50, give `CallContext` the person's zone and use it as the default.

### BL-54 — WhatsApp templates exist in English only (Low)
- **Found:** 2026-11-20, while building V4-41.
- **Evidence:** `twilio.Templates()` defines `mypa_reminder` and `mypa_briefing` with language `en`, and
  the settings hold one Content SID per template. A household that speaks Hebrew would get the reminder
  outside the 24-hour window in English. WhatsApp approves each language as its own template.
- **Fix:** a wording per supported language, a SID per template and language, and the sender
  choosing by the person's language when V4-33 sends templates.

### BL-55 — A legacy workflow run counts as the person writing (Medium)
- **Found:** 2026-11-20, while building V4-33.
- **Evidence:** `scheduler.executeWorkflowMessage` builds a `channel.Inbound` from the person's latest
  audit session and calls `ProcessMessage` without marking the turn as started by the assistant. So
  `RecordInbound` sets the account's `last_inbound_at` to now, although the person did not write; the
  delivery policy then predicts an open window that WhatsApp does not have, and `Release` sends the
  person's held messages into it. The workflow's own answer goes straight to that chat, not through the
  policy. A refusal still comes back as a delivery report and is re-routed, so nothing is lost, but
  the prediction is wrong and the message goes out late.
- **Fix:** run a workflow as a turn the assistant started, with its answer delivered as a
  notification through the policy (an exported engine method, as `SendDueBriefings` does for the
  briefing), and on the person's delivery account rather than their latest session.
- **Done:** 2026-11-24 (`fix/time-automation-batch`), the owner's decision: a `prompt:` rule (the legacy workflows are such
  rules since V4-07) runs as a turn the assistant started (`Engine.StartAssistantTurn`): no `RecordInbound`, no
  `Release`, on the person's delivery account, its answer a notification through the delivery policy. Test:
  `TestDelivery_APromptRuleRunIsATurnTheAssistantStarted`.

### BL-56 — Telegram's "blocked by the user" is an error, not a refusal (Low)
- **Found:** 2026-11-20, while building V4-33.
- **Evidence:** the Telegram adapter returns every Bot API failure as an error. A person who blocked
  the bot gets `403 Forbidden: bot was blocked by the user`, which the delivery policy treats as a
  failure to retry: it tries three times, a minute apart, before it moves to the person's other
  channel or holds the message.
- **Fix:** map Bot API 403 (blocked, user deactivated, chat not found) to `channel.NotReachable` in
  `telegram.Adapter.Send`, so the chain moves on at once.

### BL-57 — Undo restores a memory's old embedding (Medium)
- **Found:** 2026-11-20, while building V4-03.
- **Evidence:** `RollbackSnapshot` (`internal/db/durability_repo.go`) writes a mutation's `BeforeJSON` back into
  the row, for an undone update and for a restored delete. For a `TopicMemory` that snapshot holds the whole
  `embedding` (BL-25). `mypa embeddings reembed` replaces the vectors in `topic_memories` but not the copies in
  `mutation_logs`.
- **Impact:** after a re-embed into another model of the same size, a memory restored with `/undo` or from the
  dashboard comes back with a vector of the old model. It is not a zero vector, so `reembed --missing` does not
  find it, and recall ranks it by a meaningless distance. After a change of size the restore fails outright
  (the old vector does not fit the column). Nothing is affected until an install changes `EMBEDDING_MODEL`.
- **Fix:** together with BL-25: keep the embedding out of the snapshot, and give a restored memory a zero vector
  (or embed it again on restore). Test: after a re-embed into another model, a restored memory holds no vector
  of the old model.
- **Direction (owner, 2026-11-20):** one change with BL-25, after V4-07 and before the V4.0 release, not part of any stage-4 item. An undo snapshot no
  longer carries the embedding, and a memory restored by undo is marked as needing one, so the re-embed path from
  V4-03 (`mypa embeddings reembed --missing`) fills it in the current model.
- **Done:** 2026-11-24 (`fix/undo-batch`, with BL-25). `RollbackSnapshot` drops any `embedding` from a memory
  snapshot and gives a restored memory a zero vector of the stored size (an undone update only when it changes the
  text); an old snapshot that still holds a vector is not rewritten and its vector is ignored. The undo's own log
  entry and its reply leave an old vector out. `TestUndo_RestoredMemoryIsEmbeddedInTheCurrentModel`: after a
  re-embed into another model, a restored memory holds no vector of the old one, `reembed --missing` finds it and it
  is found by meaning again, from a new snapshot and from an old one.

## Operations (owner)

| ID | Task | Status |
| :--- | :--- | :--- |
| OP-1 | Move `C:\Users\nivik\.mypa\backup-age.key` into a password manager, then delete the local copy (needed to restore encrypted backups — set `BACKUP_AGE_IDENTITY_FILE` only while restoring). Delete `C:\Users\nivik\.mypa\env-backup-*` once the new `.env` has proven itself, and `C:\Users\nivik\.mypa\db-backup-pre-op9-*.dump` (unencrypted pre-purge dump, owner-only ACL) once the purge has proven itself. Gates V4-43: the backup key has to travel with a restore. | **done 2026-11-18** — owner: key copied to the password manager and the local copies deleted |
| OP-2 | Second Google user (platform id ending `0357`) runs `/connect`; then delete both `legacy-token:*` Redis keys (tokens encrypted under the old public fallback key). The owner's own key can already go. Separate from the re-encryption pass in BL-16 and V4-07. | partly done — owner reconnected, owner's legacy key deleted 2026-11-16; second user pending |
| OP-3 | Old `pgadmin` container (pre-P3-5 image, unpinned, old credentials) is still running; stop/remove it. Start the new one with `docker compose --profile tools up -d pgadmin` when needed. | done 2026-11-16 (stopped and removed) |
| OP-4 | Push `dev`; watch the first CI run. A `-race` timing flake on runners → apply the `timingSlack` pattern (D5-3). | pushed 2026-11-16; first run: race tests, lint, dashboard, compose build green (no `-race` flakes); govulncheck red (go1.26.4 stdlib advisories) → fixed by DP-3; **done** — run 43824019172 on `9284c49` all 5 jobs green |
| OP-5 | Log in to the dashboard once (`JWT_SECRET` was rotated, old sessions are invalid). Replaced by per-person sign-in when V4-35 ships. | open |
| OP-6 | Choose an off-machine backup target (backups are encrypted but stay on this disk). | **decided 2026-11-18**: backups stay local for now; nothing in V4.0 needs an off-machine copy. Accepted risk: losing this machine's disk loses the data and its backups together. Follow-up in OP-11 |
| OP-7 | After a few stable weeks: migration to drop the legacy `memories` table. Carried out as part of V4-07. | **done 2026-11-24** — checked read-only on the live database: `memories` holds 1 row, created 2026-11-05 (before P2-4), so nothing has written to it since; every row has its copy in `topic_memories` (0 missing); the 2026-11-18 backup includes it. The drop itself is part (1) of V4-07 |
| OP-9 | Purge the test fixtures BL-11 left in the live DB: 154 fixture users plus 722 audit sessions, 167 automation rules, 268 mutation logs, 243 reminders, 176 topic memories, 89 maintenance items, 2 preferences, 1 baby log — each predicate names a fixture identity (`AttackerFamily`/`VictimFamily` users, `*_IDOR`, `DurabilityTestFamily`, `durability_test_*`, `FamilyAlphaSnapshots`, `victim-usr`/`attacker-usr`). Dry run (rolled back) left exactly the MyFamily data. Take a backup first. | **done 2026-11-17** — owner approved; `pg_dump -Fc` backup first (see OP-1), purge in one transaction with the same counts as the dry run; afterwards every tenant table holds only `MyFamily` rows and every audit session belongs to a real user; dashboard GraphQL view unchanged |
| OP-8 | WhatsApp runs on the Twilio **sandbox**: membership expires and replies then fail with 63015 (hit 2026-11-16). Short term: re-send `join <word>` to the sandbox number. Long term: register a WhatsApp sender in Twilio. Both modes stay supported (DE-8, V4-41). | open |
| OP-10 | Cut a baseline release from `dev` before any V4 work starts. `dev` is 57 commits ahead of `master` and of `v3.3.2` (remediation, backlog fixes, docs). Merge, tag it by the rule in `AGENTS.md` §5, and take a backup of the running system on that release. This is the rollback point and the baseline that V4-43 restores from. | **done 2026-11-18**: tag `v3.4.0` (merge `178abbf` on `master`); backup `backup_2026-11-18_21-52-46.dump.age` taken on that release |
| OP-11 | Off-machine copy of backups (deferred from OP-6). Revisit when the first of these happens: MyPA is deployed to a server (V4-40), the conversation cold tier is built (V4-12), or a household other than the owner's relies on the install. Before anything leaves the machine: delete the three unencrypted dumps from before 2026-11-17 in `backups/`, and encrypt the conversation archives (the `audit_archives` volume), which are stored unencrypted today. Cheap step in the meantime: keep a copy of `backups/` on a second physical disk. | open (deferred) |
| OP-12 | Dry run of the V4 migrations on real data. **Steps: [`TEST_INSTALL.md`](./TEST_INSTALL.md), steps 1 to 10**: the newest nightly backup of the live install is restored into a test install beside it (a separate clone, compose project `mypa-test`, its own Telegram bot and ports, no WhatsApp; the app is not started before step 14, and never with the live bot's token, which would take the live bot away from the family). Check that the import ends with no line starting with `!`, and review the households, people, memberships and the count of rows without a person. Needs the private backup key and a copy of `.env`, so only the owner can do it. When: after stage 3 of V4.0 and before stage 4; at the latest before the V4.0 release. It does not replace the V4-43 release checks. On the same copy, run the three `embeddings` commands in [`EMBEDDINGS.md`](./EMBEDDINGS.md) ("Checking the path on a copy of real data"), then mark V4-03 Done (DE-11). | open (deferred by the owner on 2026-11-20) |

## Decisions

None is pending. Decisions for V4 (DE-3 onward) are recorded in [`ROADMAP.md`](./ROADMAP.md) §7; this table keeps the two that were raised here.

| ID | Decision | Where |
| :--- | :--- | :--- |
| DE-1 | Accept/reject the service-topology ADR | **decided 2026-11-18** (accepted: option 1c, and the queue moves to Postgres by V4.2; see `docs/adr/0001-service-topology.md` and `docs/ROADMAP.md` §7) |
| DE-2 | Users with an empty family group: GraphQL resolves them to a shared tenant `"default"` (two such users would see each other's data), while the orchestrator falls back to the user's own ID. Align on per-user isolation, or refuse users without a group? Changes visibility of existing rows. | **decided 2026-11-16** (`fix/de-2`, DP-14): per-user isolation. Worse than logged — `ActivePairingCodes`/`MutationLogs` treated `"default"` as *no filter*, so such a caller saw every family's codes and logs. Live data: 0 users without a group, 0 rows in `"default"` → no visible change |
