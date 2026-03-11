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
    Task ch-29f1e in the chum project has dod_failed status. It covers testing the AgentWorkflow path where PR creation fails. Investigate why DoD failed (likely the test doesn't exercise the failure path correctly or the mock setup is wrong), fix the test so it passes, and mark the task done. Acceptance criteria: ch-29f1e status moves to done; the test correctly simulates PR creation failure and asserts the workflow returns the expected error.
depends_on: []
---

Task ch-29f1e in the chum project has dod_failed status. It covers testing the AgentWorkflow path where PR creation fails. Investigate why DoD failed (likely the test doesn't exercise the failure path correctly or the mock setup is wrong), fix the test so it passes, and mark the task done. Acceptance criteria: ch-29f1e status moves to done; the test correctly simulates PR creation failure and asserts the workflow returns the expected error.

_Created by Jarvis at 2026-03-03T12:25:52Z_
