---
name: git-commit
description: Commit only currently unstaged Git changes with an orthodox production-grade message using Conventional Commit style, project-aware scope inference, optional branch ticket prefix, and a strict 20-word limit. Use when asked to auto-stage non-staged tracked/untracked files and produce a professional commit summary.
---

# Git Commit

Use this skill when the task is: "commit unstaged changes" with production-grade commit hygiene.

## Workflow
1. Verify the current directory is inside a Git repository.
2. Collect files that are currently unstaged:
- Tracked files with unstaged edits (`git diff --name-only`)
- Untracked files (`git ls-files --others --exclude-standard`)
3. If no unstaged files exist, stop and report that nothing qualifies.
4. Stage only those unstaged files with `git add -- <files>`.
5. Run `scripts/commit_unstaged.sh` to generate a professional commit message (<=20 words) and create the commit.
6. Return the commit hash and the final message.

## Message Rules
- Keep commit message at 20 words or fewer.
- Use Conventional Commit subject format: `type(scope): summary`.
- Infer scope from dominant project area (for this repo: prefer `cmd`, `internal`, or `repo`).
- Infer type from file mix:
  - `feat` for added source functionality
  - `fix` when branch name suggests bug/hotfix
  - `refactor` for source-only modifications without feature additions
  - `docs`, `test`, `chore` for corresponding change groups
- Prefix ticket ID when branch contains pattern like `ABC-123`.
- Summarize change types (add/modify/delete/rename) from staged diff.
- Keep message single-line, clean, and professional.

## Script
- `scripts/commit_unstaged.sh`
  - Detect unstaged files.
  - Stage only those files.
  - Build and enforce <=20-word commit message.
  - Commit and print commit hash + message.
