---
title: "Auto-spawn bug morsels on calcified script quarantine"
status: ready
priority: 2
type: task
labels:
  - calcifier
  - self-healing
  - bug
estimate_minutes: 45
acceptance_criteria: |
  - QuarantineAndRewireActivity creates a new morsel in the DAG when quarantining a script
  - Bug morsel has title "Rewrite calcified script: {type}", priority 1 (highest)
  - Bug morsel description includes stack trace, failed input, expected output format
  - Bug morsel labels include ["calcifier", "bug", "risk:high"]
  - Bug morsel is automatically picked up by the dispatcher on next tick
  - go build ./... clean, existing tests pass
design: |
  Modify QuarantineAndRewireActivity in calcifier_activities.go:
  1. After quarantining the script in the store, create a new morsel via graph.DAG.CreateTask
  2. Activities struct needs a DAG field (already has it)
  3. Morsel fields:
     - Title: "Rewrite calcified script: {morselType}"
     - Description: Include the quarantine reason, script path, and the last successful prompt/output
     - Status: "ready" (goes straight to dispatch)
     - Priority: 1 (highest)
     - Labels: ["calcifier", "bug", "risk:high"]
     - Acceptance: "Script must pass shadow validation (3 consecutive matches)"
  4. Log the created morsel ID for traceability
depends_on:
  - vivification-execute-interceptor
---

When a calcified script breaks (non-zero exit or output mismatch), the engine quarantines it and spawns a high-priority bug morsel. This morsel feeds back through the entire pipeline — the LLM rewrites the script, which then goes through shadow validation again. Fully autonomous self-healing loop.
