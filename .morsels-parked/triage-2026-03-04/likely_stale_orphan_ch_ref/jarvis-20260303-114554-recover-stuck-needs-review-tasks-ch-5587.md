---
title: "Recover stuck needs_review tasks ch-55879 and ch-79602"
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
    Both tasks show needs_review status but gh pr list shows zero open PRs — the PRs were either never created or were closed. Reset ch-55879 (auth middleware) and ch-79602 (RateLimit config) to open status so the agent re-attempts them. Check if there are any closed/rejected PRs for these branches first. DoD: both tasks back in open state with a comment noting the recovery, agent picks them up next cycle.
depends_on: []
---

Both tasks show needs_review status but gh pr list shows zero open PRs — the PRs were either never created or were closed. Reset ch-55879 (auth middleware) and ch-79602 (RateLimit config) to open status so the agent re-attempts them. Check if there are any closed/rejected PRs for these branches first. DoD: both tasks back in open state with a comment noting the recovery, agent picks them up next cycle.

_Created by Jarvis at 2026-03-03T11:45:54Z_
