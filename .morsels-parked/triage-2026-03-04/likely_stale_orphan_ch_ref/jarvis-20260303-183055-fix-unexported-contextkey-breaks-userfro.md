---
title: "Fix unexported contextKey breaks UserFromContext across packages"
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
    In internal/auth/middleware.go (or wherever contextKey is defined on branch ch-55879), the contextKey type is unexported. This means any package outside auth that calls UserFromContext will always get an empty string because Go context lookup is type-keyed — the type itself is part of the key. Fix: export the contextKey type (rename to ContextKey) OR replace the type-based key with a package-level exported sentinel variable (e.g. var contextKeyUserID = contextKey('userID')). The second approach is idiomatic Go and avoids leaking internal types. Acceptance criteria: (1) UserFromContext works correctly when called from a handler in a different package (e.g. a test in a separate package that imports auth), (2) existing tests still pass, (3) PR #44 gets updated with the fix and the doctorspritz review comment is addressed.
depends_on: []
---

In internal/auth/middleware.go (or wherever contextKey is defined on branch ch-55879), the contextKey type is unexported. This means any package outside auth that calls UserFromContext will always get an empty string because Go context lookup is type-keyed — the type itself is part of the key. Fix: export the contextKey type (rename to ContextKey) OR replace the type-based key with a package-level exported sentinel variable (e.g. var contextKeyUserID = contextKey('userID')). The second approach is idiomatic Go and avoids leaking internal types. Acceptance criteria: (1) UserFromContext works correctly when called from a handler in a different package (e.g. a test in a separate package that imports auth), (2) existing tests still pass, (3) PR #44 gets updated with the fix and the doctorspritz review comment is addressed.

_Created by Jarvis at 2026-03-03T18:30:55Z_
