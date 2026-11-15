# Remediation Decision Log

Design decisions made while executing [`REMEDIATION_PLAN.md`](./REMEDIATION_PLAN.md). Each entry lists the options considered, the choice, why it won (including the counter-arguments it had to survive), and any follow-ups. Owner: please review; anything marked **Revisit?** is a call you may want to overturn.

Format per decision: **Options** → **Chosen** → **Why / challenge** → **Follow-up**.

---

## P1-1 — Webhooks fail closed + setWebhook

### D1-1.1 What "Twilio enabled" means for the startup check
- **Options:** (a) always require `TWILIO_AUTH_TOKEN` (plan wording); (b) require it only when `TWILIO_ACCOUNT_SID` is set; (c) require it only if the `/webhook/twilio` route is reachable (i.e. never conditionally).
- **Chosen:** (b), plus the handler rejects every request (403) when the token is empty.
- **Why / challenge:** (a) bricks Telegram-only installs for no security gain, because the handler already fails closed. Challenge: "an attacker can still hit `/webhook/twilio` on a Telegram-only install" — yes, and gets 403; nothing is published. Same rule later reused in P1-2 for both gateway and orchestrator.
- **Owner-approved** on 2026-11-15.

### D1-1.2 Fail-closed at two layers (startup + handler)
- **Options:** startup-only validation; handler-only check; both.
- **Chosen:** both.
- **Why:** startup validation gives a loud, early failure; the handler check protects every construction path (tests, future refactors, a forgotten `Validate` call). Note that `subtle.ConstantTimeCompare("", "") == 1` and an empty-key HMAC is forgeable, so "always verify" alone would have *accepted* forged requests — the explicit empty-secret check is the real fix.

### D1-1.3 setWebhook retry policy
- **Options:** (a) call once, log failure; (b) retry forever; (c) bounded exponential backoff; (d) crash on failure.
- **Chosen:** (c) 8 attempts, 2 s doubling, capped at 2 min (≈ 6 min total), background goroutine cancelled on shutdown.
- **Why / challenge:** (d) violates the plan ("don't crash on network failure") and would crash-loop during a Telegram outage. (b) hides permanent misconfiguration (bad token → 401 forever). (c) survives transient outages and gives up loudly on permanent errors. Challenge: "a permanent 4xx shouldn't be retried" — true, but distinguishing adds code for a ~6-minute worst case; accepted.
- **Follow-up:** none.

### D1-1.4 Telegram secret format validation at startup
- **Chosen:** enforce Telegram's documented `^[A-Za-z0-9_-]{1,256}$` in config validation. Otherwise a secret with e.g. `+`/`=` (base64) passes startup but `setWebhook` fails with 400 and *every* update is rejected.

### D1-1.5 Bot-token leakage in errors
- **Finding:** `net/http` wraps failures in `*url.Error` whose message includes the full URL — and Telegram puts the bot token in the path.
- **Chosen:** `SetWebhook` strips the URL (`redactURLError`). Pre-existing `SendMessage`/`GetFile`/`DownloadFile` have the same leak — fixed under **P3-3** (logging hygiene) to keep P1-1 scoped.

---

## P1-2 — Central config validation

### D1-2.1 Where validation lives
- **Options:** (a) ad-hoc checks in each `cmd/*/main.go` (status quo for proxy); (b) struct tags + a validator library (`go-playground/validator`, `envconfig required:"true"`); (c) one `Config.Validate(service)` method with a per-service required-key table plus coded conditional rules.
- **Chosen:** (c).
- **Why / challenge:** tags can't express "service X needs key Y" (the same `Config` struct is shared by 4 services) or conditional rules (Telegram secret only if Telegram configured; JWT ≠ encryption key), so (b) would end up half tags, half code, plus a new dependency. (a) is what let gaps appear. (c) is a single reviewed place, testable as a table, and the required-key table doubles as documentation (`RequiredEnvKeys()` drives a test that `.env.example` lists every key).

### D1-2.2 Report all errors at once
- **Chosen:** `errors.Join` of every problem, wrapped with the service name. Fix-restart-fix loops on a fresh install are the main pain this avoids.

### D1-2.3 Placeholder detection
- **Options:** only check non-empty; also reject `.env.example` placeholders.
- **Chosen:** reject values starting with `your-` or containing `<YOUR_`.
- **Why / challenge:** copying `.env.example` → `.env` and forgetting one key would otherwise start the orchestrator with `ENCRYPTION_KEY=your-32-byte-encryption-key` — a publicly known key. Challenge: false positive on a real secret starting with `your-` — negligible for generated secrets.

### D1-2.4 Minimum `ENCRYPTION_KEY` length — **not enforced**
- **Options:** require ≥ 32 bytes; warn; do nothing.
- **Chosen:** do nothing in code; document "generate with `openssl rand -hex 32`".
- **Why / challenge:** best practice says enforce key strength. But a running deployment may already encrypt OAuth tokens with a shorter key (Phase 0 note); enforcing would force rotation = every user re-runs `/connect`. HKDF stretches whatever it gets, so a short-but-random key is weak only by entropy, which a length check can't measure anyway.
- **Revisit?** If you confirm your current key is ≥ 32 random chars, a hard check is cheap to add.

### D1-2.5 Test-binary detection for the dev encryption key
- **Options:** (a) `flag.Lookup("test.v") != nil` only (plan); (b) remove the fallback entirely and set `ENCRYPTION_KEY` in every package's `TestMain`; (c) a build tag (`-tags devkey`).
- **Chosen:** (a), behind an overridable `testBinaryDetector` var so tests can simulate production.
- **Why / challenge:** (b) is the purist answer (no hard-coded key in prod binaries at all) but touches dozens of test packages that rely on the fallback implicitly — high churn for little extra safety once env vars can't trigger it. (c) is easy to forget in CI. (a) can't be triggered by deployment config. Follow-up candidate if you want zero hard-coded keys in the binary.

### D1-2.6 `DeriveKey` on missing key
- **Options:** keep returning the dev-key hash (silent fallback — status quo); panic; change signature to `([]byte, error)`; return `nil`.
- **Chosen:** return `nil`.
- **Why:** it's exported but only used in tests; a nil key makes any `aes.NewCipher` caller fail loudly. Changing the signature is cleaner but is API churn for an unused function. Silent fallback was the worst option — a production "key" equal to a value in the source.

### D1-2.7 `JWT_SECRET` → `ENCRYPTION_KEY` fallback
- **Chosen:** leave the fallback code for **P3-2** (where the plan lists it) but `Validate("orchestrator")` now requires `JWT_SECRET` and rejects it being equal to `ENCRYPTION_KEY`, so the fallback is unreachable at runtime.

### D1-2.8 Startup panic → clean exit
- The subprocess test `TestSEC08_ProductionStartupFails_WhenMissingKey` now asserts a non-zero exit **without** `panic:` in output, and also runs with `ENV=test` to prove the env var no longer unlocks the dev key.

---

## P1-3 — Per-user prompt cache keys

### D1-3.1 Cache granularity
- **Options:** (a) key every section per user `ctx_prompt:<fg>:<user>:<mod>` (plan default); (b) let each module declare a scope (`family` vs `user`) and keep family-shared sections family-keyed; (c) never cache modules that render personal data.
- **Chosen:** (a).
- **Why / challenge:** (b) gives a better hit rate, but a single mislabelled module re-opens the leak, and the "memory"/"automation"/"schedule" sections mix family and personal content in one string anyway. For a family of 2–6 people the extra misses cost a few ms of DB work per member per 10 min. (c) throws away the cache for the most expensive sections. Safe-by-default wins; (b) can be layered on later as an explicit opt-in.

### D1-3.2 Invalidation strategy
- **Options:** (a) `SCAN MATCH ctx_prompt:<scope>:*` then filter by module and `DEL` (plan); (b) `KEYS` (blocking — rejected outright); (c) per-family generation counter in the key (`ctx_prompt:<fg>:v<N>:…`, `INCR` to invalidate, old keys age out via TTL); (d) a Redis SET index of keys per family.
- **Chosen:** (a), one SCAN per scope (not per module), `COUNT 500`.
- **Why / challenge:** (c) is O(1) and also closes the miss-then-stale-write race, but adds a round trip to every prompt build and more moving parts. (d) needs index maintenance on every write and can drift. SCAN cost scales with Redis key count, which for this app is tiny (hundreds of keys); mutations are infrequent. **Known, pre-existing race:** a prompt build that misses, then a mutation invalidates, then the build writes its (now stale) section — bounded by the 10-min TTL. (c) is the upgrade path if that ever matters.

### D1-3.3 Key-segment encoding and glob escaping
- **Finding:** family-group names are free-form. With a 4-segment key, family `a:b` + user `c` and family `a` + user `b:c` collide on `ctx_prompt:a:b:c:mod` — a cross-tenant leak path. A family named `*` (or containing `?[]`) would also turn the invalidation SCAN pattern into a wildcard over other families.
- **Chosen:** percent-encode `%`→`%25` and `:`→`%3A` in every segment (`state.PromptCacheKey`), and glob-escape the scope segment before using it in `SCAN MATCH`. Both covered by tests.
- **Alternative rejected:** hashing segments (opaque keys, harder to debug in `redis-cli`).

### D1-3.4 Unknown identity
- **Chosen:** if `CallContext.ActorID()` is empty, do not read or write the cache (previously such calls were cached under the family key). Rare path (system-initiated prompts); correctness over hit rate.

### D1-3.5 Scope of invalidation for users without a family group
- `CallContext.TenantFamilyGroup()` falls back to the user ID, so such users are cached as `ctx_prompt:<uid>:<uid>:<mod>`; invalidating scope `event.ActorUserID` covers them. Kept both scopes (family + actor) from the old design.

---

## P1-4 — Inbound idempotency + no retries after side effects

### D1-4.1 Delivery semantics: at-most-once for side-effecting turns
- **Options:** (a) at-least-once (status quo: any error re-runs the turn); (b) at-most-once via a dedup claim; (c) effectively-once via per-tool idempotency keys (each tool call carries `<messageID>:<callIndex>` and every external API/DB write dedups on it).
- **Chosen:** (b) now; (c) noted as the long-term ideal.
- **Why / challenge:** (a) is what created duplicate calendar events/emails. (c) is best practice for payment-style systems but needs every integration (Google Calendar, Gmail, Tasks, Twilio, internal repos) to accept an idempotency key — a cross-cutting change far larger than this item, and Google APIs only partly support it. (b) is a small, central change. Its cost: if the orchestrator **crashes mid-turn**, the redelivery is dropped and the user must resend. For an assistant that takes real-world actions, "you may need to repeat yourself" beats "it booked twice".
- **Revisit?** If lost messages after crashes ever matter, add a `processing`→`done` two-phase claim with a short processing TTL plus a recovery notice to the user.

### D1-4.2 When to release the claim
- **Chosen:** release (`DEL`) on retryable failure; keep on success and on non-retryable failure. A naive `SET NX` without release would silently drop the broker's own retries — breaking "LLM failed before tools → requeued".
- Release uses `context.WithoutCancel` + 2 s timeout because the turn context may already be expired (5-min turn timeout is a common failure cause).

### D1-4.3 What counts as a side effect
- **Counted:** registry tool executed (even if it returned an error — it may have partially applied), `/backup`, `/restore`, `/undo`, pairing-code redemption, recorded failed pairing attempt (a retry would double-count toward lockout), final reply delivered.
- **Not counted:** progress notices ("Transcribed: …", "Starting backup…" is followed by the counted backup itself), staging a pending CONFIRM action (idempotent overwrite), `get_current_time`.
- **Why / challenge:** counting *every* outgoing message is simpler, but then a Gemini 503 after the transcription notice becomes a lost message instead of a retry. Repeating a progress notice is harmless; losing the answer is not.

### D1-4.4 Non-retryable → ack vs dead-letter
- **Options:** ack (plan); reject to DLQ for inspection.
- **Chosen:** ack.
- **Why / challenge:** DLQ messages tend to be replayed by an operator, which would re-run the side effects this item exists to prevent. The failure is still recorded: error log + the audit session (`status=failed`).

### D1-4.5 Dedup key scope
- `inbound:<source>:<chatID or userID>:<messageID>`: Telegram `message_id` is unique only per chat; Twilio `MessageSid` is global (user scope is fine). Segments reuse the prompt-cache percent-encoding. Messages without an ID (proactive broadcasts) skip dedup with a warning. Scheduler-generated IDs (`wf-<id>-<unix>`) are unique per firing, so dedup is harmless there.

### D1-4.6 Redis unavailable during claim
- **Chosen:** fail open (process without dedup) with a warning. The turn needs Redis for chat history anyway, so a real Redis outage fails the turn retryably; failing closed would add a second failure mode for no gain.

### D1-4.7 User-facing error notice
- Sent only when `errors.Is(err, ErrNonRetryable) || info.IsFinal()`, via new `Consumer.ConsumeWithInfo` (attempt number from the `x-retries` header). `Consume` kept as a compatibility wrapper.

---

## P1-5 — `/restore` confirmation + pre-restore backup

### D1-5.1 Reuse the pending-action slot vs a dedicated key
- **Options:** (a) reuse `pending_meta_tool:<userID>` with `ToolName="restore"` (plan); (b) a dedicated `pending_admin:<userID>` key.
- **Chosen:** (a).
- **Why / challenge:** one slot means staging a restore overwrites an unconfirmed meta-tool action (and vice versa). That's acceptable — both are short-lived, user-initiated, and the user sees which action is pending. A second key doubles the confirmation surface for no real benefit. `restore` is not an LLM tool name, so the model cannot stage it through the meta-tool gate.

### D1-5.2 Restore the staged file, not "latest at confirm time"
- The safety backup taken on confirmation becomes the newest file on disk; resolving "latest" at confirm time would restore the *current* state (a silent no-op that looks like success). The exact path is staged and validated (`.dump`, directly inside the backup dir, still exists).

### D1-5.3 Explicit `CONFIRM RESTORE` only
- The existing gate executes whatever is pending on a bare `CONFIRM`. For destructive actions (`isDestructiveAction`) the action name is mandatory; a bare `CONFIRM` re-stages with the *remaining* TTL (not a fresh 5 min) and explains.

### D1-5.4 Safety backup failure aborts the restore
- **Options:** abort; warn and continue.
- **Chosen:** abort. `pg_restore -c` drops objects first; without a fresh dump there is no way back from a bad backup file. Safety backup uses retention `3+1` so it can never evict the target file, even if a nightly backup landed between staging and confirmation.

### D1-5.5 Re-authorize at confirmation
- Role is re-checked on `CONFIRM RESTORE` (the staged record lives in Redis and the role may have changed within the TTL).

### D1-5.6 "Same for any future destructive admin commands"
- `isDestructiveAction` + the staged-action pattern in `restore.go` is the template. `/backup` is not destructive (additive, retention-bounded) and stays immediate.

---

## P2-1 — Make all tools reachable

### D2-1.1 New domains vs promoting to core
- **Options:** (a) promote the 4 tools to Tier-1 core (always sent); (b) new `DomainMemory` + `DomainHistory` keyword groups; (c) per-tool domain mapping instead of per-module.
- **Chosen:** (b).
- **Why / challenge:** (a) is simplest and guarantees availability, but costs ~4 declarations (~500 tokens) on *every* turn for tools used rarely, against the design goal of Tier-2 scoping. (c) would be more precise but changes the mapping model for all modules; module-level mapping works here because each module's core tools are already handled by `CoreToolNames`, so mapping `memory`/`durability` only adds their non-core tools.
- **Risk accepted:** keyword routing misses phrasings not in the lists. P2-2 (sticky domains) reduces this for follow-ups; the invariant test guarantees no tool is *structurally* unreachable.

### D2-1.2 Keyword choices
- Broad words like "changed"/"export" may add the history tools to unrelated turns (false positive = 2 extra declarations, harmless). Hebrew "תזכור" (remember) is included while "תזכורת" (reminder) is not matched thanks to the existing word-end guard.

### D2-1.3 Invariant test location
- An external test package (`orchestrator_test`) so it can import every domain module (they import `orchestrator`, so an internal test would be an import cycle). The module list mirrors `cmd/orchestrator/main.go`; adding a module there without adding it to the test is the remaining gap — noted for P5-5 (generate the catalogue from one registration list).

---

## P2-2 — Sticky domains for follow-ups

### D2-2.1 What to carry forward
- **Options:** (a) carry the full selection (matched ∪ inherited sticky) — domains accumulate for as long as the user keeps talking; (b) carry only this turn's *used* domains: text-matched ∪ domains of tools actually executed; (c) keep a decaying score per domain over N turns.
- **Chosen:** (b).
- **Why / challenge:** (a) slowly re-creates "send every tool", defeating Tier-2 scoping. (c) is more nuanced but adds state and tuning for little gain in a chat where follow-ups are almost always about the immediately preceding turn. (b) gives one-turn carry-over and decays automatically; a domain in active use keeps renewing itself via called tools.

### D2-2.2 Where tool calls are recorded
- On the per-turn `turnEffects` (from P1-4), appended only when the registry actually handled the tool. Core tools are ignored by `DomainsForTools` (they're always offered anyway).

### D2-2.3 Failure handling
- Load/save errors are logged and ignored — stickiness is an optimization; it must never fail a turn.
- Saved before the reply is sent so it reflects the turn even if delivery fails.

### D2-2.4 TTL
- 15 minutes (plan). A follow-up after a longer pause usually restates context, and keyword matching takes over again.

### D2-2.5 Follow-up fix to P1-4 (separate commit)
- While wiring this, found that `Registry.Execute` reports `get_current_time` as handled, so P1-4 counted it as a side effect — contradicting D1-4.3. Fixed by excluding read-only built-ins from `markSideEffect`.

---

## P2-3 — Use the HNSW index (and two bigger bugs found on the way)

### Finding 1 (more severe than the plan): vector search had no ORDER BY at all
- GORM v1.31.2 `Order()` only handles `clause.OrderBy`, `clause.OrderByColumn` and `string`; a `gorm.Expr` falls through its type switch and is **silently dropped**. All three similarity queries (`SearchSimilarMemories`, its legacy fallback, `Client.SearchMemories`) therefore returned the first N matching rows in physical order — the per-message memory retrieval injected *arbitrary* memories, not relevant ones. A test inserting the nearest row last reproduced it (returned the 3 *least* similar rows).
- **Fix:** `cosineOrder()` returns `clause.OrderBy{Expression: clause.Expr{…}}`. Documented in SYSTEM.md as a rule ("never pass gorm.Expr to Order").
- **Expect a behaviour change after deploy:** retrieved memories will now actually be the relevant ones.

### Finding 2: using the index naively would silently lose a user's memories
- With HNSW, Postgres takes the `ef_search` (40) nearest rows *table-wide* and applies the tenant `WHERE` afterwards. Reproduced in testcontainers (pgvector 0.8.6) with realistic dense vectors: another family owning the 120 nearest rows → the user got **0 of 3** memories.
- **Options:** (a) iterative scans (`hnsw.iterative_scan`, pgvector ≥ 0.8); (b) raise `hnsw.ef_search` (only shifts the cliff); (c) per-tenant partial indexes (unbounded index count); (d) keep sequential scan (exact; fine at today's size, O(n) later).
- **Chosen:** (a) with `strict_order` (exact distance ordering; `relaxed_order` is faster but may reorder results and top-5 retrieval is cheap either way), set per query via `SET LOCAL` inside a transaction, enabled only when `pg_extension.extversion >= 0.8` (cached per pool) — on older pgvector the GUC prefix is reserved and `SET` would error.
- A mutation check (forcing iterative scan off) makes the recall test fail, so the test really guards this.

### D2-3.1 Index-matching expression (the plan's fix)
- `embedding::halfvec(3072) <=> ?::halfvec(3072)` — verified with `EXPLAIN` to use `idx_topic_memories_embedding`; the old `embedding <=> ?` does not.
- Note: on small tables the planner still prefers a B-tree index + sort for filtered queries (exact and cheap); HNSW kicks in as data grows. Tests force the HNSW path with `enable_sort = off`.

### D2-3.2 Test data
- Exactly orthogonal unit vectors produced an HNSW graph where the user's rows were unreachable (no inbound edges), so even iterative scans found nothing. That's a synthetic-data artefact; tests use seeded dense, correlated vectors (`mixVec`) that behave like real embeddings.

### D2-3.3 [DECIDE] 768-dim embeddings — skipped per owner default
- Revisit after P2-4 (memory unification): one re-embedding migration could do both.

---

## P2-4 — Unify memory systems

### D2-4.1 Migration mechanism
- **Options:** (a) idempotent `INSERT … SELECT … WHERE NOT EXISTS` run on every startup; (b) one-shot script run manually; (c) versioned migration (P4-3 territory — goose isn't introduced yet).
- **Chosen:** (a), in `db.NewClient` after `ApplyCustomIndexes`, **fatal on error**.
- **Why / challenge:** (b) is easy to forget, and once this release ships nothing reads the legacy table — an unmigrated fact would silently vanish from the assistant's memory. Fatal-on-error is deliberate for the same reason. Cost: one anti-join per startup over a small table. When P4-3 introduces versioned migrations this becomes a one-time migration.

### D2-4.2 Dedup key for already dual-written facts
- `(user_id, fact)` exact match. The old `remember_fact` already dual-wrote most facts (always as topic `personal`, even family-scoped ones); those keep their existing v2 row. Rejected: matching on embedding similarity (expensive, fuzzy) or deleting/re-creating v2 rows (would lose pins/supersession).

### D2-4.3 Family group for migrated rows
- v1 rows have no family group (v1 family visibility was "any family member's user_id"). Taken from `users.family_group` via `LEFT JOIN`; users with no `users` row keep `''` (still visible to themselves).

### D2-4.4 Remove the runtime legacy fallback
- `SearchSimilarMemories` used to fall back to `memories` when v2 had 0 hits — and it passed the *family group name* as a user ID, so family-scoped legacy facts were never actually found. Removed; the startup migration replaces it.

### D2-4.5 Port shape
- Reused the existing `Client.SearchSimilarMemories(ctx, userID, familyGroup, emb, limit)` for the `DBClient` port instead of adding a new method (an initial `SearchSimilarTopicMemories` collided with an existing keyword-search helper and duplicated it). `SaveMemory`/`SearchMemories` removed from the port and from `db.Client`. Engine no longer needs `GetUsersByFamilyGroup` for retrieval — tenant visibility is the repo's job.

### D2-4.6 `remember_fact` without a v2 repo
- Now returns an error message instead of silently writing only to v1. In production the repo always resolves from `*db.Client`.

### D2-4.7 Dropping the `memories` table
- Not done in code (plan: "keep old table until verified"). **Owner action:** after confirming retrieval in production, `DROP TABLE memories;` and remove `models.Memory` from AutoMigrate (a P4-3 versioned migration is the right vehicle).

---

## P2-5 — Skip embedding + retrieval for trivial messages

### D2-5.1 Classification rule
- **Options:** (a) token count only (≤ 3 words); (b) all-filler words AND ≤ 3 words; (c) a tiny classifier / LLM call.
- **Chosen:** (b), plus empty and emoji/punctuation-only text.
- **Why / challenge:** (a) would skip "remind me tomorrow" or "no meat tonight", losing relevant memories exactly when short messages carry intent. (c) costs more than the embedding it saves. With (b) the failure mode is benign: an unlisted filler word just means we embed as before.
- "ok ok ok thanks thanks" (>3 filler words) is deliberately *not* trivial — the threshold is a hard cap so the rule stays predictable; it's a named constant (`maxTrivialTokens`) with a test.
- CONFIRM messages never reach retrieval (handled earlier in `ProcessMessage`), so no special case is needed.

---

## P2-6 — Cache-friendly prompt ordering

### D2-6.1 Section order
- static instructions → calendar conventions → user profile → tool guidance → module sections → memories → current time. Each section changes more often than the one before it. User profile sits before tool guidance because it is stable per user across turns while the tool set varies per turn.
- Challenge: Gemini also hashes tool declarations into the request; they vary per turn anyway (Tier-2 scoping), so filtering guidance by tools costs no extra cacheability beyond what tool selection already costs, and the static block (the bulk of the instructions) remains a shared prefix for every user.

### D2-6.2 Tool guidance: filtered list in the engine vs per-module `SystemPromptSection`
- **Options:** (a) keep guidance in the engine as a list keyed by tool names (`toolGuidance`, any/all conditions); (b) move each line into its module's `SystemPromptSection`; (c) derive guidance from tool descriptions only.
- **Chosen:** (a).
- **Why / challenge:** (b) is cleaner ownership but module sections are cached *per user* for 10 minutes regardless of which tools are active this turn, so the guidance would not follow tool selection; it would also churn 6 modules. (c) loses cross-tool workflows ("email with a flight → create a calendar event"), which are expressed with `all:` conditions. Revisit in P4-1 when `ProcessMessage` is split.

### D2-6.3 Time precision and zone
- Minute precision (plan). Also fixed: the old prompt printed `time.Now()` in the *server's* zone (UTC in Docker) while telling the model it was the user's timezone. Now rendered in `DEFAULT_TIMEZONE`.

### D2-6.4 "User is in Israel / week starts Sunday"
- **Options:** per-user preference (needs a model + tool change); deployment config; derive from timezone (wrong in general — e.g. US weeks start Sunday but weekend is Sat/Sun).
- **Chosen:** deployment config `WEEK_START_DAY` (default Sunday) + `WEEKEND_DAYS` (default Friday,Saturday) — preserves today's behaviour; invalid values fall back to defaults rather than failing startup. "Israel" itself is dropped (the timezone conveys location). Per-user override is a possible follow-up via `update_user_preferences`.

### D2-6.5 Static text edits
- The instruction text is preserved, re-grouped. Two wording changes: the JSON-summarizing rule no longer names specific tools (it applies to all), and a new line "Only call tools that are available to you in this conversation turn."
