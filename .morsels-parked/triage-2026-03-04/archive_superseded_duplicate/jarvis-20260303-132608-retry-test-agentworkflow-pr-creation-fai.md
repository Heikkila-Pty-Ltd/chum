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
    Task ch-29f1e previously failed DoD. Re-attempt: in internal/workflow/agent_workflow_test.go, add a test for AgentWorkflow where the PR creation step returns an error. The workflow should propagate the error and not proceed to review. Use mockDAG and stub out the GitHub client. Acceptance: test named TestAgentWorkflow_PRCreationFailure compiles, runs, and passes with go test ./internal/workflow/... — no skips, no panics.
depends_on: []
---

Task ch-29f1e previously failed DoD. Re-attempt: in internal/workflow/agent_workflow_test.go, add a test for AgentWorkflow where the PR creation step returns an error. The workflow should propagate the error and not proceed to review. Use mockDAG and stub out the GitHub client. Acceptance: test named TestAgentWorkflow_PRCreationFailure compiles, runs, and passes with go test ./internal/workflow/... — no skips, no panics.

_Created by Jarvis at 2026-03-03T13:26:08Z_
