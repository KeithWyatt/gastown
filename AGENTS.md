# Agent Instructions

See **CLAUDE.md** for complete agent context and instructions.

This file exists for compatibility with tools that look for AGENTS.md.

> **Recovery**: Run `gt prime` after compaction, clear, or new session

Full context is injected by `gt prime` at session start.

## CRITICAL RULES

- NEVER comment on PRs you didn't create - leave PR reviews to the repo owner
- Before researching or implementing fixes, check if a PR already exists for the issue (use: `gh pr list --search '<keywords>'`)
