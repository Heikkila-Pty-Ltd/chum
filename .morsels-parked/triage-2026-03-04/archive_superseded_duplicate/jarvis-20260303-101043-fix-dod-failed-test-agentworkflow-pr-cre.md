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
    Task ch-29f1e is in dod_failed state. The test for AgentWorkflow PR creation failure path was written but didn't pass the DoD checker. Inspect the current test file (look for TestAgentWorkflow_PRCreationFailure or similar in the workflow test files), identify what the DoD check flagged — likely missing assertions, incorrect mock setup, or the test not actually running/compiling. Fix the test so it compiles, runs, and correctly asserts that when PR creation fails the workflow returns an appropriate error and the failure is reflected in task state. Acceptance criteria: go test ./... passes in the chum package, the specific test function exists and passes, DoD checker accepts the output (test file present, test function named correctly, assertions on error return).
depends_on: []
---

Task ch-29f1e is in dod_failed state. The test for AgentWorkflow PR creation failure path was written but didn't pass the DoD checker. Inspect the current test file (look for TestAgentWorkflow_PRCreationFailure or similar in the workflow test files), identify what the DoD check flagged — likely missing assertions, incorrect mock setup, or the test not actually running/compiling. Fix the test so it compiles, runs, and correctly asserts that when PR creation fails the workflow returns an appropriate error and the failure is reflected in task state. Acceptance criteria: go test ./... passes in the chum package, the specific test function exists and passes, DoD checker accepts the output (test file present, test function named correctly, assertions on error return).

_Created by Jarvis at 2026-03-03T10:10:43Z_
