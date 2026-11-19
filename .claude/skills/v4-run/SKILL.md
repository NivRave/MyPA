---
name: v4-run
description: Runs the MyPA V4 roadmap item by item with review gates. Invoke as /v4-run <scope>, for example /v4-run stage 1.
---
# V4 roadmap runner

You are the orchestrator for MyPA V4. The source of truth is docs/ROADMAP.md. The rules are AGENTS.md and CLAUDE.md. Read the relevant sections of SYSTEM.md before designing; do not load it whole.

Scope: the argument names what to run. Run only that scope, in the build order recorded in ROADMAP §3, then stop with a report.

## Standing permissions for this run

- You may commit on feature branches and merge them into `dev` without asking, once every check below passes. This replaces the "wait for approval" step in AGENTS.md §3 for this run only.
- You may never: touch `master`; create tags; force-push or rewrite history; edit `.env`; start, stop, restart or rebuild the running containers; run `/backup` or `/restore`; run anything against the live database; delete, skip or weaken a test or guard; change an item's "done when"; change a decision in ROADMAP §7.

The live system runs the latest `v3.4.x` release until `v4.0.0`. Nothing on `dev` is deployed to it.

## For each item

1. Resume. Read docs/ROADMAP.md again, including any item or decision added since the last item, and the branch list. If an item is "In progress", continue it on its branch.
2. Pick the next "Planned" item in build order. Confirm its requirements are Done, any ADR that ROADMAP §7 lists for it is accepted, `dev` is clean and CI is green. If not, stop.
3. Create `feat/v4-NN-short-name` from `dev`. Set the item to "In progress" in the roadmap.
4. Plan. State the files, migrations, tests and risks. Record any real design choice in docs/DECISIONS_LOG.md.
5. Build test-first. The tests must prove the "done when" literally.
6. Verify. All must pass: go build; go vet; go test -race -p 1 ./...; golangci-lint; govulncheck; the dashboard lint, tests and build if it was touched; docker compose build; go run ./cmd/devtools/gendocs if tool declarations changed.
7. Review. Save `git diff dev...HEAD` to `.claude/tmp/V4-NN.diff` and give the v4-reviewer subagent only the item ID and that path. On FAIL, fix and review again, three rounds at most.
8. Docs on the same branch: SYSTEM.md when a model, Redis key, queue, tool, GraphQL type, environment variable or dashboard route changed; README if usage changed; the roadmap status to "Done" only if the "done when" is met; BACKLOG for anything found outside the scope. Do not fix out-of-scope findings here.
9. Verify the staged files belong to this item only. Commit as `type(scope): summary (V4-NN)`. Merge into `dev` with --no-ff, push, and wait for CI. If CI is red, fix it or stop.
10. Report what changed, how to test it by hand, and what I must check on the live system. Then take the next item.

## When the plan changes

- The roadmap is read again before every item, so a change I make takes effect at the next item. Never carry a remembered version of the plan across items.
- You do not add, remove, reorder or reword roadmap items or decisions yourself. If something should change, propose it and stop.
- A finished item is never edited. Rework is a new item that I add.
- Anything you find outside the current item goes to docs/BACKLOG.md and is not fixed here, unless it blocks the item; then stop.
- If I report a bug in the live system, do not fix it on `dev`. Finish or park the current item at a clean commit, then follow the hotfix rule in AGENTS.md §5, and only when I ask.

## Stop and report instead of continuing

- A required ADR is not accepted. Draft it in docs/adr/ and stop.
- The scope is finished. Releases are mine.
- The item needs something only I have: an account, token, key, domain, phone, or a check on the live system. Build and test against fakes, list the live checks, and continue only with items that do not depend on them.
- An owner task in BACKLOG gates the item.
- The roadmap looks wrong, or an item cannot meet its "done when". Propose the change; do not make it.
- Three failed review rounds, or tests that only pass if weakened.
- Anything that would touch live data or the running system.
