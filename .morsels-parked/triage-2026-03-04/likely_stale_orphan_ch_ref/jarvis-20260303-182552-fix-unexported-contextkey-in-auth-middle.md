---
title: "Fix unexported contextKey in auth middleware"
status: ready
priority: 1
type: feature
labels:
  - jarvis-initiative
  - chum-factory
estimate_minutes: 30
acceptance_criteria: |
  - Task completed as described
design: |
    PR #44 (branch chum/ch-55879) has a blocking review: userIDKey is an unexported contextKey type in internal/auth/, which means UserFromContext only works within the auth package — any handler in another package calling UserFromContext will get nil silently. Fix: export the key type or use a package-level exported sentinel value so UserFromContext works from any package. Also add a test that calls UserFromContext from a context set by middleware to confirm it returns the expected user ID. Acceptance criteria: (1) UserFromContext works correctly when called from a package other than auth, (2) existing auth tests still pass, (3) doctorspritz review concern is addressed so PR can be merged.
depends_on: []
---

PR #44 (branch chum/ch-55879) has a blocking review: userIDKey is an unexported contextKey type in internal/auth/, which means UserFromContext only works within the auth package — any handler in another package calling UserFromContext will get nil silently. Fix: export the key type or use a package-level exported sentinel value so UserFromContext works from any package. Also add a test that calls UserFromContext from a context set by middleware to confirm it returns the expected user ID. Acceptance criteria: (1) UserFromContext works correctly when called from a package other than auth, (2) existing auth tests still pass, (3) doctorspritz review concern is addressed so PR can be merged.

_Created by Jarvis at 2026-03-03T18:25:52Z_
