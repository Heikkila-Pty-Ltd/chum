---
title: "Reset stale needs_review tasks with no commits to ready"
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
    Tasks ch-32633, ch-03718, ch-62703 are in needs_review but have no task-specific commits. They need to be reset to ready so the agent picks them up again. Acceptance: all three tasks back in ready state and visible to the dispatcher.
depends_on: []
---

Tasks ch-32633, ch-03718, ch-62703 are in needs_review but have no task-specific commits. They need to be reset to ready so the agent picks them up again. Acceptance: all three tasks back in ready state and visible to the dispatcher.

_Created by Jarvis at 2026-03-04T00:47:07Z_
