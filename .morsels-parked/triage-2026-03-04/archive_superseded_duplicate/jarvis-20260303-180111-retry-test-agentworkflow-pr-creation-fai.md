---
title: "Retry: Test AgentWorkflow PR creation failure path (ch-29f1e)"
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
    ch-29f1e has dod_failed status. Re-implement the test for AgentWorkflow when PR creation fails. The test should: (1) set up a mock that returns an error from CreatePR, (2) verify AgentWorkflow returns an error and does not proceed to review stage, (3) verify the task state transitions to a failed/error state. Acceptance criteria: test compiles, passes with go test, and covers the PR creation failure branch. Must not exceed 15 min of work.
depends_on: []
---

ch-29f1e has dod_failed status. Re-implement the test for AgentWorkflow when PR creation fails. The test should: (1) set up a mock that returns an error from CreatePR, (2) verify AgentWorkflow returns an error and does not proceed to review stage, (3) verify the task state transitions to a failed/error state. Acceptance criteria: test compiles, passes with go test, and covers the PR creation failure branch. Must not exceed 15 min of work.

_Created by Jarvis at 2026-03-03T18:01:11Z_
