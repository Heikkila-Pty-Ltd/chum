---
title: "Fix exported UserFromContext context key in auth middleware"
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
    PR #44 (branch chum/ch-55879) has a bug: userIDKey is an unexported contextKey type, which means UserFromContext only works inside the auth package. Any HTTP handler in another package calling UserFromContext will always get nil/zero. Fix: export the context key type or use a package-level var with an exported name so handlers across packages can retrieve the user ID. Acceptance criteria: UserFromContext can be called from any package and correctly retrieves the user ID set by the middleware. Existing tests still pass. PR #44 is then ready to merge.
depends_on: []
---

PR #44 (branch chum/ch-55879) has a bug: userIDKey is an unexported contextKey type, which means UserFromContext only works inside the auth package. Any HTTP handler in another package calling UserFromContext will always get nil/zero. Fix: export the context key type or use a package-level var with an exported name so handlers across packages can retrieve the user ID. Acceptance criteria: UserFromContext can be called from any package and correctly retrieves the user ID set by the middleware. Existing tests still pass. PR #44 is then ready to merge.

_Created by Jarvis at 2026-03-03T18:36:00Z_
