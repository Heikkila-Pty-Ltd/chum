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
    Task ch-29f1e was marked dod_failed. Re-implement the test for AgentWorkflow that mocks the PR creation step to return an error and asserts the workflow returns that error correctly. Acceptance criteria: test compiles, passes go test, follows same pattern as ch-bc2ea (setup failure path) and ch-c730f (review rejection). Max 15 min.
depends_on: []
---

Task ch-29f1e was marked dod_failed. Re-implement the test for AgentWorkflow that mocks the PR creation step to return an error and asserts the workflow returns that error correctly. Acceptance criteria: test compiles, passes go test, follows same pattern as ch-bc2ea (setup failure path) and ch-c730f (review rejection). Max 15 min.

_Created by Jarvis at 2026-03-03T12:45:56Z_
