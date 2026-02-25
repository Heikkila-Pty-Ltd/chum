---
title: "Calcified script fast-path dispatch (LATM Functional Cache)"
status: open
priority: 0
type: task
labels:
  - whale:crystallization
  - cost-optimization
  - strategic-deferred
estimate_minutes: 120
acceptance_criteria: |
  - ChumAgentWorkflow checks for an active calcified script BEFORE invoking LLM planning.
  - If a matching active script exists for the morsel type, run it directly in the worktree.
  - If the script's output passes DoD verification, the workflow completes with zero LLM tokens.
  - If the script fails DoD, fall through to normal LLM planning path (no regression).
  - New activity `TryCalcifiedScriptActivity` created and registered.
  - Notification emitted when a calcified fast-path is used: "calcified_hit".
  - Existing calcification and dispatch tests still pass.
design: |
  **Context:** CHUM already generates `.shadow` scripts via `CalcifyPatternActivity` when a morsel
  type succeeds N consecutive times. These scripts are stored in the `calcified_scripts` table
  with status `shadow` or `active`. However, the dispatcher NEVER checks for active scripts before
  invoking the full LLM pipeline. This is the LATM "Functional Cache" gap — we're paying LLM tokens
  for patterns we've already solved deterministically.

  **Academic reference:** LATM (Large Language Models as Tool Makers) — the "Functional Cache"
  concept. See docs/chum-vs-sota.md and docs/deep-research.md Section 5.

  **Approach:**
  1. New activity `TryCalcifiedScriptActivity(ctx, req TaskRequest) (CalcifiedRunResult, error)`:
     - Derive morsel type from `req.TaskID` (reuse logic from CalcifyPatternActivity)
     - Call `store.GetActiveScriptForType(morselType)`
     - If no match: return `{Used: false}`
     - If match: `exec.Command` the script in `req.WorkDir`, capture stdout/stderr/exitcode
     - Return `{Used: true, ExitCode, Output, ScriptPath}`
  2. Add fast-path check at top of `ChumAgentWorkflow`, AFTER worktree setup, BEFORE planning:
     - If `calcifiedResult.Used && calcifiedResult.ExitCode == 0`: run DoD checks
     - If DoD passes: close task, record outcome with zero tokens, notify "calcified_hit"
     - If DoD fails: fall through to normal LLM path, add script failure to PreviousErrors
  3. Add `CalcifiedRunResult` struct to types.go.
---

# Calcified Script Fast-Path Dispatch

Skip the LLM entirely when a calcified active script exists for the morsel type.
The stochastic→deterministic migration — we fire the LLM.

This is the LATM "Functional Cache" pattern: the expensive tool-maker phase (LLM generating
code) has already been paid for. Now the cheap tool-user phase (running the script) should
be the default path for known patterns.

**Trade metric:** Track `calcified_hit` vs `calcified_miss` ratio per project. Target: 100%
of calcified species should use the fast-path.
