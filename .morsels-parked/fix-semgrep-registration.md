---
title: "Register RunSemgrepScanActivity in Temporal worker"
status: ready
priority: 0
type: task
labels:
  - whale:infrastructure
  - bug
  - temporal
estimate_minutes: 5
acceptance_criteria: |
  - RunSemgrepScanActivity is registered in worker.go alongside other activities
  - go build ./cmd/chum/ compiles cleanly
  - CHUM log no longer shows "unable to find activityType=RunSemgrepScanActivity"
design: |
  1. Open internal/temporal/worker.go
  2. Add w.RegisterActivity(acts.RunSemgrepScanActivity) in the activities section
  3. Verify the function signature exists in the codebase
depends_on: []
---

RunSemgrepScanActivity is called from ChumAgentWorkflow but never registered in the Temporal worker.
Causes non-fatal skip of Semgrep scans on every dispatch.
