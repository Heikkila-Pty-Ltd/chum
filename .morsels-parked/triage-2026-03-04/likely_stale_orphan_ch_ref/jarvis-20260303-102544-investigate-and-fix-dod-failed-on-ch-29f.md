---
title: "Investigate and fix dod_failed on ch-29f1e: Test AgentWorkflow PR creation failure path"
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
    Task ch-29f1e has status dod_failed. Pull the task detail from chum.db (sqlite3 ~/projects/chum-factory/chum.db), find what DoD check failed, fix the test so it passes the acceptance criteria, then re-run. Acceptance: task moves to needs_review or completed with passing tests.
depends_on: []
---

Task ch-29f1e has status dod_failed. Pull the task detail from chum.db (sqlite3 ~/projects/chum-factory/chum.db), find what DoD check failed, fix the test so it passes the acceptance criteria, then re-run. Acceptance: task moves to needs_review or completed with passing tests.

_Created by Jarvis at 2026-03-03T10:25:44Z_
