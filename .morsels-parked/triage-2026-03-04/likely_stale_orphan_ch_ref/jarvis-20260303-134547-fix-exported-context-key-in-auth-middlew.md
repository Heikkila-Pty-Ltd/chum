---
title: "Fix exported context key in auth middleware (PR #44)"
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
    PR #44 has a blocking review comment: userIDKey is an unexported contextKey type, meaning UserFromContext only works within the auth package. Any handler in another package calling UserFromContext will always get empty string because Go context value lookup is type-keyed. Fix: either export the context key constant (e.g. rename to UserIDKey or use a string key) or export it as a documented API. Then address the PR review comment with a reply, push the fix to branch chum/ch-55879, and ensure go build and go test pass. Acceptance criteria: go build passes, go test ./... passes, UserFromContext is usable from outside the auth package, PR review comment is addressed.
depends_on: []
---

PR #44 has a blocking review comment: userIDKey is an unexported contextKey type, meaning UserFromContext only works within the auth package. Any handler in another package calling UserFromContext will always get empty string because Go context value lookup is type-keyed. Fix: either export the context key constant (e.g. rename to UserIDKey or use a string key) or export it as a documented API. Then address the PR review comment with a reply, push the fix to branch chum/ch-55879, and ensure go build and go test pass. Acceptance criteria: go build passes, go test ./... passes, UserFromContext is usable from outside the auth package, PR review comment is addressed.

_Created by Jarvis at 2026-03-03T13:45:47Z_
