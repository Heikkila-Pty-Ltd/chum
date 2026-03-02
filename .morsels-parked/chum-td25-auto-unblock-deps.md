---
title: "Auto-unblock morsels when all dependencies are done"
status: ready
priority: 0
type: task
labels:
  - whale:infrastructure
  - dispatch
  - critical-path
estimate_minutes: 45
acceptance_criteria: |
  - When a morsel is marked `done`, walk the DAG's `task_edges` to find
    all downstream tasks that depend on it.
  - For each downstream task with status `blocked`, check if ALL of its
    dependencies are now `done` or `closed`.
  - If yes, auto-promote the morsel from `blocked` → `ready` in both:
    (a) the DAG database
    (b) the morsel `.md` file on disk (update frontmatter `status: ready`)
  - Git commit the changed morsel files.
  - Log each auto-unblock event with the morsel ID and which dep triggered it.
  - Add test that creates a chain A→B→C, completes A, verifies B auto-unblocks,
    completes B, verifies C auto-unblocks.
design: |
  **Where to add it:**
  Two possible hooks:
  1. **After `MarkMorselDoneActivity` (chum-td23)** — when we auto-mark a morsel done,
     immediately scan downstream deps. This is the cleanest.
  2. **In `ScanCandidatesActivity`** — on each dispatcher tick, scan all blocked
     morsels and check if their deps are now done. Simpler but adds work to every tick.
  
  Recommend option 1 for efficiency: only check downstream deps when a morsel
  actually transitions to done, not on every tick.
  
  **Implementation:**
  - Add `DAG.GetDependents(ctx, taskID)` — reverse edge query:
    `SELECT from_task FROM task_edges WHERE to_task = ?`
  - Add `DAG.AutoUnblockDependents(ctx, completedTaskID)`:
    - Get dependents
    - For each dependent with status=blocked, check ALL its deps
    - If all deps are done/closed, update status to ready
  - Call from the success path after morsel is marked done
  
  **Morsel file update:**
  - Same mechanism as MarkMorselDoneActivity: read file, sed status, git commit
depends_on: ["chum-td23-auto-mark-done"]
---

When a shark catches and lands a morsel, walk the dependency graph and
auto-promote any downstream blocked morsels whose deps are all now done.
Currently this transition has to be done manually — the W4 pages were stuck
as blocked for days despite all their W3/W2 dependencies being complete.
