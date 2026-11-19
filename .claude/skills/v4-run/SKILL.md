---
name: v4-run
description: Runs one MyPA V4 roadmap item with review gates. Invoke as /v4-run V4-NN, for example /v4-run V4-01.
---
# V4 roadmap runner

You are the orchestrator for MyPA V4. The source of truth is docs/ROADMAP.md. The rules are AGENTS.md and CLAUDE.md.

Scope: the argument names one roadmap item (`/v4-run V4-NN`). Run only that item, then stop with a report. Never continue to the next item and never use `/goal`. If the argument is not exactly one item ID, stop and ask.

## Standing permissions for this run

- You may commit on feature branches and merge them into `dev` without asking, once every check below passes. This replaces the "wait for approval" step in AGENTS.md §3 for this run only.
- The full check in step 9 replaces the unconditional container build in AGENTS.md §1 for this run only.
- You may never: touch `master`; create tags; force-push or rewrite history; edit `.env`; start, stop, restart or rebuild the running containers; run `/backup` or `/restore`; run anything against the live database; delete, skip or weaken a test or guard; change an item's "done when"; change a decision in ROADMAP §7.

The live system runs the latest `v3.4.x` release until `v4.0.0`. Nothing on `dev` is deployed to it.

## Reading

Read the item's row in docs/ROADMAP.md, ROADMAP §2, AGENTS.md and the source files the item needs. Search for the item ID to find its row and its mentions in §3 and §7. Do not read the whole roadmap, SYSTEM.md or docs/DECISIONS_LOG.md up front; search them and open only the parts the item needs. Before designing, open the sections of SYSTEM.md the item touches.

## The item

1. Resume. Read the item's row and the branch list. If the item is "In progress", continue it on its branch.
2. Confirm the item is "Planned" or "In progress", its requirements are Done, any ADR that ROADMAP §7 lists for it is accepted, `dev` is clean and CI is green. If not, stop.
3. Create `feat/v4-NN-short-name` from `dev`. Set the item to "In progress" in the roadmap.
4. Plan. State the files, migrations, tests and risks. Record any real design choice in docs/DECISIONS_LOG.md.
5. Build test-first. The tests must prove the "done when" literally. While building, run only the tests of the packages being changed. A guard test must not be able to skip. A new test that calls `t.Skip` needs a reason in the report.
6. Docs on the same branch: SYSTEM.md when a model, Redis key, queue, tool, GraphQL type, environment variable or dashboard route changed; README if usage changed; the roadmap status to "Done" only if the "done when" is met; BACKLOG for anything found outside the scope. Do not fix out-of-scope findings here. Run `go run ./cmd/devtools/gendocs` if tool declarations changed. Any count written into a doc comes from command output in this session. After changing tests, search README.md, SYSTEM.md, dashboard/README.md, AGENTS.md and CLAUDE.md for test and suite counts and check each. A stale count in AGENTS.md or CLAUDE.md is reported, not edited, unless the item's text says to edit that file.
7. Verify the staged files belong to this item only. Commit as `type(scope): summary (V4-NN)`.
8. Review. Save `git diff dev...HEAD` to `.claude/tmp/V4-NN.diff`. Call the v4-reviewer subagent only when the item touches migrations or schema, identity, auth or tenant scoping, channel adapters, backup or restore, or is size M or larger; give it only the item ID and that path. Otherwise check the item against the eleven checks in .claude/agents/v4-reviewer.md yourself, fix what fails, and put the result in the report. One review round: after a FAIL, fix, save the diff again, and ask the reviewer to recheck only the failed findings. A second FAIL is a stop condition.
9. Full check, once, on the final commit before the merge. All must pass: `go build ./...`; `go vet ./...`; the race suite in the golang container (`go test -race -count=1 -p 1 ./...`); golangci-lint. Dashboard lint, tests and build only if `dashboard/` changed. `docker compose -p mypa-v4verify build` only if a Dockerfile, docker-compose.yml or go.mod changed. Note the commit the full check ran on. If a fix follows, commit it and rerun the full check.
10. Merge into `dev` with --no-ff, push, and wait for CI. If CI is red, fix it or stop.
11. Report in under 25 lines: what changed, how to test it by hand, what I must check on the live system, the commit the full check ran on, the review result (the reviewer's verdict, or your result for the eleven checks), and the reason for any new `t.Skip`. Write no separate handoff file unless the run stops on a stop condition. Then stop.

## When the plan changes

- The item's row is read fresh at every invocation, so a change I make takes effect at the next run. Never carry a remembered version of the plan across runs.
- You do not add, remove, reorder or reword roadmap items or decisions yourself. If something should change, propose it and stop.
- A finished item is never edited. Rework is a new item that I add.
- Anything you find outside the current item goes to docs/BACKLOG.md and is not fixed here, unless it blocks the item; then stop.
- If I report a bug in the live system, do not fix it on `dev`. Finish or park the current item at a clean commit, then follow the hotfix rule in AGENTS.md §5, and only when I ask.

## Stop and report instead of continuing

- A required ADR is not accepted. Draft it in docs/adr/ and stop.
- The item is finished. The next item and releases are mine.
- The item needs something only I have: an account, token, key, domain, phone, or a check on the live system. Build and test against fakes, and list the live checks in the report.
- An owner task in BACKLOG gates the item.
- The roadmap looks wrong, or the item cannot meet its "done when". Propose the change; do not make it.
- A second review FAIL, or tests that only pass if weakened.
- Anything that would touch live data or the running system.
