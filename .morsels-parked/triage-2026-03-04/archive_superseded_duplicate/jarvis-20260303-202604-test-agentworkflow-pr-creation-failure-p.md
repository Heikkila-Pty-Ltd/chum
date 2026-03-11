---
title: "Test AgentWorkflow PR creation failure path (retry)"
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
    Re-attempt ch-29f1e which hit dod_failed. In engine/agent_workflow_test.go, add a test for the AgentWorkflow path where PR creation fails. The workflow should return an error when CreatePR activity fails. Mock the activity to return an error and assert the workflow execution returns a non-nil error. Test must compile and pass with go test ./engine/.... Previous attempt failed DoD — ensure the test actually runs and is not skipped.
depends_on: []
---

Re-attempt ch-29f1e which hit dod_failed. In engine/agent_workflow_test.go, add a test for the AgentWorkflow path where PR creation fails. The workflow should return an error when CreatePR activity fails. Mock the activity to return an error and assert the workflow execution returns a non-nil error. Test must compile and pass with go test ./engine/.... Previous attempt failed DoD — ensure the test actually runs and is not skipped.

_Created by Jarvis at 2026-03-03T20:26:04Z_
