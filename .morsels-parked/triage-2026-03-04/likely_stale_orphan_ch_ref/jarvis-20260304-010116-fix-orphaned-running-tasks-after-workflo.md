---
title: "Fix orphaned 'running' tasks after workflow completion"
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
    Tasks ch-32633, ch-08021, ch-41062, ch-52426, ch-a3af0 are stuck in 'running' status but have no corresponding active Temporal workflow. The DAG store is not being updated when AgentWorkflow terminates (success, failure, or crash). Acceptance criteria: when AgentWorkflow completes for any reason, the associated task status is updated to reflect the outcome. Add a deferred status-update at the top of AgentWorkflow that runs on any exit path. Bonus: add a reconciliation pass in DispatcherWorkflow that detects running tasks with no active workflow and resets them to ready.
depends_on: []
---

Tasks ch-32633, ch-08021, ch-41062, ch-52426, ch-a3af0 are stuck in 'running' status but have no corresponding active Temporal workflow. The DAG store is not being updated when AgentWorkflow terminates (success, failure, or crash). Acceptance criteria: when AgentWorkflow completes for any reason, the associated task status is updated to reflect the outcome. Add a deferred status-update at the top of AgentWorkflow that runs on any exit path. Bonus: add a reconciliation pass in DispatcherWorkflow that detects running tasks with no active workflow and resets them to ready.

_Created by Jarvis at 2026-03-04T01:01:16Z_
