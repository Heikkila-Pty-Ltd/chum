# Stingray Runbook

## Scope

This runbook documents the **current Stingray implementation state** and the expected operating path for a code-health audit feature that currently has:

- persistence schema in place
- `GatherMetricsActivity` implemented and registered
- **no** dedicated workflow/controller/endpoint that can start Stingray on a schedule yet

It also captures recovery steps when activity-only mode is degraded.

## Required prerequisites

Before any Stingray work:

- State DB path is configured and writable.
- `store` migrations include `stingray_runs` and `stingray_findings` tables.
- Temporal worker binary is buildable with latest schema.
- `go` and `golangci-lint` are available in the runtime PATH when collecting lint metrics.
- Repository contains at least one valid Go module (`go.mod`) in target `work_dir`.
- If using scheduled runs, a separate trigger process exists (external scheduler, manual workflow start, or test harness).

## Current architecture state (important)

- `internal/temporal/worker.go` registers `acts.GatherMetricsActivity`.
- There is no active `StingrayWorkflow` registration to start the activity chain.
- There is no API endpoint in `internal/api/api.go` for start/resume/health of Stingray itself.
- Current runtime gap is documented in `docs/architecture/STINGRAY_DESIGN.md`.

## Normal workflow (what is supported today)

### 1) Readiness checks

```bash
sqlite3 "$STATE_DB" "SELECT name FROM sqlite_master WHERE type='table' AND name IN ('stingray_runs','stingray_findings') ORDER BY name;"
sqlite3 "$STATE_DB" "SELECT name FROM sqlite_master WHERE type='index' AND name LIKE 'idx_stingray_%' ORDER BY name;"
```

### 2) Verify code paths are loadable

```bash
go test ./internal/store -run TestStingray -count=1
go test ./internal/temporal -run Stingray -count=1
```

### 3) Validate activity behavior in context

Use a targeted test or temporary runner that invokes `GatherMetricsActivity` input for a project and `work_dir`.

### 4) Verify persistence after a run attempt

```bash
sqlite3 "$STATE_DB" "SELECT id, project, run_at, findings_total, findings_new, findings_resolved FROM stingray_runs ORDER BY id DESC LIMIT 10;"
sqlite3 "$STATE_DB" "SELECT id, project, category, severity, title, file_path, status FROM stingray_findings ORDER BY id DESC LIMIT 25;"
```

### 5) Validate structured output fields

Look for:

- `go_vet` command result
- `golangci-lint` issue count
- `go test -coverprofile` + `go tool cover -func`
- outdated dependency counts
- TODO/HACK/FIXME/WORKAROUND hits
- module graph edge count

## Degraded workflow (when scheduling is unavailable)

When no runtime trigger exists:

- keep collection non-blocking in core CHUM operations
- record an operator note that Stingray collection is pending
- rely on store query checks rather than runbook completion signals
- skip escalation unless repeated collection failures indicate process breakage

## Monitoring and verification signals

- **Worker logs**
  - look for `[🦂 STINGRAY]` markers in activity output
  - expect paired `Gathering code health metrics` and `Metrics collected` events

- **Database signals**
  - growth in `stingray_runs`
  - open findings in `stingray_findings`
  - status transitions (`open` / `filed` / `resolved` / `wont_fix`)

- **Command execution signals**
  - non-zero exit codes in `go vet`, `golangci-lint`, `go test`, `go tool cover`
  - parse failures in dependency/lint output parsing paths

## Common failure scenarios and responses

- **Missing `go` binary**
  - install Go runtime in worker host and rerun readiness checks

- **Missing `golangci-lint`**
  - `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`
  - mark run degraded if policy permits

- **`go test` timeout or module download stalls**
  - extend command budget in operator run scripts
  - run with warmed module cache when possible

- **`grep` exit code 1 with no TODO hits**
  - expected and not an automatic failure in Stingray parsing

- **Database lock during write**
  - stop competing writers temporarily and retry with short backoff

- **Persistent parse errors**
  - persist command output and last-good logs to keep evidence
  - review parser expectations in activity tests before changing output formats

## Recovery and cleanup

1. Collect recent logs for the specific work item.
2. Verify DB connectivity and lock state.
3. Re-run `go test ./internal/temporal -run Stingray -count=1`.
4. Restart worker process.
5. Re-run `sqlite3` checks from readiness section.
6. Only clear `stingray_findings` if an operator explicitly approves; keep audit evidence (`id`, `title`, `last_seen`) before deletion.

## Escalation

Escalate to maintainers when any condition persists for two consecutive attempts:

- repeated command-timeout failures
- worker startups that cannot import `GatherMetricsActivity`
- no new rows written despite successful command completion
- recurring parse-corruption in lint/test output handling

Include in escalation package:

- project + work_dir
- command + exit code snapshot
- one representative log excerpt containing `[🦂 STINGRAY]`
- list of failing checks from DB snapshots

## Pre-run checks before handoff

- [ ] `stingray_runs` and `stingray_findings` tables exist.
- [ ] `go test ./internal/store -run TestStingray -count=1` passes.
- [ ] `go test ./internal/temporal -run Stingray -count=1` passes.
- [ ] `GatherMetricsActivity` output contains command summaries in logs.
- [ ] `stingray_runs` has at least one completed row after a manual test run.

## References

- [`docs/architecture/STINGRAY_DESIGN.md`](../architecture/STINGRAY_DESIGN.md) — target architecture and intended workflow.
- [`docs/api/ENDPOINTS.md`](../api/ENDPOINTS.md) — endpoint inventory and control-plane status/error conventions.
- [`internal/temporal/worker.go`](../../internal/temporal/worker.go) — current worker registrations.
- [`internal/store/stingray.go`](../../internal/store/stingray.go) and [`internal/store/schema.go`](../../internal/store/schema.go) — persistence contract.
