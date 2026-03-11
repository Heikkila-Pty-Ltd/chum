---
title: "Fix unexported contextKey type in auth middleware (PR #44)"
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
    PR #44 (branch chum/ch-55879) has been blocked by a review comment for multiple cycles. The issue: userIDKey uses an unexported type for context keys, which risks collisions with other packages using the same context. Fix: define a named unexported type (e.g. `type contextKey string`) and use a const of that type as the key — standard Go pattern. Acceptance criteria: (1) contextKey uses a named type not a raw string/int, (2) existing tests still pass, (3) no new exported symbols required, (4) push to branch chum/ch-55879 so PR #44 updates automatically.
depends_on: []
---

PR #44 (branch chum/ch-55879) has been blocked by a review comment for multiple cycles. The issue: userIDKey uses an unexported type for context keys, which risks collisions with other packages using the same context. Fix: define a named unexported type (e.g. `type contextKey string`) and use a const of that type as the key — standard Go pattern. Acceptance criteria: (1) contextKey uses a named type not a raw string/int, (2) existing tests still pass, (3) no new exported symbols required, (4) push to branch chum/ch-55879 so PR #44 updates automatically.

_Created by Jarvis at 2026-03-03T19:00:51Z_
