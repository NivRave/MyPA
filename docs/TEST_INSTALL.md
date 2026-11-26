# Test install beside the live system

A test install of `dev` (V4) on the same Windows machine as the live `v3.4.0` system, loaded with a
copy of the live data. The live system keeps running and is never stopped, changed or restarted by
these steps. The test install has its own folder, its own compose project (`mypa-test`), its own
database, its own Telegram bot and its own ports:

| | Live (`v3.4.0`, project `mypa`, `D:\MyPA`) | Test (`dev`, project `mypa-test`, `D:\MyPA-test`) |
| :--- | :--- | :--- |
| App | proxy `8000`, gateway `8080`, orchestrator `8081` | app `127.0.0.1:8001` |
| Dashboard | `http://localhost:3030` | `http://127.0.0.1:3031` |
| Postgres, RabbitMQ, Redis, ngrok | `5432`, `5672`, `15672`, `6379`, `4040` | none published |
| Telegram bot | the family's bot | a new bot, for the test install only |
| WhatsApp | on | off |

This is also the copy that closes OP-12 (dry run of the V4 migrations on real data) and V4-03 (the
re-embed on a copy of real data), in steps 6 to 10.

Files used:

- [`deploy/test-install/docker-compose.test.yml`](../deploy/test-install/docker-compose.test.yml):
  copied to `docker-compose.override.yml` in the test clone. It sets the project name, the ports,
  and makes the dashboard and the sign-in links reach the test app only (see "How the dashboard
  reaches the app" at the end).
- [`deploy/test-install/check-test-env.ps1`](../deploy/test-install/check-test-env.ps1): run before
  the first start. It compares the test `.env` with the live one and stops if the Telegram token is
  the live bot's, if any Twilio setting is present, or if a setting the app now refuses is still
  there. It prints setting names only, never values.

> **Run every command in the test folder.** Each section starts with `Set-Location $Test`. A
> `docker compose` command typed in `D:\MyPA` acts on the live system.

## 0. In every new PowerShell window

Paste this first. It sets the two folders, moves to the test folder, and defines `Invoke-TestSql`,
which runs SQL on the **test** database only (it refuses to run anywhere but the test folder):

```powershell
$Live = 'D:\MyPA'
$Test = 'D:\MyPA-test'
if (Test-Path $Test) { Set-Location $Test }
function Invoke-TestSql([string]$Sql) {
    if ((Get-Location).Path -ne $Test) { throw "Run this in $Test, not in $((Get-Location).Path)" }
    $Sql | docker compose exec -T postgres sh -c 'psql -U $POSTGRES_USER -d $POSTGRES_DB -v ON_ERROR_STOP=1'
}
```

The SQL goes in through standard input, so no quoting is passed to `docker` (Windows PowerShell
5.1 mangles quotes inside arguments to programs). `$POSTGRES_USER` and `$POSTGRES_DB` are expanded
inside the test postgres container, from the test `.env`.

## 1. A separate clone

```powershell
git clone --branch dev (git -C $Live remote get-url origin) $Test
Set-Location $Test
git config core.hooksPath .githooks
git log -1 --oneline
```

The last line shows the `dev` commit under test. Note it for the test report.

## 2. The compose override

```powershell
Set-Location $Test
Copy-Item deploy\test-install\docker-compose.test.yml docker-compose.override.yml
```

`git status` now lists `docker-compose.override.yml` as untracked. That is expected; never commit
it, and never copy it into `D:\MyPA`.

## 3. The newest nightly backup and the private backup key

Copy the newest backup of the live system into the test folder. The live `v3.4.0` writes a bare
dump (`backup_<time>.dump.age`, no manifest; see [RESTORE_AND_MIGRATE.md](./RESTORE_AND_MIGRATE.md),
"What a backup is"):

```powershell
Set-Location $Test
New-Item -ItemType Directory -Force "$Test\backups" | Out-Null
$Dump = Get-ChildItem "$Live\backups" -Filter 'backup_*.dump*' | Sort-Object LastWriteTime | Select-Object -Last 1
$Dump.Name, $Dump.LastWriteTime
Copy-Item $Dump.FullName "$Test\backups\"
$DumpName = $Dump.Name
```

Check that the time shown is last night's 02:00 run. This only reads the live folder.

Put the private backup key in place for the restore. Copy the key (the line starting
`AGE-SECRET-KEY-1`) from the password manager to the clipboard, then:

```powershell
Get-Clipboard | Out-File -Encoding ascii "$Test\backups\backup-key.txt"
Set-Clipboard -Value ' '
```

`-Encoding ascii` writes it without a byte-order mark. The key stays only until step 7, where it is
deleted.

## 4. A new Telegram bot for the test install

**Starting the test app with the live bot's token would take the live bot away from the family.**
A bot has one receiver. The test app has no public address, so it polls Telegram: it first removes
the bot's webhook and then fetches every message itself. With the live token that removes the live
system's webhook, the family's messages go to the test copy instead of the live system, and the
live system receives nothing until its webhook is registered again.

So the test install gets a bot of its own:

1. In Telegram, open **@BotFather** and send `/newbot`.
2. Name it, for example `MyPA Test`, and give it a username ending in `bot`.
3. BotFather replies with the token (`<digits>:<letters>`). Keep it for step 5.
4. Do not share the test bot's username with the family (see step 12).

Do not use `/token` or `/revoke` on the live bot: that changes the live system's token.

## 5. The test `.env`

Start from a copy of the live `.env` and edit it:

```powershell
Set-Location $Test
Copy-Item "$Live\.env" "$Test\.env"
notepad "$Test\.env"
```

The lists below come from `internal/config` (`RetiredEnvKeys`, `replacedEnvKeys`, `Validate`),
`internal/deploy`, the Telegram and Twilio adapters' settings, and `.env.example`.

**Remove** these lines if they are there:

| Setting | Why |
| :--- | :--- |
| `DASHBOARD_API_SECRET`, `DASHBOARD_FAMILY_GROUP`, `JWT_SECRET` | V4-35 ended them; the app refuses to start while one is set |
| `TELEGRAM_WEBHOOK_URL`, `GOOGLE_REDIRECT_URL`, `NGROK_DOMAIN`, `TWILIO_STATUS_CALLBACK_URL` | `PUBLIC_URL` replaced them (V4-40); the app refuses to start while one is set |
| Every `TWILIO_` line: `TWILIO_ACCOUNT_SID`, `TWILIO_AUTH_TOKEN`, `TWILIO_FROM_NUMBER`, and `TWILIO_WHATSAPP_MODE`, `TWILIO_SANDBOX_JOIN_CODE`, `TWILIO_TEMPLATE_REMINDER_SID`, `TWILIO_TEMPLATE_BRIEFING_SID` if present | No WhatsApp here. A configured WhatsApp needs a public address, so with `INGRESS=none` the app refuses to start; and a second install must never send from the family's number |
| `NGROK_AUTHTOKEN`, and `CLOUDFLARE_TUNNEL_TOKEN`, `TAILSCALE_AUTHKEY` if present | No tunnel here; the live tunnel's credentials stay with the live system |
| `TELEGRAM_SECRET_TOKEN` | Only for webhooks; a polling install does not use it. It is the live webhook's secret |
| `GATEWAY_URL`, `ORCHESTRATOR_URL` | No longer read since V4-01 |
| `COMPOSE_PROJECT_NAME`, `COMPOSE_FILE` | Could make compose act on another project than `mypa-test` |
| `GEMINI_MODEL` | Optional, but recommended: without it the release's default chat model is used (V4-09). Keep it only to compare with the live model |

**Change** these:

| Setting | New value |
| :--- | :--- |
| `TELEGRAM_BOT_TOKEN` | the test bot's token from step 4 |
| `BACKUP_AGE_IDENTITY_FILE` | empty (the key is passed with `-e` during a restore only) |
| `EXTERNAL_DATABASE_URL`, `EXTERNAL_REDIS_URL`, `EXTERNAL_RABBITMQ_URL` | empty, if present |

**Add** these:

```
INGRESS=none
PUBLIC_URL=
COMPOSE_PROFILES=
DASHBOARD_URL=http://127.0.0.1:3031
LINK_CODE_SECRET=<a new secret, see below>
```

- `INGRESS=none` with an empty `PUBLIC_URL`: nothing reaches the install and Telegram polls. The
  override sets both again for the app, so a stray value cannot register a webhook anywhere.
- `COMPOSE_PROFILES` empty: no tunnel or proxy container starts.
- `DASHBOARD_URL`: sign-in links from `/dashboard` open the test dashboard (the override sets it for
  the app too).
- `LINK_CODE_SECRET` is new in V4-32; without it `/link` is refused. Make one (64 hex characters,
  printed once; paste it into `.env`):

  ```powershell
  $b = New-Object byte[] 32; [Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($b); -join ($b | ForEach-Object { $_.ToString('x2') })
  ```

**Keep unchanged**: `ENCRYPTION_KEY` (the restored secrets open only with the live key, and
`import` checks it), `GEMINI_API_KEY` (chat and embeddings; the test install's calls count against
the same quota as live), `POSTGRES_*`, `RABBITMQ_DEFAULT_*`, `RABBITMQ_URL`, `REDIS_URL`,
`DATABASE_URL` (inside Docker compose builds the addresses itself), `GOOGLE_CLIENT_ID`,
`GOOGLE_CLIENT_SECRET`, `GROQ_API_KEY`, `TAVILY_API_KEY`, `DEFAULT_TIMEZONE`, `WEEK_START_DAY`,
`WEEKEND_DAYS`, `LOG_LEVEL`, `BACKUP_AGE_RECIPIENTS` (the test install's own backups are then
encrypted with the same public key). `ALLOWED_USERS` may stay: it is read only on an install with no
household, and the restored data has one. `HTTP_PORT` may be left out: the override sets `8001`.

Save the file. The check in step 13 confirms these changes; it can be run now too.

## 6. Restore with `transfer import`

Start only the test infrastructure, then restore and migrate:

```powershell
Set-Location $Test
docker compose up -d postgres rabbitmq redis
docker compose ps
docker compose --profile tools build transfer
docker compose --profile tools run --rm -e BACKUP_AGE_IDENTITY_FILE=/backups/backup-key.txt transfer import $DumpName
$LASTEXITCODE
```

(In a new window, set `$DumpName` to the file name from step 3 first.)

`docker compose ps` must show `mypa-test-postgres-1`, `mypa-test-rabbitmq-1` and
`mypa-test-redis-1`, with no host port. The import should look like this
([RESTORE_AND_MIGRATE.md](./RESTORE_AND_MIGRATE.md), "What `import` does, in order"):

- It opens the dump with the backup key and reads it to the end. A bare dump has no manifest, so
  this is its integrity check, and there are no archive files to restore.
- It restores into the empty test database and runs the migrations up to this release, logging
  what migrations 4, 11 and 13 changed (people created from users, person keys that found no
  person, memories and workflows moved, memory scopes corrected). Note those counts.
- It checks that `ENCRYPTION_KEY` opens the restored secrets and counts the Google sign-ins.
- It takes a first backup of the restored test install, into `$Test\backups`.
- It prints what is not carried (the last 24 hours of chat context, pending `CONFIRM`s, and so on).
- **No line starts with `!`**, and `$LASTEXITCODE` is `0`. A line starting with `!` means the
  restore is not finished: read it, fix the cause (often `ENCRYPTION_KEY`), and see
  RESTORE_AND_MIGRATE.md before trying again.

## 7. Remove the private backup key

```powershell
Remove-Item "$Test\backups\backup-key.txt"
Test-Path "$Test\backups\backup-key.txt"
```

The second line must print `False`. The key is kept off the machine again (OP-1).

## 8. Review the restored data (OP-12)

```powershell
Set-Location $Test
Invoke-TestSql "SELECT id, name, legacy_label, time_zone, currency FROM households;"
Invoke-TestSql "SELECT p.display_name, p.is_operator, m.role, h.name AS household FROM people p LEFT JOIN memberships m ON m.person_id = p.id LEFT JOIN households h ON h.id = m.household_id ORDER BY h.name, p.display_name;"
Invoke-TestSql "SELECT channel, count(*) FROM channel_accounts GROUP BY channel;"
Invoke-TestSql "SELECT 'topic_memories' AS rows_without_person, count(*) FROM topic_memories WHERE person_id IS NULL UNION ALL SELECT 'financial_transactions', count(*) FROM financial_transactions WHERE person_id IS NULL UNION ALL SELECT 'scheduled_reminders', count(*) FROM scheduled_reminders WHERE person_id IS NULL UNION ALL SELECT 'automation_rules', count(*) FROM automation_rules WHERE person_id IS NULL;"
```

What to look for:

- One household for the family, with its name and time zone.
- Every family member once, in that household. Each former admin is an operator (`is_operator`
  true, V4-35). Check that each role (`owner`, `adult`, `child` or `guest`) is what you expect.
- `channel_accounts.channel` is still empty for every account: the app fills it at its first start
  by asking the adapters which of them recognizes each ID. Run the query again after step 14.
- The rows without a person should be 0, or match the count migration 4 logged in step 6. Rows
  left without a person after the purge of OP-9 are worth a closer look.

## 9. The embeddings commands (V4-03)

From [EMBEDDINGS.md](./EMBEDDINGS.md), "Checking the path on a copy of real data". The app has never
been started here, so nothing consumes the queues:

```powershell
Set-Location $Test
docker compose run --rm --no-deps app embeddings status
docker compose run --rm --no-deps app embeddings reembed
docker compose run --rm --no-deps app embeddings status
```

What each should look like:

1. First `status`:
   ```
   Stored vectors:     gemini-embedding-001 (3072 dimensions, since <time>)
   EMBEDDING_MODEL:    gemini-embedding-001
   Schema stores:      3072 dimensions
   Memories:           <N>, of which <M> have no embedding
   ```
   followed by `! Some memories are not found by meaning ...` when `M` is above 0. `M` is the size of
   BL-24 on the live data; note it.
2. `reembed`: `Embedding ... with gemini-embedding-001 (3072 dimensions)…`, a progress count, then
   `✓ embedded <N> memories with gemini-embedding-001 (3072 dimensions).` It costs one Gemini
   request per 50 memories. A failure (a quota error, for example) changes nothing; run it again.
3. Second `status`: the same model and size, `of which 0 have no embedding` (a memory with no text
   stays without one, and `reembed` says so), and `✓ Every memory is embedded with the configured model.`

## 10. `reencrypt --dry-run` (V4-07)

```powershell
Set-Location $Test
docker compose run --rm --no-deps app reencrypt --dry-run
```

It starts with `Re-encryption pass, dry run: nothing is changed.` and gives, per store, `values`,
`current format`, `old format found` and `old format remaining`. Redis is empty in the copy, so only
the database's values count. It ends with one of:

- `✓ No old-format value remains in any store.`, or
- `! <N> old-format value(s) would be rewritten: run `mypa reencrypt` without --dry-run.` On a
  `v3.4.0` copy this is expected: `N` is what step 6 of RESTORE_AND_MIGRATE.md section A will
  rewrite on the live upgrade.

A line `! not readable with this key: <N>` means values written with another `ENCRYPTION_KEY`, or
damaged: those Google sign-ins will need `/connect` after the live upgrade. To see the real pass
work, `docker compose run --rm --no-deps app reencrypt` may be run here too, on the copy only; it
must end with `✓ No old-format value remains in any store.`

**What closes OP-12 and V4-03:**

- **OP-12** is done when the import in step 6 ended with no line starting with `!` (exit code 0),
  and the review in step 8 shows the expected households, people and memberships, with the rows
  without a person explained. The `reencrypt` count from this step goes into the OP-12 note.
- **V4-03** is done (DE-11) when `reembed` in step 9 succeeded on this copy and the second `status`
  ended with `✓ Every memory is embedded with the configured model.`

Record the dates and counts in `docs/BACKLOG.md` (OP-12) and `docs/ROADMAP.md` (V4-03).

## 11. Before the first start: switch off what acts outside

The copy holds the family's automation rules and Google sign-ins. Until you connect or enable
something on purpose, the test install must not act on real calendars, mail or outside services. On
the **test** database only:

```powershell
Set-Location $Test
Invoke-TestSql "UPDATE automation_rules SET is_active = false WHERE is_active;"
Invoke-TestSql "SELECT count(*) AS rules_still_on FROM automation_rules WHERE is_active;"
```

The first prints `UPDATE <n>` (the rules that were on, legacy workflows included); the second
must print `0`. Every rule, scheduled or triggered by an event, is now off.

List the rules whose action is a webhook. Each would send a POST to a real address when it runs:

```powershell
Invoke-TestSql "SELECT id, name, trigger_type, left(btrim(action_instruction), 100) AS action FROM automation_rules WHERE btrim(action_instruction) ILIKE 'http://%' OR btrim(action_instruction) ILIKE 'https://%' OR btrim(action_instruction) ILIKE 'webhook:%' ORDER BY id;"
Invoke-TestSql "SELECT id, name, base_url FROM external_api_connectors ORDER BY id;"
```

Do not switch one of the listed rules back on in the test install. The API connectors in the second
list are called only when someone asks the assistant to; keep that in mind when testing.

Remove the copied Google sign-ins, so no calendar, mail, task or contact of a real account can be
read or changed from the test install:

```powershell
Invoke-TestSql "SELECT count(*) AS google_sign_ins FROM encrypted_credentials WHERE provider = 'google_oauth';"
Invoke-TestSql "DELETE FROM encrypted_credentials WHERE provider = 'google_oauth';"
Invoke-TestSql "SELECT count(*) AS google_sign_ins FROM encrypted_credentials WHERE provider = 'google_oauth';"
docker compose exec redis redis-cli DBSIZE
```

The last count must be `0`. Redis was created empty and is not in a backup, so it holds no cached
sign-in (`DBSIZE` is `0` or very small).

To test Google on purpose later: add `http://localhost:8001/auth/google/callback` as an authorised
redirect URI of the OAuth client in the Google Cloud console, then send `/connect` to the test bot.
Until then `/connect` stops at Google with a redirect error, which is intended. After a `/connect`
the test install acts on that real Google account.

## 12. What still reaches real people

- **Pending reminders go out through the test bot to anyone who has started it.** So do morning
  briefings and partner notifications from the copied data. A Telegram bot can only write to
  someone who has opened it and pressed Start, and a person's Telegram ID is the same for every
  bot, so the test bot recognizes each family member from the copy the moment they start it. If
  only you start it, only you receive them. Do not share its username.
- Nothing goes out on WhatsApp: it is off.
- Model calls (Gemini, and any provider you add), web search (Tavily) and voice transcription
  (Groq) use the live keys and count against the same quotas and bills.

List what is waiting:

```powershell
Invoke-TestSql "SELECT id, user_id, due_time, urgent, left(message, 60) AS message FROM scheduled_reminders WHERE NOT is_sent ORDER BY due_time;"
Invoke-TestSql "SELECT id, title, due_time, assigned_to_user_id FROM shared_task_reminders WHERE NOT is_completed AND due_time IS NOT NULL AND reminded_at IS NULL ORDER BY due_time;"
```

Reminders already past due are sent at the first start. If you do not want yours to arrive at
once, mark them sent in the copy:

```powershell
Invoke-TestSql "UPDATE scheduled_reminders SET is_sent = true WHERE NOT is_sent AND due_time < now();"
```

## 13. Check the test `.env`

```powershell
Set-Location $Test
powershell -ExecutionPolicy Bypass -File "$Test\deploy\test-install\check-test-env.ps1" -LiveDir $Live -TestDir $Test
```

It ends with `OK: the test .env passed every check.` (exit code 0), or with `[STOP]` lines naming
each setting to fix and `Do not start the test install.` (exit code 1). It names settings, never
values. `[note]` lines do not stop. Fix every `[STOP]` and run it again before step 14.

## 14. First start

```powershell
Set-Location $Test
docker compose up -d --build
docker compose ps
docker compose logs app --tail 80
```

- `docker compose ps`: `app`, `audit-worker`, `dashboard`, `postgres`, `rabbitmq` and `redis` up,
  `migrate` exited with 0. Published ports: `127.0.0.1:8001->8001` (app) and
  `127.0.0.1:3031->3000` (dashboard), nothing else.
- The app log has `telegram polling for updates (no public address)`.
- If the log has `telegram refused getUpdates: another install is polling this bot or has set a
  webhook`, the token is not the test bot's: `docker compose stop app` at once, then step 4.

Check that the live system is untouched: `docker ps --filter label=com.docker.compose.project=mypa`
still lists the live containers, and a message to the live bot is answered as before.

Then run step 8's `channel_accounts` query again: every Telegram account now has the channel
`telegram`; WhatsApp accounts stay unresolved, because WhatsApp is off.

## 15. `/doctor`

In Telegram, open the test bot, press **Start**, and send `/doctor` (every former admin is the
operator since V4-35). Expect:

- Telegram: healthy, delivering by **polling** (no webhook).
- WhatsApp: not configured.
- Google: no sign-ins (removed in step 11).
- The queues and the database healthy, nothing in the dead-letter queues.
- No failure line. A failure names its next step; follow it, or note it for the test report.

## 16. Sign in to the test dashboard

Send `/dashboard` to the test bot in the direct chat. The link starts with
`http://127.0.0.1:3031/signin`. Open it in the browser, press the sign-in button, and the dashboard
shows the family's household.

Use `127.0.0.1:3031`, not `localhost:3031`: a browser keeps cookies per host, not per port, so on
`localhost` the test dashboard's session cookie would sit beside the live dashboard's (which stays
on `http://localhost:3030`).

## 17. Test plan

Do each in a direct chat with the test bot or on the test dashboard. "Copy only" means it changes
the test database, never the live one. Note what fails, with the time, for the logs
(`docker compose logs app --since 10m`).

**V4.0 items**

| Item | Try | Expect |
| :--- | :--- | :--- |
| V4-01 one binary | `docker compose ps` | one `app` container; no `proxy`, `gateway` or `orchestrator` |
| V4-02 providers | if you have another provider's key: add `LLM_PROVIDER=<anthropic, openai, grok or ollama>`, its `_API_KEY` and `_MODEL` to `.env`, `docker compose up -d --force-recreate app`, ask anything; then remove them and recreate again | the answer comes from that provider; without a key, skip |
| V4-03 embeddings | step 9, then ask about something saved long ago, in other words than it was saved | the memory is found by meaning |
| V4-04 usage caps | Usage page; then `LLM_DAILY_TOKENS_PER_PERSON=1` in `.env`, `docker compose up -d --force-recreate app`, send a question; remove it and recreate | tokens per day and per turn are shown; over the cap, a clear message and no answer, while `/doctor` still works |
| V4-05 quiet hours | Preferences page: quiet hours from now until 15 minutes from now (household time); in chat "remind me in 3 minutes to stretch", and "urgent: remind me in 4 minutes to check the oven" | the urgent one arrives on time; the other arrives when quiet hours end, not before. Clear the quiet hours afterwards |
| V4-06 `remember_fact` gone | "remember that the bike lock code is 4321", then Memory page | saved as a topic memory |
| V4-07 legacy retired | Automations page | former scheduled workflows are cron rules (all off since step 11); step 10 ran |
| V4-08 calendar tools | "add an event next Tuesday at 10:00: dentist", then "what's on next week?" | listed; not sent to Google (no sign-in) |
| V4-09 default model | `GEMINI_MODEL` removed in step 5 | answers come on the release's default model |
| V4-17 `/doctor` | step 15 | as described there |
| V4-27 packs | Tools & Packs page; switch one pack off (for example House), look at the menu, ask about it in chat; switch it on again | packs holding data are on after the migration; when off, its pages are hidden and chat does not use its tools |
| V4-30 household | Settings page: change the household's time zone, then back | saved and shown; the assistant uses the household's zone |
| V4-31 channel layer | ask for a long list (for example 60 numbered items); send a voice note (needs `GROQ_API_KEY`) | the long reply arrives split at paragraph or line breaks; the voice note is transcribed and answered |
| V4-32 people and `/link` | `/link`; then `/link 000000` from the same chat; with a second Telegram account, send the real code from it and confirm on the first | a code valid for 10 minutes; the wrong code is refused with the attempts left (5 wrong ones lock for an hour); with a second account, both become one person (copy only) |
| V4-33 delivery policy | "remind me in 2 minutes to drink water" | arrives on Telegram at that minute |
| V4-34 conformance | nothing by hand | CI runs the suite |
| V4-35 sign-in and roles | open the step 16 link a second time; Users page | the second use is refused; each member shows their role |
| V4-40 run locally | step 14 and `/doctor` | Telegram polling, no tunnel container |
| V4-41 Twilio modes | not testable here | WhatsApp is off in the test install |
| V4-43 restore and migrate | steps 6 and 18 | as described there |

**Fix batches merged since**

| Batch | Try | Expect |
| :--- | :--- | :--- |
| `fix/bl-43-stored-booleans` (BL-43, BL-62, BL-44) | "create a rule, switched off, that notifies me when a shopping item is added"; "log a private expense of 30 for a gift" | the rule shows off on the Automations page; the transaction is private on the Finance page; chat lists finances by the dashboard's rule |
| `fix/undo-batch` (BL-25, BL-57, BL-47, BL-65) | save a memory, change the household name on Settings, then `/undo` | `/undo` undoes the memory (your own last change); the settings change is not in the way |
| `fix/time-automation-batch` (BL-50, BL-49, BL-63, BL-55, BL-67) | **a reminder in local time**: "remind me today at <a time 5 minutes ahead> to call the bank"; **a prompt rule**: "every day at <a time 3 minutes ahead>, summarise my spending this week" | the confirmation names that local time and the reminder arrives then, not hours off; the rule's answer arrives as a message at its time. Switch that rule off afterwards |
| `fix/access-memory-batch` (BL-41, BL-38, BL-39, BL-26) | Users page: a member's accounts; Memory page: memories once saved as `Family` | the owner sees a Remove action for a member's account (copy only); those memories show as family |
| `fix/undo-complete-batch` (BL-68, BL-66) | **`/undo` after a write**: "I spent 12 on coffee", then `/undo`; **editing a transaction**: Finance page, Edit on one of your own transactions, change the amount or make it private; in chat "make transaction <id> private" | `/undo` names and removes the expense; the edit is saved; another member's transactions have no Edit |
| `fix/pre-test-batch` (BL-70, BL-72, BL-69, BL-59) | "remind me tomorrow at 9 to pay the bill", then `/undo`; "list recent changes" | the reminder is cancelled; the changes listed are the ones the dashboard shows you, with no other member's private transaction |
| `fix/ci-flake-93` (BL-74) | `/dashboard` three times | each link opens the sign-in page |

BL-25, BL-38, BL-39, BL-55, BL-57, BL-59, BL-67 and BL-72 are covered by tests and need a second
person or crafted input to see by hand; skip them unless a family member joins the test bot.

To pause the test install: `docker compose stop` (in `$Test`). To resume: `docker compose start`.

## 18. Refresh the test data from a newer backup

```powershell
Set-Location $Test
git pull
Copy-Item deploy\test-install\docker-compose.test.yml docker-compose.override.yml -Force
docker compose stop app audit-worker dashboard
$Dump = Get-ChildItem "$Live\backups" -Filter 'backup_*.dump*' | Sort-Object LastWriteTime | Select-Object -Last 1
Copy-Item $Dump.FullName "$Test\backups\"
$DumpName = $Dump.Name
```

Put the private backup key in place as in step 3, then:

```powershell
docker compose --profile tools build transfer
docker compose --profile tools run --rm -e BACKUP_AGE_IDENTITY_FILE=/backups/backup-key.txt transfer import $DumpName --replace
$LASTEXITCODE
Remove-Item "$Test\backups\backup-key.txt"
```

`--replace` is needed because the test database holds users; the tool first takes a safety backup
of the test database. Then **repeat step 11** (the restore brings back the rules that were on and
the Google sign-ins), step 12 (the waiting reminders), steps 9 and 10 if wanted, step 13, and start
with `docker compose up -d --build`. Old test backups in `$Test\backups` can be deleted.

## 19. Remove the test install completely

```powershell
Set-Location $Test
docker compose -p mypa-test --profile tools down --volumes --remove-orphans --rmi local
Set-Location $Live
Remove-Item -Recurse -Force $Test
docker volume ls --filter name=mypa-test
docker image ls --filter reference=mypa-test*
```

- `-p mypa-test` names the project explicitly, so the command can only remove the test install's
  containers, volumes, network and the images it built, never the live project's.
- The last two lines must list nothing.
- In BotFather, `/deletebot` and choose the test bot.
- If you added `http://localhost:8001/auth/google/callback` at Google, remove it.

## How the dashboard reaches the app

Checked in `dashboard/src`:

- The browser never calls the app. The only request the browser makes is to the dashboard's own
  `/api/auth/signin` (and sign-out), a relative address. Every GraphQL call, and the sign-in
  exchange, is made by the dashboard's server process (`lib/graphql.ts`, `lib/auth_routes.ts`).
- That process uses `GRAPHQL_ENDPOINT` first. The override sets it to `http://app:8001/graphql`,
  and `app` there is the `mypa-test` project's app: each project has its own network.
- `NEXT_PUBLIC_APP_URL` is only a fallback after `GRAPHQL_ENDPOINT` and `APP_URL`, and no
  browser code reads it. It is set from compose at run time, so no build argument is needed; the
  override sets it to the test app anyway.
- `DASHBOARD_URL` is read by the app, not the dashboard: it is where `/dashboard` sign-in links
  point. The override sets it to `http://127.0.0.1:3031`.
- The Google sign-in return address comes from the app's port when there is no public address. The
  override makes the test app listen on `8001`, so it is `http://localhost:8001/auth/google/callback`,
  never the live system's `8000`.
