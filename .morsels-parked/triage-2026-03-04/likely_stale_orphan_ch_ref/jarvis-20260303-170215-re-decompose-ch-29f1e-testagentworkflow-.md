---
title: "Re-decompose ch-29f1e: TestAgentWorkflow PR creation failure path"
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
    ch-29f1e has been dod_failed for multiple cycles. The existing task asks for TestAgentWorkflow_PRCreationFailure asserting full success path followed by CreatePRInfoActivity error, task closure with failure detail, AND cleanup. This is too broad for one morsel. Break it into: (1) a unit test that mocks CreatePRInfoActivity returning an error and asserts workflow returns an error, (2) a separate test that asserts the task is marked closed with failure detail. Remove the cleanup assertion if it requires internal workflow state not exposed by the public API. DoD: both subtask tests compile and pass with go test. The dod_failed status on ch-29f1e should be noted as the reason for re-decomposition.
depends_on: []
---

ch-29f1e has been dod_failed for multiple cycles. The existing task asks for TestAgentWorkflow_PRCreationFailure asserting full success path followed by CreatePRInfoActivity error, task closure with failure detail, AND cleanup. This is too broad for one morsel. Break it into: (1) a unit test that mocks CreatePRInfoActivity returning an error and asserts workflow returns an error, (2) a separate test that asserts the task is marked closed with failure detail. Remove the cleanup assertion if it requires internal workflow state not exposed by the public API. DoD: both subtask tests compile and pass with go test. The dod_failed status on ch-29f1e should be noted as the reason for re-decomposition.

_Created by Jarvis at 2026-03-03T17:02:15Z_
