---
title: "CLI: chum review --task TASK_ID for manual review trigger"
status: ready
priority: 1
type: feature
labels:
  - jarvis-initiative
  - chum
estimate_minutes: 30
acceptance_criteria: |
  - Task completed as described
design: |
    Add a chum review subcommand that takes a task ID, finds the open PR on GitHub (by branch name or gh pr list --head chum/TASK_ID), and starts a Temporal workflow to run the review loop against that PR. This is the manual escape hatch for orphaned needs_review tasks. Acceptance: chum review --task ch-79602 --project chum triggers the review+merge workflow and the task ends up either merged or in dod_failed.
depends_on: []
---

Add a chum review subcommand that takes a task ID, finds the open PR on GitHub (by branch name or gh pr list --head chum/TASK_ID), and starts a Temporal workflow to run the review loop against that PR. This is the manual escape hatch for orphaned needs_review tasks. Acceptance: chum review --task ch-79602 --project chum triggers the review+merge workflow and the task ends up either merged or in dod_failed.

_Created by Jarvis at 2026-03-03T08:46:54Z_
