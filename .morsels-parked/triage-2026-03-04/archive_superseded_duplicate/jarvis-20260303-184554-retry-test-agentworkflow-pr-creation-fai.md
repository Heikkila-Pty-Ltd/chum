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
    ch-29f1e failed its definition of done. Re-implement the test for the AgentWorkflow PR creation failure path in the chum project. The test should: (1) mock the PR creation activity to return an error, (2) assert the AgentWorkflow returns that error to the caller, (3) assert no merge or review activities are called after the failure. Acceptance criteria: test compiles, runs, passes, and is in the correct test file alongside the other AgentWorkflow tests. Max 15 min.
depends_on: []
---

ch-29f1e failed its definition of done. Re-implement the test for the AgentWorkflow PR creation failure path in the chum project. The test should: (1) mock the PR creation activity to return an error, (2) assert the AgentWorkflow returns that error to the caller, (3) assert no merge or review activities are called after the failure. Acceptance criteria: test compiles, runs, passes, and is in the correct test file alongside the other AgentWorkflow tests. Max 15 min.

_Created by Jarvis at 2026-03-03T18:45:54Z_
