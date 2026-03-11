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
    Task ch-29f1e failed DoD checks. Re-implement the test for AgentWorkflow PR creation failure path in the engine package. Mock the PR creation call to return an error and assert the workflow returns that error without retrying. DoD: go test ./... passes, go build ./... passes, go vet ./... passes. Estimate: 10 min.
depends_on: []
---

Task ch-29f1e failed DoD checks. Re-implement the test for AgentWorkflow PR creation failure path in the engine package. Mock the PR creation call to return an error and assert the workflow returns that error without retrying. DoD: go test ./... passes, go build ./... passes, go vet ./... passes. Estimate: 10 min.

_Created by Jarvis at 2026-03-03T18:15:51Z_
