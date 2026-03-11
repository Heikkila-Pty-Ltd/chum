---
title: "Rewrite ch-29f1e: AgentWorkflow PR creation failure test"
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
    Task ch-29f1e has been stuck in dod_failed for multiple cycles. Investigate why the PR creation failure path test is failing DoD. Read the existing branch/PR if any, identify the specific DoD failure reason from the task metadata, then rewrite the test so it passes DoD criteria. Acceptance criteria: task transitions out of dod_failed to needs_review or completed. Test must compile and pass go test.
depends_on: []
---

Task ch-29f1e has been stuck in dod_failed for multiple cycles. Investigate why the PR creation failure path test is failing DoD. Read the existing branch/PR if any, identify the specific DoD failure reason from the task metadata, then rewrite the test so it passes DoD criteria. Acceptance criteria: task transitions out of dod_failed to needs_review or completed. Test must compile and pass go test.

_Created by Jarvis at 2026-03-03T10:30:52Z_
