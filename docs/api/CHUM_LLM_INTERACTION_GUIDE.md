# CHUM LLM Interaction Guide

This guide is for LLM agents operating CHUM safely and effectively.

## Goal

Use CHUM as the orchestration control plane for development work:

1. Observe system state.
2. Control scheduler behavior.
3. Inspect and triage dispatches.
4. Trigger retries/cancellations when appropriate.
5. Feed findings back into Beads.

## Operating Model

Treat CHUM as:

- Source of execution truth: dispatch and health state in SQLite/API.
- Not source of product truth: requirements and backlog remain in Beads.
- Policy executor: selection and retries are scheduler-managed.

## Fast Start

### 1) Start CHUM

```bash
./chum --config chum.toml --dev
```

For a single cycle:

```bash
./chum --config chum.toml --once --dev
```

### 2) Check Baseline Health

```bash
curl -s http://127.0.0.1:8900/status
curl -s http://127.0.0.1:8900/health
curl -s http://127.0.0.1:8900/scheduler/status
```

### 3) Watch Work

```bash
curl -s http://127.0.0.1:8900/projects
curl -s http://127.0.0.1:8900/teams
curl -s http://127.0.0.1:8900/metrics
```

## Interaction Playbooks

## A) Safe Pause for Maintenance

```bash
curl -s -X POST http://127.0.0.1:8900/scheduler/pause
curl -s http://127.0.0.1:8900/scheduler/status
```

Use this before risky config edits or runtime maintenance.

Resume:

```bash
curl -s -X POST http://127.0.0.1:8900/scheduler/resume
```

## B) Investigate a Bead Execution

```bash
curl -s http://127.0.0.1:8900/dispatches/<bead_id>
```

Inspect:

- `status`, `stage`, `exit_code`
- `failure_category`, `failure_summary`
- `output_tail`

Then correlate with Beads:

```bash
bd show <bead_id>
```

## C) Retry a Failed Dispatch

1. Confirm failure and likely transient cause.
2. Mark retry:

```bash
curl -s -X POST http://127.0.0.1:8900/dispatches/<dispatch_id>/retry
```

3. Verify retry picked up in subsequent ticks.

## D) Cancel a Problematic Running Dispatch

```bash
curl -s -X POST http://127.0.0.1:8900/dispatches/<dispatch_id>/cancel
```

Use when:

- execution is clearly runaway
- wrong target/branch/provider was selected
- maintenance window requires drain

## E) Read Improvement Signals

```bash
curl -s "http://127.0.0.1:8900/recommendations?hours=24"
```

Use recommendations as decision support, not auto-apply authority.

## Decision Rules for LLM Agents

1. Prefer `observe -> diagnose -> act` sequence.
2. Do not repeatedly retry the same failure without new evidence.
3. Pause scheduler before disruptive operations.
4. Keep Beads as canonical work narrative; log triage findings there.
5. Respect ownership locks and in-progress assignments.
6. Escalate instead of forcing through persistent unknown failures.

## Common Failure Patterns and Response

- `compile_error`:
  - Route back to coding stage with concrete failing lines.
- `test_failure`:
  - Attach failing test output and retry only after fix lands.
- `rate_limited`:
  - let scheduler/provider fallback handle it; avoid manual churn.
- `timeout`:
  - inspect prompt scope and backend; split bead if oversized.
- `session_disappeared` / unknown exit:
  - treat as infra issue first, not task-completion.

## Minimal Command Set for LLMs

```bash
# Status
curl -s http://127.0.0.1:8900/status
curl -s http://127.0.0.1:8900/health
curl -s http://127.0.0.1:8900/scheduler/status

# Control
curl -s -X POST http://127.0.0.1:8900/scheduler/pause
curl -s -X POST http://127.0.0.1:8900/scheduler/resume

# Dispatch triage
curl -s http://127.0.0.1:8900/dispatches/<bead_id>
curl -s -X POST http://127.0.0.1:8900/dispatches/<dispatch_id>/retry
curl -s -X POST http://127.0.0.1:8900/dispatches/<dispatch_id>/cancel

# Beads context
bd ready
bd show <bead_id>
bd update <bead_id> --notes "triage findings"
```

## Prompting Guidance for External LLM Controllers

When directing an LLM to operate CHUM, use explicit constraints:

- State objective and time horizon.
- Require API evidence before each action.
- Require a rollback/safe-state step (`pause`/`resume` discipline).
- Require Beads note updates for every intervention.
- Disallow blind retry loops.

Example control prompt:

```
Operate CHUM for 30 minutes as reliability steward.
Every action must cite current API evidence.
Pause scheduler before risky changes.
Update bead notes for all interventions.
Never retry the same failed dispatch more than once without new evidence.
```

## Launch-Oriented Usage (Later)

When packaging CHUM for broader use:

1. Keep this guide as the default "LLM operator contract."
2. Add a stable API version policy.
3. Add authn/authz in front of control endpoints.
4. Publish incident response runbooks aligned with these playbooks.

