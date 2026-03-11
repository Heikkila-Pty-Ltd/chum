---
title: "Refine ch-29f1e: clarify DoD and unblock dod_failed state"
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
    Task ch-29f1e (Test AgentWorkflow PR creation failure path) has been dod_failed for multiple cycles. The error_log only shows dod_failed/dod_failed with no PR or review URL — Cortex is not getting useful feedback. Steps: 1) Read the existing test file in the CRAB engine package to understand what tests exist around AgentWorkflow. 2) Write TestAgentWorkflow_PRCreationFailure — mock CreatePRInfoActivity to return an error, assert the workflow closes the task with a failure detail, assert cleanup runs. 3) DoD: the test compiles and passes with go test. If the test was already written but failing, read it and fix the assertion. Acceptance: go test ./... passes, ch-29f1e moves to needs_review.
depends_on: []
---

Task ch-29f1e (Test AgentWorkflow PR creation failure path) has been dod_failed for multiple cycles. The error_log only shows dod_failed/dod_failed with no PR or review URL — Cortex is not getting useful feedback. Steps: 1) Read the existing test file in the CRAB engine package to understand what tests exist around AgentWorkflow. 2) Write TestAgentWorkflow_PRCreationFailure — mock CreatePRInfoActivity to return an error, assert the workflow closes the task with a failure detail, assert cleanup runs. 3) DoD: the test compiles and passes with go test. If the test was already written but failing, read it and fix the assertion. Acceptance: go test ./... passes, ch-29f1e moves to needs_review.

_Created by Jarvis at 2026-03-03T10:50:51Z_
