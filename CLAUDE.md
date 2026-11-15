@AGENTS.md

## Claude Code notes
- Section 2's tool names (view_file, grep_search, etc.) refer to another agent; use Claude Code's equivalents (Read, Edit, Grep, Glob). The intent still applies: prefer file tools over shell for reading/editing.
- PowerShell `-Encoding utf8` rule: applies only if you write files via PowerShell; normal Edit/Write tools already produce UTF-8 without BOM.
- Read SYSTEM.md on demand (it's ~100 KB) — don't import it.
- Active work plan: docs/REMEDIATION_PLAN.md
