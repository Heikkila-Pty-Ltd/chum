# Docker Dispatcher Design

Last updated: **2026-02-22**

## Scope

This document is the canonical design narrative for CHUM's container execution path currently implemented as a legacy `DockerDispatcher` in `internal/dispatch/docker.go`.

The active runtime does not currently select this path. All dispatch execution in shipping code goes through the `dispatch.Backend` abstraction with active backends `headless_cli` and `openclaw`.

## Current implementation inventory

### Canonical implementation files

- `internal/dispatch/docker.go` — concrete container launcher and session lifecycle methods.
- `internal/dispatch/dispatch.go` — legacy `DispatcherInterface` for PID-managed process execution.
- `internal/dispatch/backend.go` — active `Backend` interface used by scheduler activities.
- `internal/dispatch/openclaw.go` and `internal/dispatch/headless.go` — active `Backend` implementations.
- `internal/config/validate.go` and `internal/config/types.go` — config validation of dispatch routing.

### Confirmed facts from code

- `DockerDispatcher` exposes `Dispatch(ctx, agent, prompt, provider, thinkingLevel, workDir)`, `IsAlive`, `Kill`, `GetHandleType`, `GetSessionName`, and `GetProcessState`.
- `DockerDispatcher` does not satisfy the active `dispatch.Backend` interface.
- `docker` is not an accepted `dispatch.routing.*_backend` value today.
- `internal/config/validate.go` only allows `headless_cli` and `openclaw`.
- No workflow or scheduler path currently instantiates `NewDockerDispatcher`.

## Terminology alignment

- **Docker dispatcher**: the concrete implementation in `internal/dispatch/docker.go`.
- **Dispatch backend**: interface implemented by runtime selection components in `internal/dispatch/backend.go`.
- **Docker container session**: one `docker` container started for a dispatch handle.
- **Handle**: integer identifier returned by `DockerDispatcher.Dispatch`.
- **Session name**: container name format `chum-agent-<handle>-<unix-ns>`.
- **Migration target**: moving this legacy dispatcher to the active backend contract.

## Architecture overview

The Docker path is currently a self-contained execution module with explicit fixed behavior.

### Component responsibilities

- `DockerDispatcher.Dispatch`
  - allocate handle and container session name.
  - create `os.TempDir()/chum-ctx-<session>` context directory.
  - write execution metadata (`prompt.txt`, `agent.txt`, `thinking.txt`, `provider.txt`, `script.sh`).
  - start a container from `chum-agent:latest` with bind mounts and provider credentials.
  - store session mapping in internal maps.

- `DockerDispatcher.IsAlive`
  - inspect container running state for a handle.

- `DockerDispatcher.GetProcessState`
  - map runtime state to simplified process state values.

- `DockerDispatcher.Kill`
  - force-remove container and remove context directory.

- `CaptureOutput`
  - separate package function that collects container logs from a session name.

- `CleanDeadSessions`
  - enumerate all local containers and remove stopped `chum-agent-*` instances.

- `IsDockerAvailable` and `HasLiveSession`
  - currently placeholder stubs.

## Execution flow

```text
HTTP/cron/scheduler
  └─ dispatch selection logic (active backends only today)

(For docker path only)
  ┌─ Dispatch(call params)
  │
  ├─ allocate handle and session name
  ├─ write temp context files
  ├─ create container config + host config
  ├─ mount volumes and inject provider env vars
  ├─ create/start container
  ├─ track handle→session metadata
  └─ return handle to caller

Lifecycle management
  ├─ IsAlive/ GetProcessState for polling
  ├─ CaptureOutput(session_name) for visibility
  ├─ Kill(handle) for manual abort
  └─ CleanDeadSessions() for cleanup sweep
```

### Runtime sequence in detail

1. `Dispatch` increments `nextHandle` and builds deterministic session name.
2. Context directory is created and metadata files are persisted.
3. `openclawShellScript()` content is copied into `/chum-ctx/script.sh`.
4. Container is created with `ContainerConfig`:
   - image: `chum-agent:latest`
   - command: shell entrypoint over `/chum-ctx/script.sh` with four parameter files
   - working directory: `/workspace`
5. Host mounts are prepared:
   - `/chum-ctx` read-only for prompt and context
   - `/workspace` writable workspace fallback to temp on creation errors
   - `$HOME/.openclaw` mapped to `/root/.openclaw`
6. Container is started and handle metadata persisted in memory.
7. Caller can query state via container inspect or kill container by handle.

## State model

### In-memory state

- `sessions map[int]string` — handle to container session name.
- `metadata map[string]string` — last known `agent=<agent>,provider=<provider>` per session.
- `nextHandle int` — monotonic integer for stable handle allocation.
- `sync.Mutex` — serializes state transitions.

### Derived execution state

`GetProcessState(handle)` returns:

- `running` when container state is running.
- `exited` when container is not running and not flagged `dead`/`oom`.
- `failed` when container state is `Dead` or `OOMKilled`.
- `unknown` when handle lookup or inspect fails.

`GetProcessState` always returns `ExitCode` from container state and does not infer exit reason beyond that mapping.

### Data model side effects

- Context directory path is based on system temp directory.
- Container logs are collected outside the backend contract through `CaptureOutput`.
- Container cleanup on `Kill` removes temp context directory for that handle.

## Configuration model

Docker behavior currently uses internal constants and environment values directly:

- hardcoded image: `chum-agent:latest`
- hardcoded env exposure: `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, `CHUM_TELEMETRY`
- hardcoded cleanup behavior: `AutoRemove: false`

To activate this path as an official **dispatch backend**, migration should move these items into explicit config:

- `dispatch.routing` entries in `dispatch.routing.*_backend`.
- per-backend image, mount policy, timeout, and cleanup policy.
- secure credential delivery strategy (per-provider only, rotated credentials, optional vault/secret provider).
- backend adapter layer between `DockerDispatcher` and `dispatch.Backend`.

## Failure handling

### Direct failure classes

- Context directory creation failure returns an immediate error.
- `prompt.txt` and related file writes are fail-fast.
- Container create/start failures are returned with wrapped contextual errors.
- Container inspect failures convert state to `unknown` and can be observed through caller logic.
- `Kill` and `CleanDeadSessions` return/ignore errors from Docker remove operations, preserving best-effort cleanup.

### Retry and resilience behavior

There is no retry wrapper inside `docker.go`.

- Failed start/create calls are immediate caller-level failures.
- No exponential backoff or queue admission control is built into this file.
- Caller logic must implement retry classification and escalation.

## Observability and diagnostics

### Existing observable signals

- Structured logs use injected slog logger for dispatcher and container lifecycle actions.
- Health/state is inferred via inspect-based polling APIs.
- Output visibility is available via:
  - `CaptureOutput(sessionName)` snapshots
  - manual `docker logs <container>`
- No dedicated Prometheus metrics exist for Docker state in this file.

### Recommended first-level checks

- verify docker daemon availability before first dispatch.
- verify image presence and script compatibility for `openclawShellScript()`.
- verify context workspace permissions before starting bulk dispatch.

## Security boundaries

### Current risk posture

- All provider API keys (`ANTHROPIC`, `OPENAI`, `GEMINI`, `CHUM_TELEMETRY`) are injected broadly into every container.
- Temporary context and logs remain on local filesystems under `os.TempDir()` for the life of dispatch.
- `AutoRemove` is disabled, so manual cleanup is required for storage reclamation.
- `isDocker` availability checks are stubbed and not authoritative.

### Minimum hardening for migration

- move to task-specific secret scoping.
- add optional read-only workspace for non-mutating jobs.
- ensure container output and context directories are encrypted or short-lived per deployment policy.
- implement explicit resource limits and network policy controls.

## Verification

### Static verification (required)

```bash
rg -n "type DockerDispatcher|func \(d \*DockerDispatcher\)|openclawShellScript|chum-agent:latest" internal/dispatch/docker.go internal/dispatch/backend.go internal/config/validate.go internal/config/types.go
go test ./internal/dispatch -run Docker -count=1
```

### Runtime verification (current behavior)

1. Confirm no runtime routing references `docker` in dispatch routing config.
2. Launch one local dispatch if a test environment is prepared.
3. Observe handle allocation, inspect output, and force-kill path.
4. Confirm `CleanDeadSessions()` removes stopped `chum-agent-*` containers and context directories.

### Cross-document verification points

- `docs/api/ENDPOINTS.md` for runtime contract references that should not cite Docker as active.
- `docs/architecture/CONFIG.md` for dispatch backend options currently recognized by validation.

## Known limitations and migration gaps

### Known limitations

- `DockerDispatcher` uses legacy interface, so it cannot be selected by current backend wiring.
- `dispatch.routing` does not accept `docker`.
- `IsDockerAvailable` and `HasLiveSession` are placeholders.
- No backend adapter means metrics and lifecycle hooks are missing from active scheduler instrumentation.
- Image and credential policy are hard-coded.
- Cleanup relies on manual cleanup paths and best-effort sweeps.

### Migration gaps to productionize

1. Add a backend adapter that satisfies `dispatch.Backend`.
2. Register `docker` as a valid routing backend.
3. Add `docker` to runtime config schema and docs.
4. Add contract tests against `Backend` interface semantics.
5. Decide and enforce per-provider secret scoping.
6. Add telemetry counters for start/fail/kill/retry states.

## Maintenance guidance

Update this document when any of these files change:

- `internal/dispatch/docker.go`
- `internal/dispatch/backend.go`
- `internal/config/validate.go`
- `docs/architecture/CONFIG.md`
