# Planning Operating Model

## Goal

Keep implementation costs low by moving decision-making into plan-space:

- Build a high-quality, dependency-aware backlog before coding starts.
- Run execution only from approved, ready work.
- Use ad-hoc replanning when risk changes, not fixed clock ceremonies.

## Roles

- Chief Scrum Master: portfolio-level planning, dependency arbitration, cross-project sequencing.
- Project Scrum Masters: project backlog refinement, readiness checks, and local prioritization.
- Human Operator: approves execution plans and policy-impacting changes.
- CHUM Scheduler: dispatches only when plan gate is open.

## Daily Workflow

1. Run planning ceremony (`/plan start` equivalent workflow).
2. Produce plan package:
   - priority order,
   - dependency DAG,
   - parallel execution waves,
   - readiness report and unresolved blockers.
3. Human approves plan (`/plan approve` equivalent workflow).
4. Activate plan in CHUM control plane.
5. CHUM dispatches only from ready, unblocked frontier morsels.

## Readiness Definition

The `stage:ready` gate is a hard preflight with two enforcement points:

- DAG readiness checks enforce all `stage:ready` requirements (acceptance + test + DoD + design notes + estimate)
- dispatcher preflight auto-reverts any invalid `stage:ready` task back to `stage:planning`

A morsel is executable only when all are true:

- `stage:ready`
- acceptance criteria exists and explicitly includes:
  - a test requirement (for example: `test`, `unit test`, `integration test`, or `e2e`)
  - a DoD requirement (`DoD` or `definition of done`)
- design notes exists (non-empty)
- estimate exists (`estimated_minutes > 0`)
- dependencies are satisfied
- morsel is included in the active approved plan frontier

Invalid `stage:ready` morsels are auto-reverted by scheduler preflight to `stage:planning`, with a morsel note and `stage_ready_gate_reverted` health event.

## Execution Gate

When `chief.require_approved_plan = true`, CHUM blocks implementation dispatch unless an active approved plan is set.

- Status endpoint: `GET /scheduler/plan`
- Activate plan: `POST /scheduler/plan/activate` with JSON body:

```json
{
  "plan_id": "plan-2026-02-18-main",
  "approved_by": "operator-name"
}
```

- Clear plan gate: `POST /scheduler/plan/clear`

## Ad-hoc Replanning Triggers

Replanning should be proposed when any of the following occur:

- new P0/P1 appears,
- critical dependency chain blocks beyond threshold,
- churn/retry/conflict indicators spike,
- major scope or ownership change is detected.

Human approval is required before plan policy changes become active.

## Autonomy Boundaries

- Bot-autonomous: execution, monitoring, routine summaries, and optimization suggestions.
- Human-required: activating plans and approving policy-impacting changes.
- Human-by-exception: reviews/retros when risk thresholds are breached.
