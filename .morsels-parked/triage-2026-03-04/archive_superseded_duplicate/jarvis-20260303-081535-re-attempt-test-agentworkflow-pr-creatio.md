---
title: "Re-attempt: Test AgentWorkflow PR creation failure path"
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
    Task ch-29f1e had dod_failed status — previous attempt didn't pass acceptance criteria. Re-implement the test for the AgentWorkflow PR creation failure path. Test should mock the PR creation step to return an error and assert the workflow returns that error correctly. Acceptance: go test passes for that specific test, no skips.
depends_on: []
---

Task ch-29f1e had dod_failed status — previous attempt didn't pass acceptance criteria. Re-implement the test for the AgentWorkflow PR creation failure path. Test should mock the PR creation step to return an error and assert the workflow returns that error correctly. Acceptance: go test passes for that specific test, no skips.

_Created by Jarvis at 2026-03-03T08:15:35Z_
