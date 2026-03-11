---
title: "Recover ch-29f1e: Test AgentWorkflow PR creation failure path"
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
    Task ch-29f1e is marked dod_failed — the agent produced output but it didn't pass the DoD: 'PR creation failure test passes. Verifies correct CloseDetail reason.' Investigate what the agent wrote (check the PR or worktree artifacts if any remain), identify why the test didn't satisfy the acceptance criteria, and either fix the test or rewrite it. Acceptance: the test for AgentWorkflow PR creation failure compiles, runs, and passes with correct CloseDetail reason asserted. 15 min cap.
depends_on: []
---

Task ch-29f1e is marked dod_failed — the agent produced output but it didn't pass the DoD: 'PR creation failure test passes. Verifies correct CloseDetail reason.' Investigate what the agent wrote (check the PR or worktree artifacts if any remain), identify why the test didn't satisfy the acceptance criteria, and either fix the test or rewrite it. Acceptance: the test for AgentWorkflow PR creation failure compiles, runs, and passes with correct CloseDetail reason asserted. 15 min cap.

_Created by Jarvis at 2026-03-03T15:16:04Z_
