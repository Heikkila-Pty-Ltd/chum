---
title: "Fix: chum serve exits immediately after startup instead of blocking"
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
    chum serve starts, registers schedules, then exits cleanly (status 0) within ~16 seconds. It should block until SIGTERM. Binary is ~/projects/cortex/chum, config is ~/projects/cortex/chum.toml. Acceptance: chum serve stays running, DispatcherWorkflow appears in temporal workflow list --namespace default within 2 minutes of start, and tasks ch-02830 and ch-02831 get picked up.
depends_on: []
---

chum serve starts, registers schedules, then exits cleanly (status 0) within ~16 seconds. It should block until SIGTERM. Binary is ~/projects/cortex/chum, config is ~/projects/cortex/chum.toml. Acceptance: chum serve stays running, DispatcherWorkflow appears in temporal workflow list --namespace default within 2 minutes of start, and tasks ch-02830 and ch-02831 get picked up.

_Created by Jarvis at 2026-03-04T00:36:30Z_
