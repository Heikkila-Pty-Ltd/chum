# CHUM Release Workflow

This document defines the operational release process for CHUM.

## Scope

Applies to standard releases, patch releases, and emergency hotfixes.

## Roles And Access

- Release manager: drives checklist completion and sign-off.
- Reviewer: confirms quality gates and rollback readiness.
- Operator: performs deployment and post-release verification.

Required access:

- Write access to repository and tags.
- Access to deployment host/service controls.
- Access to CHUM API and state DB backup paths.

## Release Phases

## 1. Pre-Release

### 1.1 Freeze and branch state

Acceptance criteria:

- Target branch is up to date with `origin/main`.
- No unresolved high-severity incidents.

Validation commands:

```bash
git fetch origin
git status
git log --oneline --decorate -n 10
```

### 1.2 Build and test gates

Acceptance criteria:

- Build succeeds.
- Core test suites pass.

Validation commands:

```bash
make build
GOCACHE=/tmp/go-build go test ./internal/graph ./internal/health ./internal/api
```

### 1.3 Security gate

Acceptance criteria:

- API control endpoint authn/authz enabled in deployment topology.
- Security config validates.

Validation commands:

```bash
rg -n "\[api.security\]|enabled|allowed_tokens|require_local_only" chum.toml
GOCACHE=/tmp/go-build go test ./internal/config ./internal/api -run Auth
```

### 1.4 Changelog gate

Acceptance criteria:

- Changelog draft prepared from merged work.
- Notable fixes and operational changes called out.

Validation commands:

```bash
git log --oneline --no-merges <last-tag>..HEAD
```

## 2. Release Execution

### 2.1 Version and tag

Acceptance criteria:

- Release version is chosen and unique.
- Annotated tag exists locally.

Validation commands:

```bash
VERSION=vX.Y.Z
git tag -a "$VERSION" -m "CHUM $VERSION"
git show "$VERSION" --no-patch
```

### 2.2 Artifact and config readiness

Acceptance criteria:

- Build artifact exists for release commit.
- Rollback assets prepared.

Validation commands:

```bash
make build
cp build/chum build/chum.rollback
cp chum.toml chum.toml.rollback
ls -la build/chum.rollback chum.toml.rollback
```

### 2.3 Dry run

Acceptance criteria:

- Release flow executed in non-production mode.
- CHUM starts, passes health check, and shuts down cleanly.

Validation commands:

```bash
scripts/release/dry-run-release.sh
```

## 3. Post-Release Verification

### 3.1 Service and API checks

Acceptance criteria:

- API responds.
- Scheduler status is healthy.

Validation commands:

```bash
curl -s http://127.0.0.1:8900/status
curl -s http://127.0.0.1:8900/health
curl -s http://127.0.0.1:8900/metrics
```

### 3.2 Operational readiness checks

Acceptance criteria:

- Pause/resume control path works.
- Backup/restore artifacts are current.

Validation commands:

```bash
curl -s -X POST http://127.0.0.1:8900/scheduler/pause
curl -s -X POST http://127.0.0.1:8900/scheduler/resume
ls -la artifacts/launch/runbooks
```

## 4. Emergency Procedures

### 4.1 Hotfix flow

Acceptance criteria:

- Hotfix branch created from production baseline.
- Minimum gate set passes (build, targeted tests, rollback prepared).

Validation commands:

```bash
git checkout -b hotfix/<ticket> <release-tag>
make build
GOCACHE=/tmp/go-build go test ./internal/api ./internal/scheduler
./scripts/prepare-rollback-assets.sh
```

### 4.2 Rollback execution

Acceptance criteria:

- Known-good binary/config available.
- Rollback steps executed and verified.

Validation commands:

```bash
# Stop the running instance
systemctl stop chum

# Swap in the known-good binary and config
cp build/chum.rollback build/chum
cp chum.toml.rollback chum.toml

# Restart and verify
systemctl start chum
curl -s http://127.0.0.1:8900/health
curl -s http://127.0.0.1:8900/status
```

## Quality Gates Summary

- Build gate: pass
- Test gate: pass
- Security gate: pass
- Changelog gate: pass
- Dry-run gate: pass
- Rollback readiness gate: pass
- Post-release verification gate: pass

If any gate fails, stop release and execute rollback/hotfix decision protocol.

## Required Evidence Artifacts

- Dry-run output from `scripts/release/dry-run-release.sh`
- Changelog from `scripts/release/generate-changelog.sh`
- Rollback binary and config backups (`build/chum.rollback`, `chum.toml.rollback`)
- Post-release health check outputs

## Release Scripts

All scripts live in `scripts/release/`:

| Script | Purpose |
|--------|---------|
| `bump-version.sh` | Increment VERSION file |
| `create-release-tag.sh` | Create annotated git tag |
| `dry-run-release.sh` | Full pre-release validation |
| `generate-changelog.sh` | Generate changelog from git log |
| `validate-release.sh` | Post-release validation checks |

## Related Documents

- [api-security.md](../api/api-security.md)
- [CONFIG.md](../architecture/CONFIG.md)
- [CONTRIBUTING.md](../../CONTRIBUTING.md)
