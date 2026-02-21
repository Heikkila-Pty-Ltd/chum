# Git Worktree Workflow

> Branch-per-feature development with worktree isolation. Each agent or human developer works in a dedicated worktree to prevent commit cross-talk and merge conflicts.

---

## Quick Start

### 1. Create a Feature Branch + Worktree

```bash
cd /path/to/chum                    # canonical workspace
git checkout master
git pull --rebase
git worktree add -b feature/your-feature ../chum-feature-your-feature master
```

### 2. Install Branch Guards

```bash
./scripts/hooks/install.sh
```

This installs pre-commit hooks that:
- Block direct commits to `master`
- Validate branch naming conventions
- Run pre-push linting

### 3. Work in Isolation

```bash
cd ../chum-feature-your-feature

# Normal development cycle
git add .
git commit -m "feat(scope): describe change"
git push -u origin feature/your-feature
```

### 4. Clean Up

```bash
cd /path/to/chum
git worktree remove ../chum-feature-your-feature
git branch -d feature/your-feature     # after merge
```

---

## CHUM Agent Worktree Management

When `use_branches = true` is configured, the scheduler automatically creates worktrees for each dispatched bead:

```
/home/user/projects/
├── chum/                              # canonical workspace (master)
├── chum-chum-bvnv/                  # auto-created for bead chum-bvnv
├── chum-chum-o4ni/                  # auto-created for bead chum-o4ni
└── chum-chum-w7dk/                  # auto-created for bead chum-w7dk
```

**Branch naming convention:** `{branch_prefix}{bead-id}` (e.g., `feat/chum-bvnv`).

**Lifecycle:**
1. Scheduler creates branch from `base_branch`
2. Worktree is added at `../chum-{project}-{bead-id}`
3. Agent is dispatched into the worktree
4. On completion, PR is created via `merge_method`
5. Post-merge checks run and worktree is cleaned up
6. Stale branches are pruned after `branch_cleanup_days`

---

## Branch Naming Convention

| Pattern | Use |
|---------|-----|
| `feat/{scope}` | Feature work |
| `fix/{scope}` | Bug fixes |
| `chore/{scope}` | Maintenance, docs, CI |
| `chum/{bead-id}` | Auto-created by scheduler |

---

## Parallel Work Rules

- **One worktree per active feature.** Never share a worktree between tasks.
- **Never commit from canonical workspace** while worktrees exist for the same branch.
- **Max concurrent worktrees** is governed by `dispatch.git.max_concurrent_per_project` (default: 3).
- **Merge conflicts** are handled by the scheduler — if a merge conflict is detected, the dispatch is failed and the bead is re-queued.

---

## Troubleshooting

### Locked Worktree

```bash
# If a worktree is locked (e.g., after a crash)
git worktree unlock ../chum-feature-your-feature
git worktree remove ../chum-feature-your-feature
```

### Stale Worktree References

```bash
# Prune references to removed worktrees
git worktree prune
```

### Branch Already Exists

```bash
# If the branch from a previous dispatch wasn't cleaned up
git branch -D chum/chum-bvnv
git worktree add -b chum/chum-bvnv ../chum-chum-bvnv master
```

### Pre-Commit Hook Not Installed

```bash
# Verify hook is installed
ls -la .git/hooks/pre-commit

# Re-install
./scripts/hooks/install.sh
```

---

## Configuration

```toml
[projects.chum]
base_branch   = "master"
branch_prefix = "feat/"
use_branches  = true
merge_method  = "squash"          # "squash", "merge", "rebase"
auto_revert_on_failure = true     # revert merge if post-merge checks fail
post_merge_checks = ["go build ./...", "go test ./..."]

[dispatch.git]
branch_prefix              = "chum/"
branch_cleanup_days        = 7
merge_strategy             = "squash"
max_concurrent_per_project = 3
```

---

## Team Onboarding Checklist

- [ ] Open one feature branch
- [ ] Open one isolated worktree for it
- [ ] Verify pre-commit hook is installed: `./scripts/hooks/install.sh`
- [ ] Attempt one commit without pushing — confirm branch name checks pass
- [ ] Confirm that committing to `master` is blocked by pre-commit
- [ ] Open a draft PR and confirm CI shows the branch policy check

**Related docs:**
- [CONTRIBUTING.md](../../CONTRIBUTING.md)
- [CONFIG.md](../architecture/CONFIG.md)
