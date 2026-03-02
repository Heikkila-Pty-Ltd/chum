---
title: "Auto-mark morsel as done when shark catches land"
status: ready
priority: 0
type: task
labels:
  - whale:infrastructure
  - reliability
estimate_minutes: 30
acceptance_criteria: |
  - When ChumAgentWorkflow completes with status "completed" (DoD passed), the morsel file is auto-updated to status: done.
  - The dispatcher no longer re-dispatches completed morsels.
  - If the morsel file update fails, log WARN but don't fail the workflow.
design: |
  **Step 1:** Add `MarkMorselDoneActivity`:
  - Input: project name, task ID, work directory
  - Read the morsel file from `.morsels/{task_id}.md`
  - Replace `status: ready` with `status: done` in the YAML frontmatter
  - Git add + commit the change
  
  **Step 2:** Wire into ChumAgentWorkflow:
  - Call after `recordOutcome` on the SUCCESS path (after "DoD PASSED")
  - Non-fatal: if it fails, log WARN
  
  **Step 3:** Add redundant check in ScanCandidatesActivity:
  - After listing ready tasks, re-verify each task's file status before dispatching
  - Skip any task whose file status has changed to done/backlog since DAG loaded
depends_on: []
---

Auto-mark morsel done on catch landed. Prevents re-dispatch of completed tasks.
