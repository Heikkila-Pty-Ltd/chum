# STINGRAY Operations Runbook

Last updated: **2026-02-22 (UTC)**.

## Purpose

Stingray is intended as an automated repository health and debt auditor. This runbook documents what is currently implemented and how operators should verify, monitor, and incident-handle the existing code paths.

## Current implementation state

Stingray is partially wired in code, with substantial pieces in place but not connected to an end-to-end control plane yet.

### What exists today

- Data model types are defined in `internal/temporal/stingray_types.go`.
- Metrics collection logic is implemented in `internal/temporal/stingray_activities.go` via `GatherMetricsActivity`.
- Subprocess helpers and parsing logic are implemented in `internal/temporal/stingray_helpers.go`.
- Store persistence methods and schema assumptions are implemented in `internal/store/stingray.go`.
- Store schema migration creates `stingray_runs` and `stingray_findings` tables.

### What is not yet wired

- No `workflow_stingray.go` in `internal/temporal`.
- No worker wiring for a Stingray workflow.
- `temporal.StartWorker` currently registers `GatherMetricsActivity` as a standalone activity but does not attach a `StingrayWorkflow`.
- No API endpoint starts or controls Stingray.
- No scheduled activity registration for periodic stingray runs exists in `cmd/chum/main.go`.

This means operators should treat Stingray as an implementation layer that can be called only if a workflow layer is added.

## Startup and discovery checklist

1. Start CHUM with normal flags and config.
2. Confirm process logs are visible and no startup errors around store migration or worker startup.
3. Confirm `stingray_runs` and `stingray_findings` tables exist in state DB.
4. Confirm no runtime panic from activity registration and inspect logs for a line containing `worker started`.
5. Confirm `cmd/chum/main.go` has no Stingray schedule creation for the selected runtime.

## Verify tables and schema health

Use the configured state DB path from `General.StateDB`, for example:

```bash
STATE_DB=./state.db
sqlite3 "$STATE_DB" "SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'stingray%';"
sqlite3 "$STATE_DB" "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='stingray_runs';"
sqlite3 "$STATE_DB" "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='stingray_findings';"
```

Recommended row-level checks:

```bash
sqlite3 "$STATE_DB" "SELECT id, project, run_at, findings_total, findings_new, findings_resolved FROM stingray_runs ORDER BY id DESC LIMIT 5;"
sqlite3 "$STATE_DB" "SELECT id, project, category, severity, title, file_path, status, first_seen, last_seen FROM stingray_findings ORDER BY last_seen DESC LIMIT 20;"
```

## Scheduled activity health checks

Because Stingray scheduling is not currently wired, the standard expected state is:

- no active periodic schedule for a Stingray workflow.
- no `StingrayWorkflow` visible in registered workflow sets.

Verification commands:

```bash
rg -n "StingrayWorkflow|workflow_stingray|stingray" internal/temporal cmd/chum
rg -n "Create\(ctx|ExecuteWorkflow\(|RegisterWorkflow\(.*Stingray" internal/temporal/worker.go cmd/chum/main.go
```

If/when a workflow is introduced, expected checks are:

- schedule cadence visibility in Temporal UI or CLI.
- last execution timestamp and run duration for `StingrayWorkflow`.
- presence of new `stingray_runs` rows after each run.
- number of new findings and trend by project over time.

## Runtime and incident runbook

### When GatherMetricsActivity behavior appears missing

1. Confirm the activity exists in source and is registered in worker startup logs.
2. Confirm Temporal activity dispatch is reaching the worker process:
   - grep logs for `🦂 STINGRAY` prefix.
3. Confirm shell commands used by the activity are available:
   - `go`, `golangci-lint`, and `go mod` commands.
4. If commands are missing on host, fix worker environment and restart.

### When rows do not appear in stingray tables

1. Confirm DB path in config points to the same file the operator is querying.
2. Confirm the scheduler/trigger exists in production.
3. Verify migration ran in startup logs.
4. If needed, inspect table creation and ensure worker startup did not log schema errors.

### When activity command outputs are unexpectedly empty

1. Inspect `CommandResult` fields in persisted findings or logs for stdout/stderr trim and `timed_out` flags.
2. Re-run command manually in the same runtime environment for confirmation.
3. Re-check file permissions for code directories and git workspace.
4. Increase timeout in constants only when command behavior is consistently slow and safe.

### When parsing or persistence fails

1. Check `StingrayMetrics.Errors` data in the run payload (from code path).
2. Correlate with `error` logs around the same timestamp.
3. Validate input path: `WorkDir` must exist and be readable.
4. Confirm module and VCS state is not half-checked out.

## Log locations and diagnostics

- Chum logs are written through the configured logger (`os.Stderr`) in both JSON and text mode.
- Runbook-specific entries from this code include prefix `🦂 STINGRAY` in activity logs.
- No dedicated Stingray metrics endpoint or alert channel is implemented in this branch.
- Query diagnostics are available through:
  - process logs,
  - workflow history in Temporal,
  - `stingray_runs`, `stingray_findings` rows,
  - and `cmd/chum/main.go` startup and schedule logic.

## Known not-yet-wired boundaries

- No CLI/API path to manually trigger stingray today.
- No cron/schedule registration for Stingray in startup.
- No workflow file that composes:
  - `GatherMetricsActivity`
  - analysis activity
  - dedup/funnel
  - reporting/filing
- `StingrayMetrics` can be collected but is not part of an end-to-end operational control flow yet.

## Verification section

For every operator handoff, include these checks:

1. `go test ./internal/store -run Stingray -count=1`
2. `rg -n "gatherMetrics|GolangCILintResult|CoverageResult|TODOScanResult|DepGraphResult" internal/temporal`
3. `rg -n "stingray_runs|stingray_findings|StingrayWorkflow" internal`
4. Verify that `internal/temporal/worker.go` does not assume Stingray workflow registration.

## Known limitations

- The implementation can generate metrics but does not yet provide an active schedule or control endpoint.
- Existing code has no external visibility into command-level failure context unless logs and `CommandResult` parsing are read together.
- Findings persistence and trend analysis are not exercised by automated end-to-end production runs in this release.
- Documentation in this repository may still mention future Stingray objectives (e.g., `internal/temporal/workflow_stingray` style architecture) that are not yet complete.

## Maintenance note

Update this runbook when any of these files change:

- `internal/temporal/stingray_types.go`
- `internal/temporal/stingray_helpers.go`
- `internal/temporal/stingray_activities.go`
- `internal/temporal/worker.go`
- `internal/store/stingray.go`
- `cmd/chum/main.go`
