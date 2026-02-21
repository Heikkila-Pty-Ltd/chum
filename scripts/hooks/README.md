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
