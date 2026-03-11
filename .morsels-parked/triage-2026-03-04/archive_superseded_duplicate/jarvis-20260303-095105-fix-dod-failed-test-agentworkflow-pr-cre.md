---
title: "Fix dod_failed: Test AgentWorkflow PR creation failure path"
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
    Task ch-29f1e passed agent work but failed DoD checks (go build ./... or go test ./... or go vet ./...). Investigate what broke in the test for AgentWorkflow PR creation failure path. Check the PR/branch associated with ch-29f1e, find the failing check, and fix it. Acceptance criteria: go build ./..., go test ./..., and go vet ./... all pass in chum-factory workspace.
depends_on: []
---

Task ch-29f1e passed agent work but failed DoD checks (go build ./... or go test ./... or go vet ./...). Investigate what broke in the test for AgentWorkflow PR creation failure path. Check the PR/branch associated with ch-29f1e, find the failing check, and fix it. Acceptance criteria: go build ./..., go test ./..., and go vet ./... all pass in chum-factory workspace.

_Created by Jarvis at 2026-03-03T09:51:05Z_
