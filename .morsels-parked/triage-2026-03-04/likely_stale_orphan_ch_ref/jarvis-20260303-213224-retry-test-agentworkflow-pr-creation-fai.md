---
title: "Retry: Test AgentWorkflow PR creation failure path"
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
    ch-29f1e previously failed its definition of done. Re-implement TestAgentWorkflow_PRCreationFailure in the workflow tests package. The test should: (1) mock CreatePRActivity to return an error, (2) assert the workflow returns that error, (3) assert no merge or review activities were called. Must pass go test ./... and all existing tests must remain green. Acceptance: test file compiles, test passes, dod_failed status resolved.
depends_on: []
---

ch-29f1e previously failed its definition of done. Re-implement TestAgentWorkflow_PRCreationFailure in the workflow tests package. The test should: (1) mock CreatePRActivity to return an error, (2) assert the workflow returns that error, (3) assert no merge or review activities were called. Must pass go test ./... and all existing tests must remain green. Acceptance: test file compiles, test passes, dod_failed status resolved.

_Created by Jarvis at 2026-03-03T21:32:24Z_
