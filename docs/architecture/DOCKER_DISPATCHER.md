# Docker Dispatcher Design

## Scope and status

`internal/dispatch/docker.go` contains `DockerDispatcher`, a container-native execution path for agent prompts.

As of 2026-02-22, this implementation is **not the active runtime path**.

- It is not registered by the dispatch backend factory used for scheduler runs.
- It is not a valid value in `dispatch.routing` (`headless_cli` and `openclaw` are the only validated backends).
- It is therefore a capability component with no production selection route yet.

The `dispatch` docs should treat this as a migration target and not a current default.

## Purpose

`DockerDispatcher` isolates each execution inside a disposable container with explicit context directories and separate state handles.

Primary intent:

- provide stronger process containment than PID-based dispatching;
- make per-run cleanup explicit by handle;
- enable host-independent runtime assumptions for future deployments.

## Architecture

### Main actors

- `DockerDispatcher` in `internal/dispatch/docker.go`
- Docker daemon client from `github.com/docker/docker/client`
- context work dir under `TMPDIR/chum-ctx-<session>`
- per-dispatch container named `chum-agent-<handle>-<unixns>`

### Responsibilities

- create per-run context directories and write execution artifacts (`prompt.txt`, `agent.txt`, `thinking.txt`, `provider.txt`, `script.sh`);
- launch a container with image `chum-agent:latest`;
- map context and workdir mounts into the container;
- track handle→session mapping and optional session metadata;
- expose handle lifecycle and process state via `DispatcherInterface`;
- provide cleanup and dead-session cleanup utilities.

## Lifecycle model

1. `Dispatch` allocates a numeric handle and stable session name.
2. `Dispatch` creates `chum-ctx-<session>` and writes prompt/agent/provider inputs.
3. Container starts with `sh /chum-ctx/script.sh` and is tracked by session name.
4. The caller observes state through `IsAlive(handle)` or `GetProcessState(handle)`.
5. Termination uses `Kill(handle)`, which force-removes the container.
6. `CleanDeadSessions()` prunes stopped `chum-agent-*` containers outside active process flow.

### State transitions

- `dispatch -> running` (container reported as `running`)
- `dispatch -> exited` (container stopped without dead/oom)
- `dispatch -> failed` (container dead or OOM-killed)
- `dispatch -> unknown` (inspect failure / missing mapping)

## Interface compatibility

### Implements `DispatcherInterface`

`DockerDispatcher` currently implements the legacy interface used by older scheduler paths:

- `Dispatch(ctx, agent, prompt, provider, thinkingLevel, workDir) (int, error)`
- `IsAlive(handle int) bool`
- `Kill(handle int) error`
- `GetHandleType() string`
- `GetSessionName(handle int) string`
- `GetProcessState(handle int) ProcessState`

### Not a `Backend` yet

`DockerDispatcher` does not implement `dispatch.Backend` (`internal/dispatch/backend.go`):

- missing `Dispatch(ctx context.Context, opts DispatchOpts) (Handle, error)`
- missing `Status(handle Handle)`, `CaptureOutput(handle Handle)`, `Cleanup(handle Handle)`
- missing `Name() string` on the struct

This is the primary API gap for migration.

## Migration notes from legacy execution model

Current production dispatch routing resolves via `dispatch.routing` and `dispatch.Backend` (`headless_cli`, `openclaw`).

To integrate Docker cleanly:

- create a `DockerBackend` adapter that implements `dispatch.Backend`;
- update routing validation to include `docker` as an opt-in backend;
- map `DispatchOpts` fields to container inputs;
- normalize status states (`running` / `completed` / `failed` / `unknown`) to workflow expectations;
- decide and document output and cleanup ownership (who deletes host temp files and when);
- gate rollout behind config to allow rollback to `headless_cli` or `openclaw`.

## Operational assumptions and caveats

- image hard-coded to `chum-agent:latest`;
- container lifecycle is explicit (`AutoRemove: false`), so cleanup can leak without periodic maintenance;
- provider secrets are passed as env vars to each container (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY`, `CHUM_TELEMETRY`);
- per-container secrets are not isolated by provider yet;
- `workDir` mount is best-effort: fallback to `TMPDIR/chum-workspace-<session>` when target path cannot be created/written;
- CLI client creation errors are logged and `DockerDispatcher` is still constructed with an internal nil-check path.

## Operational caveats and known gaps

- `IsDockerAvailable()` currently always returns `true` (probe gap).
- `HasLiveSession()` currently always returns `false` (placeholder behavior).
- no automatic success-path cleanup.
- no `Backend` adapter to reuse runtime orchestration.
- no route through `dispatch.routing` in current config validation.

## Failure handling

- invalid Docker daemon connection fails at container API call time (`Dispatch`, `Kill`, `IsAlive`, `GetProcessState`);
- inspect or logs calls return fallback state (`unknown` or error text in caller logs).
- `dispatch` failures from container start return structured errors, for example:
  - `failed to create container`
  - `failed to create context dir`
  - `write <artifact>: <err>`

Cleanup recommendation:

- when container handles are stuck, run periodic `CleanDeadSessions()` and periodic removal of stale `TMPDIR/chum-ctx-*` directories.

## Operational verification

### Health checks

- `docker ps -a --filter name=chum-agent-` should show known session container names;
- `docker logs <session_name>` should return run output for recent sessions;
- `docker rm -f <session_name>` for manual recovery.

### Command checks

- `docker ps --format '{{.Names}}' | rg '^chum-agent-'`
- `docker ps -a --filter name=chum-agent- --filter status=exited`
- `go test ./internal/dispatch -run Docker -count=1` (as far as available test coverage permits)

## Runtime wiring references

- `internal/dispatch/docker.go` (implementation)
- `internal/dispatch/backend.go` (`Backend` and legacy interface definitions)
- `internal/config/validate.go` (`dispatch.routing` accepts `headless_cli` and `openclaw` only)
- `docs/architecture/CONFIG.md` (`[dispatch.routing]` section)
