---
title: "Add pre-crab blast radius scanner"
status: ready
priority: 2
type: feature
labels:
  - whale:infrastructure
  - ai
estimate_minutes: 45
acceptance_criteria: |
  - Before CrabDecompositionWorkflow runs, a BlastRadiusScanActivity analyzes the target morsel.
  - Scanner identifies: affected files, function call graph, import/dependency chains, test coverage.
  - Output is passed to the crab's decomposition prompt as structured context.
  - Uses tree-sitter or AST parsing (not LLM) for deterministic, cheap analysis.
design: |
  **Tools to evaluate:**
  - `madge` (npm) — JS/TS dependency graphs, circular dependency detection
  - `go-callvis` — Go function call graphs
  - `tree-sitter` CLI — language-agnostic AST parsing
  - `depcheck` — unused dependency detection
  - Built-in: `go list -m -json all`, `tsc --listFiles`
  
  **Approach:**
  1. Add `BlastRadiusScanActivity` that runs before crab decomposition.
  2. Parse the morsel's acceptance_criteria and design fields to identify target files/packages.
  3. Run the appropriate scanner (Go or TS based on project language).
  4. Output: file list, dependency graph, estimated blast radius (LOC affected).
  5. Inject this into the crab's prompt alongside the repo map.
  
  **Benefit:** Crabs produce tighter, more accurate decompositions because they know
  exactly which files and functions are involved, not just package names.
depends_on: []
---

Pre-crab blast radius scanning. Use static analysis tools (tree-sitter, madge, go-callvis)
to map affected files and dependencies before the crabs decompose a morsel.
