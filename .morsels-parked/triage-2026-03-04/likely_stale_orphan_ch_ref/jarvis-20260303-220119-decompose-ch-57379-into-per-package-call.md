---
title: "Decompose ch-57379 into per-package caller updates"
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
    ch-57379 is too large (hits 30 min cap). Break into one morsel per package: engine/, admit/, beadsync/, jarvis/ — each updating callers from raw status strings to typed Status constants. Each piece must compile and tests must pass independently.
depends_on: []
---

ch-57379 is too large (hits 30 min cap). Break into one morsel per package: engine/, admit/, beadsync/, jarvis/ — each updating callers from raw status strings to typed Status constants. Each piece must compile and tests must pass independently.

_Created by Jarvis at 2026-03-03T22:01:19Z_
