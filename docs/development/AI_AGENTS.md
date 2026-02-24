# Agent Instructions

This project uses **CHUM** for automated task dispatch. Tasks (morsels) live in `.morsels/` as markdown files and are managed by the CHUM Temporal workflow engine. **Do NOT run `bd` commands** — the beads database has been replaced by CHUM's SQLite store.

## Branch and Worktree Onboarding

Before coding in CHUM, enforce the branch workflow:

1. Install the local hook:
   - `./scripts/hooks/install.sh`
2. Start from clean `master`, then create one of:
   - `feature/*`, `chore/*`, `fix/*`, `refactor/*`
3. Optionally create a worktree when running multiple tasks:
   - `git worktree add -b feature/your-feature ../chum-feature`
4. Run the worktree training checkpoint in:
   - `docs/development/GIT_WORKTREE_WORKFLOW.md`

Team training checkpoint:

- Confirm hook installation:
  - `./scripts/hooks/install.sh`
- Confirm branch guard behavior:
  - Create and switch to `feature/*`, `chore/*`, `fix/*`, or `refactor/*` before first commit.
- Confirm PR review enforcement:
  - Open a draft PR and verify workflow check runs in CI.

For all code changes, keep PRs on branches only (never direct `master` commits), and include reviewable commits before finishing a morsel.

## Quick Reference

```bash
# View available morsels
ls .morsels/                          # List all morsel files
cat .morsels/<morsel-id>.md           # View morsel details

# CHUM API (if running)
curl http://localhost:8900/health     # Check CHUM status
curl http://localhost:8900/api/tasks  # List tasks

# Quality gates
scripts/test-safe.sh ./internal/temporal/...  # Locked + timeout + JSON go test
```

## Landing the Plane (Session Completion)

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create `.morsels/<id>.md` files for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
  ```bash
  # Use locked test wrapper to avoid cross-agent test contention
  TEST_SAFE_LOCK_WAIT_SEC=600 scripts/test-safe.sh ./...
  ```
3. **Update morsel status** - Mark finished morsels as `done` in their `.md` files
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   git add .morsels/ <changed files>
   git commit -m "..."
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds

---

## Morsels Workflow Integration

This project uses `.morsels/` directory for task tracking. Morsels are markdown files managed by CHUM's automated dispatch pipeline.

### Creating Morsels

Create a `.morsels/<morsel-id>.md` file with this structure:

```markdown
---
title: "Implement X in Y"
status: ready
priority: 2
type: task
estimate: 90
---

## Description

Goal, scope boundaries, touched files/components, and dependency context.

## Acceptance Criteria

- Behavior/outcome is observable and testable
- Add/update tests covering changed behavior; targeted test suite passes
- DoD: closure notes include verification evidence
```

### Key Concepts

- **Priority**: P0=critical, P1=high, P2=medium, P3=low, P4=backlog
- **Types**: task, bug, feature, epic, question, docs
- **Status**: `ready` → CHUM dispatches automatically. `done` → completed.

### Test Contention Guardrail

Use `scripts/test-safe.sh` instead of raw `go test` in shared workspaces.

- Uses `flock` lock file: `.tmp/go-test.lock`
- Uses bounded `go test -timeout` (default `10m`)
- Emits `go test -json` for machine-readable logs
- Optional env overrides:
  - `TEST_SAFE_LOCK_WAIT_SEC` (default: `600`)
  - `TEST_SAFE_GO_TEST_TIMEOUT=15m`
  - `TEST_SAFE_JSON_OUT=.tmp/test-$(date +%s).jsonl`

If lock contention blocks a run, wait for the owning process to finish, then retry:
```bash
TEST_SAFE_LOCK_WAIT_SEC=600 scripts/test-safe.sh ./internal/temporal ./internal/coordination
```

### Session Protocol

```bash
git status                    # Check what changed
scripts/test-safe.sh ./...    # Run tests with lock/timeout/json output
git add <files> .morsels/     # Stage code + morsel changes
git commit -m "..."           # Commit
git push                      # Push to remote
```

### Sizing Guidance (minutes)

- Small fix/docs: `30-60`
- Typical task/bug: `60-120`
- Large feature slice: `120-240` (prefer splitting)

### Definition of Ready Checklist

- Unambiguous scope and non-goals
- Concrete acceptance criteria (not vague outcomes)
- Test plan implied by acceptance criteria
- DoD clause present
- Estimate set
- Dependencies declared

### Best Practices

- Check `.morsels/` at session start to understand available work
- Update morsel status as you work (`ready` → `in_progress` → `done`)
- Create new `.morsels/<id>.md` files when you discover tasks
- Use descriptive titles and set appropriate priority/type
- Always `git push` before ending session
