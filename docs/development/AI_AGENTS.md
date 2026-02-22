# Agent Instructions

This project uses **bd** (morsels) for issue tracking. Run `bd onboard` to get started.

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
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --status in_progress  # Claim work
bd close <id>         # Complete work
bd sync               # Sync with git
scripts/test-safe.sh ./internal/temporal/...  # Locked + timeout + JSON go test
```

## Landing the Plane (Session Completion)

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
  ```bash
  # Use locked test wrapper to avoid cross-agent test contention
  TEST_SAFE_LOCK_WAIT_SEC=600 scripts/test-safe.sh ./...
  ```
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   bd sync
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


<!-- bv-agent-instructions-v1 -->

---

## Morsels Workflow Integration

This project uses [beads_viewer](https://github.com/Dicklesworthstone/beads_viewer) for issue tracking. Issues are stored in `.morsels/` and tracked in git.

### Essential Commands

```bash
# View issues (launches TUI - avoid in automated sessions)
bv

# CLI commands for agents (use these instead)
bd ready              # Show issues ready to work (no blockers)
bd list --status=open # All open issues
bd show <id>          # Full issue details with dependencies
bd create --title="..." --type=task --priority=2
bd update <id> --status=in_progress
bd close <id> --reason="Completed"
bd close <id1> <id2>  # Close multiple issues at once
bd sync               # Commit and push changes
```

### Workflow Pattern

1. **Start**: Run `bd ready` to find actionable work
2. **Claim**: Use `bd update <id> --status=in_progress`
3. **Work**: Implement the task
4. **Complete**: Use `bd close <id>`
5. **Sync**: Always run `bd sync` at session end

### Key Concepts

- **Dependencies**: Issues can block other issues. `bd ready` shows only unblocked work.
- **Priority**: P0=critical, P1=high, P2=medium, P3=low, P4=backlog (use numbers, not words)
- **Types**: task, bug, feature, epic, question, docs
- **Blocking**: `bd dep add <issue> <depends-on>` to add dependencies

### Session Protocol

**Before ending any session, run this checklist:**

```bash
git status              # Check what changed
scripts/test-safe.sh ./...  # Run tests with lock/timeout/json output
git add <files>         # Stage code changes
bd sync                 # Commit morsels changes
git commit -m "..."     # Commit code
bd sync                 # Commit any new morsels changes
git push                # Push to remote
```

### Test Contention Guardrail

Use `scripts/test-safe.sh` instead of raw `go test` in shared workspaces.

- Uses `flock` lock file: `.tmp/go-test.lock`
- Uses bounded `go test -timeout` (default `10m`)
- Emits `go test -json` for machine-readable logs
- Optional env overrides:
  - `TEST_SAFE_LOCK_WAIT_SEC` (default: `600`)
  - `TEST_SAFE_GO_TEST_TIMEOUT=15m`
  - `TEST_SAFE_JSON_OUT=.tmp/test-$(date +%s).jsonl`

If lock contention blocks a run, wait for the owning process to finish, then retry. Use a longer lock window on shared machines instead of rerunning immediately:
```bash
TEST_SAFE_LOCK_WAIT_SEC=600 scripts/test-safe.sh ./internal/temporal ./internal/coordination
```

### Best Practices

- Check `bd ready` at session start to find available work
- Update status as you work (in_progress → closed)
- Create new issues with `bd create` when you discover tasks
- Use descriptive titles and set appropriate priority/type
- Always `bd sync` before ending session

### OpenClaw Main: Creating Morsels (Required Format)

For `open`/`in_progress` morsels, CHUM dispatch expects proper structure. Missing fields will block assignment.

- Required before work can dispatch:
  - Clear scope in `description`
  - `acceptance_criteria` with explicit test requirement
  - `acceptance_criteria` with explicit DoD requirement
  - Positive estimate in minutes (`--estimate > 0`)

Use this pattern:

```bash
# 1) Create scoped morsel
bd create \
  --type task \
  --priority 2 \
  --title "Implement X in Y" \
  --description "Goal, scope boundaries, touched files/components, and dependency context."

# 2) Add execution gates (AC + estimate)
bd update <id> \
  --acceptance "- Behavior/outcome is observable and testable.
- Add/update tests covering changed behavior; targeted test suite passes.
- DoD: closure notes include verification evidence, risk/rollback notes, and follow-ups." \
  --estimate 90

# 3) Wire dependencies/parent where relevant
bd dep add <id> <depends-on-id>
# or
bd update <id> --parent <parent-id>

# 4) Verify morsel shape before leaving it open
bd show <id>
bd show <id>  # verify task shape
```

Sizing guidance (minutes):
- Small fix/docs: `30-60`
- Typical task/bug: `60-120`
- Large feature slice: `120-240` (prefer splitting instead of bigger estimates)

Definition of Ready checklist:
- Unambiguous scope and non-goals
- Concrete acceptance criteria (not vague outcomes)
- Test plan implied by acceptance criteria
- DoD clause present
- Estimate set
- Dependencies declared

### Epic Breakdown Completion Checklist

**When completing epic breakdown tasks (like "Auto: break down epic X"):**

1. ✅ **Break down the epic** - Create concrete sub-tasks with acceptance criteria
2. ✅ **Close the epic** - Use `bd close <epic-id> --reason="Epic completed - broken down into N tasks"`
3. ✅ **Close the breakdown task** - Use `bd close <task-id> --reason="Epic breakdown completed successfully"`
4. ✅ **Verify both are closed** - Run `bd show <epic-id>` and `bd show <task-id>` to confirm

**CRITICAL**: Both the epic AND the breakdown task must be closed, or the system will churn trying to re-work completed tasks.

**Example workflow:**
```bash
# 1. Create subtasks for the epic
bd create --title="Implement feature X" --parent=epic-123

# 2. Close the epic when all subtasks created
bd close epic-123 --reason="Epic completed - broken down into 5 executable tasks"

# 3. Close the breakdown task that requested this work
bd close breakdown-task-456 --reason="Epic breakdown completed successfully"

# 4. Verify both are closed
bd show epic-123      # Should show CLOSED
bd show breakdown-task-456  # Should show CLOSED
```

<!-- end-bv-agent-instructions -->
