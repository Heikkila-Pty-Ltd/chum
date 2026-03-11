---
title: "Retry ch-29f1e: Test AgentWorkflow PR creation failure path"
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
    Task ch-29f1e has dod_failed status — the acceptance criteria for the test weren't met. Revisit the test for AgentWorkflow PR creation failure path. Read the dod_failed feedback if available in the worktree or task notes. Reimplement the test so it correctly simulates a PR creation error and asserts the workflow returns the expected error. Acceptance criteria: test passes, go test ./... green, no regressions.
depends_on: []
---

Task ch-29f1e has dod_failed status — the acceptance criteria for the test weren't met. Revisit the test for AgentWorkflow PR creation failure path. Read the dod_failed feedback if available in the worktree or task notes. Reimplement the test so it correctly simulates a PR creation error and asserts the workflow returns the expected error. Acceptance criteria: test passes, go test ./... green, no regressions.

_Created by Jarvis at 2026-03-03T12:15:45Z_
