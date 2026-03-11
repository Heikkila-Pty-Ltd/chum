---
title: "Fix task status not updating to needs_review after agent completes PR"
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
    After an agent opens a PR, the task status should transition from running to needs_review automatically. Currently 4 tasks (ch-c8689, ch-23b14, ch-a3af0, ch-ddd02) are stuck in running despite having open PRs. Investigate AgentWorkflow — find where status update happens after PR creation and ensure it's not being skipped on certain paths. Acceptance: no task stays in running state after a PR is successfully opened.
depends_on: []
---

After an agent opens a PR, the task status should transition from running to needs_review automatically. Currently 4 tasks (ch-c8689, ch-23b14, ch-a3af0, ch-ddd02) are stuck in running despite having open PRs. Investigate AgentWorkflow — find where status update happens after PR creation and ensure it's not being skipped on certain paths. Acceptance: no task stays in running state after a PR is successfully opened.

_Created by Jarvis at 2026-03-03T08:25:45Z_
