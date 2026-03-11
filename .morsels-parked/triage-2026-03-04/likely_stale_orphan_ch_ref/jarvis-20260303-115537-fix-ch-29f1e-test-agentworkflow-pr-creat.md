---
title: "Fix ch-29f1e: Test AgentWorkflow PR creation failure path (dod_failed)"
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
    Task ch-29f1e is stuck in dod_failed — the test for AgentWorkflow PR creation failure path was written but didn't satisfy the DoD check. Investigate what the DoD check expects vs what the test actually does. Fix the test so it properly exercises the PR creation failure path and the DoD check passes. Acceptance criteria: ch-29f1e moves to done, test runs green in CI.
depends_on: []
---

Task ch-29f1e is stuck in dod_failed — the test for AgentWorkflow PR creation failure path was written but didn't satisfy the DoD check. Investigate what the DoD check expects vs what the test actually does. Fix the test so it properly exercises the PR creation failure path and the DoD check passes. Acceptance criteria: ch-29f1e moves to done, test runs green in CI.

_Created by Jarvis at 2026-03-03T11:55:37Z_
