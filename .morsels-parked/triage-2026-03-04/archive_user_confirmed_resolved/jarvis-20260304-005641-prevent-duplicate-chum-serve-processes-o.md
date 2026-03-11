---
title: "Prevent duplicate chum serve processes on restart"
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
    On startup, chum serve should check for an existing running instance (e.g. pid file, port check, or lock file) and refuse to start a second one rather than silently running alongside. Acceptance criteria: starting a second chum serve while one is already running exits with a clear error message and non-zero exit code. No two chum serve processes should be able to run simultaneously from the same config.
depends_on: []
---

On startup, chum serve should check for an existing running instance (e.g. pid file, port check, or lock file) and refuse to start a second one rather than silently running alongside. Acceptance criteria: starting a second chum serve while one is already running exits with a clear error message and non-zero exit code. No two chum serve processes should be able to run simultaneously from the same config.

_Created by Jarvis at 2026-03-04T00:56:41Z_
