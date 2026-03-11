---
title: "Kill duplicate chum serve and prevent double-start"
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
    Two chum serve processes are always running simultaneously (currently PIDs 2300365 and 2332593). This causes race conditions on task pickup. Investigate how chum serve is being launched (systemd? cron? Temporal schedule?) and ensure only one instance runs at a time. Add a pidfile lock or use systemd's single-instance guarantee. Acceptance criteria: only one chum serve process running at any time, confirmed over two restart cycles.
depends_on: []
---

Two chum serve processes are always running simultaneously (currently PIDs 2300365 and 2332593). This causes race conditions on task pickup. Investigate how chum serve is being launched (systemd? cron? Temporal schedule?) and ensure only one instance runs at a time. Add a pidfile lock or use systemd's single-instance guarantee. Acceptance criteria: only one chum serve process running at any time, confirmed over two restart cycles.

_Created by Jarvis at 2026-03-04T00:21:01Z_
