---
title: "Mark tasks done when their branch has no diff vs master"
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
    Tasks ch-32633, ch-03718, ch-62703 are stuck in needs_review but their branches have zero commits vs master — the work was already merged. The system should detect this case (branch exists but no diff vs base) and automatically transition the task to done. Acceptance criteria: after merge, running chum tasks shows those tasks as completed/done, not needs_review.
depends_on: []
---

Tasks ch-32633, ch-03718, ch-62703 are stuck in needs_review but their branches have zero commits vs master — the work was already merged. The system should detect this case (branch exists but no diff vs base) and automatically transition the task to done. Acceptance criteria: after merge, running chum tasks shows those tasks as completed/done, not needs_review.

_Created by Jarvis at 2026-03-04T00:56:41Z_
