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
