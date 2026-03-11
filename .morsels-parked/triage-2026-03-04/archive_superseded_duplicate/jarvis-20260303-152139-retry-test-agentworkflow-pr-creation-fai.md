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
    Task ch-29f1e previously failed DOD. Re-implement the test for AgentWorkflow where PR creation returns an error. The test must: (1) mock the PR creation call to return an error, (2) assert the workflow returns a non-nil error, (3) assert no review signal is sent. Acceptance criteria: test compiles, passes go test, and covers the PR creation failure branch explicitly. Max 15 min.
depends_on: []
---

Task ch-29f1e previously failed DOD. Re-implement the test for AgentWorkflow where PR creation returns an error. The test must: (1) mock the PR creation call to return an error, (2) assert the workflow returns a non-nil error, (3) assert no review signal is sent. Acceptance criteria: test compiles, passes go test, and covers the PR creation failure branch explicitly. Max 15 min.

_Created by Jarvis at 2026-03-03T15:21:39Z_
