---
title: "Add pre-crab blast radius scanner"
status: done
priority: 2
type: feature
labels:
  - whale:infrastructure
  - ai
estimate_minutes: 60
acceptance_criteria: |
  - BlastRadiusScanActivity runs before CrabDecompositionWorkflow.
  - Scanner outputs: affected files, dependency graph, import chains, dead code, test coverage.
  - Output is injected into the crab's decomposition prompt as structured context.
  - Language-aware: uses Go tools for Go projects, JS/TS tools for Node projects.
  - All tools run as CLI subprocesses — no library dependencies in the CHUM binary.
design: |
  **Tier 1 — JS/TS projects (npx-able, zero config):**
  - `madge` — dependency graph, circular dep detection, orphan detection
    - `npx madge --json ./app` → JSON dep graph
    - `npx madge --circular ./app` → circular deps
    - `npx madge --orphans ./app` → dead code
  - `knip` — finds unused files, dependencies, exports, types
    - `npx knip --reporter json` → unused code report
  - `depcheck` — unused package.json dependencies
    - `npx depcheck --json` → unused deps
  
  **Tier 2 — Go projects:**
  - `go-callvis` — function-level call graphs
    - `go-callvis -format json ./cmd/chum` → call graph
  - `go list -m -json all` — module dependency tree (built-in)
  - `go vet ./...` — static analysis (built-in)
  
  **Tier 3 — Universal (any language):**
  - `tree-sitter` CLI — language-agnostic AST parsing for symbol extraction
  - Aider-style repomap — tree-sitter + graph ranking to find most relevant
    files for a given context. Port the approach from github.com/paul-gauthier/aider
    into a Go helper that builds ranked file graphs.
  
  **Integration:**
  1. Add `BlastRadiusScanActivity` that detects project language (Go vs TS).
  2. Runs the appropriate tier of tools.
  3. Aggregates output into a structured `BlastRadiusReport`.
  4. CrabDecompositionWorkflow receives this report alongside the repo map.
  5. Crabs use it to produce tighter decompositions with exact file lists.
depends_on: []
---

Pre-crab blast radius scanning using madge, knip, go-callvis, tree-sitter,
and aider-style repomap ranking. Language-aware, deterministic, cheap.
