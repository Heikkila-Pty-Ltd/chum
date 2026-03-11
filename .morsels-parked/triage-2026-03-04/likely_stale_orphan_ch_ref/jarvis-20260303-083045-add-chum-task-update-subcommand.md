---
title: "Add chum task update subcommand"
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
    The chum CLI has no way to update a task's status from the command line. Only 'create' exists. Add a 'chum task update --id TASK_ID --status STATUS' subcommand that updates an existing task's status in the DAG store. Acceptance criteria: running 'chum task update --id ch-c8689 --status needs_review' succeeds and the task shows needs_review in 'chum tasks' output. Handle invalid IDs and invalid status values with clear error messages.
depends_on: []
---

The chum CLI has no way to update a task's status from the command line. Only 'create' exists. Add a 'chum task update --id TASK_ID --status STATUS' subcommand that updates an existing task's status in the DAG store. Acceptance criteria: running 'chum task update --id ch-c8689 --status needs_review' succeeds and the task shows needs_review in 'chum tasks' output. Handle invalid IDs and invalid status values with clear error messages.

_Created by Jarvis at 2026-03-03T08:30:45Z_
