---
title: "Skill Honing — auto-promote shadow scripts to active via varied testing"
status: open
priority: 3
type: task
labels:
  - whale:crystallization
  - reliability
  - strategic-deferred
estimate_minutes: 120
acceptance_criteria: |
  - New `HoneCalcifiedScriptActivity` generates 3-5 varied test inputs for shadow scripts.
  - Shadow scripts are run against each test input in isolated worktrees.
  - If all test runs pass DoD: shadow script auto-promoted to active status.
  - If any test run fails: failure logged, script remains shadow with failure notes.
  - Integration with calcified fast-path (td26): promoted active scripts are used automatically.
design: |
  **Context:** SkillWeaver's "Skill Honing" phase — after synthesizing a skill API, it generates
  varied test cases and runs them against the live environment. Only APIs that survive are committed.
  CHUM's calcified shadow scripts are currently tested once (when created) but never stress-tested
  before promotion.

  **Academic reference:** SkillWeaver Skill Honing.
  See docs/chum-vs-sota.md Gap 4 and docs/deep-research.md Section 7.2.

  **Approach:**
  1. New `HoneCalcifiedScriptActivity`: for each shadow script, use fast LLM to generate
     3-5 plausible morsel inputs (varied file paths, project contexts, edge cases).
  2. Run shadow script in isolated worktree for each input.
  3. Run DoD checks after each execution.
  4. Score: if all pass → auto-promote to active. If any fail → log and keep as shadow.
  5. Run this on the Paleontologist schedule, after consolidation (td27).
depends_on: ["chum-td26-calcified-fastpath", "chum-td27-sleep-phase"]
---

# Skill Honing

SkillWeaver-inspired stress-testing for calcified shadow scripts. Auto-promote scripts
that survive varied inputs. The Darwin test: only the fittest scripts earn active status.
