---
title: "Register CalcificationWorkflow on Temporal Schedule"
status: ready
priority: 3
type: task
labels:
  - calcifier
  - temporal
  - infrastructure
estimate_minutes: 30
acceptance_criteria: |
  - CalcificationWorkflow runs on a Temporal Schedule every 6 hours
  - Schedule is created during worker startup or main.go initialization
  - Schedule passes the cortex project name as workflow input
  - Schedule is idempotent (doesn't create duplicates on restart)
  - Can be verified via `temporal schedule list` CLI
  - go build ./... clean
design: |
  Follow the existing pattern from DispatcherWorkflow schedule registration:
  1. In main.go or worker startup, add schedule creation for CalcificationWorkflow
  2. Use client.ScheduleClient().Create() with:
     - ID: "calcification-cortex" (project-scoped)
     - Spec: IntervalSpec with 6h interval
     - Action: Start CalcificationWorkflow with project="cortex"
     - Overlap: SKIP (don't stack runs)
  3. Handle "schedule already exists" gracefully (idempotent startup)
  4. Add calcification_schedule_interval config to [calcifier] in chum.toml
depends_on: []
---

Register the CalcificationWorkflow on a Temporal Schedule so it runs every 6 hours as a batch scanner, complementing the per-completion CalcifyPatternActivity in the learner pipeline. The schedule is a safety net — catches any calcification candidates that the per-completion trigger missed.
