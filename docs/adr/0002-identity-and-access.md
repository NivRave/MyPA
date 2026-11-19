# ADR 0002 — Identity and access: households, people, channel accounts, roles, sign-in and invites

- **Status:** Accepted (2026-11-19). The owner's decisions are in §13. Not yet implemented.
- **Date:** 2026-11-19
- **Context owner:** MyPA maintainers
- **Needed before:** stage 2 of V4.0 (V4-30, V4-32, V4-35) and V4-36. Fixed by [`ROADMAP.md`](../ROADMAP.md) and not reopened here: many households in the model, one in the experience (DE-7); the role names and the mapping `admin` → owner and operator, `family` → adult (V4-35); every family label becomes a household and a user without one gets their own (V4-30); sign-in is a short-lived, single-use link from `/dashboard` in chat (V4-35).

## 1. Context: the code as it is (`dev` at `3807fb9`)

**Who a user is**
- `users` has `id` (UUID), `platform_id` (unique), `name`, `role` and `family_group` (`internal/models/models.go:114-121`). There is no channel column: `platform_id` holds a Telegram user ID (`internal/telegram/handler.go:94`) or a phone number (`internal/twilio/handler.go:68`), and the code tells them apart by a leading `+` (`cmd/orchestrator/main.go:216-226`). One person on two channels is two unrelated rows.
- A household does not exist as a record. The family is a free-text label on `users`, on `pairing_codes` and on 18 data tables (`internal/db/migrations/00001_baseline.sql`, for example lines 64, 459, 733, 790). SQL compares it exactly (`internal/db/db.go:259`), the dashboard API compares it ignoring case (`internal/graph/resolver.go:199`, `internal/graph/auth_middleware.go:50`), and chat trims it (`internal/orchestrator/registry.go:47-48`).
- A user without a label is their own tenant, keyed by their platform ID (`internal/orchestrator/registry.go:46-51`, `internal/graph/resolver.go:169-175`, DE-2).
- The person key written into data rows is not one thing. Most modules write the platform ID (`internal/orchestrator/registry.go:36-41`); finance writes the `users.id` UUID (`internal/domains/finance/handlers.go:81-84`, `internal/domains/finance/service.go:42-47`); the dashboard writes the literal `dashboard-service` (`internal/graph/auth_middleware.go:117-122`, used at `internal/graph/schema.resolvers.go:88`). The partner notifier already has to match on either key (`internal/domains/shared/notifier.go:114`).

**How the tenant reaches a query**
- Chat: the user row is read once per message from the sender's platform ID (`internal/orchestrator/engine.go:390-394`) and carried in `CallContext` (`internal/orchestrator/engine.go:157-165`). Each module asks it for the tenant (`internal/orchestrator/registry.go:46-51`) and passes a `familyGroup` string to its repository. No tool takes a family as an argument. Three tools take another person as free text, stored or used without checking that the person is in the household: `user_id` in the finance summary (`internal/domains/finance/declarations.go:74`, `internal/domains/finance/handlers.go:187`), `shared_with_user_id` and `assigned_to_user_id` (`internal/domains/shared/module.go:247`, `:341`). Finance also accepts a family from `CallContext.Extra` (`internal/domains/finance/service.go:36`).
- Dashboard API: most resolvers accept a `familyGroup` argument from the client and check it against the caller (`internal/graph/resolver.go:156-203`; arguments at `internal/graph/schema.resolvers.go:26`, `:72`, `:1430`, `:1466`). The `users` query applies no filter at all (`internal/graph/schema.resolvers.go:1409-1427`; logged as BL-31). An admin sees every member's audit sessions, prompts and replies included (`internal/graph/schema.resolvers.go:1942`).
- Scheduled work takes the tenant from the stored row (`internal/scheduler/scheduler.go:158-165`).
- Messages are processed in one serial lane per channel and sender (`internal/broker/consumer.go:150`); chat history, Google sign-ins and pending approvals are Redis keys by platform ID (`internal/state/store.go:81`, `:139`, `:210`).

**Getting in**
- Pairing: a dashboard admin creates a 6-digit code carrying a role and a family, valid 24 hours (`internal/graph/schema.resolvers.go:26-45`). There is no separate approval step: issuing the code is the approval. The code is stored in clear (`internal/models/models.go:126`) and copied into the mutation log (`internal/graph/schema.resolvers.go:47-56`). An unknown sender who types it, or sends `/start <code>`, becomes a user named `Unknown` at once (`internal/orchestrator/engine.go:278-303`). The check and the redemption are two steps (`internal/db/db.go:232-254`; BL-32). Five wrong codes lock that sender out for an hour (`internal/state/store.go:236-260`); there is no limit across senders.
- `ALLOWED_USERS` (`id:name:role:family`, `internal/config/config.go:193-228`) is written over the `users` table on every start (`cmd/orchestrator/main.go:187-193`, `internal/db/db.go:214-215`), so a role changed in the database is reverted at the next restart.
- Dashboard: there is no sign-in. The Next.js server sends one shared secret with every request (`dashboard/src/lib/graphql.ts:77-83`), and the API treats the holder as an admin of the family named in `DASHBOARD_FAMILY_GROUP` (`internal/graph/auth_middleware.go:114-125`). Whoever can open the dashboard port is that admin; the port is bound to this machine only (`docker-compose.yml:252`). A signed token with role `admin` would be an admin of every family (`internal/graph/auth_middleware.go:29-31`, `:206`), but nothing in the repository issues one (only `parseAndVerifyJWT`, `:150`).
- Roles are two strings compared in place: `user.Role != "admin"` (`internal/orchestrator/commands.go:157`, `internal/orchestrator/restore.go:80`).
- A single-use secret already exists as a pattern: `/connect` stores 32 random bytes for 10 minutes and reads them with an atomic get-and-delete (`internal/orchestrator/commands.go:139-145`, `internal/state/store.go:180-198`).

**Tests that must keep holding:** `internal/db/multitenant_isolation_security_test.go`, `internal/graph/tenant_isolation_security_test.go`, `internal/graph/tenant_bound_admin_test.go`, `internal/graph/empty_family_tenant_test.go`, `internal/orchestrator/auth_gate_security_test.go`, and the `pairing` and `pairing_lockout` conformance scenarios (`internal/conformance/scenarios_test.go:131-187`).

## 2. Decision 1 — Schema and roles

### Options
| | Option | Pros | Cons |
| --- | --- | --- | --- |
| 1a | Keep `users`; add `household_id` and `channel` to it | Smallest change | A person is still one row per channel, so V4-32 ("same user, same data") cannot be met |
| 1b | `households`, `people`, `channel_accounts`; the household and role are columns on `people` | Three tables, simple joins | A person in two households later means moving two columns out of the central table and touching every reader |
| 1c | As 1b, with a `memberships` table (person, household, role), limited to one membership per person for now | Allowing a second household later is dropping one constraint, not a schema change; the role sits where it belongs, on the membership | One more join on every authentication |
| 1d | Roles and permissions as data (role and permission tables) | Flexible | Nothing asks for custom roles; permission bugs become data bugs that tests do not see |

### Challenge
- *Against 1c:* DE-7 does not ask for a person in two households, so the extra table may never earn its keep. It stays in because the join is hidden behind one function (Decision 3), and because moving the role later means rewriting the security core a second time.
- *Fixed roles in code are rigid.* True, and intended: V4-11 adds per-member risk tiers on top; the base table should be small enough to test exhaustively.

### Recommendation: 1c, with roles as a fixed table in code
```
households        id uuid PK · name · legacy_label UNIQUE NULL · language · time_zone · currency · created_at   (packs: V4-27)
people            id uuid PK (an upgraded person keeps users.id) · display_name · is_operator · preferred_account_id NULL
memberships       person_id FK UNIQUE · household_id FK · role in (owner, adult, child, guest)
channel_accounts  id uuid PK · person_id FK · channel · external_id · UNIQUE (channel, external_id) · linked_at · last_inbound_at
access_tokens     id · purpose in (invite, link, signin) · token_hash UNIQUE · household_id · person_id NULL · role NULL · created_by · expires_at · used_at NULL
sessions          id · secret_hash UNIQUE · person_id · created_at · last_seen_at · expires_at · revoked_at NULL
join_requests     id · token_id FK · channel_account_id FK · created_at · decided_by NULL · decided_at NULL · outcome NULL
```
- **Household roles.** *Owner:* members, invitations, roles, packs, sharing, household settings, export. *Adult:* their own data and connected accounts, the household's shared data, dashboard sign-in. *Child:* chat only, a member of shared topics by default, no dashboard. *Guest:* chat only, and only the topics shared with them by name (for example a babysitter and the baby log). A household may have several owners; the last one cannot be removed or demoted.
- **What an owner sees.** The household's shared data, and each member's activity: who, when, which tools, and the cost. Not the text of another adult's private chats, and not their personal data. Today the dashboard shows an admin every member's sessions with their prompts (`internal/graph/schema.resolvers.go:1942`); that ends with V4-35. What an owner may see of a child's or a guest's chats is open (§11).
- **Operator** is a flag on the person, not a household role: backups, restore, `/doctor`, keys, usage, logs. It gives no access to a household's content (question 2).
- **One check.** Every permission test becomes `scope.Can(action)`, backed by one role-to-action table with a test that walks every pair. The string comparisons on `Role` are removed.

## 3. Decision 2 — Upgrade path and rollback

### Options
| | Option | Pros | Cons |
| --- | --- | --- | --- |
| 2a | One migration renames `family_group` to `household_id` and `users` to the new tables | One step, no leftover columns | Every repository, resolver and test changes in the same commit as the data; nothing proves the data moved correctly before the code depends on it |
| 2b | Expand, switch, contract: add the new tables and columns and fill them; switch the code; drop the old columns in a later release | Each step is small and checkable; the old columns stay as the reference to compare against | Two keys live side by side for one release; writes go to both during it |
| 2c | Keep the label as the key and make it a foreign key to `households` | Tests and repositories untouched | V4-30 requires the ID; a household could never be renamed |

### Challenge
- *2b costs a release of double writes.* Yes. That is the price of being able to compare old and new on real data, which is the only convincing proof that isolation did not move.
- *"Rolled back" is not free under any option.* The supported rollback is DE-10: restore the backup taken before the upgrade with `transfer import --no-migrate` and start the old release (`internal/transfer/transfer.go:231-234`). It loses what was written after the upgrade. 2b narrows that: until the code switch, the old release's columns are still complete.

### Recommendation: 2b
1. **Migration A (V4-30), additive only.** Create `households`. One household per distinct label on `users` and `pairing_codes`, compared after trimming and otherwise exactly, as SQL does today; two labels that differ only in case stay two households, because merging them would join two tenants. Every user without a label gets a household of their own; rows written under that user's platform-ID tenant map to it. A label found only on data rows becomes a household with no members. Rows with an empty label go to one memberless holding household, so they stay as invisible as they are today. Add `household_id` (NOT NULL, foreign key, leading column of an index) to the 20 tables and fill it. `family_group` stays and is still written.
2. **Migration B (V4-32), additive only.** `people` from `users`, keeping the ID; one `channel_accounts` row per user, the channel taken from the same leading-`+` rule the code uses today; one `memberships` row per person, with the household only; `access_tokens`, for link codes. Add `person_id` beside every person column and fill it where the old value is a known platform ID or `users.id`; other values (`dashboard-service`) keep `person_id` empty and the old column, and the migration prints how many.
3. **Migration C (V4-35), additive only.** Fill `memberships.role` (`admin` → owner, `family` → adult) and set `people.is_operator` for every `admin`; create `sessions`. A household with no `admin` gets no owner automatically; `/doctor` reports it and an operator assigns one.
4. **Migration D (V4-36), additive only.** A join request per redeemed invite: the invite, the channel account, who decided, when, and the outcome.
5. **Nothing is merged automatically.** Two existing rows that are the same human stay two people until that human links them (Decision 4).
6. **No data is lost** is a test, not a promise: the committed baseline (`cmd/orchestrator/testdata/baseline_v3.4.0.dump` with its counts file) is restored and migrated, and for every table the row count per label equals the row count per mapped household, with no empty `household_id`.
7. **Contract: V4-44, in V4.1, after one release of V4.0 has been in use.** Drop `family_group`, the old person columns, `users` and `pairing_codes`, once a comparison report on the live data shows the old and new keys agree on every row. From then on the rollback point is the backup taken before that release.

**How the tenant-isolation tests keep passing.** Migration A changes no query, so every test listed in §1 runs unmodified against it. When the code switches to `household_id`, still in V4-30, those tests change only where they create a tenant: a helper turns the label (`"VictimFamily"`) into a household, and no assertion is edited. The reviewer checks that against the diff of exactly those files. Two tests are added so that coverage does not depend on someone remembering: one walks every registered model (`internal/db/migrations.go:20-55`) and proves a row of household A cannot be read, changed or deleted with the scope of household B; one fails if a table has a `household_id` that is nullable, has no foreign key, or has no index starting with it.

## 4. Decision 3 — How the tenant reaches every query

### Options
| | Option | Pros | Cons |
| --- | --- | --- | --- |
| 3a | As today: each caller passes an ID string to each repository method | No new concept | Any string fits; today's stray paths (§1) are this option's failures |
| 3b | A `Scope` value (household, person, role, operator, audience) that only the identity package can construct, required as the first argument of every repository method | A method without a scope does not compile; there is one place where identity becomes a tenant | Every repository signature changes once |
| 3c | A database hook that adds the tenant filter from the request context | No signature changes | Invisible; raw SQL and vector queries bypass it; a missing context has to fail closed everywhere |
| 3d | Postgres row-level security with a per-transaction setting | Holds even against a forgotten filter | Every query must run inside a transaction that set it; pooling, the migrations and the coming job queue each need care |

### Challenge
- *3b still trusts the author to use the scope inside the method.* The model-walking test from Decision 2 catches a method that ignores it. 3d would be a real second fence; it is left for later (§11) because, done half-right, it fails open silently.

### Recommendation: 3b
- A scope is built in three places only: from a verified channel account (an inbound message), from a session (the dashboard), and from a stored row (a scheduled job). Until V4-32 and V4-35 provide those, the sources are the ones in §9. The household is always read from the database, never from input.
- **The signatures change once,** in V4-30, when `Scope` replaces the `familyGroup` string. V4-32 and V4-35 change how a scope is filled, never its type or the methods that take it.
- **Never from tool arguments.** A test fails if any tool declaration has a parameter that names a household, family or tenant. An argument that names another person (`assigned_to`, `shared_with`, the finance filter) is resolved with `scope.Member(ref)`, which looks only inside the caller's household and errors on a miss.
- **Never from the client.** The `familyGroup` arguments leave the GraphQL schema; the household comes from the session. A test fails if a field takes one.
- An operator's scope has no household. Operator queries return install data (health, usage, counts), not household content.

## 5. Decision 4 — Linking a second channel account

### Options
| | Option | Pros | Cons |
| --- | --- | --- | --- |
| 4a | An owner types the new account's number or ID in the dashboard | No step for the member | Nothing proves the new account belongs to the person; a typo hands their data to a stranger |
| 4b | The person asks for a code on a linked channel, types it on the new one, and confirms back on the first | Proof from both sides; works wherever typing works | Three messages |
| 4c | Match accounts by phone number | Automatic | Numbers are recycled; Telegram and Discord IDs are not numbers; a silent match is a silent takeover |
| 4d | Code typed on the new channel only, no confirmation | Two messages | A code that is read out, forwarded or guessed is enough to become the person |

### Challenge
- *4b is slow for the commonest case, the owner adding their own second app.* It is three short messages, once per account. The confirmation is the only step an attacker holding a leaked code cannot perform.

### Recommendation: 4b
`/link` in a direct chat returns an 8-character code valid for 10 minutes. `/link <code>` from an account that belongs to nobody raises a prompt on the issuing channel: "Add WhatsApp ending 0123 to your account? Reply `CONFIRM LINK`." Only that reply links. If the second account already is a person, the same proof from both sides starts a merge instead, allowed only inside one household, after a backup (question 4).

| Takeover attempt | How it is stopped |
| --- | --- |
| Guessing a code | About 40 bits, 10 minutes, single use; the per-sender lockout that exists today plus a limit across all senders; and a guess still needs the victim's `CONFIRM LINK` |
| "Read me the code you just got" | The confirmation names the channel and the end of the number or handle that typed it; without the reply nothing changes; the person's other accounts and the owners are told about every link |
| "Type my code on your phone", to pull the victim's account into the attacker's | Only an account that belongs to nobody can redeem a code; an account that is already a person goes down the merge path, which needs a confirmation from both and one household |
| Crossing households | Linking never changes household or role; a merge across households is refused |
| Reusing or racing a code | Redemption is one statement: mark used where unused and unexpired, returning the row |
| A stolen, unlocked phone adds the thief's own account | Every link is announced to the other accounts and the owners; a new account cannot issue link codes or sign-in links, or remove older accounts, for 24 hours; an owner or any older account can remove it |
| Unlinking the real accounts to lock the person out | An account may remove itself; removing another needs an older account or an owner; the last account cannot be removed |
| Forged sender | Identity comes only from a webhook that passed its check (`cmd/gateway/main.go:134-139`, `internal/twilio/handler.go:98-111`); ADR 0003 makes this part of the adapter contract |
| Codes shown to a group | `/link`, `/dashboard`, invitation codes and their confirmations work in direct chats only (ADR 0003 §5) |
| Injected text makes the assistant link an account | Linking is a command handled before the model runs (`internal/orchestrator/engine.go:396-404`), not a tool, and its confirmation needs its own name, as `CONFIRM RESTORE` does today (`internal/orchestrator/restore.go:164-168`) |
| A phone number passes to someone else | Not solved here; the same exposure exists today. Reduced by removal and by `last_inbound_at` being visible to owners. See §11 |

## 6. Decision 5 — The dashboard sign-in link and invite links

### Options (where the secret lives)
| | Option | Pros | Cons |
| --- | --- | --- | --- |
| 5a | A signed token in the link, nothing stored | No table | Cannot be single-use or revoked without a store, which V4-35 requires |
| 5b | A random secret; only a hash of it is stored in Postgres (`access_tokens`), claimed in one statement | Single-use and revocable; nothing to move in V4-23; for a 256-bit link token a database or backup leak yields no usable link (typed codes need a keyed hash, below) | A table and a clean-up job |
| 5c | The same in Redis, like the `/connect` secret | The code exists | Redis leaves in V4-23; stored in clear |
| 5d | The browser shows a code and the person types it in chat | Works when the chat is on another device | Phishable: "send this code to the bot" signs the attacker's browser in. It also is not the link V4-35 specifies |

### Challenge
- *A link is a bearer secret: whoever opens it first is the person.* So it is short-lived, it is sent only to the person's own direct chat, opening it changes nothing until a button is pressed, and the person is told in chat when a session starts.
- *Chat apps and proxies fetch links to preview and log them.* The token therefore travels in the part of the address after `#`, which browsers never send to a server; the page reads it and submits it in a POST. A preview fetch can neither consume it nor leave it in a log.
- *A typed code is short.* An 8-character code has about 40 bits, so a plain hash from a leaked database or backup can be reversed offline by trying every code. Hence the keyed hash below.

### Recommendation: 5b
| | Sign-in link | Invite |
| --- | --- | --- |
| Issued | To the person, by `/dashboard` in a direct chat; owner, adult and operator only | By an owner, in the dashboard and later in chat (V4-38) |
| Form | A link with a 256-bit token after `#`; the host is the public address, or the local one when there is none | An 8-character typed code on every channel, plus a link where the adapter can make one (ADR 0003) |
| Lifetime | 10 minutes | 72 hours; an owner can revoke it |
| Single use | Yes; used when the button on the page is pressed | Yes |
| Stored | SHA-256 of the token only, with person, expiry and use | A keyed hash of the code only (below), with household, role, issuer, expiry and use |
| Grants | A session: a random ID in an `HttpOnly`, `SameSite=Lax` cookie (`Secure` on HTTPS, below), its hash in `sessions`; ends after 24 hours idle or 7 days, on `/signout`, or when an owner removes the person | Adult, child or guest, never owner; an owner is made by promoting a member. Membership starts when an owner approves the join request (question 3) |

- **Typed codes** (invite and link codes) are stored as an HMAC-SHA-256 under a server secret that is not in the database or in a backup: a new setting, introduced with the first typed code in V4-32. Only the 256-bit sign-in token is protected by a plain hash. Consequences: a code is shown once, when it is created, and can never be displayed again (an owner who lost one revokes it and issues another); and outstanding codes stop working after a restore onto a machine with a different secret, so the restore procedure has to say so.
- **On a home install** the dashboard port is bound to this machine (`docker-compose.yml:252`, `127.0.0.1:3030`), so the link (`http://localhost:3030/…#token`) works only in a browser on that machine, and `/dashboard` says so above the link. On that plain local address there is no TLS, so the cookie is set without `Secure`; it stays `HttpOnly` and `SameSite=Lax`, with no `Domain`, so it is sent only to `localhost` and never leaves the machine. Cookies are not separated by port, so other web apps on this machine's `localhost` could receive it; that is accepted on a machine the owner controls. With a public HTTPS address (V4-40) the cookie is `Secure` and carries the `__Host-` prefix.
- **Opened on another device,** `localhost` names that device, so no MyPA page loads at all: the browser shows its own "cannot connect" error, the token is never sent anywhere, and it expires unused after 10 minutes. The person asks again on the machine itself, or uses the public address once there is one.
- Role and membership are read from the database on every request, not baked into the cookie, so a demotion or removal takes effect at once.
- The shared dashboard secret, `DASHBOARD_FAMILY_GROUP` and the signed-token path are removed (V4-35). The dashboard forwards the browser's cookie; without one it shows how to get a link.
- Today's pairing codes move into `access_tokens` as invites of the same form; the six-digit kind ends with V4-36.
- **Logged**, at INFO and as an audit record: purpose, token row ID, person, household, channel, outcome (issued, used, expired, refused and why). The remote address on use goes in the audit record only, never at INFO (principle 4). **Never logged or stored in clear:** the token, the link, the code. The mutation log stops receiving the code. A test in the style of the existing log-hygiene tests holds this.

## 7. Decision 6 — Bootstrap on a fresh install from `ALLOWED_USERS`

### Options
| | Option | Pros | Cons |
| --- | --- | --- | --- |
| 6a | As today: apply the setting on every start | Editing the file adds a user | The file overrides the database for ever; a role changed in the dashboard is undone at restart |
| 6b | Seed once: read it only when no household exists | The database is the single truth afterwards; the wizard (V4-24) later does the same job | After the first start, editing the file does nothing, which must be said out loud |
| 6c | Drop the setting now and require a command | Clean | Breaks the documented install before its replacement exists; V4-30 says the setting stays until the wizard |

### Challenge
- *6b removes the owner's quick way to add someone.* Pairing codes and then invites are that way. The real loss is recovery after a lockout, which gets an offline command (§10).

### Recommendation: 6b
- On a start with no household: one household per label in the setting (normally one; more than one works and is logged as unusual); an entry with no label gets its own. Each `admin` entry becomes an owner and an operator; if a household has no `admin`, its first entry becomes the owner and a warning says so. The channel comes from an optional fifth field, and from the leading-`+` rule when it is absent.
- An empty setting on a fresh install creates nothing: the bot answers nobody and the log says how to start.
- When a household exists, the setting is ignored; the start-up log and `/doctor` say so.

## 8. Decision 7 — Many households in the model, one in the experience (DE-7)
- **Options.** *7a:* build one household and add the key later. Rejected: it contradicts DE-7 and repeats this migration. *7b:* key everything by household now and build no multi-household screens. *7c:* build the switcher and cross-household administration now. Rejected: no user has asked for it.
- **Challenge.** 7b can rot, because code written for one household quietly assumes one. An upgraded install can already hold several (each label, each unlabelled user), and the model-walking test in Decision 2 always runs with two.
- **Recommendation: 7b.** In the model: every row has a household, a membership names its household, invites carry one, the operator is separate from every household. Not built: a household switcher, a second membership for a person (the unique constraint), a screen to create a second household (an operator command instead), sharing between households.

## 9. What each item delivers

Each item's "done when" in the roadmap is reached with what that item and the earlier ones deliver; nothing waits on a later item.

| Item | Migration and tables | What the `Scope` carries | `familyGroup` arguments in the dashboard API | Settings that end |
| --- | --- | --- | --- | --- |
| V4-30 | A: `households`; `household_id` on the 20 tables, `users` and `pairing_codes` included; `family_group` kept and still written | Household: always from the database (the sender's `users.household_id`, the household whose label is `DASHBOARD_FAMILY_GROUP`, a stored row's `household_id`). Person: the sender's `users` row; each module keeps writing the key it writes today. Role and operator: `users.role`, read only through `scope.Can` with today's two rules (`admin` may do what an admin may today, operator commands included; `family` the rest). Audience: direct | Kept. Each is resolved to a household by its label, ignoring case as today (a label that matches two households is refused), and must be the caller's household, else refused; every query then filters by the ID | None. `ALLOWED_USERS` is read only when no household exists (Decision 6) |
| V4-32 | B: `people` (keeping `users.id`), `channel_accounts`, `memberships` (household only), `access_tokens` for link codes; `person_id` beside every person column | Person: the `people` row of the verified channel account; person-level queries read `person_id`. Household: from the membership. Role and operator: still `users.role` of the person's own row, through the same two rules | Unchanged | None. New: the server secret for typed codes (Decision 5) |
| V4-35 | C: `memberships.role`, `people.is_operator`, `sessions`; sign-in tokens in `access_tokens`; pairing codes now carry adult, child or guest | Role and operator: from the membership and the person, through the role-to-action table; the two legacy rules go. Dashboard: from the session. An owner's scope stops reaching other adults' private chats (Decision 1) | Removed from the schema; the household comes from the session; a test fails if a field takes one | `DASHBOARD_API_SECRET`, `DASHBOARD_FAMILY_GROUP`, `JWT_SECRET` |
| V4-36 | D: join requests; invites in `access_tokens`; `pairing_codes` no longer written | Unchanged. A person whose join request is open has a channel account and no membership, so no scope can be built; the gate answers only "waiting for an owner" | None remain | None. The six-digit pairing code ends; outstanding ones work until they expire, 24 hours at most |

**History, ordering and approvals.** Until V4-32, nothing changes: one processing lane per channel and sender (`internal/broker/consumer.go:150`), and history and approvals keyed by the platform ID. From V4-32: one processing lane per person, whichever channel or conversation a message comes from; direct-chat history belongs to the person and is shared across their channels; group history belongs to the conversation; a pending approval belongs to the person and to the conversation it was raised in, and can be confirmed only there.

## 10. Consequences
- `SYSTEM.md` (models, API, settings, security), `README.md` and `.env.example` change with each item. `DASHBOARD_API_SECRET`, `DASHBOARD_FAMILY_GROUP` and `JWT_SECRET` end with V4-35.
- The GraphQL schema loses its `familyGroup` arguments; every dashboard page that sends one changes (for example `dashboard/src/app/users/page.tsx:26-28`).
- With V4-32, chat history and pending approvals move from platform-ID keys to the keys §9 describes (`internal/state/store.go:81`, `:210`). The 24-hour chat window is not carried over, as with any restore.
- **Google sign-ins** are in Redis under the platform ID (`internal/state/store.go:139`), with their encrypted database copy under the same key (V4-43), and a database migration cannot move Redis keys. They stay as they are until V4-23: a person's sign-in is found through their channel accounts, the preferred one first. V4-23 moves them into Postgres keyed by person. Restoring a pre-V4 backup therefore needs no re-keying.
- The `pairing` scenario asserts a role and a label (`internal/conformance/scenarios_test.go:147-148`); it is updated when roles change, deliberately, with its transcripts.
- Undo must keep refusing the identity tables, as it refuses `users` and `pairing_codes` today.
- A new offline operator command, beside `transfer`: grant owner or operator to a person, for recovery.
- BL-31 and BL-32 are closed by this design; either can be fixed earlier on its own.

## 11. Not decided here
- A phone number that passes to another human, and whether a long-silent account must prove itself again.
- Row-level security in Postgres as a second fence (option 3d).
- What child and guest may do tool by tool: V4-11.
- What an owner may see of a child's or a guest's chats: asked when V4-35 is built.
- A person in two households, and anything shared between households.
- Passwords, passkeys or another sign-in that does not need the bot.
- How a merge resolves two rows that cannot both stay (preferences, calendar settings, Google sign-in): decided in V4-32, recorded in `DECISIONS_LOG.md`.

## 12. Questions for the owner
1. **Are child and guest two roles or one?** Recommended: two stored values. A child sees shared topics by default; a guest sees only what is shared with them by name. Splitting one role later means re-deciding every member.
2. **May the operator read a household's content (audit sessions hold the prompts)?** Recommended: no. The operator sees health, usage and counts; reading content takes membership. On a one-household install the owner is both, so nothing changes for you.
3. **Should joining with an invite need an owner's approval?** Recommended: yes, one reply. A forwarded or overheard code then gives a stranger nothing. The cost is that the invited person waits until an owner answers.
4. **Two existing users who are the same human: merge them?** Recommended: yes, but only when that human links them, inside one household, with a backup first. Never automatically.
5. **How long does a dashboard session last?** Recommended: 24 hours idle, 7 days at most, ended by `/signout`. Shorter is safer on a shared computer; longer means fewer trips to the chat.

## 13. Decision record (2026-11-19)

Sections 1 to 12 are the draft of 2026-11-19 after an independent review. This section records what the owner decided.

- **Questions 1 to 5: as recommended.** Child and guest are two roles; the operator does not read household content; joining with an invite needs one owner reply; two existing users are merged only when that human links them, inside one household, after a backup; a session lasts 24 hours idle and 7 days at most, and ends on `/signout`.
- **Google sign-ins** keep their platform-ID keys until V4-23, are found through the person's channel accounts meanwhile, and move into Postgres keyed by person in V4-23 (§10).
- **History, ordering and approvals:** one processing lane per person from V4-32; direct-chat history per person, group history per conversation; an approval belongs to the person and its conversation (§9).
- **Owner visibility:** shared data and each member's activity, not another adult's private chats or personal data; the current view of every member's prompts ends with V4-35 (Decision 1).
- **Typed codes** are stored as a keyed hash under a server secret outside the database, shown once, and lost by a restore onto a machine with a different secret (Decision 5).
- **The contract step** (Decision 2, step 7) is its own item, V4-44, in V4.1; it is not part of V4-07.
