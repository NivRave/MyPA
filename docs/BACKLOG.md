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
| BL-16 | Medium | No re-encryption path for old-format (unsalted) ciphertexts, so the legacy decryption fallback cannot be removed safely | open — tracked as V4-07 |
| BL-17 | Low | WhatsApp message splitter cuts by bytes and can split a character in unbroken non-Latin text | open |
| BL-18 | Low | A document sent on Telegram gets no answer at all | open — the channel layer (V4-31) is the natural place |
| BL-19 | Low | Reminder and transcription labels render as italics, probably meant as bold | open |
| BL-20 | Low | `/doctor` shows the same host twice when the webhook addresses differ only in path | open |
| BL-21 | Medium | `/restore` in chat restores over the live schema and does not migrate, which breaks once V4 adds a migration | open: decide before the first V4 migration |
| BL-22 | Low | Restore tool: the rollback test cannot fail yet, and the piped dump check runs only against a fake | open |
| BL-23 | Low | A failing archive step makes the nightly backup discard its database dump | open |
| BL-24 | Medium | Memories saved with `save_topic_memory` before V4-06 have no embedding, so they are not recalled by meaning | open: belongs with the re-embedding path of V4-03 |
| BL-25 | Low | Undo snapshots of saved memories carry the full 3,072-number embedding | open: decide with BL-24 and V4-03 |
| BL-26 | Medium | `save_topic_memory` stores `scope` as given, so a value such as `Family` is saved but never found | open |
| BL-27 | High | The real-phone-number guard and the `.gitignore` guard are skipped in every full test run, including CI | done 2026-11-19 (`chore/bl-27-guards`) |
| BL-28 | Low | The one-default-model guard misses a model alias with no version digit (`gemini-flash-latest`) and does not search `.mjs`, `.sh`, `.ps1` or `.sql` files | open |
| BL-29 | Medium | The LLM client sends `Temperature: 0.2`; Gemini 3.6 Flash ignores sampling parameters and later model generations are to reject them with HTTP 400 | open: resolve before the default moves past 3.6, as part of V4-03 |
| BL-30 | Low | Tests in `cmd/orchestrator` and `internal/crypto` change environment variables and do not put them back exactly | open |

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
  and reports how many old-format values remain; delete the fallback only when the report shows zero (V4-07).

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
- **Fix:** make `/restore` use the same path as `transfer import --replace` (reset, restore, migrate), or, as
  that cannot be done safely inside the running app, retire `/restore` in favour of the tool and have the
  command explain how to run it. Decide before the first V4 schema migration ships.

### BL-22 — Two restore-tool paths are tested less directly than the rest (Low)
- **Found:** 2026-11-19, in the review of V4-43 (non-blocking, after the item passed).
- **Evidence:** (1) `TestTransfer_RollbackRestoresWithoutMigrating` (`cmd/orchestrator/transfer_test.go`)
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
- **Fix:** resolve before the default moves past 3.6, as part of the provider work (V4-03): send no sampling
  parameters, or send them only to models that accept them, decided per provider and model. Test: the
  request built for the default model carries no `Temperature`.

### BL-30 — Other tests change the process environment and do not restore it exactly (Low)
- **Found:** 2026-11-19, in the search of every `_test.go` file done for BL-27.
- **Evidence:** `cmd/orchestrator/playground_security_test.go:18-19` and `:44-45` set `ENV` and
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

## Operations (owner)

| ID | Task | Status |
| :--- | :--- | :--- |
| OP-1 | Move `C:\Users\nivik\.mypa\backup-age.key` into a password manager, then delete the local copy (needed to restore encrypted backups — set `BACKUP_AGE_IDENTITY_FILE` only while restoring). Delete `C:\Users\nivik\.mypa\env-backup-*` once the new `.env` has proven itself, and `C:\Users\nivik\.mypa\db-backup-pre-op9-*.dump` (unencrypted pre-purge dump, owner-only ACL) once the purge has proven itself. Gates V4-43: the backup key has to travel with a restore. | **done 2026-11-18** — owner: key copied to the password manager and the local copies deleted |
| OP-2 | Second Google user (platform id ending `0357`) runs `/connect`; then delete both `legacy-token:*` Redis keys (tokens encrypted under the old public fallback key). The owner's own key can already go. Separate from the re-encryption pass in BL-16 and V4-07. | partly done — owner reconnected, owner's legacy key deleted 2026-11-16; second user pending |
| OP-3 | Old `pgadmin` container (pre-P3-5 image, unpinned, old credentials) is still running; stop/remove it. Start the new one with `docker compose --profile tools up -d pgadmin` when needed. | done 2026-11-16 (stopped and removed) |
| OP-4 | Push `dev`; watch the first CI run. A `-race` timing flake on runners → apply the `timingSlack` pattern (D5-3). | pushed 2026-11-16; first run: race tests, lint, dashboard, compose build green (no `-race` flakes); govulncheck red (go1.26.4 stdlib advisories) → fixed by DP-3; **done** — run 43824019172 on `9284c49` all 5 jobs green |
| OP-5 | Log in to the dashboard once (`JWT_SECRET` was rotated, old sessions are invalid). Replaced by per-person sign-in when V4-35 ships. | open |
| OP-6 | Choose an off-machine backup target (backups are encrypted but stay on this disk). | **decided 2026-11-18**: backups stay local for now; nothing in V4.0 needs an off-machine copy. Accepted risk: losing this machine's disk loses the data and its backups together. Follow-up in OP-11 |
| OP-7 | After a few stable weeks: migration to drop the legacy `memories` table. Carried out as part of V4-07. | open |
| OP-9 | Purge the test fixtures BL-11 left in the live DB: 154 fixture users plus 722 audit sessions, 167 automation rules, 268 mutation logs, 243 reminders, 176 topic memories, 89 maintenance items, 2 preferences, 1 baby log — each predicate names a fixture identity (`AttackerFamily`/`VictimFamily` users, `*_IDOR`, `DurabilityTestFamily`, `durability_test_*`, `FamilyAlphaSnapshots`, `victim-usr`/`attacker-usr`). Dry run (rolled back) left exactly the MyFamily data. Take a backup first. | **done 2026-11-17** — owner approved; `pg_dump -Fc` backup first (see OP-1), purge in one transaction with the same counts as the dry run; afterwards every tenant table holds only `MyFamily` rows and every audit session belongs to a real user; dashboard GraphQL view unchanged |
| OP-8 | WhatsApp runs on the Twilio **sandbox**: membership expires and replies then fail with 63015 (hit 2026-11-16). Short term: re-send `join <word>` to the sandbox number. Long term: register a WhatsApp sender in Twilio. Both modes stay supported (DE-8, V4-41). | open |
| OP-10 | Cut a baseline release from `dev` before any V4 work starts. `dev` is 57 commits ahead of `master` and of `v3.3.2` (remediation, backlog fixes, docs). Merge, tag it by the rule in `AGENTS.md` §5, and take a backup of the running system on that release. This is the rollback point and the baseline that V4-43 restores from. | **done 2026-11-18**: tag `v3.4.0` (merge `178abbf` on `master`); backup `backup_2026-11-18_21-52-46.dump.age` taken on that release |
| OP-11 | Off-machine copy of backups (deferred from OP-6). Revisit when the first of these happens: MyPA is deployed to a server (V4-40), the conversation cold tier is built (V4-12), or a household other than the owner's relies on the install. Before anything leaves the machine: delete the three unencrypted dumps from before 2026-11-17 in `backups/`, and encrypt the conversation archives (the `audit_archives` volume), which are stored unencrypted today. Cheap step in the meantime: keep a copy of `backups/` on a second physical disk. | open (deferred) |

## Decisions

None is pending. Decisions for V4 (DE-3 onward) are recorded in [`ROADMAP.md`](./ROADMAP.md) §7; this table keeps the two that were raised here.

| ID | Decision | Where |
| :--- | :--- | :--- |
| DE-1 | Accept/reject the service-topology ADR | **decided 2026-11-18** (accepted: option 1c, and the queue moves to Postgres by V4.2; see `docs/adr/0001-service-topology.md` and `docs/ROADMAP.md` §7) |
| DE-2 | Users with an empty family group: GraphQL resolves them to a shared tenant `"default"` (two such users would see each other's data), while the orchestrator falls back to the user's own ID. Align on per-user isolation, or refuse users without a group? Changes visibility of existing rows. | **decided 2026-11-16** (`fix/de-2`, DP-14): per-user isolation. Worse than logged — `ActivePairingCodes`/`MutationLogs` treated `"default"` as *no filter*, so such a caller saw every family's codes and logs. Live data: 0 users without a group, 0 rows in `"default"` → no visible change |
