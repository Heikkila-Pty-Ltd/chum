---
title: "Diagnose and fix DispatcherWorkflow not starting from chum serve"
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
    When `chum serve` runs, DispatcherWorkflow should be started via Temporal. Multiple observation cycles confirm it never starts (temporal workflow list returns empty). Task ch-16434 was decomposed to investigate this but the root cause has not been fixed. Investigate: (1) does chum serve actually call temporal.ExecuteWorkflow for DispatcherWorkflow on startup? (2) is there a namespace/task-queue mismatch? (3) are there startup errors being swallowed? Acceptance criteria: `chum serve` starts, and within 30 seconds `temporal workflow list --query WorkflowType=DispatcherWorkflow` returns a running workflow. Test by running chum serve and polling for 60 seconds.
depends_on: []
---

When `chum serve` runs, DispatcherWorkflow should be started via Temporal. Multiple observation cycles confirm it never starts (temporal workflow list returns empty). Task ch-16434 was decomposed to investigate this but the root cause has not been fixed. Investigate: (1) does chum serve actually call temporal.ExecuteWorkflow for DispatcherWorkflow on startup? (2) is there a namespace/task-queue mismatch? (3) are there startup errors being swallowed? Acceptance criteria: `chum serve` starts, and within 30 seconds `temporal workflow list --query WorkflowType=DispatcherWorkflow` returns a running workflow. Test by running chum serve and polling for 60 seconds.

_Created by Jarvis at 2026-03-03T23:51:29Z_
