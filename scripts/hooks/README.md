# Git Hooks for CHUM

## Installation

Run the install script to install or refresh local hooks:

```bash
./scripts/hooks/install.sh
```

## Hooks

### pre-commit
Prevents direct commits to `master`/`main` and enforces approved branch naming.

**Rationale:** All work must happen on a dedicated branch to:
- Prevent breaking the primary branch
- Enable review isolation
- Keep history clean and reversible

**Bypass (emergencies only):**
```bash
export CHUM_ALLOW_MASTER_HOTFIX=1
git commit --no-verify
```

### pre-push
Blocks pushes from branch names that would fail CI branch workflow policy.

Allowed source branch patterns:
- `feature/*`
- `chore/*`
- `fix/*`
- `refactor/*`
- `hotfix/*` (approved production hotfixes only)

This ensures branch naming violations are caught before opening/updating PRs.

### stop-checks.sh
Use this as an agent **Stop hook** (Claude Code, Codex, etc.) to keep sessions green.

Behavior:
- If the git worktree is clean, it exits immediately.
- If the worktree is dirty and checks for the same tree state are older than the freshness window (default 60s), it runs checks.
- If checks pass, it updates `$(git rev-parse --git-dir)/chum-stop-hook.state`.
- If checks fail, it exits non-zero so the agent must fix issues before finishing.

Default checks:
- `go build ./...`
- `go vet ./...`
- `go test ./...`

Run manually:

```bash
./scripts/hooks/stop-checks.sh
```

Customize:
- `CHUM_STOP_HOOK_FRESHNESS_SEC` (default `60`)
- `CHUM_STOP_HOOK_STATE_FILE` (default `$(git rev-parse --git-dir)/chum-stop-hook.state`)
- `CHUM_STOP_HOOK_CHECKS` (newline-separated commands)

Example custom checks:

```bash
export CHUM_STOP_HOOK_CHECKS=$'go test ./internal/temporal/...\ngolangci-lint run ./internal/temporal/...'
./scripts/hooks/stop-checks.sh
```

## Branch Workflow

Before starting work:

```bash
# 1. Start from clean master
git checkout master
git pull --rebase

# 2. Create a branch
git checkout -b feature/your-feature-name
# or:
git checkout -b chore/cleanup-old-jobs
git checkout -b fix/repro-fix
git checkout -b refactor/scheduler-loop
```

Allowed branch naming for standard work:
- `feature/*` - New features
- `chore/*` - Maintenance tasks
- `fix/*` - Bug fixes
- `refactor/*` - Code refactoring

Hotfix handling:
- `hotfix/*` is allowed only for approved production hotfixes.
- If blocked by this hook during approved hotfix flow, use `CHUM_ALLOW_MASTER_HOTFIX=1`.

## Worktree Setup

For parallel work, use `git worktree`:

```bash
git worktree add ../chum-feature feature/your-feature-name
cd ../chum-feature
```

When done:

```bash
git worktree remove ../chum-feature
```
