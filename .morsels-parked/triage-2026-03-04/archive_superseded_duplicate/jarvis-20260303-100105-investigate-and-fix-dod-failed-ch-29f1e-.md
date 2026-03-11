---
title: "Investigate and fix dod_failed ch-29f1e: Test AgentWorkflow PR creation failure path"
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
    Task ch-29f1e has been in dod_failed state across multiple cycles. Read the task detail from chum.db (tasks table), find the DoD failure reason, fix the underlying test or test setup so it passes DoD, then move status back to open so Cortex can re-attempt. Acceptance criteria: task DoD check passes on next Cortex run.
depends_on: []
---

Task ch-29f1e has been in dod_failed state across multiple cycles. Read the task detail from chum.db (tasks table), find the DoD failure reason, fix the underlying test or test setup so it passes DoD, then move status back to open so Cortex can re-attempt. Acceptance criteria: task DoD check passes on next Cortex run.

_Created by Jarvis at 2026-03-03T10:01:05Z_
