# Remediation Decision Log

Design decisions made while executing [`REMEDIATION_PLAN.md`](./archive/REMEDIATION_PLAN.md). Each entry lists the options considered, the choice, why it won (including the counter-arguments it had to survive), and any follow-ups. Owner: please review; anything marked **Revisit?** is a call you may want to overturn.

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

---

## P2-7 — Resolve user once per turn

### D2-7.1 Carrier
- **Options:** (a) context value set after the pre-auth lookup; (b) thread `*models.User` through `handleToolCalls` → `executeToolsConcurrently` → `executeSingleTool` signatures; (c) a per-turn struct (merging `TurnSecurityState`/`turnEffects`).
- **Chosen:** (a), next to the other per-turn context state (`turnEffects`).
- **Why / challenge:** (b) is more explicit but touches every signature in the tool loop and dozens of test call sites, with P4-1 about to restructure exactly that code. (c) is the right end state — noted for P4-1 (one `turnState` instead of three context keys). A fallback `GetUser` remains for calls outside a turn (tests, direct use) so behaviour is unchanged there.

### D2-7.2 Staleness
- A user's role/family group changing mid-turn is not observed until the next turn. Turns last seconds to minutes; authorization-critical paths that span turns (e.g. `CONFIRM RESTORE`) re-check on their own turn. Accepted.

### Verification
- Mutation check: without `withTurnUser`, a 3-tool turn made 4 `GetUser` calls; with it, 1.

### Incidental: flaky cross-tenant baby test (separate commit)
- `TestSEC04_CrossTenant_BabyProfileAndCareLogs_Isolation` asserted the JSON response did not contain the substring `"120"` (the other family's 120 ml feeding). The response embeds nanosecond timestamps, and one run produced `…36.1200304` → false failure. Replaced with structural assertions on the parsed JSON (`logs_returned == 0`, no `recent_logs`, zero feedings/volume) — stricter, and clock-independent. Grepped for the same numeric-substring pattern elsewhere: none.

---

## P2-8 — Parallel users, ordered per user

### D2-8.1 Concurrency model
- **Options:** (a) N independent consumers on the queue (no per-user ordering — two messages from one user could race, corrupting chat history order); (b) consistent-hash exchange / per-user queues (broker-side ordering, heavy topology change); (c) single consumer, `Qos(N)`, in-process keyed lanes; (d) per-user mutex around the handler with one goroutine per delivery (orders by lock acquisition, which is *not* FIFO in Go).
- **Chosen:** (c). FIFO per key is guaranteed by the lane's queue (unlike (d)); total concurrency is bounded by prefetch with no extra semaphore; no topology change. Revisit only if the orchestrator is scaled to multiple replicas (then (b), or keep one replica — see P4-4 ADR).

### D2-8.2 Retry delay
- **Options:** TTL'd retry queue dead-lettering back to the main queue (no sleeping goroutine, survives restarts); sleep inside the lane worker.
- **Chosen:** sleep inside the lane worker (plan's second option): delays only that user, zero topology change, ≤ 1.4 s total. The TTL-queue approach is the better long-term design if retries grow longer — noted.

### D2-8.3 Panics
- Converted to handler errors (`callContained`) so the delivery is retried/dead-lettered. Merely recovering in the worker would leave the delivery unacked forever, permanently consuming a prefetch slot.

### D2-8.4 Reconnect
- On channel close the loop drains in-flight lanes before reconnecting, so a redelivered message never runs concurrently with its still-running original (P1-4's dedup claim would also catch it).

### D2-8.5 Default prefetch = 4
- A family assistant rarely has > 4 simultaneous turns; each turn may hold several Gemini/Google calls. Configurable via `RABBITMQ_PREFETCH`.

### Verification
- Unit: per-key FIFO with no overlap (200 tasks), cross-key independence, worker restart after drain, panic containment.
- Integration (RabbitMQ testcontainer): slow user A doesn't delay user B; A's two messages stay ordered. Mutation check (all deliveries on one lane) fails as expected.
- **Race detector:** no local C toolchain, so ran `go test -race` in a `golang:1.26` Linux container: dispatcher tests + orchestrator turn tests + a new 12-concurrent-users turn test (3 runs) — clean. CI does not run `-race` today → proposed for P5-3.

---

## P2-9 — Persist tool context in history ([DECIDE] resolved by owner 2026-11-15)

**Owner decision:** Redis 24 h TTL, 30 messages, compact per-turn tool context; `tool_summary` on `audit_sessions` so it flows into the existing 30-day archive; archive gzip-compressed behind an interface; blob storage deferred to the P3-4 off-machine decision. (Owner first asked about moving Redis history to blob after 24 h; analysis showed every turn is already durable in Postgres → archive, and the Redis copy is tiny — so the space win is compressing the archive, not moving Redis.)

### D2-9.1 How tool context is stored in history
- **Options:** (a) a text note appended to the assistant reply ("[Actions: created evt_1]"); (b) a separate text message; (c) native function-call / function-response pairs, which `models.ChatMessage` (`ToolCalls`/`ToolResponses`) and `llm.Client.Chat` already support and replay as Gemini `functionCall`/`functionResponse` parts.
- **Chosen:** (c).
- **Why / challenge:** (a)/(b) put system-generated text into the model's own voice — models tend to imitate it in later replies. (c) is the format the model was trained on for tool use. Cost: Gemini requires a call turn to be immediately followed by its response turn, and `LTRIM` can split them → `sanitizeHistory` on load (tested by trimming at every offset). Multiple tool rounds in one turn are collapsed into one call/response pair (parallel calls are valid Gemini shape).
- Bounds: results ≤ 400 runes (rune-safe for Hebrew), string args ≤ 200; read-only built-ins (`get_current_time`) omitted.

### D2-9.2 Audit tool summary
- One line, `name: result-prefix | …`, ≤ 1000 runes, stored as `text`. Published with the session (asynchronous telemetry) and therefore archived with it. Not exposed via GraphQL/dashboard yet — follow-up if wanted.

### D2-9.3 Archive format and sink
- gzip JSONL (`.jsonl.gz`), written to `<name>.tmp`, fsynced, then renamed — previously a crash could leave a truncated `.jsonl`. Sessions are deleted from Postgres only after `Write` succeeds (unchanged guarantee).
- `ArchiveSink` interface with one implementation (`localGzipSink`). **Not done:** encryption at rest of archives (they contain conversations) — should reuse whatever P3-4 chooses for backups (`age`); and the blob/object-storage sink (needs the owner's provider choice).

### D2-9.4 TTL interaction with sticky domains / pending actions
- 24 h history means yesterday's tool results are visible to today's first message. That is the intent ("delete that event" next morning). Prompt-injection exposure is unchanged: tool results were already in the turn that produced them; P3-1 broadens untrusted-content marking.

---

## P3-1 — Broader prompt-injection protection

### D3-1.1 Central marking vs per-module marking
- **Options:** (a) each module calls `MarkUntrusted` (status quo — only 3 call sites, easy to forget); (b) one `untrustedOutputTools` list consulted in `executeSingleTool`.
- **Chosen:** (b), keeping existing module-level marking/wrapping as defence in depth (the engine skips re-wrapping already wrapped output). A new external-data tool is protected by adding one map entry.

### D3-1.2 Gate list
- Plan list + the original four meta tools, in one map (`confirmationRequiredWhenUntrusted`). Criteria: persists instructions, destroys data, messages other people, reaches external services, or writes long-term memory (memory poisoning is the persistent form of injection).
- **Known limitation:** the pending-action slot holds one action; if an untrusted turn requests several gated tools, only the last is staged (the others report "confirmation required" to the model). A batch-confirm flow is a possible follow-up; acceptable because untrusted multi-write turns are exactly the case that deserves friction.
- UX cost accepted: "read my email and delete the spam" now asks for confirmation of the deletion.

### D3-1.3 Images, PDFs, documents, voice
- Images and PDFs from any channel mark the turn untrusted (they can contain rendered instructions). Telegram *documents* are not ingested at all (`models.Message` has no document field) — nothing to mark. Voice notes are not marked: they are the user's own speech (residual risk: a played-back recording).

### D3-1.4 Framing stored user data — deviation from the plan's wording
- Plan: wrap standing instructions/memories with `WrapUntrustedContent`. That wrapper tells the model to *never follow* instructions inside, which would make the model ignore the user's legitimate preferences ("always answer in French").
- **Chosen:** a separate `WrapUserProvidedData` framing — honour preferences and facts as context, but no authority to override rules, grant permissions, or by itself justify actions — and remove the "STRICT REQUIREMENT" label. Applied to standing instructions, pinned memories + entity profiles, and retrieved memories.

### D3-1.5 Closing-tag breakout (not in the plan)
- Both wrappers embedded content verbatim, so a payload containing `</untrusted_external_content>` could end the block early. Now neutralized with a zero-width space (`neutralizeTag`). An existing adversarial test asserted the payload appeared verbatim — updated to assert neutralization and exactly one closing tag.

### Incidental: test-only `handleToolCall` double-executes `search_emails`
- The helper runs `search_emails`, compares the output to an exact string, and otherwise falls through to `handleToolCalls`, which runs the tool again. Production does not use it (only tests). Made the shortcut content-based; removal belongs to P4-1.

---

## P3-2 — JWT / claims hardening ([DECIDE] dashboard scoping resolved by owner 2026-11-15)

### D3-2.1 JWT rules
- Header `alg` must equal `HS256` exactly (rejects `none`, `HS512`, case variants — no algorithm confusion). `exp` mandatory, in the future, and ≤ `MaxTokenLifetime` = 24 h ahead.
- **Why 24 h without `iat`:** no code in the repo mints JWTs (they are minted externally), so issuance time can't be trusted anyway; capping `exp - now` bounds the damage of a leaked token regardless of how it was minted. **Revisit?** if a longer-lived client exists, mint shorter tokens rather than raising the cap.

### D3-2.2 Dashboard secret scoping (owner: "scope via DASHBOARD_FAMILY_GROUP, fail closed")
- **Options:** (a) give the dashboard a non-admin role in its family (breaks admin-only dashboard features such as editing family members' preferences); (b) keep `Role=admin` and add a server-side `TenantBound` flag, replacing every cross-tenant `Role == "admin"` bypass with `IsGlobalAdmin()`; (c) a separate API for the dashboard.
- **Chosen:** (b). Eight resolver bypasses converted (shared reminder lookup, automation rule get/delete, user preference read/write, rollback scope, two audit system-wide views). Per-user targets use `resolveTargetUser` → `canActOnUser` (tenant-bound admin may only reach users whose `users.family_group` matches).
- `TenantBound` is `json:"-"` and explicitly reset after JWT parsing — a token payload can never set it (tested).
- `DASHBOARD_FAMILY_GROUP` is **required** by the orchestrator's `Validate` and the secret is ignored if it's empty (fail closed).
- **Dashboard follow-through:** the Next.js app hard-coded `"Smith Family"` in 17 places — it only worked because the dashboard was a global admin. Options were (i) the server silently coercing any requested family to the bound one, or (ii) the dashboard reading the same `DASHBOARD_FAMILY_GROUP`. Chose (ii): the authorization layer should never silently ignore what a client asked for. `getDashboardFamilyGroup()` in `lib/graphql.ts` (server-side env), passed to the dashboard container in compose; falls back to the demo family when unset (the server then rejects the mismatch). Dashboard tests + lint pass.
- **Behaviour change for the owner:** if your real family group isn't "Smith Family", the dashboard previously showed the demo family; it now shows *your* family once `DASHBOARD_FAMILY_GROUP` is set.

### D3-2.3 Missing claims
- `resolveTenantAndActor` returned user `"user"` in the *requested* family when claims were nil; `RollbackMutation` ran as a global `"system"` rollback. Both now error. The middleware always sets claims on `/graphql`, so this only removes a fall-open path.

### D3-2.4 JWT_SECRET fallback
- Removed (`cmd/orchestrator` read `os.Getenv` with `ENCRYPTION_KEY` fallback); auth secrets now come from validated config (`authCfg`). P1-2 already made `JWT_SECRET` required and distinct.

### Noted, not changed
- Members with an empty family group resolve to the shared tenant `"default"` in `resolveTenantAndActor` — two such users would share data in GraphQL. The orchestrator side uses the user ID as the fallback tenant instead. Aligning them changes data visibility for existing rows → follow-up for the owner.

---

## P3-3 — Logging hygiene + rotation

### D3-3.1 What replaces message text in logs
- **Options:** (a) length + content hash (plan); (b) length + message ID; (c) nothing.
- **Chosen:** (b). A hash of short, predictable family messages ("ok", "yes", "at the dentist") is reversible by dictionary and lets anyone confirm a guess; correlation — the hash's purpose — is already provided by the platform message ID. Full text remains available in a separate DEBUG record.
- Added `LOG_LEVEL` (all Go services, via `internal/logging.Setup`) — previously the level was hard-coded to INFO, so "full text at DEBUG" would have been unreachable.

### D3-3.2 Broker unparseable payloads
- Log `body_bytes` only. The payload is a user message; the DLQ keeps the original for inspection.

### D3-3.3 Bot token in errors (carried over from P1-1)
- `SendMessage`, `SendMessageHTML`, `GetFile`, `DownloadFile` now route request/transport errors through `redactURLError` (the API URL contains the token). Tested by a transport that always fails.

### D3-3.4 Log rotation
- One YAML anchor (`x-logging`) applied to all 10 services (including third-party images); structural test parses the compose file and checks driver/size/count per service, so a newly added service without rotation fails CI.

### Not covered
- Third-party containers' own logs (Postgres statement logging, RabbitMQ) may contain data; their defaults don't log message bodies. The dashboard (Next.js) doesn't log message content.

---

## P3-4 — Backup encryption & retention ([DECIDE] resolved: `age`; off-machine copy deferred)

### D3-4.1 Encryption tool & integration
- **Owner choice:** `age`. Integrated via the reference Go library `filippo.io/age` v1.2.1 rather than shelling out to an `age` binary (no extra package in the image, testable in-process).
- **Streaming:** `pg_dump -Fc` → stdout → `age.Encrypt` → `0600` temp file → fsync → rename. No plaintext dump ever touches the disk. Restore streams `age.Decrypt` into `pg_restore`'s stdin (custom-format restore from a pipe is sequential — fine for this size; parallel `-j` restore would need a seekable file).
- **Keys:** public keys only on the server (`BACKUP_AGE_RECIPIENTS`); the private key (`BACKUP_AGE_IDENTITY_FILE`) is needed only to restore — keep it off-box, mount it when restoring. Multiple recipients allowed (e.g. owner + offline escrow key).

### D3-4.2 Missing recipients → warn, don't fail
- **Options:** refuse to back up (fail closed) or write unencrypted with a warning.
- **Chosen:** warn. The dump sits on the same host as the live database, so an unencrypted local dump adds little exposure, while a skipped backup is unrecoverable. Encryption matters most once copies leave the machine — the off-machine copy decision should *require* recipients.

### D3-4.3 Password out of argv
- `pgConnArgs` strips the password from the URL and passes `PGPASSWORD` in the child env (argv is world-readable via `ps`/`/proc`). `.pgpass` rejected: needs a file with strict permissions in the container for no extra benefit.

### D3-4.4 Retention
- 3 latest + newest per day (7 days) + newest per ISO week (4 weeks). The **latest-3** rule is essential: a `/restore` (P1-5) takes a same-day safety backup; a pure per-day rule would prune the very file about to be restored. Only `backup_<ts>.dump[.age]` files are ever deleted. Timestamps are parsed from names (not mtime) so copies/rsync don't confuse the policy.

### D3-4.5 Restore path validation (fix to P1-5)
- P1-5 accepted only `filepath.Ext == ".dump"`, which would reject every encrypted backup (`.dump.age`). Now uses `db.IsBackupFile`; tested.

### Not done
- Off-machine copy (owner to choose target). Encrypting the P2-9 conversation archives (`audit_archives`) with the same recipients is the natural next step.

---

## P3-5 — Compose: no committed domain, pinned images

### D3-5.1 How to "require" NGROK_DOMAIN and the pgAdmin password
- **Options:** (a) `${VAR:?message}`; (b) no default, let the program fail at start.
- **Verified empirically:** Compose interpolates `${VAR:?}` for *every* service — including services in inactive profiles — and aborts the whole command. (a) on the pgAdmin password would break `docker compose up` for everyone not using pgAdmin; on `NGROK_DOMAIN` it would break `docker compose build` (and the P5-3 CI build, which has no `.env`).
- **Chosen:** (b). `--url=${NGROK_DOMAIN:-}` → ngrok fails loudly at start without a domain; `PGADMIN_DEFAULT_PASSWORD: ${...:-}` → pgAdmin's entrypoint refuses to start without a password. The committed domain is gone from the file; a test asserts it stays gone.

### D3-5.2 Pinning
- **Options:** version tag (`ngrok/ngrok:3.39.8`), digest, both.
- **Chosen:** `tag@sha256:digest` using the exact images already running on this machine (ngrok agent 3.39.8, pgAdmin 9.17), so pinning changes nothing at runtime. The digest is authoritative (immutable); the tag is for humans. Updating is a deliberate change — Dependabot (P5-4) can bump digests.

### D3-5.3 pgAdmin opt-in
- `profiles: ["tools"]`; start with `docker compose --profile tools up -d pgadmin`. Default email kept (not a secret).

### Owner action
- **Rotate exposure:** the old default ngrok domain is in git history. If the repo is (or was) public, treat that domain as known (Phase 0 note) — ngrok domains aren't secrets, but webhooks behind it rely on P1-1's secret checks.

---

## P4-1 — Split ProcessMessage

### D4-1.1 Shape
- `ProcessMessage` (462 → 95 lines) keeps only orchestration: dedup claim, telemetry + retry defers (which depend on the named `err`), auth, then four calls. Each phase returns `(response, handled|stop, err)` so every early-return path keeps the exact error the defers see.
- `commands.go`: CONFIRM redemption + a command table (`map[string]commandHandler`; `/undo <id>` by prefix). Adding a command is one map entry + one method.
- `media.go`: `ingestMedia` → `mediaAttachment{Data, MimeType}`; may rewrite `msg.Text` (transcription, inlined documents) via pointer, exactly as the inline code mutated the local `msg`.
- `turn.go`: `runTurn` + the tool loop (`executeToolsConcurrently`, `handleToolCalls`, `executeSingleTool`).

### D4-1.2 How "no behaviour change" was ensured
- Moved blocks were extracted programmatically from a snapshot by line range and transformed only by mechanical renames (`photoData`→`media.Data`, `llmResponse=`→`response=`, `return X`→`return …, X`), not retyped. The full suite (incl. idempotency, restore, sticky domains, injection, history, concurrency tests added earlier) passes unchanged.

### D4-1.3 Test-only `handleToolCall`
- Moved to `engine_testhelpers_test.go` (it double-executed `search_emails`; see P3-1 note). Production code no longer contains it; its 12 test callers are unchanged.

### Not done (follow-ups)
- One `turnState` struct instead of three context keys (`TurnSecurityState`, `turnEffects`, turn user) — would touch every tool-loop signature; better as its own change.
- Tool guidance (`toolGuidance`) still lives in the engine (see D2-6.2).

---

## P4-2 — Split large domain modules

### D4-2.1 Method: an AST tool, not hand-editing
- A throwaway Go program (scratchpad `splitmod`) parsed each `module.go`, classified every top-level declaration, and copied its **exact source bytes including doc comments** into `module.go` / `declarations.go` / `handlers.go` / `service.go`. Rules: `Declarations` → declarations; `Execute`, `execute*`, `handle*` → handlers; `NewModule`, `With*`, `Name`, `SystemPromptSection`, `resolveRepo`, types/consts/vars → module; anything else mentioning `genai.` → declarations; the rest → service (guaranteed genai-free).
- **Verification:** a line-multiset comparison (old file vs. union of new files, ignoring imports) proved nothing was lost or altered except two floating section-divider comments (`// Helper utilities`, `// Helpers`) — dropped intentionally; their content now *is* `service.go`. Imports per file: generated by usage, then compiler-verified (string literals like "…time." produced false positives that were removed by parsing `go build` errors). Full test suite unchanged.

### D4-2.2 Scope boundary
- `handlers.go` holds most lines because business logic is interleaved inside the `execute*` methods (argument parsing + repository calls + formatting). Pulling that into `service.go` would be a behavioural refactor per tool — out of scope for a "no behaviour change" item; the file split makes that follow-up reviewable per module.

## P4-3 — Versioned migrations (goose)

### D4-3.1 Tool: goose (pre-agreed), Provider API
- Options: **goose** (chosen, pre-agreed), golang-migrate, Atlas (declarative diff from GORM models), keep AutoMigrate.
- Atlas would derive migrations from models automatically, but adds a CLI + HCL/config surface and a second source of truth for "desired" schema; golang-migrate has no Go-function migrations (needed for the P2-4 legacy-memory copy). goose supports embedded SQL + Go migrations in one ordered sequence.
- Used goose v3.29 `NewProvider` (no package-level globals, so tests can run providers against separate databases) with `lock.NewPostgresSessionLocker` — an advisory lock so two migrators (e.g. a replica restarting during a deploy) cannot interleave.

### D4-3.2 Baseline = pg_dump of the AutoMigrate schema
- Options: (a) hand-write CREATE TABLEs; (b) **dump the schema the old code actually produced** (chosen); (c) baseline = "run AutoMigrate once as a Go migration".
- (c) keeps GORM's migrator in the production path, which is exactly what P4-3 removes. (a) is error-prone for 29 tables / 99 indexes. (b) is exact by construction: a fresh pgvector:pg15 database was built with the old `NewClient`, dumped with `pg_dump --schema-only --no-owner --no-privileges --no-comments`, and `SET`/`set_config(search_path)`/`\restrict` lines stripped (non-SQL or connection-state-leaking). All 29 models are registered via `init()`, so the dump is complete.
- Down for the baseline is intentionally a no-op comment: "un-creating" the whole schema is not a meaningful rollback.

### D4-3.3 Adopting existing (pre-goose) databases
- Options: (a) require operators to run `goose ... fix`/manual SQL; (b) make the baseline idempotent (`IF NOT EXISTS` everywhere); (c) **auto-adopt** (chosen).
- (b) silently masks drift between the live DB and the baseline. (c): if `users` exists and `goose_db_version` does not, `Migrate` creates the version table and records versions 0 and 1 without executing the baseline, inside a transaction holding `pg_advisory_xact_lock` with a re-check. Version 2 (legacy memories copy) still runs — it is idempotent (`INSERT … WHERE NOT EXISTS`), so databases that already ran it in P2-4 are unaffected.
- Residual risk (accepted): a pre-goose DB that was *older* than the baseline (missing a column) would be adopted as-is. Mitigation: every pre-goose DB ran AutoMigrate on each start, so any DB that ran the current code already matches.

### D4-3.4 Explicit step, services refuse to start
- Options: migrate on every service start (status quo semantics); **explicit `-migrate-only` step + startup check** (chosen).
- Chosen per plan: a dedicated one-shot `migrate` compose service (orchestrator image, `command: ["-migrate-only"]`, `restart: "no"`); `orchestrator`/`audit-worker` depend on it with `service_completed_successfully` and call `db.EnsureMigrated`, which fails fast with an actionable message. No new binary/Dockerfile target needed (reuses orchestrator, which already has the DB config).

### D4-3.5 AutoMigrate kept for tests only — with a drift guard
- `NewClient` split into `db.Open` (connect + ping, production) and `db.NewClientAutoMigrate` (tests/dev). The rename makes any accidental production use visible in review.
- Risk: with two schema paths, a model change could ship without a migration and tests (which AutoMigrate) would still pass. **`TestMigrations_FreshDatabaseMatchesModels`** builds one DB via goose and one via AutoMigrate and compares columns (type, nullability, default) and index definitions. Mutation-checked: adding a field to a model makes it fail. Also `_AdoptsExistingDatabase`, `_Idempotent`, `TestEnsureMigrated`.

### D4-3.6 Dependency bumps from `go mod tidy`
- Adding goose raised shared transitive deps via MVS (x/crypto 0.54→0.58, x/net, x/sys, pgx 5.10→5.11, testify 1.11→1.12, otel 1.44→1.47, grpc 1.83→1.84, …). All minor/patch; full test suite + compose build pass. Not pinned back: MVS requires them and they include security-relevant x/crypto/x/net updates.

## P4-4 — Service topology (ADR only)

- Written as [`docs/adr/0001-service-topology.md`](adr/0001-service-topology.md); **nothing implemented** (pre-agreed).
- Recommendation in the ADR: (1) one binary with a `--role` flag (default `all`) replacing proxy+gateway+orchestrator, packages unchanged; (2) keep RabbitMQ for now (P1-4/P2-8 removed the correctness arguments for switching); if it is replaced later, prefer River (transactional enqueue, in backups, periodic jobs with leader election) **with an explicit per-user ordering guard**, because open-source River has no per-key FIFO; Redis Streams is the smaller-change alternative.
- Challenge noted: the gateway's ingress isolation only matters during orchestrator restarts (seconds), which Telegram covers with its own webhook retries.
- Verification: documentation only; no code changed, so the P4-3 build/test run applies unchanged.

## P5-1 — Remove stray files

### D5-1.1 What was deleted
- Untracked: `tmp_orch.exe`, `graph.test.exe`, `test_db.exe`, `ngrok.exe` (~143 MB; ngrok runs in its compose container since P3-5), `bin/` (stale `gateway/orchestrator/proxy.exe`).
- **Plan premise partly wrong:** `coverage` (a Go cover profile from August) was **tracked**, not untracked — removed with `git rm` and now gitignored (`/coverage`, `/bin/`).
- `_test_oauth.go` deleted: it printed an auth URL for hard-coded fake credentials; `internal/calendar` tests cover `NewOAuthConfig`.

### D5-1.2 `_list_models.go` → `cmd/devtools/listmodels`
- Options: delete; keep as `_`-file; **move to `cmd/devtools/listmodels/main.go`** (chosen).
- Go ignores files starting with `_`, so it had never been vetted — and indeed it **no longer compiled** (`Models.List` now returns `(Page, error)` in genai v1.67). Moved and rewritten with the `Models.All` iterator; it is now covered by `go vet ./...` and `go build ./...`. Listing available models is genuinely useful when changing `GEMINI_MODEL`. Not added to the Dockerfile.

### D5-1.3 REVIEW.md stays private
- Moved to `docs/reviews/2026-11-03-review.md` as planned, but `.gitignore` deliberately listed `REVIEW.md` under "audit reports", so `docs/reviews/` is now gitignored too rather than committing a 57 KB security review. Owner can un-ignore it if the review should be versioned.
- `.agents/teamwork/` left untouched (pre-agreed).

### D5-1.4 Clean script instead of `make clean`
- Options: Makefile `clean` target; Taskfile; a Go program; **`scripts/clean.ps1` + `scripts/clean.sh`** (chosen).
- `make` is not installed on the owner's Windows machine, so a Makefile would be documentation, not a tool. A Go program for `rm -rf` is overkill. Two ~15-line scripts (PowerShell for Windows, POSIX sh for CI/Git Bash) with the same target list; both were exercised against probe files and leave sources untouched. `scripts/` and `coverage` added to `.dockerignore`.
- Added `.gitattributes` (`*.sh text eol=lf`): with `core.autocrlf=true` a checked-out `clean.sh` would get CRLF and fail on `set -eu\r`.

## P5-2 — Behaviour-named tests

### D5-2.1 Scope: files *and* function prefixes, but keep review IDs
- Options: rename files only; rename files + strip agent-iteration prefixes from function names (chosen); also strip review-finding IDs.
- 28 files renamed (e.g. `adversarial_m2a_challenger2_test.go` → `turn_engine_adversarial_test.go`, `cooldown_adversarial_iter2_test.go` → `cooldown_medication_classes_test.go`, `adversarial_sec01_test.go` → `auth_gate_security_test.go`); 74 test functions lost prefixes that only named the agent run (`TestChallenger2_`, `TestChallenge1_`, `TestChallenge2_/3_`, `TestEmpirical_`, `TestEmpiricalChallenge_`, `_M1C_`).
- **Kept** review-finding IDs (`SEC01`, `SEC09`, `ARCH01`, `Scale01`, `bug03_lifecycle_test.go`): they trace a test to the security review finding it guards, which is behavioural information.

### D5-2.2 No merges, no deletions
- Exact-duplicate scan (normalized test bodies per package): **0 duplicates**, so nothing deleted.
- Merging small files (e.g. the two timezone files) would need hand-written import unions for no behavioural gain; kept as separate behaviour-named files.

### D5-2.3 Verification that no test case was lost
- `go test -list '.*' ./...` before vs after: 612 = 612 entries; the 74 removed names map one-to-one onto the 74 added names under the prefix rules (checked as a bijection). Renames were byte-level regex substitutions (line endings preserved); `go vet` proves no collisions. Full test suite + compose build re-run.

## P5-3 — CI

### D5-3.1 Principle: CI must be green the day it is introduced
- The old workflow only ran on `master` (this whole remediation lives on `dev`) and installed `golangci-lint@master`, so nothing had ever gated the code. Every new gate was run locally with the exact pinned tool **before** committing: lint (golangci/golangci-lint:v2.15.0 image), govulncheck v1.9.0, the dashboard job in `node:22`, `go test -race` in `golang:1.26`, and a compose `config` simulation with only `.env.example`.
- They were not green, so P5-3 also fixes what they found (separate commit on the same branch) rather than shipping red gates.

### D5-3.2 Pinning
- Actions pinned to **commit SHAs** with the release in a comment (tags are mutable; Dependabot updates both). Releases chosen were ≥ 4 weeks old: checkout v7.0.1, setup-go v7.0.0, setup-node v7.1.0, golangci-lint-action v9.3.0; tools: golangci-lint v2.15.0, govulncheck v1.9.0. Go version comes from `go.mod` (`go-version-file`), so CI and builds cannot drift.
- `permissions: contents: read` and a per-ref `concurrency` group (cancel superseded runs).

### D5-3.3 golangci-lint configuration (`.golangci.yml`, v2)
- Pinned linter found 56 issues (45 errcheck, 11 staticcheck) — and **107 more** hidden by the default `max-same-issues: 3` cap (found by re-running uncapped; worth knowing when reading lint output).
- errcheck (45, all `Close()` on read paths, shutdown paths and test bodies): the v2 `std-error-handling` exclusion preset — the community default. Both `internal/db/backup.go` sites were checked by hand: read paths, so ignoring Close is correct. Caveat noted in the config: Close on a *write* path still needs explicit checking in review.
- staticcheck: fixed 7 real simplifications (tagged switches, De Morgan, inferred type, merged conditional assignment, `fmt.Fprintf(w, …)`). Disabled two quick-fix checks: **QF1008** (`db.Dialector.Name()` is clearer than the promoted `db.Name()`) and **QF1012** (`b.WriteString(fmt.Sprintf())` → `fmt.Fprintf(&b, …)`: 107 sites in non-hot prompt builders; mass rewrite = churn). Options considered: `new-from-rev` baseline (hides real debt and breaks on rebases — rejected), fixing all 107 (rejected as churn), excluding test files (rejected — tests deserve the same linters).

### D5-3.4 govulncheck findings → patched
- Reachable vulnerabilities in 4 modules: `golang.org/x/net` 0.61→0.62 (html parser, used by the scraper), `rabbitmq/amqp091-go` 1.13→1.14 (pre-negotiation frame limit), `klauspost/compress` 1.20.1→1.20.2, `moby/go-archive` 0.2→0.3 (testcontainers). Patch/minor bumps; all tests + race suite pass.

### D5-3.5 Dashboard: a critical, shipped vulnerability
- `npm audit` found **`next@16.3.4` with critical advisories including unauthenticated RCE** (Image Optimization with SVG; `use cache` typed arrays) plus SSRF/cache-poisoning. Upgraded to `next@16.4.6` / `eslint-config-next@16.4.6` (latest 16.4 patch, 1 week old — for an RCE fix within the framework's own patch line, taking all later fixes outweighs the soak-time preference).
- `shadcn` moved to devDependencies: it is only imported from `globals.css` (`@import "shadcn/tailwind.css"`, compiled by Tailwind at build). As a runtime dependency it pulled `@modelcontextprotocol/sdk` → express → `proxy-addr` (critical) into the production tree. The Docker build stage runs `npm ci` with devDependencies, so the build is unaffected.
- `npm audit fix` (in-range lockfile updates). Result: **0 vulnerabilities in production dependencies**. Remaining: dev-only (vitest 3 → tinypool critical, minimatch, deepmerge); fix requires vitest 5 (major) → **P5-4**.
- Gate: `npm audit --omit=dev --audit-level=high` is blocking; full audit runs report-only (`continue-on-error`) until P5-4 makes it blocking.

### D5-3.6 Compose build without secrets
- Services use a required `env_file: .env`, which compose resolves even for `build` (verified: `env file … not found`). Options: `required: false` in compose; `cp .env.example .env` in CI (chosen). Making it optional would let a real deployment start silently without secrets; the CI-only copy keeps production fail-closed.

### D5-3.7 Tests under `-race`
- CI runs `go test -race -count=1 -p 1 ./...` (proposed in P2-8). Verified locally beforehand: the full suite under `-race` in `golang:1.26` with the Docker socket mounted for testcontainers. **No data races in 41 packages.**
- One failure: `TestIntentMatcher_AdversarialStressAndReDoS` asserts absolute wall-clock budgets (100 KB < 250 ms, 1 MB < 3 s); under `-race` it took 0.44 s / 6.4 s. Options: skip the subtest under race; run `-race` only on selected packages; **scale budgets by a build-tagged `timingSlack` constant** (chosen; `race_on_test.go` = 10, `race_off_test.go` = 1 — the stdlib's `internal/race.Enabled` pattern). Scaling keeps the assertion meaningful: real ReDoS is exponential, not a constant factor. Re-run twice under `-race` and once without: green.
- Risk noted: other wall-clock assertions passed under `-race` locally but could flake on slower shared CI runners; if one does, apply the same `timingSlack` pattern in that package.

## P5-4 — Dependency updates

### D5-4.1 Version policy
- Latest patch of the chosen release line, **digest-pinned** where it is an image, with Dependabot (below) proposing updates. Chosen: `node:24.22.3-alpine` (Node 24 = active LTS; 20 is EOL; 22 is already maintenance-only), `rabbitmq:4.3.7-management-alpine` (4.4.0 skipped: a brand-new minor for the one stateful broker), `gqlgen v0.17.98`, `vitest 5.1.0`. CI's Node moved to 24 to match the image.

### D5-4.2 RabbitMQ 4.3 — compatibility and a persistence bug
- Compatibility: all exchanges/queues are durable, non-exclusive classic queues with DLX arguments; 4.x removes classic mirroring and deprecates *transient* non-exclusive queues — neither is used. Integration tests (`internal/broker`, `tests/e2e`) now run on the same 4.3.7 image (they were on 3.12 while production ran 3.13).
- **Found a real persistence bug:** the compose service had no `hostname`, so the node name was `rabbit@<container-id>` (confirmed on the running stack: `rabbit@15fdd0d9ad5d`). Every container recreation (image update, `compose up` after config change) started a fresh empty node; queued and dead-lettered messages were stranded in the volume. Added `hostname: rabbitmq`.
- **Verified empirically** (scratch script, 4.3.7): create a vhost → `docker rm -f` → recreate on the same volume. Without hostname: vhost gone. With hostname: vhost persists.
- In-place 3.13→4.3 data upgrade: not needed and not attempted — the hostname change means the first 4.3 start is a fresh node anyway; topology is re-declared by the services. README documents the operator step (stop gateway, drain, export DLQs if wanted).

### D5-4.3 gqlgen 0.17.49 → 0.17.98
- First regenerated with the *old* version as a baseline: generated files were in sync (only CRLF), but `gqlgen generate` quarantined 22 hand-written helpers at the bottom of `schema.resolvers.go` under a "will be deleted" warning. Moved them byte-for-byte to `internal/graph/converters.go` (gqlgen's own recommendation), so regeneration is now stable.
- Upgrade: `generated.go` shrank ~9k lines (newer, more compact codegen); resolver file changed only in header/type-block formatting. gqlparser 2.5.16→2.5.64 came along.

### D5-4.4 Dashboard test toolchain
- vitest 3→5.1 (fixes the dev-only tinypool critical deferred in P5-3) needs vite as a peer: added `vite ^8.3.8` explicitly, `@types/node ^24`. `@vitejs/plugin-react` → **5.2.0, not 6.x**: 6 is a rolldown/oxc rewrite whose peer resolution conflicted; 5.2 supports vite 4–8 and nothing here needs 6. All 33 tests unchanged and passing.
- `npm audit --audit-level=high` is now clean for **all** dependencies (only moderate/low in dev tooling remain), so the CI full-audit step is now **blocking**.

### D5-4.5 Dependabot (not Renovate)
- Options: Renovate (more powerful grouping/automerge, needs an app install), **Dependabot** (native to GitHub, zero install; chosen). Ecosystems: gomod, npm (`/dashboard`), docker (`/`, `/dashboard`), docker-compose, github-actions. All PRs target `dev`; minor/patch grouped per ecosystem; majors individual. It understands the `@sha # vX` action pins and image digests.
- Not done (noted): the Go service base images (`golang:1.26-alpine`, `distroless/static-debian12`, `debian:12-slim`) and `redis:7-alpine` / `pgvector:pg15` are still tag-pinned, not digest-pinned — Dependabot will track tags; digest-pinning them is a small follow-up.

## P5-5 — Documentation drift

### D5-5.1 Generated regions in the docs, guarded by a test
- Options: (a) a separate generated `TOOLS.md`; (b) **marker-delimited regions inside SYSTEM.md/README.md** (chosen); (c) keep hand-written docs and add only a count check.
- (a) splits the blueprint AGENTS.md tells agents to read; (c) catches counts but not stale descriptions/params. (b): block markers `<!-- BEGIN GENERATED: name -->…<!-- END GENERATED: name -->` and inline `<!-- gen:name -->…<!-- /gen:name -->` (invisible when rendered; inline works inside table cells). Regions: `tool-catalog` (§6, per-module tables with tier, parameters, descriptions), `tool-tiers` (§3.2 Tier-1 list + Tier-2 domain groups), `tool-summary` / `tool-count` / `module-count` (prose).
- `go run ./cmd/devtools/gendocs` rewrites them (`-check` for a dry run). **`internal/toolcatalog` `TestDocsAreUpToDate`** runs in the normal `go test ./...`, so CI fails on drift without a new CI step. Line endings of each doc are preserved.

### D5-5.2 One module list, checked against main.go
- The tool list comes from `toolcatalog.ProductionModules()` (nil dependencies; `Declarations()` is pure in all 13 modules — verified). The reachability test's private copy now calls it, so there is a single list.
- Risk: someone wires a new module in `cmd/orchestrator/main.go` and forgets the list → docs silently incomplete. **`TestProductionModulesMatchOrchestratorMain`** parses main.go's AST and compares the `<domainpkg>.NewModule(` calls with the list. Options considered: moving module construction out of main into a shared package (cleaner, but a larger refactor of startup wiring — not worth it for docs); grepping main.go (brittle).
- Both guards mutation-tested: duplicating a module in the list → sync test fails; editing a tool description → docs test fails ("SYSTEM.md is stale: run gendocs").

### D5-5.3 What generation revealed / what was kept by hand
- The docs said **70** tools; the code declares **68** (13 modules + the registry's built-in `get_current_time`). The per-domain tier lists were accurate — only the headline counts had drifted.
- The old §6.2 table also carried knowledge the code cannot generate (what each module's `SystemPromptSection` injects; whether it emits `MutationEvent`s). Kept as a hand-maintained **"Module behaviour"** table directly after the generated region, minus its tool-list column.
- Counts inside the Mermaid diagram and the playbook prose were removed rather than generated (HTML comments are not safe inside Mermaid). Stale hand-written dashboard test counts ("31 tests") were dropped instead of updated.

### D5-5.4 README trimmed to overview + quick start (404 → 134 lines)
- Kept verbatim: Quick Start (Docker, local dev, RabbitMQ upgrade note) and Testing & CI. New: a short Highlights list, a Documentation index linking SYSTEM.md sections (all 10 anchors checked against GitHub's slug rules), compact required-env list in Quick Start, Prerequisites updated (Go 1.26, Node 24 — the old text said Go 1.24).
- Removed (all covered by SYSTEM.md — checked before deleting): ASCII architecture, pillars, per-domain feature detail, dashboard route table, security section, env-var table (all 22 vars present in §8.1), services table, project structure. Two details existed only in the README and were **moved into SYSTEM.md** first: request body limits (`MaxBytesReader` 10 MB webhooks / 1 MB GraphQL) and WhatsApp's 1,500-character chunking — both re-verified in code.

## Post-plan — rollout findings (2026-11-16)

### DP-1 Container infrastructure URLs never come from the host `.env`
- Found while preparing the first deploy of the remediated `dev`: P0 (`7a6e87c`) had changed compose to `RABBITMQ_URL=${RABBITMQ_URL:-amqp://…@rabbitmq:5672/}` (same for `REDIS_URL`, `DATABASE_URL`) so credentials were not hard-coded. But `env_file: .env` and `${VAR}` interpolation both read the same `.env`, and the owner's `.env` holds **host-side** URLs (`localhost`, for `go run`). `docker compose config` resolved the gateway/orchestrator to `localhost:5672` / `localhost:6379` — inside a container that is the container itself, so the new stack could not have started. CI missed it because `.env.example` used container hostnames; a P0 test even required the interpolation.
- Options: (a) tell owners to keep container hostnames in `.env` (breaks the documented `go run` flow, and the bug returns silently); (b) a separate override variable such as `DOCKER_RABBITMQ_URL` (an extra knob nobody needs — in this stack the broker/cache/DB are always the compose services); (c) **compose builds each container URL from the credential components (`RABBITMQ_DEFAULT_USER/PASS`, `POSTGRES_USER/PASSWORD/DB`) plus the fixed service name, and never interpolates the full URL** (chosen). `environment:` overrides `env_file:`, so the host URLs that `env_file` still injects are shadowed.
- The `*_URL` keys in `.env.example` now say "host runs only" and use `localhost`; the container side stays configurable through the credential variables, which also configure the broker/DB containers themselves, so the two can no longer disagree.
- Guard: `TestCompose_ContainerURLsTargetServiceNames` (every service's URL must target `rabbitmq`/`redis`/`postgres` and must not reference `${<same key>`); the P0 test's mechanism assertion was replaced by an intent assertion (URL built from the credential variables).
- Not changed: `migrate`/`audit-worker` still receive unused `localhost` `REDIS_URL`/`RABBITMQ_URL` via `env_file`; they never read them (their `requiredKeys` exclude them).

### DP-2 Google Calendar failures are reported, not swallowed (BL-1)
- Found during the first-deploy smoke test: `schedule` discarded every Google error (`calClient, _ := ...`; `if err == nil` merges), so a broken token or API outage looked identical to "no Google events" and creates were reported as "Sync enabled (offline/pending)" although nothing ever retries.
- Options for the failure path: (a) return a tool error (the model would get nothing — local events lost too); (b) log only (operator sees it, the user is still misled); (c) **degrade and say so: keep local results, append a user-facing note, and log a WARN** (chosen). For writes, (c) means the event is saved in MyPA and the result says the Google part failed — never "Synced ✅".
- "Not connected" is a normal state (never ran `/connect`), not an incident: it gets a note (with the `/connect` hint) but no WARN. That needed the factory to stop collapsing everything into `"unauthorized"`: new sentinel `orchestrator.ErrCalendarNotConnected` (message kept as `"unauthorized"` for text-comparing callers) for "no token"; read/decode failures are wrapped errors.
- The factory existed twice (`cmd/orchestrator/main.go` for production, `engine.go` for the engine/tests) with identical code. Options: fix both copies, or **one `orchestrator.NewCalendarFactory(getToken, oauth)` used by both** (chosen — the duplicate is exactly how one copy would have kept the old semantics). `workspace_calendar` now uses `errors.Is` and logs the broken-token case it also hid.
- Not done: retrying failed syncs (no queue/outbox exists; would be a feature). "Pending" wording removed instead of implementing it. Partner-sync semantics unchanged (first partner that succeeds wins; a failing partner is reported only if none succeeded).

### DP-3 Minimum Go version raised to 1.26.11 (first CI run, govulncheck)
- The first CI run on `dev` failed only in govulncheck: 21 standard-library advisories published after P5-3 affect go1.26.4 (fixes up to go1.26.11). `setup-go` reads the exact `go` directive, so CI built and scanned with 1.26.4. Production was **not** affected: the four running binaries report go1.26.11 (the `golang:1.26-alpine` build image had moved on) — checked with `go version` on each extracted binary.
- Options: (a) **raise the `go` directive to 1.26.11** (chosen: one minimum for CI, local builds and Docker; older local toolchains auto-download it); (b) add a `toolchain` line (whether `setup-go` honours it depends on the action version — fragile); (c) let CI use the newest 1.26.x (CI would then scan something newer than what developers build, hiding exactly this). Go 1.27 is a feature upgrade, left to Dependabot.
- Remaining: GO-2026-5932 in `golang.org/x/crypto` v0.58.0 — not reachable from our code (module-level only) and fixed only in an untagged pseudo-version. Not pinned to a pseudo-version for an unreachable finding; Dependabot will offer the tagged release.
- Lesson recorded: a green local scan ages — advisories keep being published, which is why CI runs govulncheck on every push.

### DP-4 WhatsApp delivery reports via a per-message status callback (BL-8)
- Problem: Twilio's REST API answers "accepted"; delivery fails later (63015 on 2026-11-16) and only Twilio's log knew. Our turn looked successful, so the user saw a silent bot.
- Where the callback URL comes from — options: (a) **explicit optional `TWILIO_STATUS_CALLBACK_URL`, sent as `StatusCallback` on every message** (chosen: visible in config, validated at startup, testable, unchanged behaviour when empty); (b) derive from `NGROK_DOMAIN` (couples the app to the tunnel); (c) set it once in the Twilio Console / Messaging Service (invisible to the repo, untestable, silently lost when the sender changes, and the sandbox has its own field).
- Receiver: gateway `POST /webhook/twilio/status`, behind the **same** fail-closed signature check as inbound (extracted into `Handler.authenticated`, so the two cannot drift). It never publishes to RabbitMQ and never logs content. Only `failed`/`undelivered` are WARN (with `error_code` and a hint for known codes: 63003, 63015, 63016, 63018, 63024, 63038); `queued`/`sent`/`delivered`/`read` are DEBUG — Twilio posts up to four reports per message, and logging them all at INFO/WARN would bury the one that matters. The proxy already forwards everything under `/webhook/twilio`.
- Not done: feeding failures back to the user (impossible on the failed channel) or to the conversation history (the model "remembering" an undelivered reply caused the BL-9 confusion — a possible follow-up), and storing statuses in the DB (logs are enough for a household deployment).

### DP-5 SQL logging through slog without bound values (BL-2)
- Problem: `gorm.Open(..., &gorm.Config{})` used GORM's default logger — coloured plain text on stdout (not the JSON slog stream), full SQL **with bound values** (phone numbers in `WHERE user_id = '+972…'`), and every `ErrRecordNotFound` printed as an error.
- Options: (a) silence GORM entirely (`logger.Silent`) — loses real SQL errors and slow queries; (b) write our own `gorm/logger.Interface` adapter onto slog — more code to own; (c) **GORM's built-in `logger.NewSlogLogger` (v1.31.2) with `ParameterizedQueries`, `IgnoreRecordNotFoundError`, `LogLevel: Warn`, `SlowThreshold: 200ms`** (chosen: maintained upstream, same JSON handler as everything else, placeholders instead of values).
- `ParameterizedQueries` works because GORM asks the logger's `ParamsFilter` before rendering the SQL, so the values never reach the log line at all (not redacted afterwards). Verified against a real Postgres: a failing query with a phone-number parameter logs `$1`; mutation (`ParameterizedQueries: false`) makes the test fail.
- The logger binds `slog.Default()` at `db.Open`; every service calls `logging.Setup()` first (checked for orchestrator incl. `-migrate-only`, and audit-worker). The test-only admin connections in `testutil`/`db_test` keep GORM's default logger — test output only.

### DP-6 Ignore every `.env` variant (BL-3)
- `.gitignore` had exactly `.env`, so `.env.bak`/`.env.local`/`.env.prod` (copies made while editing secrets) were committable. Options: list known names (misses the next one) or **`.env.*` + `!.env.example`** (chosen: deny by default, allow the one template). Guarded by `TestGitignore_IgnoresEveryEnvCopyButTheExample`, which asks `git check-ignore --no-index` rather than reading the file text.

### DP-7 WhatsApp bold is written only at the outermost level (BL-10)
- A heading containing bold rendered as `*📅 *Event Details**` (live reply, 2026-11-16): headings and `**strong**` each wrapped their text in `*`, and WhatsApp cannot nest identical markers. Options: strip `**` inside headings only (misses bold-in-bold), or **a bold-depth counter shared by headings and strong emphasis, writing `*` only on the outermost open/close** (chosen: one rule covers both). Italic (`_`) is unaffected — `*_x_*` is valid WhatsApp. Table tests cover heading+bold, bold-in-bold, heading+italic, and bold after a heading.

### DP-8 Quiet 404s on the public proxy (BL-6)
- Every browser OAuth callback page fetched `/favicon.ico` → `WARN proxy route not found`. Options: (a) answer only `/favicon.ico` with 204 (other scanner noise stays WARN); (b) only downgrade the log; (c) **both: favicon → 204 with no log, any other unknown path → DEBUG + 404** (chosen). The proxy is the ngrok-exposed endpoint, so random paths from the internet are routine traffic, not incidents; WARN is reserved for things an operator should act on (e.g. unauthenticated `/graphql`, which stays WARN).

### DP-9 Pin every remaining image as tag@digest (BL-7)
- Unpinned: `golang:1.26-alpine`, `gcr.io/distroless/static-debian12` (×3), `debian:12-slim`, `redis:7-alpine`, `pgvector/pgvector:pg15`. Floating tags let a rebuild pick up different bits silently — exactly how the build image had moved to go1.26.11 unnoticed (DP-3).
- Which digest: (a) the image currently running/pulled locally, (b) **the registry's current digest for the exact version tag** (chosen), (c) the newest tag. pgvector: (a)=(b) — `0.8.6-pg15@a947c4…` is byte-identical to the running database image (Postgres 15.19, pgvector 0.8.6). Redis: the running 7.4.11 build (`ff02b5…`) has since been rebuilt under the same tag on a patched Alpine (`d94363…`); pinned to the rebuild — same Redis version and AOF format, newer OS packages — rather than freezing an older base. Go `1.26.11-alpine3.24` matches the `go` directive. Debian `12.15-slim`. Distroless has no version tags → digest only. (c) rejected: version upgrades are Dependabot PRs, not a side effect of pinning.
- Guard: `TestAllExternalImagesArePinnedByDigest` covers every compose `image:` and every Dockerfile `FROM` that is not an earlier stage (both Dockerfiles), replacing the ngrok/pgadmin-only check's scope. Not updated: `docs/architecture_deep_dive.md` (older snapshot, already stale elsewhere) and `.agents/teamwork` (left alone by owner decision).

### DP-10 Summaries are scoped to the latest message (BL-9)
- Root cause found in code, not only in the model: after tools ran, the engine asked for "a final summary of **all your actions**" (and, at the 8-step limit, "all actions completed **so far**"). Since P2-9 the history also contains earlier turns' tool calls, so the model obeyed literally and recapped a calendar read from an earlier turn as if just done.
- Options: (a) drop earlier turns' tool calls from the history again (undoes P2-9: "delete that event" needs the IDs); (b) label replayed tool messages as "earlier turn" (a model-format change across `models.ChatMessage`/Gemini conversion, for the same effect); (c) **scope the two summary prompts to "the user's latest message" and add one standing rule to `staticInstructions`** (chosen: fixes the instruction that caused it, smallest change, keeps P2-9). Putting the rule in the static prefix keeps it cache-friendly (P2-6); the one-time prefix change costs a single cache miss.
- Test: the prompts must not contain "all your actions"/"so far" and must reference the latest message and earlier turns; the system prompt carries the rule in its static prefix. A unit test cannot prove model behaviour — this checks the instructions we control; confirm in live use.

### DP-11 Every env_file service overrides all infrastructure URLs (BL-4)
- After DP-1, `migrate` (RABBITMQ_URL, REDIS_URL) and `audit-worker` (REDIS_URL) still inherited the host's `localhost` values via `env_file`; unused today, a trap for the first code path that reads them. Options: (a) drop `env_file` from `migrate` and list its keys explicitly (it would also lose keys the shared config loader may read, e.g. for validation, and needs upkeep per new key); (b) **declare all three URLs on every service that loads `.env`** (chosen: one uniform rule, three lines). Guard: `TestCompose_EnvFileServicesOverrideEveryInfraURL` (fails if any env_file service lacks an override), complementing `TestCompose_ContainerURLsTargetServiceNames` (overrides target service names). Verified with `docker compose config`: all four services resolve to `postgres`/`rabbitmq`/`redis`.

### DP-12 Inbound queue renamed to `inbound.messages`; queue names are constants (BL-5)
- `telegram.inbound` carried WhatsApp too, and the name was a string literal in two `main.go` files (same for `audit.events`), so publisher and consumer could drift apart silently. Options: (a) leave it (the backlog first suggested waiting for a broker change, ADR 0001); (b) rename and keep literals; (c) **rename and move both queue names into `broker.InboundMessagesQueue` / `broker.AuditEventsQueue`** (chosen, owner asked to do all backlog items). `audit.events` keeps its name (accurate, no migration).
- Guard: `TestServicesUseQueueConstants` parses every `cmd/*/main.go` and fails on a string literal passed to `broker.NewSafePublisher`/`NewConsumer`/`NewConsumerWithOptions`; `TestQueueNames` pins the values.
- Migration cost: a rename strands anything left in the old queue, so the deploy is drain-and-switch (stop gateway → old queue at 0 → `up -d` → delete the empty `telegram.inbound` + `.dlq`), documented in the README upgrade notes. Not done: a dual-consume transition (consuming both names for a release) — overkill for a single-host deployment where draining takes seconds. ADR 0001 and `docs/architecture_deep_dive.md` keep the old name as historical documents.

### DP-13 Integration tests get a throwaway database, never the compose one (BL-11)
- Six helpers defaulted to the compose DSN on `localhost:5432`, i.e. the live database; locally they wrote fixtures into it (and ran AutoMigrate), in CI they skipped. Options: (a) require an explicit `TEST_DATABASE_URL` and refuse the name `mypa` — still opt-in/skipped by default and a deny-list; (b) a separate `mypa_test` database on the same server — still touches the live instance, its roles and extensions; (c) **hermetic testcontainers: one container per test binary, one empty database per test** (chosen) — cannot reach production by construction, and the tests now actually run in CI.
- There were already two copies of (c): `testutil.NewTestDB` and a private one in `internal/db/db_test.go` (needed because `testutil` imports `db`, so `db`'s own tests cannot use it). Merged into one primitive, `internal/testutil/pgtest` (no `db` import): it keeps `testutil`'s 3-attempt start and serializes `CREATE/DROP DATABASE` (concurrent copies of `template1` fail). `testutil.NewTestDB` delegates to it, and the six call sites use `testutil.NewTestDB`.
- Image: the exact prod pin `pgvector/pgvector:0.8.6-pg15@sha256:a947…` instead of the floating `pgvector:pg16` both copies used — tests now exercise production's Postgres major version; `TestImageMatchesCompose` keeps them in lock-step.
- Missing Docker fails the test instead of skipping: a skip looked like a pass, which is how this went unnoticed. `NewTestDB`'s cleanup still drops immediately (`pgtest.DropDatabase`, refuses non-`testdb_` names); the drop at test end is idempotent.
- Guard `TestNoTestReachesTheLiveDatabase` scans every `_test.go` for `localhost|127.0.0.1:5432/mypa` and `os.Getenv("DATABASE_URL")`. Not chosen: an AST check of every `db.Open`/`NewClient*` argument — more precise but brittle, and the string guard already covers how the bug was written.
- Cleanup of the leftovers is a destructive production operation → OP-9 (owner approval), not part of the code change.

### DP-14 A caller without a family group is its own tenant; "all tenants" is explicit (DE-2)
- Logged as an owner decision (DE-2) because it "changes visibility of existing rows". Measured first: 0 of 156 users lack a family group and the `"default"` tenant holds 0 rows in all 20 tenant tables, so no one's view changes — the decision only matters for future users, which makes it a design choice I can make and log.
- Found while reading: worse than logged. `ActivePairingCodes` and `MutationLogs` skipped the family filter when the tenant was `"default"`, so a caller without a group listed **every** family's pairing codes (enough to join another family) and mutation logs (before/after JSON). Reachable only with a hand-minted JWT today (nothing issues JWTs; the dashboard uses its tenant-bound secret), but fail-open.
- Options: (a) keep the shared `"default"` tenant — two group-less users share data; (b) **per-user tenant = user ID** (chosen) — identical to the orchestrator's `CallContext.TenantFamilyGroup`, so the dashboard shows exactly what the bot stored for that user; (c) refuse callers without a group — safest, but the bot fully supports group-less users and the dashboard would then be unusable for them.
- With (b): a caller with neither user ID nor group is refused (the old `actorID = "user"` fallback would otherwise create one shared tenant `"user"`); a global admin without a target also lands in its own tenant instead of `"default"`.
- Cross-tenant reads: one helper `wantsAllTenants` (global admin + no group or `"*"`) — the pattern `AuditSessions`/`UserSessions` already had inline — now also used by `ActivePairingCodes`/`MutationLogs`. Consequence: a global admin without a target now sees all families' codes/logs (as it already did for sessions) instead of only its own claim group; tenant-bound admins and members are unaffected. `"*"` is rejected as a tenant by `resolveTenantAndActor`, so no write can land in a family literally named `*`.
- `SaveUserPreference` logged its mutation event under `"default"` (invisible to the family's undo/history); it now uses the caller's tenant. That equals the target's family for members and tenant-bound admins (both can only target their own family); only a global admin editing another family's user logs it under the admin's tenant — accepted (no JWT issuer exists; a target-family lookup would add a DB round-trip for a path nobody uses).

### DP-15 Test containers run exactly the compose images (BL-12)
- The backlog item named one image (`redis:7-alpine` in `testutil`); the same flaw existed for RabbitMQ in `internal/broker` and `tests/e2e_test.go` (tag without digest). Options: (a) one exported constant per image plus a sync test each (the `pgtest.Image` pattern) — three constants, three tests, and nothing stops the next test from adding a fourth literal; (b) **one guard over the whole tree** (chosen): `TestTestContainerImagesMatchCompose` parses every `.go` file and requires each string literal that is an infra image reference (`redis|rabbitmq|postgres|pgvector/pgvector` + tag/digest) to equal a `docker-compose.yml` image; literals stay where they are used.
- The regex separates image references from look-alikes it met in this repo: URLs (`redis://…` — a tag never contains `/`) and `host:port` strings in the netguard tests (`redis:6379` — a port is all digits, a tag has a non-digit). `pgtest.Image`'s own sync test stays (it documents why that constant exists). Not chosen: matching only arguments of testcontainers calls — tighter, but it misses images passed through variables or request structs (`testcontainers.ContainerRequest{Image: …}`).

### DP-16 No committed file carries a number that could reach a person (BL-13)
- Scope options: (a) fix `.env.example` only; (b) a test over `.env.example` only; (c) **a guard over every git-tracked file** (chosen) — the risk is a family member's number in any committed file (example, doc, fixture), and the pre-push scan had to find this one by hand. Scanning `git ls-files`, not the working tree: the untracked `.env` holds the real numbers, and a failing test must never print one — failures show the number masked (`+972•••…`).
- What counts as "could reach a person": (a) everything outside the fictional range plus an allowlist — tried first, it flagged ~10 obvious test dummies (`+1234567890`, `+15550001`) and would grow with every new test; (b) **structure** (chosen): a `+1` number is a risk only if it is dialable NANP (10 national digits, area code and exchange not starting with 0/1) and outside the reserved fiction block NXX-555-0100…0199; any other country code counts as real (no per-country rules — a dummy there should use the fictional range instead). One named exception: Twilio's published WhatsApp sandbox sender (`.env.example`, `config.go`, `SYSTEM.md`).
- Correction: BL-13's text proposed "`+1555010xxxx` like the SQL-logging test". That is area code 555, exchange 010 — harmless because it is not dialable, but not the reserved range. The new example uses `+1 202 555 0101/0102`; the SQL test stays (it passes the guard as non-dialable).
- Base64 runs in `go.sum`/`package-lock.json` contain `+` followed by digits; the pattern requires a non-alphanumeric character before `+`, which every real occurrence here had (quote, space, colon).

### DP-17 Channel conformance suite, stage 1: real edges, fake transport, pinned transcripts (V4-34)
- **Scope of stage 1.** The roadmap row says the item "starts in stage 1 as tests that pin down today's Telegram and WhatsApp behaviour, and is completed when the adapters exist (V4-31)". So this branch delivers the scenarios and the pins, and the item stays `In progress`. Left for V4-31, because both need the adapter interface the channel ADR defines: the rule that an adapter cannot be registered without passing, and the capability matrix generated from adapter declarations. Not done here on purpose: a stand-in registry or a hand-declared capability list would decide the adapter interface before its ADR.
- **Where the fake goes.** Options: (a) fake the engine's `TelegramClient`/`TwilioClient` ports — easy, but the formatting, chunking and request building inside the real clients would not run, and those are exactly what a port to adapters can break; (b) point the real clients at an `httptest.Server` by rewriting URLs — what the clients' own tests do, but it needs access to an unexported field and a real listener; (c) **the real clients on a fake `http.RoundTripper`** (chosen) — no socket, the request is inspected exactly as built, credentials are checked on every call, and any request to another host fails the test.
- **The seam.** The clients' HTTP client is unexported, so a suite in another package needs a way in. Options: swap `http.DefaultTransport` (global, racy, and affects every other client in the test binary); move the suite into each client's package (then it cannot drive the engine); **a `WithHTTPClient` constructor option on `telegram.NewClient` and `twilio.NewClient`** (chosen: existing callers compile unchanged, and the client is still immutable after construction). `twilio.DownloadMedia` keeps its HTTPS and host checks whatever client is injected.
- **Its own package, black-box.** `internal/conformance` uses only exported API (`orchestrator.NewEngine`, `ProcessMessage`, `CheckAndSendReminders`, the registry), so the suite survives the refactor it guards. Inside `internal/orchestrator` it could reach private state and would break when V4-31 moves it.
- **Fakes, not containers.** An in-memory `DBClient` and miniredis instead of testcontainers Postgres: the subject is the channel edge, the repositories have their own integration tests, and the suite stays fast enough (well under a second) to run on every change. The model is a script that fails the test on an unscripted call, which also proves when the model is *not* consulted (pairing, approvals, undo, reminders).
- **Transcripts plus outcomes.** Golden files pin the bytes each platform receives; assertions state the outcome in channel-neutral terms. `long_reply` has no transcript: where a channel cuts is its own business, while losing, reordering or corrupting text is not, so it is checked by properties. Transcripts are compared after normalising CRLF (Windows checkouts) rather than through a `.gitattributes` rule.
- **Telegram ingress.** The suite enters through `telegram.ParseUpdate`; the webhook's secret-token check sits in `cmd/gateway` (package `main`, not importable) and keeps its own tests. Moving that handler is V4-31's job.
- **Checked that the suite can fail:** raising the WhatsApp chunk size above the platform limit fails `long_reply`; disabling the untrusted-turn gate fails `approval` and `media_photo` on both channels. Both mutations were reverted.
- **Pinned as found, not fixed:** documents on Telegram get no answer (BL-18), reminder labels render italic (BL-19), a person who never wrote gets no reminder (V4-33). The WhatsApp splitter's byte cut (BL-17) is not reachable from the scenario text and is logged separately.

### DP-18 `/doctor`: checks as plain functions, scoped to the admin's family, healthy means no failures (V4-17)
- **Shape.** A pure package (`internal/doctor`) of independent checks whose inputs are small functions, plus thin wiring in `cmd/orchestrator/doctor.go`. Rejected: checks that hold the real clients (each failure would need Redis, RabbitMQ, Postgres and Telegram to reproduce), and putting the checks in `internal/orchestrator` (the engine would grow knowledge of queues, backups and migrations it does not otherwise have). The engine sees one port, `HealthReporter`.
- **Whose Google accounts.** Options: (a) every user of the install, because today's `admin` can already back up and restore the whole database; (b) **only the admin's own family group** (chosen). The install-level checks describe machinery; this one names people. With several households per install coming (DE-7), an admin of one must not see who belongs to another or whether their sign-in works. When V4-35 adds a separate operator role, an install-wide view can be given to that role. An admin without a family group is checked alone, and the member lookup is never made with an empty group (it would match every group-less user).
- **How to tell that a Google sign-in is dead.** Options: (a) look at the stored expiry: the access token expires every hour by design, so this says nothing; (b) let the token source decide: it only calls Google once the access token has expired, so a revoked refresh token goes unnoticed for up to an hour; (c) **force a refresh on every run** (chosen). It is the only test that catches the case behind BL-1, a refresh token that Google expired or the user revoked. Cost: one request per family member per `/doctor`. The renewed token is thrown away and Google does not invalidate the previous access token, so the check changes nothing a member would notice. Only Google's refusal (`invalid_grant`) is reported as "expired or revoked" and sends the member to `/connect`; a rejected OAuth client and an unreachable Google are reported as what they are, with their own next steps, because signing in again fixes neither. Members are checked side by side with a time limit each, so one slow answer cannot make the rest look broken. (The last two points came out of review: the first version called every refresh error "expired".)
- **Healthy means no failed check.** Warnings (Twilio sandbox, no delivery reports, an unencrypted backup, an old webhook error) are shown with a next step but do not make the report unhealthy. Otherwise an install on the sandbox, which is the documented default, could never be healthy, and V4-43 needs "`/doctor` reporting healthy" after a restore.
- **WhatsApp is read from settings only.** The mode setting belongs to V4-41, which says it will make `/doctor` report the mode and its health. Until then the check recognises Twilio's shared sandbox number (OP-8) and a missing status-callback address (BL-8). It does not call Twilio.
- **Queue depth.** Options: (a) the RabbitMQ management HTTP API: another port, another credential and a new setting; (b) the engine's existing consumer channel: a passive declare on a missing queue closes the channel, which would stop message consumption; (c) **a short-lived connection with a passive declare per queue** (chosen): read-only, cannot create a queue, cannot disturb the consumer. The whole probe is bounded by the caller's context: a deadline on the socket for the handshake (a custom dialer replaces the one in the AMQP library that would set it) and closing the connection when the context ends, so a broker that accepts and then goes silent cannot leak a goroutine per `/doctor` run (found in review).
- **Schema version without side effects.** Rather than depend on what goose's own version call does on a database that lacks its bookkeeping table, `db.SchemaVersion` looks for the table first and reports version 0 when it is absent; a test asserts the table is not created. A health check never changes the schema.
- **Thresholds are constants, not settings:** backup older than 26 hours (nightly run plus slack), 20 waiting messages, webhook error windows of 15 minutes and 24 hours. The install already has about forty settings (V4-24 exists to hide them); these can become settings if a real install needs different values.
- **One slow or broken check must not hide the rest.** Checks run concurrently with a 15-second limit each; a panic or a timeout becomes a failed line with a next step. A problem reported without a next step gets a generic one, so the rule "every failure says what to do" holds even for a future check that forgets.
- **Not strictly read-only.** Reading a token through `state.Store.GetOAuthToken` refills the Redis cache from the Postgres copy when Redis has lost it, exactly as any other read does. That is cache state, not user data; a separate non-refilling reader was not worth a second code path.
- **What is not stored or logged.** The report can name family members, so it is not recorded as the turn's final response in the audit log, and the INFO log line carries counts only. Error text shown to the admin goes through the clients' existing redaction (bot token) and never includes the broker URL.
- **"Reads correctly on every channel"** is proven by a conformance scenario (`doctor`) that runs the real `doctor.Doctor` over a fixed, partly broken install and pins the Telegram and WhatsApp transcripts, rather than by a rendering unit test: the report passes through the same formatter and splitter as every other reply.
- **Not in this item:** the dashboard page (V4-37), the WhatsApp mode and sender status from Twilio (V4-41), an off-machine backup check (OP-11 is only stated in the backup line).

### DP-19 Restore and migrate: a backup is a set of files, restored into an emptied schema, then migrated (V4-43)
- **Scope of stage 1.** The roadmap row says "the mechanism is built first and the item is marked done at the V4.0 release", and its "done when" needs a restore on a different machine. This branch delivers the mechanism, its tests and the runbook; the item stays `In progress`. The owner's checks are listed at the end of `docs/RESTORE_AND_MIGRATE.md`. Not built here because it does not exist yet: switching packs on after an upgrade (V4-27), and conversation archives (V4-12), which will ride in the same archive bundle.
- **What a backup is.** Options: (a) one container file (tar inside age) holding the dump, the archives and a manifest: one thing to copy, but a tar entry needs its size up front, so the dump would have to be spooled to disk in plaintext or split into chunks, and the pre-V4 baseline backup would need a second restore path; (b) **a set of files sharing a timestamp: the existing `backup_<ts>.dump.age`, an `archives.tar.age` and a plain `manifest.json`** (chosen). The dump is byte-for-byte what earlier releases wrote, so a bare baseline dump and a V4 set go through one import; everything streams with no plaintext on disk; the manifest is written last, so an interrupted run is recognisable. Cost: three files to copy instead of one.
- **The manifest is checksums, not a signature.** age with public-key recipients lets anyone who has the public key produce a valid encrypted file, so the manifest cannot prove who wrote a set. It guards against truncation, corruption and mismatched files, which is what actually happens to copied backups. Someone who can write to the backup directory could already replace a dump before this change; signing sets is a separate decision that needs a signing key and belongs with the off-machine target (OP-11).
- **Restore into an emptied schema, then migrate.** Options: (a) keep `pg_restore -c` as `/restore` does: it drops only what the dump contains, so an older dump restored over a newer schema keeps the newer tables and the following migrations fail on them; (b) drop and recreate the database: needs a second connection to a maintenance database and the right to create databases; (c) **`DROP SCHEMA public CASCADE`, recreate it, `pg_restore --single-transaction --no-owner --no-privileges`, then `db.Migrate`** (chosen). The result is exactly the dump, the restore is all-or-nothing, and a dump taken under one database role restores under another (a move usually changes credentials). `--no-migrate` exists for the one case where the schema must stay as backed up: rolling back to the release that took the backup.
- **Never replace data silently.** A target that holds users is refused unless `--replace`; with it a safety backup is taken first and the restore is abandoned if that fails (the rule `/restore` already follows). A test restores the safety backup to prove the way back works.
- **The queues are checked, and the tool says what that does and does not show.** Export requires both work queues empty and without consumers; import requires no consumers on the target. That makes "drains the queue before the backup" something the tool enforces, and it proves the orchestrator and the audit worker are stopped. It does not prove the gateway is: the gateway only publishes, and seeing it would need the broker's management API (another port, credential and setting) or a network probe of the gateway from the tool. The first version printed "the app is stopped" and recorded `stopped: true`; review pointed out that this claims more than was checked. The manifest field is now `drained`, the export prints what it checked and what it cannot see, and stopping the gateway first is a step of the runbook. A missing queue (an install never started) counts as idle. `--skip-stop-check` exists for the case where the broker itself is gone, and the manifest then records `drained: false`. Dead-lettered messages do not block an export (they are not work in flight), but they do not travel either: the export counts them and says so.
- **Nothing is changed until the backup has been read to its end.** The first version only checked that a backup key was configured, then took the safety backup and emptied the schema before `pg_restore` first tried the key. With `--replace` and the wrong key file that emptied a populated database for nothing (found in review). The import now decrypts the dump and runs it through `pg_restore -f -`, which converts the archive to SQL without a database and fails on a damaged or truncated one, and walks the archive bundle, before the target is examined. For a bare dump, which has no manifest and so no checksum, this read is the only integrity check. Cost: the backup is read twice.
- **A restored install gets a backup of its own.** After a successful import the tool takes a backup set of the restored, migrated database. Until the first nightly run the only backups in the directory would otherwise be the ones brought in, at the old schema version, and `/doctor` would rightly call the newest backup stale. It is taken without pruning.
- **Google sign-ins.** The roadmap says they are restored from their encrypted copy in the database. That copy is written when someone finishes `/connect`, so a sign-in older than that feature exists only in Redis and would be lost. Export copies such sign-ins into the database first and reports the count, and refuses to run without the cache unless told the cache is lost. It never overwrites an existing copy. This is a write to the source database, limited to that table.
- **The data key is checked after a restore.** A restore with the wrong `ENCRYPTION_KEY` looks fine until the first calendar request. Import tries the key on the stored secrets and, if it fails, says so and exits non-zero while leaving the restored data in place: only `.env` needs fixing.
- **Key export.** Options: (a) document "copy `ENCRYPTION_KEY` from the old `.env`": simple, and how keys end up in chat messages; (b) put the keys in the backup set: a set and the keys that open it must not be one file; (c) a separate file encrypted with a generated passphrase (age, scrypt): the first version, and review pointed out that a printed passphrase sits in Docker's log file on the same disk as the key file until the container is removed (the log driver cannot be switched off for this one service without weakening the log-rotation guard); (d) **a separate file encrypted to the backup key** (chosen as the default). The backup key's private half is already kept off the machine (OP-1) and has to be brought to a restore to open the backup, so locking the data key with it adds nothing to carry and shows no secret anywhere. The passphrase mode stays, behind `--passphrase`, for installs without a backup key and for carrying the private backup key itself (which cannot be locked with itself); its output now says where else the passphrase ends up. `import-keys` reads a passphrase from a file, never from the command line, and writes settings lines to a `0600` file rather than to the screen.
- **Where the tool lives.** A subcommand of the orchestrator binary (`orchestrator transfer`), like `-migrate-only`, run as a compose service under the `tools` profile. A new binary would need its own image stages for one occasional command, and the orchestrator image already carries the matching `pg_dump`/`pg_restore`. It runs as its own process next to a stopped app, never inside the engine: a restore must not share a connection pool with a running assistant.
- **Nightly backups and `/backup` now write sets.** "A backup is the database dump plus the archive files" would otherwise be true only for exports. The orchestrator gets the archive volume read-only; the audit worker stays the only writer. The whole archive directory is bundled every night; it is small for a household and V4-12 revisits the cost when conversation archives arrive.
- **Found while building: backup names could collide.** Names have one-second resolution and the dump was renamed into place, so a second backup within the same second (here: a safety backup right after a set was copied in) silently replaced the first. A backup now takes the next free name, and the name counts as taken if any file of a set uses it.
- **Found while building: the safety backup could delete the backup being restored.** Every backup applied the retention policy when it finished. The safety backup taken by `import --replace` did too, so restoring an old set from a directory with enough newer backups pruned that set after verification and before the restore, leaving an emptied database and no set. Reproduced in a test (forty newer backups), then fixed with `BackupOptions.SkipRetention` for the safety backup. Retention still runs for nightly backups, `/backup` and exports.
- **Testing with the real tools.** The existing backup tests replace `pg_dump`/`pg_restore` with a fake, and no CI job has the matching client version. `pgtest.ToolRunner` runs both inside the throwaway Postgres container (the production image), so the end-to-end tests exercise the real dump, restore, extension and vector index without depending on what is installed on a runner. A real RabbitMQ container backs the stop check and the queue checks of `/doctor` in the baseline test, and a second superuser role stands in for another machine's database role. `testdata/baseline_v3.4.0.dump` holds at least one row in each of the 29 tables (counts in `baseline_v3.4.0.counts.json`); the baseline test requires every row to survive the restore and the migrations, and the production `/doctor` wiring to report no failure. A V4 migration that deliberately moves baseline rows records the new count in `baselineCountChanges` in the same change. The dump was generated while the schema on `dev` was still identical to `v3.4.0` (checked with `git diff v3.4.0 dev` on the migrations and models); the generator refuses to run once the schema has moved on.
- **Tests were written alongside the code for the transfer package**, not strictly before it as for the earlier items; the backup-set primitives, the probes and the compose service did get their tests first.
- **Logged from the second review, not changed after it passed:** the rollback test and the piped form of the dump check (BL-22), and that a live backup now fails as a whole when its archive step fails (BL-23). Two documentation gaps from the same review were corrected: the runbook now says that only an export carries Google sign-ins that lived in the cache alone, and the settings table lists the tool for the two keys it reads.
- **Left as is:** the chat command `/restore` (BL-21), and encryption of the archive files at rest on the volume (OP-11); inside a backup set they are encrypted.

### DP-20 Removing `remember_fact` means moving its embedding into `save_topic_memory` (V4-06)
- **The premise was half right.** The roadmap row says `remember_fact` duplicates `save_topic_memory`. Both write a `TopicMemory`, and since P2-4 `remember_fact` was documented as an alias. But it was the only code path that called `GenerateEmbedding`: `save_topic_memory` stored a zero vector. The memory retrieval at the start of every turn orders by vector distance, so `remember_fact` was, in practice, the only tool whose memories were recalled by meaning.
- **Options.** (a) Remove the tool as the row literally says ("the tool is gone and the generated catalogue is updated"): met to the letter, and every fact saved from then on would be invisible to automatic recall. (b) Stop and propose a roadmap change: the row's outcome is right, only its reasoning is incomplete, and the fix is small and has one obvious shape. (c) **Remove the tool and make `save_topic_memory` store the embedding** (chosen): this is what makes the row's premise true. It does not change the item, its "done when" or any decision; it is how the item has to be implemented to not lose a behaviour.
- **When the embedding cannot be computed.** `remember_fact` refused to save ("embedding error"). `save_topic_memory` never depended on the embedding service, and a fact saved without an embedding is still found by keyword search and when pinned. Chosen: save anyway, log a warning without the fact's text, and tell the user nothing. Rejected: failing the save (loses the user's fact because of an outage), and a retry queue (new machinery for a rare case; the backfill of BL-24 covers stragglers).
- **Cost.** One embedding call per saved memory, which `remember_fact` already made. Nothing changes for reads. One side effect: the undo snapshot of a saved memory (`mutation_logs.after_json`) serialises the whole record, embedding included, so it now holds 3,072 real numbers where it held 3,072 zeros. That keeps an undone-then-restored memory searchable, at the price of a larger row per save (BL-25).
- **Guidance and protections carried over.** The prompt line that told the model to save what the user says about themselves (and facts seen in images) now names `save_topic_memory`. `save_topic_memory` was already a core tool and already in the confirmation gate for untrusted turns, so nothing protective is lost; a test asserts both. The declared `scope` parameter, the original BUG-01 fix made on `remember_fact`, is asserted on `save_topic_memory`.
- **Tests.** Tests that used `remember_fact` as "a core, gated memory tool" now use `save_topic_memory`. The anti-spoofing sub-test for `remember_fact` was folded into the `save_topic_memory` one with every assertion it made. Three counts that included the tool dropped by one (core tools 8 to 7, total declarations 67 to 66, the baby selection 13 to 12). Every assertion a removed test made was carried over: the static-list checks on `remember_fact` (object and string types, a required `fact`, a declared `scope`, and `scope` in the serialised form) are now made on `save_topic_memory` in the memory module's tests. (The first version of that test dropped the type and serialisation checks; review caught it.)
- **Old data.** Rows written by `remember_fact` stay where they are (`topic_memories`, topics `personal` and `family`) and keep their embeddings; no migration. Mutation-log entries with entity type `Memory`, which `remember_fact` wrote, are untouched. They were never undoable: `Memory` is not in the rollback allowlist and their entity ID was not a row ID. Saves through `save_topic_memory` write `TopicMemory` entries, which are undoable, so removing the tool also removes a write that could not be undone. Rows written by `save_topic_memory` before this change have no embedding: BL-24.
- **Not verified here:** how the model behaves when a chat history from before the upgrade still contains `remember_fact` calls (the 24-hour window in Redis). The engine replays history as recorded; confirm on the first day of live use, or let the window lapse before upgrading.

### DP-21 One default chat model, defined in `config.DefaultGeminiModel` (V4-09)
- **What was found.** The default was written in four places: `v.SetDefault` in `internal/config/config.go`, `config/config.yaml`, the fallback in `llm.NewClient`, and `.env.example`. They agreed with each other (`gemini-2.5-flash`) but not with reality, and they were not equally in charge: `config.yaml` is copied into the orchestrator image, so its value overrode the code default, and `.env.example` set the variable explicitly, so anyone who copied the template pinned the old model without choosing to.
- **Which model is "current".** The live install's own setting: `GEMINI_MODEL=gemini-3.6-flash` (read from the local settings file as that single non-secret value). It is the model the owner chose and runs, so it is the best evidence of what the default should be. No test can show that a model name is current; tests show only that the default is one name in one place. Whether this is the right name is the owner's call, and changing it is a one-line edit of the constant. Rejected: asking the API for the newest model at build time (`cmd/devtools/listmodels` needs the owner's key, and "newest" is not "the one this system was tuned on").
- **Where the one place is.** Options: (a) `config.yaml`: it already won in the image, but the LLM client and `go run` outside the image do not read it, so code would still need a fallback; (b) **a constant in `internal/config`** (chosen): every path goes through it. `SetDefault` uses it, the client's fallback uses it, `config.yaml` no longer has a `gemini.model` key, and `.env.example` shows the setting commented out and says where the default lives. `internal/llm` now imports `internal/config`; there is no cycle (`config` imports only `models`).
- **Keeping it one place.** `TestDefaultChatModelIsDefinedInOnePlace` searches the files that can decide which model an install runs (production Go, YAML, JSON, the settings template, Dockerfiles, dashboard sources) and requires exactly one chat-model name, the constant. Tests and documentation are excluded: a test may name any model. It walks the source tree itself rather than asking git for the tracked files: the first version did ask git, and review found that it was skipped in every full run of the package, because an earlier test clears the environment and `git` was then not found (the same has been happening to two existing guards, BL-27). A guard that can skip is not a guard. The pattern matches a model written either way, as the API name (`gemini-2.5-flash`) or as the display name (`Gemini 2.5 Flash`): the second review found that the dashboard's settings page showed "Gemini 2.5 Flash" as the selected model in a dropdown with no handler behind it, which the first pattern (API names only) did not see. That row now says where the model is set and names none; a dashboard test keeps it so. A second test loads a copy of the settings file laid out as in the image and requires the constant to be what an install gets; one unrelated value of the copy is changed to a number no default has, so the test fails if the file is not actually read. A third requires SYSTEM.md's settings table to show the constant's value, so the one sentence of documentation that states the default cannot drift.
- **Effect on installs.** An install that sets `GEMINI_MODEL` is unchanged. One that does not moves from `gemini-2.5-flash` to `gemini-3.6-flash` on upgrade. The live install sets it explicitly, to the same value.
- **The dashboard row.** Options: (a) show the real default there: the dashboard has no query for it, and a copied string would be a second place again; (b) leave the dropdown and only rename its options: it would still offer a choice that does nothing; (c) **replace the dropdown with a line saying the model is set on the server with `GEMINI_MODEL`** (chosen). Choosing a provider from the dashboard is V4-02 and V4-37; the two other placeholder switches on that page are left as they are.
- **Not touched:** the embedding model (`gemini-embedding-001`), which is its own setting and the subject of V4-03, and the provider layer (V4-02), which will replace a single Gemini default with a per-provider one.

### DP-22 One binary with roles: each role keeps the surface of the service it replaces (V4-01)
- **What was decided earlier.** ADR 0001 option 1c (DE-1): merge `proxy`, `gateway` and `orchestrator` into one binary with `--role`, default `all`, and keep the durable queue hand-off. The `audit-worker` stays separate until V4-23.
- **Package layout.** Options: (a) extract the gateway and orchestrator wiring into new `internal/` packages and keep `cmd/mypa` thin; (b) **move the three `main` packages into one `cmd/mypa` package with `git mv`** (chosen): `edge.go` (the proxy), `ingress.go` (the gateway), `worker.go` (the orchestrator's wiring), `main.go` (flags, roles, server, shutdown). ADR 0001 says the merge is about `main`, not the domain code; (b) changes no package outside `cmd/` and keeps every moved file's history and test.
- **The HTTP surface per role.** The public endpoint must not grow. Options: (a) one router with every route for every role; (b) **each role serves exactly the surface of the service it replaces** (chosen). `all` serves the proxy's edge (same prefixes, the same 10 MB / 1 MB body limits, the 401 guard on `/graphql` without an `Authorization` header, `/favicon.ico` 204, everything else 404), handing requests to the in-process ingress and app routers instead of forwarding them. `ingress` serves the gateway's router (webhooks and `/health`); `worker` the orchestrator's (`/graphql`, the OAuth callback, `/playground` outside production). Under (a), `/playground` and `/health` would have become reachable through the tunnel; the proxy never routed them, and the roadmap requires an ADR for a new unauthenticated surface. So under `all` the playground is gone in every environment; a developer who wants it runs `--role=worker`. `TestRouterForRole_EachRoleKeepsTheSurfaceOfTheServiceItReplaces` pins all three.
- **The hand-off stays a queue.** The webhook handler in `all` still publishes to `inbound.messages` and the worker consumes it, rather than calling the engine directly: a webhook keeps returning in milliseconds while a turn takes minutes, and a turn that crashes cannot lose a message that was accepted. `TestRun_IngressHandsWebhooksToTheDurableQueue` runs the production `run` against a real RabbitMQ.
- **One port.** `HTTP_PORT` (`server.port`), default 8000 for every role, replaces `GATEWAY_PORT`/`ORCHESTRATOR_PORT`/`PROXY_PORT` and the proxy's `GATEWAY_URL`/`ORCHESTRATOR_URL`. 8000 is the proxy's port, so the tunnel target, the public webhook paths and a public `GOOGLE_REDIRECT_URL` do not change. Named `HTTP_PORT`, not `PORT`, because `PORT` is what Next.js and many hosts read for their own process. The shipped Google redirect default moves from `:8081` to `:8000`.
- **Validation per role.** `cfg.Validate` takes `all`, `ingress`, `worker` or `audit-worker`. `all` requires what the worker requires and runs the ingress checks too (Telegram secret token, bot token for the webhook URL), because one process now receives the webhooks. The old service names are refused.
- **Image.** The app runs in the orchestrator's `debian:12.15-slim` image (it needs `pg_dump`). The internet-facing code used to run in distroless; it now shares a process with the worker anyway, which ADR 0001 accepted ("the isolation is weak in practice"). A deploy that wants the old split runs `--role=ingress` separately.
- **User-facing commands.** The `/restore` reply, the `/doctor` next steps, the restore tool's messages and the migration error named `proxy`, `gateway` and `orchestrator`. They now name `app` and stop `ngrok` first so nothing new comes in while the queue drains. `docs/RESTORE_AND_MIGRATE.md` keeps the `v3.4.x` commands for the baseline side of the upgrade, and the upgrade starts V4 with `--remove-orphans` so the three old containers are removed. `TestCompose_CommandsInCodeNameExistingServices` fails if code tells a user to stop or start a service that does not exist.
- **Tests that moved or changed.** Every test of the three packages moved into `cmd/mypa`. The proxy's tests now run against `newEdgeRouter` with stub handlers instead of upstream servers. `TestSetupProxy_InvalidURL` tested parsing the two upstream URLs, which no longer exist; it became `TestNewEdgeRouter_MissingHandler`, the same "refuse to build with a broken side" check. The `Proxy_OK`/`Proxy_Missing` validation cases became `All_*` cases plus `Removed_*` cases that refuse the old names. `repeatingReader` was defined three times and is now one helper.
- **Not touched:** the `audit-worker` (V4-23 merges it), the dashboard staying a separate Next.js container (ADR 0001 §5), the reverse proxy with TLS for a deployed install (V4-40).

### DP-23 The channel layer: adapters in their own packages, a registry, and a guard that reads comments too (V4-31)
- **What was decided earlier.** ADR 0003: one `Adapter` interface with capabilities as data and one shared renderer for the fallbacks (1a, 2b), proactive delivery as recorded rows (3b, built in V4-33), addressed-only groups (4a, V4-16), ingress derived as webhook or outward (Decision 5), and today's split lengths and functions kept, with the golden transcripts byte-identical as the proof.
- **Where the adapters live.** Options: (a) new packages under `internal/channel/<name>`; (b) **the existing `internal/telegram` and `internal/twilio` become the adapters** (chosen). They already held the platform code and its tests; (b) moves no test file. Everything platform-specific moved into them: the Markdown converters and split functions (`internal/markdown` is gone, moved with `git mv`), the Telegram webhook handler, its secret check and the `setWebhook` retry (from `cmd/mypa`), each platform's settings and validation (from `internal/config`), and each platform's `/doctor` check (from `internal/doctor`, which now exports its wording helpers). `internal/channel/adapters` is the one list that builds them; it and the two adapter packages are the only places the guard allows a platform name.
- **What "names a platform" means.** `TestNoPlatformNamedOutsideAdapters` searches every production source file outside those three directories for `telegram`, `whatsapp` or `twilio` in any case, comments and strings included, so a platform switch cannot survive as a renamed variable or a comment that says what a branch is for. It fails if an allowlisted directory disappears or if it scans fewer than 100 Go files or no dashboard file, so a broken walk cannot pass. Tests may name platforms. The first version searched Go only; review pointed out that the "done when" has no such exception (the dashboard copy and a comment in `config/config.yaml` named platforms), so both were made neutral here rather than deferred. User-facing text that named a platform outside the adapters became neutral: `/help` lists "channels" for `/doctor`, the transfer report's note on reply windows, the Google sign-in page says "return to the chat", and the dashboard copy on the home, shared, automations and finance pages. The guard reads those too: production Go, the dashboard sources (`dashboard/src`, tests excepted) and the shipped settings in `config/`. `.env.example` and the docs are outside it: they document the platforms' own setting names.
- **The neutral message.** `channel.Inbound` replaces `models.Message` as the queue payload and the engine's input, with the ADR's fields plus `SentAt` (the Telegram date a test pinned; V4-33 can use it as "last wrote"). WhatsApp messages keep an empty conversation ID, as `ChatID` was empty before, so audit sessions and the dedup key keep their shape; the adapter sends to the number. An attachment carries the label the engine uses for its untrusted mark, set by the adapter (`telegram_photo`, `whatsapp_image`, `whatsapp_pdf`, `whatsapp_document`), because those labels appear in a pinned confirmation prompt.
- **Capabilities declare what works today**, not the ADR's target table: no buttons, invite links, groups or voice replies on either adapter, no documents in on Telegram (BL-18), nothing out but text. Each later item (V4-10, V4-36, V4-16, V4-18) switches its value on. Declaring the target now would make `Render` skip the documented fallbacks for features that do not exist. Telegram group chats are already reported as `Kind: group`, not addressed; nothing reads that until V4-16.
- **Sending to a person known only by an ID.** Partner notifications, automations and the Google sign-in confirmation hold only a platform ID. Options: (a) the person's latest audit session, as reminders do: someone who never wrote would get nothing, which is a regression for partner notifications; (b) **each adapter recognizes its own IDs (`channel.Recognizer`) and `Registry.Recognize` asks them in order** (chosen): the leading-`+` rule the wiring had, moved into the adapters, until V4-32 links a person to their accounts. Side effects: an ID no adapter recognizes now fails with an error instead of going to the Telegram API; the Google sign-in confirmation, which was sent to Telegram for everyone, now reaches WhatsApp users.
- **Registration.** Both adapters are always registered, configured or not, because the ingress always served both routes and failed closed when unconfigured; the fail-closed tests pin that. ADR 0003 §6 also says an enabled webhook adapter without a public address stops the app. That check needs a notion of "enabled" and of the public address that V4-40 defines (WhatsApp has no address setting at all, Twilio's console holds it), so it is left to V4-40. The registry is an instance (`channel.NewRegistry`), not package state, so tests build their own.
- **Ports and mocks.** The engine's `TelegramClient`/`TwilioClient` ports are gone; it holds a `*channel.Registry`. Each adapter defines its platform `API` and its own mock (`internal/<adapter>/mocks`), and `adapterstest.Registry(tg, wa)` builds the real adapters around those mocks. So the engine's tests keep their expectations on the platform calls and now also run the real formatting and splitting. Two tests that built a message without a source had pinned a quirk: the old engine sent such a message to Telegram without converting it to HTML. A message must now name its channel; those two tests use WhatsApp, whose adapter hands text to the client unconverted, so their assertions are unchanged.
- **The edge** routes all of `/webhook/` to the ingress instead of two platform prefixes; unknown paths under it get the ingress's 404. **`/doctor`** lists the adapters' checks first, in registration order, then Google, queues, backups and schema (before: Telegram, Google, WhatsApp, …). The conformance `doctor` fixture keeps the pinned order, so its transcript stayed byte-identical.
- **Media error replies changed (not covered by the transcripts, which pin the successful paths).** The engine no longer knows which platform an attachment came from, so the failure replies are one set for every channel: "❌ Failed to download photo." (was also "Failed to retrieve photo metadata." on Telegram, where getting the file path and downloading were two steps, and "Failed to download WhatsApp photo."), "❌ Failed to download voice message." (was also "…voice message metadata." and "…WhatsApp voice message."), "❌ Failed to transcribe audio." (was "…WhatsApp audio." on WhatsApp), "❌ Failed to download document." and "❌ Failed to download PDF." (were "…WhatsApp document." and "…WhatsApp PDF."). The unsupported-format reply, the transcription echo and every success path are unchanged. So the claim the transcripts support is narrower than "nothing changed": nothing changed on any path the suite pins; these failure texts changed as listed here.
- **Small hardenings found while porting:** a Telegram update without `from` or `chat` used to panic in the parser (500, retried by Telegram); it is now ignored with 200.
- **Upgrade.** The queue payload changed. A message queued by an older version decodes with no channel or sender and ends in the DLQ, so the queue is drained before upgrading, as ADR 0003 §8 and the upgrade procedure require.
- **Not done here (V4-34):** making registration depend on passing the suite (the suite does not yet fail for a registered adapter without a fixture) and generating the capability matrix in SYSTEM.md; §3.10 holds a hand-written table until then.

### DP-24 Channel conformance suite completed: the registry is the list, sealed, and capabilities drive the expectations (V4-34)
- **What "cannot be registered without passing" means.** Passing is a test-time fact, so the rule is enforced in CI, as ADR 0003 §6 recommends: the suite reads the production registry and fails for a registered adapter without a fixture. Three gaps had to be closed for that to hold. (1) The suite ran a list kept in the test: `TestConformance` now iterates `adapters.Build(...).All()` and requires a fixture for each, and `TestFixturesMatchRegisteredAdapters` fails on a fixture with no adapter or a different order. (2) Code could add an adapter to the app's registry after `Build`: options were an unexported `Register` (breaks the registry's own tests and every test that builds a registry by hand), a check at app start (cannot know about the suite), or **`Registry.Seal`** (chosen): `Build` seals what it returns and a later `Register` is refused. (3) Code could build a second registry: `TestAdaptersReachTheAppOnlyThroughBuild` parses every production Go file (go/ast, import names resolved, dot imports included) and allows `channel.NewRegistry` only in `internal/channel/adapters/adapters.go`. A type-checked guard (`golang.org/x/tools/go/packages`) would also catch `Register` on any registry, but it would make an indirect dependency direct; sealing makes that unnecessary.
- **Expectations from declarations.** The fixture's hand-written `ingestsDocuments` is gone: scenarios read the adapter's `Capabilities()`. A file kind not in `AttachmentsIn` must be dropped (the pinned Telegram document behaviour); a declared kind must arrive as that kind; long replies must fit the declared `MaxTextLen` (checked here for the first time, and both adapters already did); HTML tag balance is checked where `Markup` is HTML instead of where the channel is named `telegram`. The fixture keeps only platform facts the adapter cannot declare (payload shapes, the platform's own length limit).
- **A scenario for the fallbacks.** ADR 0003 §6 says that where a capability is missing the scenario asserts the documented fallback and is never skipped. Nothing in the engine sends choices, files or voice yet, so `fallbacks` sends one such message through the production registry (what the engine sends through) and checks each fallback on the wire. For a capability that is declared there is no fallback to check and the fixture cannot yet observe the real thing, so the scenario fails with a message naming the capability. Alternatives were to skip (forbidden) or to pass vacuously (a capability switched on by V4-10 or V4-18 would then go unchecked).
- **Found, not fixed (BL-35).** `channel.ChoiceList` writes `1. Book it (reply yes)`, but both adapters' Markdown converters render every list item with a bullet, so the numbers never arrive. The rule for findings outside the item applies: it is logged, the transcripts pin what arrives, and the scenario asserts what holds (every choice in order with what to type). It does not block the "done when": the two channels pass the suite as it pins them, as with BL-18 and BL-19 in stage 1.
- **The generated matrix.** `channel.Matrix` renders one column per adapter from `Capabilities()` and `Ingress()` and one "When missing" column from the fallback the layer applies; it reads declarations only, so it names no platform. The tool-catalogue generator rejects unknown region names, so a second generator could not simply add its own region: `internal/docgen` merges both into one map for `gendocs` and owns `TestDocsAreUpToDate`, moved from `internal/toolcatalog` with its assertions unchanged plus the new region. The matrix shows an unconfigured install; when WhatsApp has two modes (V4-41) it is registered once per mode and gets a column for each. The annotations of the old hand-written table ("inline buttons: V4-10") are one sentence above the generated table, since they are plans rather than declarations. The hand-written table in §3.7 keeps only behaviour that is not a capability.
- **Checked that the suite can fail:** a third adapter added to `Build` without a fixture fails `TestConformance` and `TestFixturesMatchRegisteredAdapters`; Telegram declaring documents fails `media_document`; WhatsApp declaring voice replies fails `fallbacks`; `Build` without `Seal` fails the conformance tests and `TestBuild_ReturnsASealedRegistry`; a new package under `internal/channel/` that calls `channel.NewRegistry` fails `TestAdaptersReachTheAppOnlyThroughBuild` (review found that a first version exempted that whole tree; no file is exempt now). Each mutation was reverted.

### DP-25 Run locally or deployed: one ingress setting, one public address, Telegram polling (V4-40)
- **What was decided earlier.** DE-9: a single server running the same stack is the supported deployed path, with one public address on a domain the owner controls. ADR 0003 Decision 5: ingress is derived (`Webhook` needs a public address, `Outward` does not), Telegram polling is built here, and an enabled webhook adapter without a public address stops the app. ROADMAP §7: no ADR is needed for deployment and ingress; the choices are recorded here.
- **The settings.** `INGRESS` (`none`, `caddy`, `cloudflare`, `tailscale`, `proxy`, `ngrok`; default `none`) and `PUBLIC_URL`, in a new `internal/deploy` package that names no platform. Options for choosing the bundled tunnel or proxy: (a) separate compose files per mode, which is editing or picking files; (b) **compose profiles named exactly like the `INGRESS` value, started by `COMPOSE_PROFILES` in `.env`** (chosen). Compose reads `COMPOSE_PROFILES` from `.env`, and every app container loads `.env`, so the app sees it and refuses a profile list that misses the chosen ingress or names another one. Two settings must still agree; the check makes a disagreement a startup error instead of a silent outage. A profile given only on the command line cannot be seen and is not guessed at.
- **One address replaces four.** `TELEGRAM_WEBHOOK_URL`, `TWILIO_STATUS_CALLBACK_URL`, `GOOGLE_REDIRECT_URL` and `NGROK_DOMAIN` are derived from `PUBLIC_URL`. Options for the old settings: (a) keep reading them as overrides, which keeps the four-way disagreement possible; (b) ignore them silently, which leaves a stale value in `.env` that looks like it matters; (c) **refuse to start while one is set, naming `PUBLIC_URL`** (chosen). V4.0 is allowed breaking configuration changes, the message says what to do, and the upgrade step is in the restore guide (section A step 2, section D). The adapters own their replaced keys (`telegram.ReplacedEnvKeys`, `twilio.ReplacedEnvKeys`), so `internal/config` still names no platform. `PUBLIC_URL` must be an `https` origin with no path: every path is appended to it, and a path would also have to be stripped for Caddy and ngrok. With `INGRESS=none` it is refused, since nothing could reach it. Without it the Google redirect is `http://localhost:<HTTP_PORT>/auth/google/callback`, the old `.env.example` default. Delivery reports for WhatsApp (*BL-8*) are now always on where there is an address.
- **WhatsApp signatures against the public address.** Twilio signs the address it calls. The handler rebuilt it from `X-Forwarded-Proto`/`X-Forwarded-Host`, which ngrok sends but not every tunnel or proxy does in the same way. With `PUBLIC_URL` known, the handler now verifies against `PUBLIC_URL` + the request path, so every ingress works the same, and forwarded headers can no longer move the signed address. Without a public address (only tests) it rebuilds it as before. The conformance suite keeps signing for `https://mypa.example` with the header-based path; the new path is covered by `TestHandleWebhook_VerifiesAgainstThePublicAddress`.
- **Telegram's two modes.** `telegram.Config.Polling`, set by `LoadConfig` when there is no public address. The zero value stays the webhook, so every existing test and the conformance fixture keep their meaning; the generated matrix is built from an empty environment instead of a zero config, so it shows what a default install does (polling). Polling deletes the webhook first (`drop_pending_updates=false`), long-polls `getUpdates` for 25 s (below the client's 30 s timeout), and confirms an update only after its message was queued, which gives the webhook's retry-on-500 behaviour. A polling install serves no Telegram route and needs no secret token. A transport that ignores cancellation must not keep the loop alive, so the loop checks its context on each turn (found by the conformance fake). The `/doctor` check in polling mode fails when a webhook is registered at all, because that means updates go elsewhere, most likely to a second live install.
- **Proof in the conformance suite.** Options: (a) a second Telegram fixture, which the fixture-to-registry guard forbids (one fixture per registered name); (b) a polling-only unit test; (c) **every scenario delivers its Telegram update through both the webhook and polling, on the same fake Bot API, and requires the same `channel.Inbound`** (chosen). The transcripts are unchanged, because the engine sees the same message. A mutation (the poller changing the text) failed 11 scenarios. For `duplicate_delivery`, the fake models a restart before the confirmation, which is how polling delivers a message twice.
- **The guard on published ports.** `TestDockerComposeNetworkIsolation` requires every published port to be on `127.0.0.1`. Caddy must listen on the server's 80 and 443. Options: (a) an exception in the guard for `caddy`, which weakens a guard; (b) **`network_mode: host` for Caddy, forwarding to the app's existing `127.0.0.1:8000`** (chosen): compose publishes nothing new, and the guard is unchanged. Host networking needs Linux, which is the server DE-9 describes; on Docker Desktop a home PC uses a tunnel instead. Caddy's admin endpoint is switched off, since it would otherwise listen on the host's loopback.
- **Tunnels.** `cloudflare` runs `cloudflared tunnel run` with the token; the hostname and the target `http://app:8000` live in the Cloudflare dashboard, because a token-run tunnel takes its routes from there. `tailscale` runs in userspace networking with a serve configuration that publishes `http://app:8000` with Funnel; its address is the node's `ts.net` name, not a domain the owner controls, so a move with it changes the address (documented in the restore guide). None of them forwards to the dashboard (`TestCompose_IngressesReachTheAppOnly`).
- **Managed services.** `EXTERNAL_DATABASE_URL`, `EXTERNAL_REDIS_URL` and `EXTERNAL_RABBITMQ_URL` with the bundled addresses as compose defaults, so the existing guards (container URLs never taken from the host `.env`) still hold. The bundled containers keep starting and sit idle: compose profiles can only add services, not remove them, so making them optional would put the default install's database behind a profile. Logged as BL-36.
- **The "done when".** "A Telegram-only install runs with no tunnel" is proven by `TestRun_TelegramOnlyInstallRunsWithNoTunnel` (the production `run`, a Telegram-only configuration with no tunnel settings, a fake Bot API, a real RabbitMQ), by `TestCompose_EachIngressIsItsOwnOptInProfile` (no tunnel starts without its profile) and by the conformance suite in polling mode. "One install is moved from local to a server and back" needs a server and the live install; it is the owner's check list at the end of the restore guide, and the item stays In progress until it is done.


### DP-26 Households: Migration A, a `Scope` in every repository call, and what an empty label maps to (V4-30)
- **What was decided earlier.** ADR 0002: Decision 1 (`households` with a legacy label, roles in one table checked through `scope.Can`), Decision 2 option 2b (Migration A is additive; `family_group` stays and is still written until V4-44), Decision 3 option 3b (a `Scope` only the identity package can build, the first argument of every repository method), Decision 6 option 6b (`ALLOWED_USERS` seeds a fresh install once), and the V4-30 row of §9.
- **The household row.** `households(id, name, legacy_label UNIQUE NULL, language, time_zone, currency, created_at, updated_at)`. Packs are not a column yet: ADR 0002 lists them under V4-27, which defines what a pack is. An empty `language`, `time_zone` or `currency` means the install default (`DEFAULT_TIMEZONE`, none, USD). Options for migrated households: (a) copy `DEFAULT_TIMEZONE` into every row at start-up, which writes data from a setting on every boot; (b) **leave them empty, meaning "the install default"** (chosen). A household made by the bootstrap starts with `DEFAULT_TIMEZONE` stored. The settings are used: the turn's time zone (`Engine.location(scope)`), finance's and house's default currency (`Scope.Currency("USD")`), and one prompt line when a language is set. They are changed through the GraphQL `household` query and the admin-only `updateHousehold` mutation; the dashboard page is V4-36's household setup.
- **The legacy label.** Every household made in V4-30 has one, so `family_group` keeps being written with exactly what the code wrote before: the trimmed label, or the platform ID of a user who had none (DE-2). The holding household has none. It has the fixed ID `00000000-0000-0000-0000-000000000000` (`models.HoldingHouseholdID`), every scope constructor refuses it, and `GetPairingCode` never returns a code in it, so nobody can join it.
- **Migration A (`00003_households.sql`).** It follows ADR 0002 Decision 2 step 1: labels trimmed and otherwise exact, so case variants stay two households; one household per unlabelled user, keyed by platform ID; a label found only on data rows becomes a memberless household; NOT NULL, a foreign key and an index on all 20 tables, plus the six hot composite indexes keyed by household. It departs from the ADR's text in one place, to keep the ADR's stated goal. The ADR sends every row with an empty label to the holding household "so they stay as invisible as they are today". But rows with an empty label were *not* all invisible. Before V4-30 the reads of 8 tables also matched the person (`user_id`), whatever the label. The P2-4 copy wrote `''` into `topic_memories` for every unlabelled user, and the schedule module wrote the raw label (`''`) for them, so those users read those rows every day. Options: (a) as written, which would hide those users' memories and events after the upgrade; (b) **an empty-label row in one of those 8 tables whose `user_id` names a user (by platform ID, or by `users.id`, which finance writes) goes to that user's household; every other one goes to the holding household** (chosen). Rows that were invisible stay invisible, and rows that were visible stay visible to the same person. An unlabelled user's household holds only them, so nobody else gains anything. The schedule/shared/memory slice found the same issue independently. `TestMigrationA_EveryLabelBecomesItsHousehold` checks every row of every table against this rule; the baseline restore test checks the label-to-household mapping on the committed v3.4.0 backup.
- **Known consequence.** Before V4-30, a user also read their own rows written under a *different* label (the person clause). A row now belongs to the household its label names, so such a row follows its label. This only arises if someone's label was changed by hand; the V4-44 comparison report would show such rows.
- **The scope (`internal/identity`).** `Scope` has unexported fields and three constructors that read the household through an `identity.Store` (`db.Client` implements it): `ForUser` (a sender's users row), `ForLabel` (the dashboard's label, matched ignoring case; a label matching two households is refused), and `ForStoredRow` (a scheduled job; no admin powers). The zero scope is invalid, and every repository method returns `identity.ErrNoScope` for it. `identitytest` and `testutil/householdtest` build scopes for tests through the same constructors. Today's two role rules are one table (`roleActions`, plus the operator actions `/backup`, `/restore` and `/doctor`); `TestCan_WalksEveryRoleAndActionPair` walks it. The three in-place `user.Role != "admin"` checks are gone.
- **Repositories.** Each repository method on a household table takes the scope first. Reads filter `household_id`. Writes stamp `household_id` and the legacy label and overwrite whatever the caller set. Updates and deletes by ID include the household and report not-found on zero rows, and nothing writes by primary key alone. An upsert given an ID outside the household is not-found, never a new row with that ID. Three scheduler scans stay unscoped (`GetDueSharedReminders`, `ListActiveAutomationRules`, `ListActiveMemoriesForConsolidation`); each row's `household_id` becomes the scope that acts on it. Person-keyed tables keep their signatures. A tool argument naming another person (`shared_with_user_id`, `assigned_to_user_id`, the finance `user_id` filter) is resolved with `identity.Member`, inside the caller's household only. Finance's `CallContext.Extra` family path is removed.
- **Proof.** `TestHouseholdIsolation_EveryRepositoryMethodIsConfinedToItsScope` seeds one row of household A in every one of the 20 tables, carrying A-only markers. It then calls every exported repository method that takes a scope (118 today) with household B's scope; the arguments are built by reflection and aimed at A's rows (ID 1, A's text, A's `household_id` and label inside model arguments). It asserts that no result contains A's data, that A's rows are byte-identical afterwards, and that the zero scope is refused. An unscoped method that is not classified, with a reason, fails the test. A planted leak (`GetBabyProfile` without its filter) failed it on three counts. `TestHouseholdSchema_EveryHouseholdColumnIsGuarded` fails on a nullable `household_id`, a missing foreign key, a missing leading index, or a model that is neither household-scoped nor listed as person-keyed. `TestToolDeclarations_NoParameterNamesATenant` fails on a tool parameter naming a household, family or tenant.
- **The isolation tests named by the ADR.** They changed only where a tenant is created or passed, with one exception: `TestMultiTenant_EmptyScopeLeakVulnerability`. It passed an empty tenant string, asserted `NoError`, and only *logged* four repositories that failed open. An empty tenant can no longer be expressed; its V4-30 form is the zero `Scope`, which every repository refuses. Its four `require.NoError` calls became `require.ErrorIs(..., identity.ErrNoScope)`, every `assert.Empty` stayed, and the four logged fail-open paths became assertions that they are closed. That is stricter, not weaker. The reviewer should check that file's diff with this in mind. One test outside that list changed an assertion deliberately: `TestScale01_EventBusInvalidationStress` asserted that an actor ID is evicted as a cache scope, a fallback V4-30 removes (below).
- **Around the repositories.** `MutationEvent` carries `HouseholdID`; the mutation log, partner notifications, the automation runner (a rule fires only for events of its own household) and prompt-cache invalidation key by it. An event without one is not logged. Prompt sections are cached under `ctx_prompt:<householdID>:<userID>:<module>`. Every user has a household, so the per-user fallback scope is gone and invalidation scans only the household. Rollback reloads the log inside its transaction, refuses one from another household, filters every statement by household, and forces `household_id` and the label in restored snapshots, so a pre-V4-30 snapshot (no `household_id`) restores into the caller's household and a crafted one cannot move a row. `/doctor` lists the admin's household by ID. The dashboard's `familyGroup` arguments stay until V4-35; each resolves to a household with `ForLabel` and must be the caller's own, compared by ID.
- **Review round (V4-30).** The independent review found three things, all fixed on the branch. (1) `configure_topic_calendar` published its event without `HouseholdID`, so the mutation log dropped it and the write could not be undone; `TestModule_EveryMutationEventCarriesTheHousehold` now checks every schedule event. (2) `delete_automation_rule` in chat looked the rule up inside the caller's household, got nothing for another household's rule, deleted nothing, and still replied "Successfully deleted" and logged a `rule_deleted` event; the adversarial test missed it because its fake repository ignored the scope. The handler now refuses a rule it cannot find in the household, and the fake filters like the real repository (`TestDeleteAutomationRule_ForeignRuleIsRefusedWithoutAnEvent`). "Missing" and "another household's" cannot be told apart without a cross-household read, which would tell a caller that a rule ID exists elsewhere, so both are refused alike, as every other delete by ID is since V4-30. `TestAutomation_ToolEdgeCases` asserted that deleting a missing rule succeeds ("idempotent delete"); that expectation was changed to a refusal deliberately. (3) Four stale SYSTEM.md passages.
- **The bootstrap.** `db.BootstrapHouseholds` runs under an advisory lock, only when no household exists. `ParseAllowedUsers` now keeps the order written, because "its first entry becomes the owner" needs an order. ADR 0002's optional fifth field (the channel) is not read: there is no channel column until V4-32 adds channel accounts, so the leading-`+` rule stays where it is today. With a household present, the setting is ignored; the start-up log says so at INFO, and `/doctor` reports it as Info ("Allowed users").

### DP-27 People and channel accounts: Migration B, person keys filled by the database, linking with a confirmation (V4-32)
- **What was decided earlier.** ADR 0002: Decision 1 (`people`, `channel_accounts`, `memberships`, `access_tokens`; a person keeps `users.id`), Decision 2 step 2 (Migration B is additive; `person_id` beside every person column, filled where the old value is a known key, the rest counted), Decision 4 option 4b (code on a linked account, typed on the new one, confirmed on the first; merge only inside one household, after a backup), Decision 5 (typed codes stored as a keyed hash under a server secret outside the database), §9 and §13 (one processing lane per person; direct-chat history per person; an approval belongs to the person and its conversation; Google sign-ins found through the person's accounts until V4-23).
- **Which key the old person columns get.** Options: (a) the sender's own account ID; (b) **the person's key: the platform ID of their `users` row, the same on every account** (chosen). With (b) every module, including the ones that compare person keys in Go (partner notifications, shared tasks, finance member filter), sees one person on every channel without a change, and the old and new keys stay one-to-one for every new row, which is what the V4-44 comparison checks. `UserForAccount` returns the person's `users` row, so `ActorID()` and `Scope.PersonKey()` are that key; `Scope.PersonID()` is `users.id`.
- **How `person_id` is filled.** Options: (a) set by each repository and module on every write; (b) a GORM hook; (c) **a trigger per table that fills each companion from the old key on insert, and on update when the companion is emptied or the old key changes without it** (chosen). (a) depends on 28 columns' write paths being found and kept; (b) misses raw SQL and map updates. The trigger is generated in Go from one list (`person_keys.go`) for both the migration and the AutoMigrate test schema; `TestPersonKeys_EveryPersonColumnHasAFilledCompanion` fails for a person column (`…user_id`) without one. `mypa_person_of(key)` maps a `users.platform_id`, a `users.id` (finance) or, failing those, the one person owning an account with that external ID (a merged person's old key). ADR 0002 rejected hidden hooks for *tenant filtering* (3c); this fills a derived column and never decides who can read what.
- **How person-level reads use it.** `personIs(old, new, key)`: rows whose companion is the key's person, plus rows that name no person under exactly that old key (written before anybody was a person, or by `dashboard-service`). Never wider than before V4-32. Used by every read that filtered a person column (preferences, personal memories, own schedule events, topic calendar settings, workflows, the finance member filter, the latest audit session, shared-space lookups).
- **Migration B as a Go migration.** Version 4 runs the embedded `people_migration.sql` (tables, one person, account and membership per user; `last_inbound_at` from the audit sessions) and then the generated companion columns, foreign keys (`ON DELETE SET NULL`), indexes and triggers, in one transaction, and logs how many old keys name no person per column (counts only). A Go migration, because the ADR asks the migration to print that count and goose SQL cannot log.
- **The account's channel on upgrade.** Options: (a) the leading-`+` rule written into the migration's SQL; (b) the source of the user's latest audit session; (c) **left empty by the migration and filled by the worker at start-up from the adapter that recognizes the ID** (`ResolveAccountChannels` with `Registry.Recognize`; chosen). (a) names the platforms outside the adapters, which the channel layer forbids (V4-31) even where the guard does not read (the first review round found it in the embedded SQL); (b) misses a user who never wrote. (c) is the same rule, owned by the adapters, and runs before any message is taken; an ID no adapter recognizes is logged and cannot write.
- **Delivery.** Options: (a) the preferred account always; (b) **an account the person has written from: the preferred one first, else the latest** (chosen). (b) keeps the rule before V4-32 (a person who never wrote is not messaged; the `reminder` scenario pins it) and avoids WhatsApp's window for a never-used account; V4-33 adds the per-channel policy. A confirmed link sets the new account's `last_inbound_at` to when it typed the code.
- **Merging needs both sides.** The first build let the issuer's `CONFIRM LINK` merge on its own, which let a member pull another member's account into theirs by getting them to type a code (ADR 0002 Decision 4, "Type my code on your phone"; found by the review). Now the issuer's confirmation only records its half (`access_tokens.issuer_confirmed_at`), and the account that typed the code is warned what a merge means and must reply `CONFIRM MERGE` itself within 10 minutes; only that reply takes the backup and merges. Each confirmation names its own action.
- **Merging.** The survivor is the person who issued the code. The rows only one of them can keep: preferences (the survivor's win; the merged person's fill only empty fields; the other row is deleted), per-topic calendar settings (the survivor's win per topic; the others move). Google sign-ins need no resolution: they are found through the accounts, preferred first. The merged person's `people` and `users` rows are deleted; every other row moves by its companion. The backup is taken by the engine first; if it fails, nothing is merged.
- **Limits and quarantine.** Wrong link codes count against the sender's existing pairing lockout (5 per 15 minutes, 1 hour), checked for every sender, a member's account included, and an install-wide limit (30 per 10 minutes, `rate_limit:codes:global`, failing closed). An account linked less than 24 hours ago cannot issue codes; removing another account needs an older one, so a new account cannot remove older ones either. An owner removing a member's account needs owners (V4-35, BL-41).
- **The secret.** `LINK_CODE_SECRET`, optional: without it linking is refused and the rest works; when set it must be 32+ characters and differ from `ENCRYPTION_KEY` and `JWT_SECRET`. Options rejected: deriving it from `ENCRYPTION_KEY` (the ADR asks for a new setting, and one key per purpose); requiring it (an install upgraded without it would not start).
- **Codes in logs and audit.** `logging.LogMessageText` replaces a typed link command with "/link [code withheld]" at every level, so the adapters' debug logs carry no code either; the audit session stores the same withheld prompt. Each linking step is recorded in the turn's audit session as an `account_link` entry (purpose, outcome, token row). A test drives the whole flow with debug logging and checks that neither the code nor its hash appears anywhere.
- **Mock tests.** The auth gate now calls `UserForAccount(ctx, channel, sender)`; the engine's unit tests change only the mocked method name (their assertions are unchanged). Without an `AccountStore` (`WithAccounts` not called) delivery falls back to the latest audit session, as before; the conformance suite and the identity repository tests cover the production path.
- **Proof of the "done when".** The `link_accounts` conformance scenario runs on every channel with the other registered channel as the second account: the code, the claim from the account that belongs to nobody, a bare `CONFIRM` that links nothing, `CONFIRM LINK`, and then a turn on the other channel that continues the person's conversation and runs a tool with the same person key and household. The repository tests prove a single-use claim under a race, confirmation only from the issuing account within the window, merging only inside a household with the data following, the unlink rules and delivery.
