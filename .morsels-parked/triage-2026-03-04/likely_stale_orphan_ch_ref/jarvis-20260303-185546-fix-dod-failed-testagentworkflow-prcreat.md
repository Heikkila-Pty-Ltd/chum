---
title: "Fix dod_failed: TestAgentWorkflow_PRCreationFailure"
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
    Task ch-29f1e has status dod_failed. Investigate why it failed DoD. Check the PR that was opened for this task — look at the test output, any CI failures, and the actual PR diff. The test should: (1) succeed through the happy path up to CreatePRInfoActivity, (2) have CreatePRInfoActivity return an error, (3) assert the task is closed with a failure detail, (4) assert cleanup runs. Fix whatever caused the DoD failure. Acceptance criteria: task status becomes done, all tests pass, no regressions in agent workflow tests.
depends_on: []
---

Task ch-29f1e has status dod_failed. Investigate why it failed DoD. Check the PR that was opened for this task — look at the test output, any CI failures, and the actual PR diff. The test should: (1) succeed through the happy path up to CreatePRInfoActivity, (2) have CreatePRInfoActivity return an error, (3) assert the task is closed with a failure detail, (4) assert cleanup runs. Fix whatever caused the DoD failure. Acceptance criteria: task status becomes done, all tests pass, no regressions in agent workflow tests.

_Created by Jarvis at 2026-03-03T18:55:46Z_
