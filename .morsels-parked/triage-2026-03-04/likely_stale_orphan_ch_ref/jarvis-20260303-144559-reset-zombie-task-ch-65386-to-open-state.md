---
title: "Reset zombie task ch-65386 to open state"
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
    Task ch-65386 (Fix unexported contextKey in auth middleware) is stuck in 'running' status with no active Temporal workflow backing it. Find the task entry in the chum DAG/store (likely tasks.toml or similar in chum-factory project), set its status back to 'open' so the dispatcher will pick it up again. Acceptance criteria: chum tasks shows ch-65386 as [open] not [running].
depends_on: []
---

Task ch-65386 (Fix unexported contextKey in auth middleware) is stuck in 'running' status with no active Temporal workflow backing it. Find the task entry in the chum DAG/store (likely tasks.toml or similar in chum-factory project), set its status back to 'open' so the dispatcher will pick it up again. Acceptance criteria: chum tasks shows ch-65386 as [open] not [running].

_Created by Jarvis at 2026-03-03T14:45:59Z_
