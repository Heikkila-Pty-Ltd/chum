---
title: "Fix unexported contextKey in auth middleware (ch-65386 unblock)"
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
    File: internal/auth/middleware.go. The contextKey type is unexported (lowercase). Review comment on PR #44 requests this be fixed before merge. Change: replace 'type contextKey string' with an exported type or use a struct-based unexported key (idiomatic Go: 'type contextKeyType struct{}' then 'var userIDKey = contextKeyType{}') to avoid cross-package collisions. Also update UserIDFromContext if it exists. Acceptance criteria: (1) contextKey type uses struct-based unexported key pattern (zero-value struct), (2) all usages of userIDKey in middleware.go and middleware_test.go compile and pass, (3) existing tests still pass. This unblocks PR #44 merge.
depends_on: []
---

File: internal/auth/middleware.go. The contextKey type is unexported (lowercase). Review comment on PR #44 requests this be fixed before merge. Change: replace 'type contextKey string' with an exported type or use a struct-based unexported key (idiomatic Go: 'type contextKeyType struct{}' then 'var userIDKey = contextKeyType{}') to avoid cross-package collisions. Also update UserIDFromContext if it exists. Acceptance criteria: (1) contextKey type uses struct-based unexported key pattern (zero-value struct), (2) all usages of userIDKey in middleware.go and middleware_test.go compile and pass, (3) existing tests still pass. This unblocks PR #44 merge.

_Created by Jarvis at 2026-03-03T14:36:03Z_
