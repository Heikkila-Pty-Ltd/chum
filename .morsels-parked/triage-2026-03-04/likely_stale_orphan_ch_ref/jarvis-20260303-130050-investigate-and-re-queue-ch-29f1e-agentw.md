---
title: "Investigate and re-queue ch-29f1e: AgentWorkflow PR creation failure path"
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
    Task ch-29f1e has status dod_failed — the agent ran it but DoD was not met. Investigate what PR was opened, what the test failure was (check recent PRs on the chum repo for any test relating to AgentWorkflow PR creation failure), identify why DoD failed, and either fix the acceptance criteria or re-submit. Acceptance criteria: ch-29f1e moves from dod_failed to done. The test should cover: AgentWorkflow returns error when PR creation fails, verifying the workflow terminates cleanly and surfaces the error.
depends_on: []
---

Task ch-29f1e has status dod_failed — the agent ran it but DoD was not met. Investigate what PR was opened, what the test failure was (check recent PRs on the chum repo for any test relating to AgentWorkflow PR creation failure), identify why DoD failed, and either fix the acceptance criteria or re-submit. Acceptance criteria: ch-29f1e moves from dod_failed to done. The test should cover: AgentWorkflow returns error when PR creation fails, verifying the workflow terminates cleanly and surfaces the error.

_Created by Jarvis at 2026-03-03T13:00:50Z_
