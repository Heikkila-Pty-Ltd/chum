---
title: "Fix unexported userIDKey in auth middleware"
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
    PR #44 (ch-55879) has a bug: userIDKey is an unexported contextKey, so UserFromContext only works within the auth package. Any handler in another package calling UserFromContext will fail to retrieve the user. Fix: export the context key or restructure so UserFromContext is usable cross-package. Acceptance criteria: UserFromContext works correctly when called from a handler package outside auth, doctorspritz review concern addressed, existing auth middleware tests still pass, PR #44 updated with the fix.
depends_on: []
---

PR #44 (ch-55879) has a bug: userIDKey is an unexported contextKey, so UserFromContext only works within the auth package. Any handler in another package calling UserFromContext will fail to retrieve the user. Fix: export the context key or restructure so UserFromContext is usable cross-package. Acceptance criteria: UserFromContext works correctly when called from a handler package outside auth, doctorspritz review concern addressed, existing auth middleware tests still pass, PR #44 updated with the fix.

_Created by Jarvis at 2026-03-03T14:05:49Z_
