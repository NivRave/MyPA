# Restore and migrate

One mechanism covers three jobs (ROADMAP V4-43, decision DE-10):

1. upgrading from the pre-V4 baseline release (`v3.4.x`) to V4,
2. moving an install between machines (home PC to server and back),
3. getting data back after it was lost or damaged on a working machine.

In every case a backup is restored into a V4 install and the versioned migrations bring it up to
date. The backup that was restored is never modified, so it is also the way back.

> **Status.** The mechanism was built in stage 1 of V4.0 and is tested end to end against throwaway
> databases (real `pg_dump`, `pg_restore` and migrations). It has not yet been run on the live
> system. V4-43 is marked done at the V4.0 release, after the checks at the end of this file.

The tool is `mypa transfer`, run through the `transfer` compose service. It is never
started by `docker compose up`.

```
docker compose --profile tools run --rm transfer <command>
```

## What a backup is

A backup is a **set** of files in `backups/` that share one timestamp:

| File | Contents |
| :--- | :--- |
| `backup_<time>.dump.age` | The database (`pg_dump`, custom format), encrypted with the backup key |
| `backup_<time>.archives.tar.age` | The archive files (audit archives today), encrypted with the backup key |
| `backup_<time>.manifest.json` | Sizes and SHA-256 of the two files, the schema version, whether the queues were drained, and the list of what is not carried. Not secret |

- The nightly backup (02:00) and `/backup` write sets, taken from the running app.
- `transfer export` writes the same kind of set from a **stopped** app. Use it for an upgrade or a move.
- A backup from before sets existed is a single `backup_<time>.dump.age` with no manifest. The
  baseline backup of `v3.4.0` is one. It restores the same way, without archive files.
- Without `BACKUP_AGE_RECIPIENTS` the files are not encrypted and have no `.age` suffix.
  `transfer export` refuses that unless you pass `--allow-unencrypted`.
- Retention removes a whole set at a time.

## The two keys

| Key | Setting | Without it |
| :--- | :--- | :--- |
| Data encryption key | `ENCRYPTION_KEY` | The restored database opens, but the secrets inside it do not: Google sign-ins and API connector secrets stop working |
| Backup key (private half) | the file named by `BACKUP_AGE_IDENTITY_FILE` | The backup itself cannot be opened |

The private backup key is kept off the machine (in your password manager) and is set only while
restoring. The data encryption key lives in `.env` and has to be brought to the new install.

```
# old install: write the data encryption key to a file locked with the backup key
docker compose --profile tools run --rm transfer export-keys
#   -> backups/mypa-keys.age

# new install: open it with the private backup key and get the settings lines
docker compose --profile tools run --rm -e BACKUP_AGE_IDENTITY_FILE=/backups/backup-key.txt \
  transfer import-keys
#   -> backups/mypa-keys.env  (copy its lines into .env, then delete it)
```

- The key file is locked with the backup key (`BACKUP_AGE_RECIPIENTS`). It opens only with the
  private backup key, which you bring to the restore anyway. Nothing secret is shown or typed.
- If backups are not encrypted there is no backup key to lock the file with. Use
  `export-keys --passphrase`: the file is locked with a generated passphrase instead, and opened
  with `import-keys --passphrase-file FILE`. The passphrase is printed once. Because it is
  printed, it is also in that container's log until the container is removed, so always run the
  command with `--rm`, clear the terminal, and carry the passphrase separately from the file.
- `export-keys --passphrase --with-backup-key` also puts the private backup key into the file
  (it cannot be locked with itself). `import-keys` then writes it to
  `backups/mypa-backup-key.txt`. Use this only if you have no other way to carry that key.
- A key file is never part of a backup set.
- `import` checks the data encryption key after restoring. If it does not open the stored
  secrets it says so and exits with an error. Fix `.env` and start the app; there is no need to
  import again.

## What is not carried

Until Postgres is the only state store (V4-23), some short-lived state lives in Redis and RabbitMQ
and is not in a backup. Every export and import prints this list:

- The conversation context of the last 24 hours (the chat window). Saved facts are kept.
- Actions that were waiting for a `CONFIRM` reply.
- Google sign-in links that were sent but not finished (`/connect` again).
- Pairing lockouts and failed-attempt counters.
- Link codes (`/link`, V4-32) that were issued but not yet confirmed. Only a keyed hash of each code
  is stored, under `LINK_CODE_SECRET`, which is kept out of the database and the backup on purpose:
  on an install with a different `LINK_CODE_SECRET` they no longer work; ask for a new code. Copy
  the setting with the rest of `.env` to keep them.
- Follow-up tool context and cached prompt sections (rebuilt on the next message).
- The memory of already-handled message IDs: a message a platform re-delivers across the move
  could be answered twice.
- Messages that had failed for good (the dead-letter queues that `/doctor` reports) stay in the
  old broker. `export` says how many there are. Look at them before retiring the old install.
- WhatsApp's own 24-hour reply window is kept by WhatsApp: someone who has not written for a day
  cannot be messaged until they write.

Google sign-ins **are** carried: each has an encrypted copy in the database. `transfer export`
first copies any sign-in that exists only in the cache into the database, and reports how many.

That copy step happens only in `transfer export`. A nightly backup, `/backup`, and any bare dump
taken by `v3.4.x` (including the baseline backup) hold just the sign-ins that already had a
database copy. A sign-in made before that copy existed lives in Redis alone and is **not** in such
a backup: after restoring one, that person has to send `/connect` again. To carry every sign-in,
take the backup with `transfer export` on the stopped old install, which works against a `v3.4.x`
database too.

Messages in the queues are not carried either, which is why an export requires the queues to be
empty.

## Only one install is ever live

Two installs on the same Telegram bot and WhatsApp number would both answer. The tool checks
what the queues can show:

- `export` refuses unless both work queues are **empty** and have **no consumer**: the app (on
  `v3.4.x`, the orchestrator) and the audit worker are stopped and nothing is waiting.
- `import` refuses while anything is consuming the target's queues.

It cannot see what only accepts messages, because that publishes without consuming. Whatever
lets messages in must stop first, so the queue can drain before the app goes away. On V4 (one
`app` container, V4-01) that is the tunnel or proxy your `INGRESS` names (V4-40): the service of
the same name, `ngrok`, `cloudflare`, `tailscale` or `caddy`. With `INGRESS=proxy`, stop your
own proxy instead.

```
docker compose stop ngrok                         # or cloudflare, tailscale, caddy: nothing new comes in
# wait a few seconds: the app finishes what is queued
docker compose stop app audit-worker dashboard
```

With `INGRESS=none` there is nothing in front: the app fetches Telegram messages itself, and
stopping it stops both. If `export` then says the inbound queue still holds messages, drain it
with a worker that fetches nothing new, and stop it with Ctrl+C once the queue is empty:

```
docker compose run --rm app --role=worker
```

On the baseline (`v3.4.x`, before the upgrade in section A), the same steps name its services:

```
docker compose stop proxy gateway                 # nothing new comes in
# wait a few seconds: the orchestrator finishes what is queued
docker compose stop orchestrator audit-worker dashboard
```

Check with `docker compose ps` that none of them is still running (an app started with
`--role=ingress` in a split deploy counts as something that lets messages in). Leave `postgres`,
`rabbitmq` and `redis` running: the tool needs them. If `export` says a queue still holds
messages, start the app (on `v3.4.x`, the orchestrator) again, leaving what lets messages in
stopped, until it is empty.

While the app is stopped, Telegram keeps messages (for up to 24 hours) and delivers them when a
webhook is registered again or the app polls again. Twilio does not hold them for later: WhatsApp messages sent during the move are lost. Tell
the family first.

## A. Upgrade from the baseline (`v3.4.x`) on the same machine

1. Stop the app with the baseline's commands (see above).
2. Check out the V4 release. Do **not** run `up` yet. Change `.env` for the one public address
   (V4-40, section D): the app refuses to start while an old address setting is still there.
3. Take the final backup of the baseline. This is the rollback point:
   ```
   docker compose --profile tools build transfer
   docker compose --profile tools run --rm transfer export
   ```
4. Start V4. The `migrate` service brings the schema up to date in place. `--remove-orphans`
   removes the stopped `proxy`, `gateway` and `orchestrator` containers, which V4 replaces with
   the one `app` container (V4-01):
   ```
   docker compose up -d --build --remove-orphans
   ```
   Migration 11 (V4-07) retires two old tables on the way: any fact still only in the old
   `memories` table is copied into the memories, and each scheduled workflow becomes a cron
   automation rule (the workflow tools still list and delete it). Migration 13 (BL-26) corrects
   memories saved with a scope other than `personal` or `family`: one spelled `Family` (in any
   case, with spaces) becomes `family`, and so visible to the household again; anything else
   becomes `personal`. It logs only how many rows of each kind it changed. Migration 14 (BL-68)
   adds two columns to the change history and changes no row. A change recorded before it is
   still undone where its saved copy allows; for one that cannot be, `/undo` says which change it
   is and changes nothing.
5. Send `/doctor` as the operator (every former admin is the operator since V4-35).
6. Rewrite the secrets still in the old encryption format (V4-07). The dry run only counts; the
   second command rewrites them, and is safe while the app runs:
   ```
   docker compose run --rm app reencrypt --dry-run
   docker compose run --rm app reencrypt
   ```
   It must end with "No old-format value remains in any store". V4.1 stops reading the old
   format (V4-45), so do this before upgrading to it. A value it reports as "not readable with
   this key" is not affected by V4-45: it was written with another `ENCRYPTION_KEY` or is damaged
   (that Google sign-in needs `/connect` again), or it is a sign-in an old release cached
   unencrypted, which the app encrypts the next time it reads it.
7. Review the yes/no settings stored before the upgrade (the upgrade notes below).

**To roll back:** stop the app, restore the set from step 3 without migrating, check out `v3.4.x`
and start it.

```
docker compose --profile tools run --rm -e BACKUP_AGE_IDENTITY_FILE=/backups/backup-key.txt \
  transfer import backup_<time>.manifest.json --replace --no-migrate
git checkout v3.4.x
docker compose up -d --build --remove-orphans
```

### Upgrade notes: yes/no settings stored before V4.0 (BL-43, BL-62)

Before V4.0 the app could not store "no" in seven yes/no columns: every `false` it was given was
stored as `true` (the model's default took its place). Since V4.0 each value is stored as given,
and migration 12 (`00012_no_true_defaults.sql`) removes the columns' `DEFAULT true`. **The upgrade
does not change any row.** A row stored before it that says `true` may have been meant as `false`,
and nothing in the database tells the two apart. Review these once after upgrading:

| Table | Column | A wrong `true` means | Correct it with |
| :--- | :--- | :--- | :--- |
| `financial_transactions` | `is_shared` | a transaction its person logged as private is visible to every member, in chat and on the dashboard | its person makes it private (BL-66): in chat ("make transaction 12 private") or with Edit on the dashboard's Finance page. No one else can, an owner included |
| `shared_spaces` | `can_view` | the members of a topic set to "no view" are still notified of each change in it | the dashboard's Shared Spaces page, or `configure_shared_topic` in chat |
| `shared_spaces` | `can_edit` | a topic set to "no edit" is shown, and described to the assistant, as editable; nothing enforces the setting | the same |
| `automation_rules` | `is_active` | a rule created switched off runs on its schedule or its events | the toggle on the dashboard's Automations page |
| `recurring_bills` | `auto_renew` | a bill set to manual renewal is shown as renewing itself (a label only) | `manage_recurring_bills` (`update`) in chat |
| `schedule_events` | `sync_to_google` | an event meant to stay in MyPA is labelled as synced ("Google Synced" in the event list, `[Google]` in the assistant's context). It was **not** sent to Google: chat decided that from the value asked, and the dashboard never sends events to Google. Do not delete anything in Google Calendar because of this column | nothing in the app; the label is harmless |
| `topic_calendar_configs` | `include_in_morning_briefing` | nothing today: the setting is stored and shown, and no briefing reads it | `configure_topic_calendar` in chat, before something starts reading it |

To list the rows to review (read-only; `mypa` is the default database user and name):

```
docker compose exec postgres psql -U mypa -d mypa -c "SELECT id, user_id, merchant, amount, transaction_date FROM financial_transactions WHERE is_shared ORDER BY transaction_date"
docker compose exec postgres psql -U mypa -d mypa -c "SELECT id, topic, owner_user_id, shared_with_user_id, can_view, can_edit FROM shared_spaces"
docker compose exec postgres psql -U mypa -d mypa -c "SELECT id, name, trigger_type, is_active FROM automation_rules WHERE is_active"
docker compose exec postgres psql -U mypa -d mypa -c "SELECT id, name, auto_renew FROM recurring_bills WHERE auto_renew"
docker compose exec postgres psql -U mypa -d mypa -c "SELECT user_id, topic, include_in_morning_briefing FROM topic_calendar_configs WHERE include_in_morning_briefing"
```

## B. Move to another machine (or restore the baseline backup into V4 elsewhere)

On the old machine:

1. Stop the app (see above).
2. `transfer export` (with V4), then `transfer export-keys`. Read what the export prints: it
   reports Google sign-ins it could not carry and failed messages left in the old broker.
   If the old machine can no longer run an export and you only have a nightly or baseline
   backup, a bare `backup_<time>.dump.age` restores too, without archive files and without
   Google sign-ins that existed only in the old cache (see "What is not carried").
3. Copy the files of the set, and `mypa-keys.age`, to the new machine's `backups/` folder.

On the new machine, with V4 checked out and a `.env` prepared:

4. Start only the infrastructure: `docker compose up -d postgres rabbitmq redis`.
5. Put the private backup key in `backups/backup-key.txt` for the duration of the restore.
6. Bring the data encryption key: `transfer import-keys` (see "The two keys"), then copy the
   lines of `backups/mypa-keys.env` into `.env`.
7. Restore and migrate:
   ```
   docker compose --profile tools run --rm -e BACKUP_AGE_IDENTITY_FILE=/backups/backup-key.txt \
     transfer import backup_<time>.manifest.json
   ```
   For a bare dump, name the `.dump.age` file instead.
8. Delete every key file from `backups/`: `backup-key.txt`, `mypa-keys.age`, `mypa-keys.env`,
   and, if you used a passphrase, `mypa-backup-key.txt` and the passphrase file. The private
   backup key goes back to being kept off the machine only.
9. `docker compose up -d --build`, then `/doctor`. After restoring a baseline (`v3.4.x`) backup,
   also run `reencrypt` as in section A, step 6, and review the yes/no settings as in section A's
   upgrade notes.
10. The old install stays stopped. Keep it until the new one has proven itself; its database is
    untouched.

Public addresses (Telegram webhook, Twilio callback, Google redirect) all come from `PUBLIC_URL`
(V4-40). Moving between a home PC and a server is section D.

## C. Data lost or damaged on a working machine

1. Stop the app.
2. Pick the newest good set in `backups/` (nightly sets are taken from the running app, so a
   message in flight at 02:00 may be missing).
3. `transfer verify backup_<time>.manifest.json` checks the set without touching anything.
4. Restore:
   ```
   docker compose --profile tools run --rm -e BACKUP_AGE_IDENTITY_FILE=/backups/backup-key.txt \
     transfer import backup_<time>.manifest.json --replace
   ```
   `--replace` is required because the database still holds users. The tool first takes a safety
   backup of the database as it is, and stops if that fails.
5. `docker compose up -d`, then `/doctor`.

If RabbitMQ or Redis themselves are gone, the checks that need them can be turned off explicitly:
`--skip-stop-check`, and for an export `--skip-sign-in-check`. The manifest then records that the
queues of the source were not confirmed drained.

Backups are stored on this machine only. Losing the machine loses them (OP-11).

## D. Move between local and a server (V4-40)

The same release runs at home and on a server. A move is section B plus a change of how the
install is reached, in `.env`; no file is edited.

| Mode | `INGRESS` | `PUBLIC_URL` | `COMPOSE_PROFILES` |
| :--- | :--- | :--- | :--- |
| Home PC, Telegram only, no tunnel | `none` | *(empty)* | *(empty)* |
| Home PC with a tunnel | `cloudflare`, `tailscale` or `ngrok` | the tunnel's address | the same word |
| Server with your own domain (Linux; DNS pointing at it, ports 80 and 443 open) | `caddy` | `https://<your domain>` | `caddy` |
| Server behind your own reverse proxy, forwarding to `127.0.0.1:8000` | `proxy` | your proxy's address | *(empty)* |

**Keep the address.** On a domain you control, `PUBLIC_URL` stays the same in both modes. Only
the place it points at changes: the DNS record (for `caddy`), or the Cloudflare tunnel's
connector (for `cloudflare`, the same tunnel token on the new machine). Then nothing changes at
Twilio or Google, and Telegram's webhook is registered again at start. An ngrok or Tailscale
address belongs to that account and node, so with those the address moves with the tunnel; if it
changes, update the webhook in the Twilio console and the redirect URI at Google.

**From home to a server, and back.** The direction does not matter:

1. On the old machine, stop what lets messages in, then the app (see "Only one install is ever
   live"), and run `transfer export` and `transfer export-keys` (section B, steps 1 to 3).
2. Copy the set and `mypa-keys.age` to the new machine. Check out the same release there.
3. Prepare the new machine's `.env` from the old one, changing only `INGRESS`, `PUBLIC_URL` and
   `COMPOSE_PROFILES` for the new mode (and the tunnel's token, if the mode has one).
4. Restore (section B, steps 4 to 8).
5. For `caddy`, point the domain's DNS at the new machine now. For `none`, nothing reaches the
   install and Telegram polls; WhatsApp needs a public address and the app refuses to start with
   it configured, so remove the Twilio settings or choose another mode.
6. `docker compose up -d --build`, then `/doctor`. The Telegram line says whether Telegram
   delivers here: by polling with `none`, at `PUBLIC_URL` otherwise.
7. Send a message on every channel in use. Keep the old install stopped.

What is not carried across a move is the same as for any restore ("What is not carried").

**Upgrading the `.env` of a `v3.4.x` install.** Remove `TELEGRAM_WEBHOOK_URL`,
`TWILIO_STATUS_CALLBACK_URL`, `GOOGLE_REDIRECT_URL` and `NGROK_DOMAIN`. For the ngrok setup the
baseline used, set `INGRESS=ngrok`, `PUBLIC_URL=https://<NGROK_DOMAIN>` and
`COMPOSE_PROFILES=ngrok`. The addresses Telegram, Twilio and Google know stay the same.

## What `import` does, in order

Steps 1 to 5 change nothing. If any of them fails, the install is exactly as it was.

1. Verifies the set against its manifest (sizes and checksums).
2. Requires the backup key if the files are encrypted.
3. Opens the backup with that key and reads the dump and the archive bundle to their end. A wrong
   key, or a file that is damaged or was cut short in a copy, stops here. (A bare dump has no
   manifest, so this read is its only integrity check.)
4. Refuses if this install is running.
5. Refuses if the database already holds users, unless `--replace`; then takes a safety backup,
   and stops if that backup fails.
6. Empties the database schema and restores the dump into it, as one transaction.
7. Runs the migrations and confirms the schema is at this release's version (skipped with
   `--no-migrate`).
8. Restores the archive files. A file that already exists with the same content is left alone;
   one that exists with different content stops the step and is never overwritten.
9. Checks that `ENCRYPTION_KEY` opens the restored secrets, and counts the Google sign-ins.
10. Takes a first backup of the restored install, so it is not without one of its own until the
    next nightly run (not with `--no-migrate`, and not if a step above left a problem).
11. Prints what was not carried and what to do next. Any line starting with `!` means the restore
    is not finished, and the command exits with an error.

## `/restore` in chat

A restore is never started from chat. A restore into the running app would keep newer tables next
to older data and skip the migrations, so the chat restore was retired (BL-21), and `CONFIRM
RESTORE` no longer does anything special.

`/restore` (the operator only, the same on Telegram and WhatsApp) restores nothing. It names the newest
backup in `backups/` (the manifest of a set, or a bare dump) and replies with the steps of
section C for it: stop the app, bring the private backup key if the backup is encrypted, run
`transfer import <name> --replace`, start the app and send `/doctor`. The backup is named by its
file name only. The one path in the reply is the key location of the command in section C
(`/backups/backup-key.txt`, for an encrypted backup), and no error text is shown. With no backup it
says so; `/backup` takes one.

## Commands

| Command | What it does |
| :--- | :--- |
| `transfer export` | Final backup set of a stopped install. Flags: `--allow-unencrypted`, `--skip-stop-check`, `--skip-sign-in-check` |
| `transfer import FILE` | Restore a set (`…manifest.json`) or a dump (`….dump[.age]`) and migrate. Flags: `--replace`, `--no-migrate`, `--skip-stop-check` |
| `transfer verify FILE` | Check a set against its manifest |
| `transfer export-keys` | Write the data encryption key to a file locked with the backup key. Flags: `--out FILE`, `--passphrase` (lock with a generated passphrase instead), `--with-backup-key` (needs `--passphrase`) |
| `transfer import-keys` | Write the settings lines from a key file, opened with the private backup key. Flags: `--in FILE`, `--passphrase-file FILE`, `--out FILE` |

`FILE` may be a name inside the backup directory.

## Checks before V4-40 is marked done (owner)

- [ ] One install is moved from the home PC to a server (section D) and back, and after each
      move every feature in use still works and nothing is lost except what "What is not carried"
      lists.
- [ ] On a domain you control, the move changed nothing at Twilio or Google.
- [ ] A Telegram-only install with `INGRESS=none` and no tunnel receives and answers messages,
      and `/doctor` reports Telegram polling.
- [ ] Each ingress you intend to use (`caddy`, `cloudflare`, `tailscale`, `ngrok`) reaches the
      app once: Telegram's `/doctor` line is healthy and a WhatsApp message is answered.

## Checks before V4-43 is marked done (owner, at the V4.0 release)

- [ ] `transfer export` on the stopped `v3.4.x` install succeeds and reports the Google sign-ins.
- [ ] The baseline backup (or that export) restores into V4.0 **on a different machine**.
- [ ] Each person's Google features work there without `/connect`.
- [ ] Every feature in use is still available: migration `00006_packs.sql` switches on every pack that holds data for a household (V4-27), and the dashboard's **Tools & Packs** page shows them on.
- [ ] `/doctor` reports no failures.
- [ ] A rollback with `--no-migrate` brings `v3.4.x` back up on the old machine.
