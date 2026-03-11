---
title: "Fix dod_failed ch-29f1e: Test AgentWorkflow PR creation failure path"
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
    The existing task ch-29f1e has been dod_failed multiple cycles. Write TestAgentWorkflow_PRCreationFailure in internal/engine/agent_workflow_test.go. Pattern: follow the same approach as TestAgentWorkflow_SetupFailure (PR #35) and TestAgentWorkflow_MergeFailure (PR #36). Mock CreatePRActivity to return an error. Assert the workflow returns an error and does not proceed to review or merge steps. Acceptance criteria: (1) test compiles, (2) test passes with go test, (3) no other tests broken, (4) PR opened against main. Do NOT reuse the ch-29f1e task ID — create a new task or open a fresh PR directly.
depends_on: []
---

The existing task ch-29f1e has been dod_failed multiple cycles. Write TestAgentWorkflow_PRCreationFailure in internal/engine/agent_workflow_test.go. Pattern: follow the same approach as TestAgentWorkflow_SetupFailure (PR #35) and TestAgentWorkflow_MergeFailure (PR #36). Mock CreatePRActivity to return an error. Assert the workflow returns an error and does not proceed to review or merge steps. Acceptance criteria: (1) test compiles, (2) test passes with go test, (3) no other tests broken, (4) PR opened against main. Do NOT reuse the ch-29f1e task ID — create a new task or open a fresh PR directly.

_Created by Jarvis at 2026-03-03T19:36:24Z_
