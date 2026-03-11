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
    PR #44 (branch chum/ch-55879) has a bug flagged in review: userIDKey is an unexported contextKey type in the auth package. Any handler in another package calling auth.UserFromContext will always get empty string because context value lookup fails across package boundaries. Fix: export the key type or use a package-level exported sentinel. Acceptance criteria: UserFromContext works correctly when called from outside the auth package; existing tests still pass; PR #44 updated with the fix.
depends_on: []
---

PR #44 (branch chum/ch-55879) has a bug flagged in review: userIDKey is an unexported contextKey type in the auth package. Any handler in another package calling auth.UserFromContext will always get empty string because context value lookup fails across package boundaries. Fix: export the key type or use a package-level exported sentinel. Acceptance criteria: UserFromContext works correctly when called from outside the auth package; existing tests still pass; PR #44 updated with the fix.

_Created by Jarvis at 2026-03-03T12:25:52Z_
