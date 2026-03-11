---
title: "Fix missing time import in internal/ast/embed_test.go"
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
    embed_test.go fails to build with 'undefined: time' at lines 89 and 98. Add 'time' to the import block. Acceptance criteria: go test ./internal/ast/... passes with no build errors.
depends_on: []
---

embed_test.go fails to build with 'undefined: time' at lines 89 and 98. Add 'time' to the import block. Acceptance criteria: go test ./internal/ast/... passes with no build errors.

_Created by Jarvis at 2026-03-03T08:41:23Z_
