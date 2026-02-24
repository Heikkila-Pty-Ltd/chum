---
title: "Auto-investigate workflow failures with post-mortem LLM analysis"
status: done # decomposed into chum-td19a/b
priority: 1
type: feature
labels:
  - whale:learner
  - reliability
estimate_minutes: 120
acceptance_criteria: |
  - Every WORKFLOW_EXECUTION_FAILED event triggers a PostMortemWorkflow.
  - PostMortemWorkflow uses a premium LLM to analyze the failure history, error messages, and recent changes.
  - It identifies root causes (e.g., invalid model name, missing DB column, unregistered activity).
  - It proposes hardening actions: antibody morsel, config fix, code patch.
  - It auto-files the antibody morsel in the backlog for the sharks to execute.
  - Silent/recurring failures that were previously swallowed are surfaced.
design: |
  **Context:** When workflows fail (e.g., StrategicGroomWorkflow crashes with ModelNotFoundError),
  the failure is logged and forgotten. The human has to manually discover it, diagnose it, and fix it.
  This should be automated.
  
  **Approach:**
  1. Register a Temporal Visibility query that polls for FAILED workflows every N minutes.
  2. For each new failure, spawn a `PostMortemWorkflow` as a child.
  3. `InvestigateFailureActivity` fetches the workflow history via Temporal API, extracts error messages.
  4. Passes the error context + recent git log + repo map to `gemini-2.5-pro`.
  5. LLM outputs structured JSON: `{root_cause, severity, proposed_fix, antibody_morsel}`.
  6. `FileAntibodyActivity` creates a morsel with the proposed fix → sharks execute it.
  
  **Integration:** Hook into the DispatcherWorkflow tick — each tick checks for new FAILED
  workflows since last check, spawns PostMortemWorkflow for each.
depends_on: ["chum-td18-prevent-silent-failures"]
---

# Auto-Investigate Workflow Failures

Every workflow failure triggers a premium LLM post-mortem that identifies root causes
and auto-files antibody morsels for the sharks to fix.
