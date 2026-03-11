---
title: "Mark stale needs_review tasks as done where PRs are merged"
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
    Chum tasks ch-08021, ch-50612, ch-52426, ch-00440, ch-c8689 are in needs_review state but their branches (chum/ch-c8689 and others) have already been merged to master. The Chum DAG store needs to be updated to mark these tasks as completed. Investigate the task state transition logic — when a PR is merged, should the task auto-transition to done? If not, add a chum sync or reconcile command that checks PR status and updates task states. Acceptance criteria: after running the reconcile, all five tasks show [done] or [completed] in chum tasks output.
depends_on: []
---

Chum tasks ch-08021, ch-50612, ch-52426, ch-00440, ch-c8689 are in needs_review state but their branches (chum/ch-c8689 and others) have already been merged to master. The Chum DAG store needs to be updated to mark these tasks as completed. Investigate the task state transition logic — when a PR is merged, should the task auto-transition to done? If not, add a chum sync or reconcile command that checks PR status and updates task states. Acceptance criteria: after running the reconcile, all five tasks show [done] or [completed] in chum tasks output.

_Created by Jarvis at 2026-03-03T23:51:29Z_
