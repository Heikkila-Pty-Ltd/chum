# Docker Dispatcher Design

## Scope and current status

`internal/dispatch/docker.go` defines `DockerDispatcher`, a container-based implementation of the legacy `DispatcherInterface`.

As of 2026-02-22, it is an **implementation-layer component only**:

- It is **not selected by dispatch routing** in active production paths.
- It is **not exposed through `dispatch.Backend`**, so it cannot replace `headless_cli`/`openclaw` backends directly.
- There is no documented feature flag in `config` that can switch scheduled dispatches to Docker.

## Purpose

The dispatcher exists to isolate OpenClaw agent execution inside a disposable Docker container with explicit handle-based state tracking.

It is designed for environments where local process execution is not acceptable or where deterministic cleanup and stronger execution boundaries are required.

## Component responsibilities and ownership

`DockerDispatcher` owns:

- context filesystem lifecycle for each dispatch (`$TMPDIR/chum-ctx-<session>`)
- container lifecycle through Docker API calls
- handle-to-session bookkeeping (`handle -> container name`)
- container output capture helper (`CaptureOutput`)
- periodic dead-session cleanup (`CleanDeadSessions`)

## Architecture overview

1. `NewDockerDispatcher` creates a Docker API client via `client.NewClientWithOpts`.
2. `Dispatch` writes execution inputs to temporary files under a per-dispatch context directory.
3. It starts a container using image `chum-agent:latest`.
4. The command executes `sh /chum-ctx/script.sh` with file paths for:
   - prompt
   - agent
   - thinking level
   - provider
5. Metadata and session name are stored under a mutex-protected map.
6. `IsAlive`, `GetProcessState`, and `Kill` route from handle to container session name.
7. `CleanDeadSessions` finds stopped `chum-agent-*` containers and removes related context directories.

`DockerDispatcher` does not start HTTP polling itself; it relies on caller-side lifecycle management.

## Runtime lifecycle model

- **Dispatch**
  - Generates handle and container/session identifiers.
  - Creates context directory and writes prompt/agent/thinking/provider/script artifacts.
  - Creates/starts container.
  - Registers the handle.
  - Returns the handle immediately.

- **Observe**
  - Poll using `IsAlive(handle)` or `GetProcessState(handle)`.
  - State mapping:
    - `running`
    - `failed` when container is dead or OOM-killed
    - `exited` for normal termination
    - `unknown` for lookup/inspect failures

- **Collect output**
  - `CaptureOutput(sessionName)` reads combined stdout/stderr logs from Docker log stream.

- **Terminate/cleanup**
  - `Kill(handle)` force-removes container and cleanup directory.
  - `CleanDeadSessions()` can be used as janitor for orphaned sessions.

## Interface compatibility

### `dispatch.DispatcherInterface` (legacy)

`DockerDispatcher` currently implements:

- `Dispatch`
- `IsAlive`
- `Kill`
- `GetHandleType` (returns `"docker"`)
- `GetSessionName`
- `GetProcessState`

### `dispatch.Backend` (pluggable runtime)

`DockerDispatcher` does not implement `dispatch.Backend`:

- no `Dispatch(ctx, opts)` with `DispatchOpts`
- no `Status`, `CaptureOutput(handle)` by handle, `Cleanup`, or `Name`

## Migration notes from legacy model

Current production execution is legacy PID/headless-backed:

- `Dispatcher` executes prompt files through local shell CLI paths.
- `OpenClawBackend` adapts that dispatcher to `dispatch.Backend`.
- `HeadlessBackend` executes provider CLIs with explicit process handles and log files.

To migrate Docker into the active runtime:

1. Introduce a `DockerBackend` adapter implementing `dispatch.Backend`.
2. Translate a backend handle into `dispatch.Handle` fields.
3. Define status mapping (`running`/`completed`/`failed`/`unknown`).
4. Implement deterministic output and cleanup semantics.
5. Add runtime routing config to select backend per tier.
6. Add integration coverage for cleanup and status transitions.

## Operational assumptions and caveats

- **Image assumption**
  - Hard-coded image: `chum-agent:latest`.
  - No automatic pull policy or digest pinning in dispatcher code.

- **Runtime assumptions**
  - The daemon must be reachable via default Docker environment variables (`DOCKER_HOST`, `DOCKER_TLS_VERIFY`, etc.).
  - `workDir` is mounted from host into `/workspace`.

- **Security and secret handling**
  - Provider keys are forwarded via environment variables for all dispatches.
  - This includes `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, `CHUM_TELEMETRY`.
  - No per-dispatch secret isolation exists yet.

- **Container lifecycle and cleanup**
  - Containers are created with `AutoRemove: false`.
  - Explicit `Kill` or periodic cleanup is required.
  - There is no automatic success-path cleanup in `Dispatch` caller code by default.

- **Failure handling**
  - A failed daemon/client initialization emits a warning but leaves dispatcher usable only until a client call path panics.
  - Several methods return generic `error` envelopes without surfacing richer structured failure categories.

- **Known probe gaps (feature flags/state checks)**
  - `IsDockerAvailable()` currently always returns `true`.
  - `HasLiveSession()` currently always returns `false`.

## Health and observability

Current visibility is best effort:

- `slog` emits dispatch lifecycle events in `Dispatch`, `GetProcessState`, and `Kill` call sites.
- Container visibility is via Docker CLI and container logs.
- Dispatch errors are surfaced through direct return values.

No dedicated backend-level Prometheus metrics exist inside `DockerDispatcher` yet.

## Required runtime setup

- Docker daemon and image build/deploy pipeline.
- Read/write access for:
  - `$TMPDIR` for context directories
  - `workDir` destination
- Available keys for container provider calls.

## Recovery and manual cleanup

When sessions are dangling:

- `docker ps -a --filter name=chum-agent-`
- `docker logs <session-name>` for last output
- `docker rm -f <session-name>` for stuck containers
- remove orphaned context directories manually with `rm -rf $TMPDIR/chum-ctx-<session>` when needed

Run periodic `CleanDeadSessions()` in a service scheduler if manual cleanup is not desired.

## Operational verification

- `go test ./internal/dispatch -run Docker -count=1` (where tests exist in your branch)
- Container smoke checks in environments that support Docker:
  - dispatch creation
  - `IsAlive` transition to false after completion
  - `GetProcessState` state + exit codes
  - `CleanDeadSessions` removing non-running `chum-agent-*`

Link to runtime context:

- `docs/operations/STINGRAY_RUNBOOK.md` — operational format for long-running workers and failure response.
- `docs/architecture/CONFIG.md` — dispatch backend/router configuration for production runtime selection.
