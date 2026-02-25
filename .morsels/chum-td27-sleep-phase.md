---
title: "Sleep Phase consolidation — tidal low-tide immune system pruning"
status: open
priority: 2
type: task
labels:
  - whale:crystallization
  - tech-debt
  - strategic-deferred
estimate_minutes: 180
acceptance_criteria: |
  - Paleontologist workflow includes a consolidation step after health checks.
  - Semgrep rules with >80% similarity are merged (LLM merge + semgrep --validate).
  - Lessons table has `hit_count` column; lessons with 0 hits after 30 days are pruned.
  - Genome antibodies with similar failure patterns are consolidated per-species.
  - Superseded shadow scripts are deprecated when an active script exists for the same type.
  - Consolidation activity logs: "Consolidated N rules into M", "Pruned N stale lessons".
  - No regression in existing Paleontologist/learner tests.
design: |
  **Context:** UCT framework's "Evolution Function" — an offline memory consolidation phase
  that prevents skill library bloat. CHUM accumulates Semgrep rules, lessons, genome antibodies,
  and shadow scripts indefinitely. Without pruning, retrieval accuracy degrades and the immune
  system becomes autoimmune (too many rules cause false positives, too many antibodies cause
  over-conservative dispatch decisions).

  **Academic reference:** UCT Sleep Phase / Evolution Function.
  See docs/chum-vs-sota.md Gap 1 and docs/deep-research.md Section 8.

  **Approach:**
  1. Add `hit_count INTEGER DEFAULT 0` to lessons table; increment on retrieval.
  2. New `ConsolidateSemgrepRulesActivity`: list .semgrep/*.yaml, compute pairwise Jaccard
     similarity on patterns, merge pairs >0.8 via fast LLM, validate with semgrep --validate.
  3. New `PruneStaleLessonsActivity`: delete insight-category lessons >14d with 0 hits,
     delete pattern-category >30d with 0 hits. Never prune rules/antipatterns.
  4. New `ConsolidateGenomeAntibodiesActivity`: group antibodies by failure string similarity,
     merge groups into single representative entries.
  5. Wire all three into PaleontologistWorkflow after existing health check step.

  **Tidal metaphor:** This runs during low-tide (daily Paleontologist schedule, 5am).
  Active sharks dispatch during high-tide. The immune system is pruned while the seas are calm.
depends_on: ["chum-td26-calcified-fastpath"]
---

# Sleep Phase Consolidation

Offline Evolution Function bolted onto the Paleontologist schedule. Prevents the immune system
from becoming autoimmune through unbounded accumulation of rules, lessons, and antibodies.

The tidal ebb: while sharks rest, the Paleontologist prunes the reef.
