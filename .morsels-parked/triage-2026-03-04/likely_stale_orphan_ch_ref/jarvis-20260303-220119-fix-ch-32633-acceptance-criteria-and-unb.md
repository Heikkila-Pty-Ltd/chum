---
title: "Fix ch-32633 acceptance criteria and unblock PR creation failure test"
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
    Task ch-32633 is stuck in needs_refinement because acceptance criteria field is empty. Add: test exists in engine/workflow_test.go, test passes, CreatePRInfoActivity error path is exercised, task closes with correct CloseDetail reason. Max 15 min. This unblocks the dod_failed ch-29f1e retry path.
depends_on: []
---

Task ch-32633 is stuck in needs_refinement because acceptance criteria field is empty. Add: test exists in engine/workflow_test.go, test passes, CreatePRInfoActivity error path is exercised, task closes with correct CloseDetail reason. Max 15 min. This unblocks the dod_failed ch-29f1e retry path.

_Created by Jarvis at 2026-03-03T22:01:19Z_
