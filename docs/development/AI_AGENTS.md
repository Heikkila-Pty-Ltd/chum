# Agent Instructions

This project uses **CHUM** for automated task dispatch. Tasks (morsels) live in the SQLite DAG (`tasks` table) and are managed by the CHUM Temporal workflow engine. **Do NOT run `bd` commands** — the beads CLI has been removed.

## Branch and Worktree Onboarding

Before coding in CHUM, enforce the branch workflow:

1. Install the local hook: `./scripts/hooks/install.sh`
2. Start from clean `master`, then create one of: `feature/*`, `chore/*`, `fix/*`, `refactor/*`
3. Optionally create a worktree: `git worktree add -b feature/your-feature ../chum-feature`
4. Run the worktree training checkpoint in `docs/development/GIT_WORKTREE_WORKFLOW.md`

For all code changes, keep PRs on branches only (never direct `master` commits).

## Quick Reference

```bash
# CHUM status
curl -s http://localhost:8900/health
curl -s http://localhost:8900/status

# List available tasks
curl -s http://localhost:8900/tasks?project=chum&status=ready

# View a specific task
curl -s http://localhost:8900/tasks/<task-id>

# Create a new task (into the DAG)
curl -s -X POST http://localhost:8900/tasks \
  -H 'Content-Type: application/json' \
  -d '{"title":"Implement X","project":"chum","priority":2,"type":"task","estimate_minutes":90,"description":"Goal and scope","acceptance_criteria":"Tests pass, DoD met"}'

# Quality gates
scripts/test-safe.sh ./internal/temporal/...  # Locked + timeout + JSON go test
```

## Task Lifecycle

Tasks flow through the CHUM DAG, not through markdown files:

```
POST /tasks (open) → crabs (groom/validate) → ready → dispatcher → agent → learner
```

### Creating Tasks via API

```bash
curl -s -X POST http://localhost:8900/tasks \
  -H 'Content-Type: application/json' \
  -d '{
    "title": "Implement feature X in module Y",
    "project": "chum",
    "priority": 2,
    "type": "task",
    "estimate_minutes": 90,
    "description": "Goal, scope boundaries, touched files/components.",
    "acceptance_criteria": "- Behavior is observable and testable\n- Tests pass\n- DoD: closure notes include verification evidence",
    "depends_on": ["other-task-id"],
    "labels": ["backend", "temporal"]
  }'
```

**Required fields**: `title`, `project`  
**Defaults**: `status=open` (crabs groom to `ready`), `type=task`

### Viewing Tasks

```bash
# All ready tasks for a project
curl -s http://localhost:8900/tasks?project=chum&status=ready

# Specific task details
curl -s http://localhost:8900/tasks/<task-id>
```

### Key Concepts

- **Priority**: P0=critical, P1=high, P2=medium, P3=low, P4=backlog
- **Types**: task, bug, feature, epic, question, docs
- **Status lifecycle**: `open` → crabs groom/validate → `ready` → CHUM dispatches → `done`
- **Dependencies**: Use `depends_on` array when creating tasks. CHUM will not dispatch until dependencies are met.

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

## Landing the Plane (Session Completion)

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** — Create tasks via `POST /tasks` for anything that needs follow-up
2. **Run quality gates** (if code changed):
   ```bash
   TEST_SAFE_LOCK_WAIT_SEC=600 scripts/test-safe.sh ./...
   ```
3. **PUSH TO REMOTE** — This is MANDATORY:
   ```bash
   git pull --rebase
   git add <changed files>
   git commit -m "..."
   git push
   git status  # MUST show "up to date with origin"
   ```
4. **Verify** — All changes committed AND pushed
5. **Hand off** — Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing — that leaves work stranded locally
- NEVER say "ready to push when you are" — YOU must push
- If push fails, resolve and retry until it succeeds

## Test Contention Guardrail

Use `scripts/test-safe.sh` instead of raw `go test` in shared workspaces.

- Uses `flock` lock file: `.tmp/go-test.lock`
- Uses bounded `go test -timeout` (default `10m`)
- Emits `go test -json` for machine-readable logs
- Optional env overrides:
  - `TEST_SAFE_LOCK_WAIT_SEC` (default: `600`)
  - `TEST_SAFE_GO_TEST_TIMEOUT=15m`

## Note on `.morsels/` Directory

The `.morsels/` directory contains markdown files that serve as a **bootstrap/seeding** mechanism only. CHUM's groomer ingests these into the DAG on startup. For ongoing task management, always use the CHUM API (`POST /tasks`). Do not create `.morsels/` files as the primary way to track work.
