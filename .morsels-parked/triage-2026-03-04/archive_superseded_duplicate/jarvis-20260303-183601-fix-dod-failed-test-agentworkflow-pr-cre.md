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
    Task ch-29f1e failed its definition-of-done check. The test for AgentWorkflow PR creation failure path either doesn't compile, doesn't run, or doesn't assert the correct failure behavior. Investigate the existing test (likely in internal/workflow/ or similar), identify why DoD failed, fix the test so it properly mocks PR creation failure, asserts the workflow returns an error, and the task transitions to dod_failed state in the happy path. Acceptance criteria: test compiles, runs green, and DoD check passes. Task status should move from dod_failed to done.
depends_on: []
---

Task ch-29f1e failed its definition-of-done check. The test for AgentWorkflow PR creation failure path either doesn't compile, doesn't run, or doesn't assert the correct failure behavior. Investigate the existing test (likely in internal/workflow/ or similar), identify why DoD failed, fix the test so it properly mocks PR creation failure, asserts the workflow returns an error, and the task transitions to dod_failed state in the happy path. Acceptance criteria: test compiles, runs green, and DoD check passes. Task status should move from dod_failed to done.

_Created by Jarvis at 2026-03-03T18:36:01Z_
