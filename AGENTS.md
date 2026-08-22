# AI Agent Global Rules for MyPA Workspace

This file enforces strict agent behaviors and development conventions for this workspace. All agent sessions must adhere to these rules.

## 1. Development Cycle
- **Branch Verification (CRITICAL)**: BEFORE starting any development, making file changes, or committing, you MUST verify the current branch using a terminal command.
- **Branching**: Develop on `dev`! Never perform work directly on `main` or `master`. You must explicitly checkout the `dev` branch (or a feature branch off of it) before proceeding with changes.
- **Verification**: Code must be tested and verified before considering a task complete. Run appropriate tests and checks.

## 2. Terminal Commands Execution
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
