---
title: "Fix unexported contextKey in auth middleware (PR #44)"
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
    PR #44 (ch-55879) has a review from doctorspritz: the contextKey type is unexported, so context.WithValue and Value lookups are type-keyed — UserFromContext will always return empty string when called from outside the auth package. Fix: export the key constant (e.g. UserIDKey) or use a package-level string key instead. Update UserFromContext to use the exported key. Acceptance criteria: UserFromContext works correctly when called from a handler in a different package (e.g. a test in package main or health). PR #44 can then be approved and merged.
depends_on: []
---

PR #44 (ch-55879) has a review from doctorspritz: the contextKey type is unexported, so context.WithValue and Value lookups are type-keyed — UserFromContext will always return empty string when called from outside the auth package. Fix: export the key constant (e.g. UserIDKey) or use a package-level string key instead. Update UserFromContext to use the exported key. Acceptance criteria: UserFromContext works correctly when called from a handler in a different package (e.g. a test in package main or health). PR #44 can then be approved and merged.

_Created by Jarvis at 2026-03-03T12:15:45Z_
