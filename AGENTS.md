# AI Agent Global Rules for MyPA Workspace

This file enforces strict agent behaviors and development conventions for this workspace. All agent sessions must adhere to these rules.

## 1. Development Cycle
- **Branch Verification (CRITICAL)**: BEFORE starting any development, making file changes, or committing, you MUST verify the current branch using a terminal command.
- **Branching**: Develop on `dev`! Never perform work directly on `main` or `master`. You must explicitly checkout the `dev` branch (or a feature branch off of it) before proceeding with changes.
- **Verification**: Code must be tested and verified before considering a task complete. Run appropriate tests and checks.
- **Pre-Commit Container Build**: BEFORE deciding a task is complete and asking the user for permission to commit, you MUST run a full container build locally (e.g., `docker-compose build`) to ensure there are no hidden compilation or infrastructure errors. Never rely solely on partial `go build` commands for a single service.
- **Stage Verification (CRITICAL Guardrail)**: ALWAYS before committing, verify that the added/staged files are relevant to that commit alone. This is an important guardrail when multiple agents are touching different parts of the project concurrently.

## 2. Terminal Commands Execution
- **Native Tools First**: NEVER use PowerShell or terminal commands to read, write, search, or replace file contents (e.g., avoid commands like `Get-Content`, `cat`, `Out-File`, `sed`, `grep`, `ls`). ALWAYS prioritize and use the specialized native tools provided to you (e.g., `view_file`, `write_to_file`, `replace_file_content`, `grep_search`, `find_by_name`, `list_dir`) for these operations.
- **No Command Chaining**: When performing git operations (like `add`, `commit`, `push`) or other terminal actions, **do not** chain them together in a single command using `;` or `&&`. 
- **Separate Actions**: Execute each command as a separate action/tool call to ensure transparency and proper error handling.

## 3. Post-Milestone Reporting
After completing any feature development, bug fix, milestone, or major phase, you MUST pause and provide the user with a summary that includes:
- **What changed**: A clear explanation of what was done, added, removed, or modified.
- **How to test**: Step-by-step instructions for the user to manually test the changes.
- **Commit suggestion**: Suggest committing the changes and provide an appropriate, formatted commit message. (Wait for the user's approval before committing).

## 4. Documentation Maintenance
- **README Updates**: Always verify if the `README.md` file needs to be updated as a result of the changes made. If the new feature, bug fix, or configuration alters how the project is used or set up, update the README accordingly.

## 5. Release & Tagging Strategy
- **Semantic Versioning**: When preparing a release or merging features to `master`, you MUST suggest and apply a Semantic Version tag.
  - **PATCH (vX.X.1)**: For bug fixes, documentation updates, or minor adjustments.
  - **MINOR (vX.1.0)**: For new, backward-compatible features.
  - **MAJOR (v1.0.0)**: For massive architectural or breaking changes.
- **Workflow**: Tags must only be applied to the `master` branch after a successful merge from `dev`. Never tag `dev` directly with production version numbers.

## 6. Docker & Container Conventions
- **Unified Dockerfile**: All Go services are built from a single multi-target `Dockerfile` in the project root. When adding a new service:
  1. Add a `build-<service>` stage and a `<service>` runtime stage in `Dockerfile`.
  2. Add the service to `docker-compose.yml` with `target: <service>`.
  3. Use granular `COPY` (only `internal/`, `config/`, and `cmd/<service>/`) — never `COPY . .`.
  4. Always include BuildKit cache mounts (`--mount=type=cache`) for `/go/pkg/mod` and `/root/.cache/go-build`.
- **`.dockerignore`**: Keep `.dockerignore` up to date. Any new non-source directories or large files must be excluded.
- **Healthchecks**: Infrastructure services must include `start_period` in healthchecks to avoid unnecessary startup delays.

## 7. API & GraphQL Conventions
- **GraphQL Standard**: All new client-facing APIs must be implemented as GraphQL endpoints using `gqlgen` inside the `Orchestrator` service. Avoid creating new REST endpoints unless strictly necessary.
- **Routing**: Client applications (like the Dashboard) must communicate with the GraphQL API through the `Proxy` service (e.g., `http://proxy:8000/graphql`).
- **PowerShell Encoding**: When generating or modifying Go or GraphQL files via PowerShell tools, ALWAYS append `-Encoding utf8` to avoid UTF-16 BOM parsing errors with `gqlgen`.
