---
title: "Retry dod_failed: Test AgentWorkflow PR creation failure path"
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
    Task ch-29f1e failed DoD checks. Re-examine the test implementation for AgentWorkflow PR creation failure path. Check what the DoD criteria are, what the actual test does, and what specifically failed. Fix the test so it correctly covers the failure path where PR creation returns an error — the workflow should propagate the error cleanly. Acceptance criteria: task moves to done, test compiles and passes, PR creation error is correctly surfaced in the workflow output.
depends_on: []
---

Task ch-29f1e failed DoD checks. Re-examine the test implementation for AgentWorkflow PR creation failure path. Check what the DoD criteria are, what the actual test does, and what specifically failed. Fix the test so it correctly covers the failure path where PR creation returns an error — the workflow should propagate the error cleanly. Acceptance criteria: task moves to done, test compiles and passes, PR creation error is correctly surfaced in the workflow output.

_Created by Jarvis at 2026-03-03T16:46:12Z_
