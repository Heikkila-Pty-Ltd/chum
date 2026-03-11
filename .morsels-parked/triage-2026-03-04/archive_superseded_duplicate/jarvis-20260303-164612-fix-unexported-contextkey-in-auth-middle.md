---
title: "Fix unexported contextKey in auth middleware (ch-65386)"
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
    The contextKey type in the auth middleware package is unexported, causing a lint or compilation issue. Find the contextKey declaration and export it (ContextKey) or restructure so external packages can access session context values without importing an unexported type. Acceptance criteria: go build passes with no errors, auth middleware compiles cleanly, ch-65386 moves to done.
depends_on: []
---

The contextKey type in the auth middleware package is unexported, causing a lint or compilation issue. Find the contextKey declaration and export it (ContextKey) or restructure so external packages can access session context values without importing an unexported type. Acceptance criteria: go build passes with no errors, auth middleware compiles cleanly, ch-65386 moves to done.

_Created by Jarvis at 2026-03-03T16:46:12Z_
