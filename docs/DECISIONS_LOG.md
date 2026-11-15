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
