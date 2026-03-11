---
title: "Export contextKey in auth middleware"
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
    On branch chum/ch-55879, in the auth package, change contextKey from an unexported type to exported (e.g. ContextKey) and update userIDKey accordingly. This ensures UserFromContext works across package boundaries via context value type matching. Acceptance criteria: contextKey type is exported, UserFromContext works when called from outside the auth package, existing tests pass, PR #44 review concern is addressed.
depends_on: []
---

On branch chum/ch-55879, in the auth package, change contextKey from an unexported type to exported (e.g. ContextKey) and update userIDKey accordingly. This ensures UserFromContext works across package boundaries via context value type matching. Acceptance criteria: contextKey type is exported, UserFromContext works when called from outside the auth package, existing tests pass, PR #44 review concern is addressed.

_Created by Jarvis at 2026-03-03T21:05:58Z_
