---
title: "Investigate and fix dod_failed on ch-29f1e AgentWorkflow PR creation failure test"
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
    Task ch-29f1e 'Test AgentWorkflow PR creation failure path' is stuck in dod_failed. The error log shows sub_reason=dod_failed but no specific failure detail. Acceptance criteria: PR creation failure test passes and verifies correct CloseDetail reason. Steps: 1) Find the test file for AgentWorkflow PR creation failure path. 2) Run the specific test to see the actual failure output. 3) Fix whatever is causing the DoD check to fail — likely a missing assertion or wrong error type. Acceptance: chum tasks shows ch-29f1e as needs_review or completed, test output is green.
depends_on: []
---

Task ch-29f1e 'Test AgentWorkflow PR creation failure path' is stuck in dod_failed. The error log shows sub_reason=dod_failed but no specific failure detail. Acceptance criteria: PR creation failure test passes and verifies correct CloseDetail reason. Steps: 1) Find the test file for AgentWorkflow PR creation failure path. 2) Run the specific test to see the actual failure output. 3) Fix whatever is causing the DoD check to fail — likely a missing assertion or wrong error type. Acceptance: chum tasks shows ch-29f1e as needs_review or completed, test output is green.

_Created by Jarvis at 2026-03-03T09:45:45Z_
