# Beads Fork Scaffold (Local-Only)

This scaffold lets CHUM evaluate a fork-first Beads strategy without changing current execution paths.

## What was added

- `internal/beadsfork/client.go`
  - Isolated `bd` wrapper with fixed local safety flags:
    - `--no-daemon`
    - `--no-auto-import`
    - `--no-auto-flush`
  - Pinned version check (`DefaultPinnedVersion = 0.56.1`)
  - Scoped methods for:
    - `create`
    - `list`
    - `show`
    - `update`
    - `dep add`
    - `ready`
    - `blocked`
    - `sync --flush-only`

- `internal/beadsfork/client_test.go`
  - Unit tests for command construction, version pin check, and mixed-output JSON parsing.

- `internal/beadsfork/contract_test.go`
  - Opt-in real `bd` contract flow in a temp git repo.
  - Disabled by default; enable with `CHUM_BD_CONTRACT=1`.

- `scripts/dev/beads-fork-smoke.sh`
  - Runs scaffold tests.

- `scripts/dev/temporal-local.sh`
  - Starts/stops an isolated Temporal Docker container on `127.0.0.1:8233`.

## Run it

### 1) Unit tests (default)

```bash
scripts/dev/beads-fork-smoke.sh
```

### 2) Real `bd` contract tests (opt-in)

```bash
CHUM_BD_CONTRACT=1 scripts/dev/beads-fork-smoke.sh
```

Optional strict pin in contract mode:

```bash
CHUM_BD_CONTRACT=1 CHUM_BD_PINNED_VERSION=0.56.1 scripts/dev/beads-fork-smoke.sh
```

### 3) Enable in CHUM main (flag-gated)

The integration is disabled by default. Enable it explicitly:

```bash
go run ./cmd/chum \
  --config chum.toml \
  --enable-beads-fork \
  --beads-fork-workdir "$(pwd)" \
  --beads-fork-pinned-version 0.56.1
```

Optional binary override:

```bash
--beads-fork-binary /path/to/bd
```

## Isolated Temporal (optional)

Start:

```bash
scripts/dev/temporal-local.sh start
```

Set env for CHUM shell:

```bash
eval "$(scripts/dev/temporal-local.sh env)"
```

Then set your CHUM config to use that host value in `[general].temporal_host_port`
(for example, in a local copy passed via `--config`).

Status/logs/stop:

```bash
scripts/dev/temporal-local.sh status
scripts/dev/temporal-local.sh logs
scripts/dev/temporal-local.sh stop
```

## Notes

- Runtime integration is now available but strictly flag-gated (`--enable-beads-fork`).
- It is a local evaluation harness for deciding fork-vs-native planning strategy.
