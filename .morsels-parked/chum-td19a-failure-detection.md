---
title: "Auto-Postmortem Step 1: Failure Detection & History Fetching"
status: ready
priority: 1
type: feature
labels:
  - whale:learner
  - reliability
estimate_minutes: 45
acceptance_criteria: |
  - DispatcherWorkflow tick checks for FAILED workflows since last check via Temporal Visibility API.
  - Failed workflow IDs are tracked to avoid duplicate investigations.
  - For each new failure, the workflow history (error messages, activity types, stack traces) is fetched and structured.
  - A PostMortemWorkflow is spawned with the failure context.
design: |
  **Step 1:** Add `CheckFailedWorkflowsActivity`:
  - Query Temporal: `ExecutionStatus="Failed" AND CloseTime > <last_check>`
  - Store last_check timestamp in SQLite to avoid re-processing
  - Return list of failed workflow IDs + error summaries
  
  **Step 2:** Add `FetchFailureContextActivity`:
  - For each failed workflow, fetch its event history via Temporal API
  - Extract: error messages, failed activity types, attempt counts, durations
  - Fetch recent git log (last 10 commits) for context
  
  **Step 3:** Register PostMortemWorkflow:
  - Spawn as child of DispatcherWorkflow for each new failure
  - Pass failure context as input
depends_on: ["chum-td18-prevent-silent-failures"]
---

Step 1 of the Auto-Postmortem system. Detects failed workflows and
fetches their error context for LLM investigation.
