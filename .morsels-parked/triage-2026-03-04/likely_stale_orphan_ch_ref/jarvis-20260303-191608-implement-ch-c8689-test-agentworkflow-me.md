---
title: "Implement ch-c8689: Test AgentWorkflow merge failure path"
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
    Write a unit test for the AgentWorkflow merge failure path. Should simulate a merge activity returning an error and assert the workflow surfaces it correctly. Pattern: mirror existing tests for setup failure (ch-bc2ea) and PR creation failure (ch-29f1e). Acceptance criteria: test compiles, runs, passes, coverage delta is positive.
depends_on: []
---

Write a unit test for the AgentWorkflow merge failure path. Should simulate a merge activity returning an error and assert the workflow surfaces it correctly. Pattern: mirror existing tests for setup failure (ch-bc2ea) and PR creation failure (ch-29f1e). Acceptance criteria: test compiles, runs, passes, coverage delta is positive.

_Created by Jarvis at 2026-03-03T19:16:08Z_
