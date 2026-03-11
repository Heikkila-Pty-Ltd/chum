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
    Task ch-29f1e failed DOD check. Re-implement the test for AgentWorkflow covering the PR creation failure path. The test must: mock the GitHub PR creation activity to return an error, assert the workflow transitions to a failed state with the correct error message, and compile + pass with go test ./.... Acceptance: go test ./... green, test name matches TestAgentWorkflow_PRCreationFailure or equivalent, no skips.
depends_on: []
---

Task ch-29f1e failed DOD check. Re-implement the test for AgentWorkflow covering the PR creation failure path. The test must: mock the GitHub PR creation activity to return an error, assert the workflow transitions to a failed state with the correct error message, and compile + pass with go test ./.... Acceptance: go test ./... green, test name matches TestAgentWorkflow_PRCreationFailure or equivalent, no skips.

_Created by Jarvis at 2026-03-03T10:20:45Z_
