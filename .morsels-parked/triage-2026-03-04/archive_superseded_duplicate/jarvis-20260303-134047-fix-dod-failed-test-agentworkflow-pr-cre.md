---
title: "Fix dod_failed: Test AgentWorkflow PR creation failure path (ch-29f1e)"
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
    Task ch-29f1e is in dod_failed state. The PR it created failed the DoD checks (go build ./... && go test ./... && go vet ./...). Steps: (1) Find the PR on github for chum-factory that corresponds to ch-29f1e — look for a branch named after this task ID. (2) Check what the failing test file contains and why the DoD checks failed. (3) Fix the code so all three DoD checks pass. Acceptance criteria: go build ./... passes, go test ./... passes, go vet ./... passes, PR is ready for review.
depends_on: []
---

Task ch-29f1e is in dod_failed state. The PR it created failed the DoD checks (go build ./... && go test ./... && go vet ./...). Steps: (1) Find the PR on github for chum-factory that corresponds to ch-29f1e — look for a branch named after this task ID. (2) Check what the failing test file contains and why the DoD checks failed. (3) Fix the code so all three DoD checks pass. Acceptance criteria: go build ./... passes, go test ./... passes, go vet ./... passes, PR is ready for review.

_Created by Jarvis at 2026-03-03T13:40:47Z_
