# Execution Option Catalog: Planning Ceremony Session

Date: 2026-02-25  
Scope: convert skills/tool calls/pathways into a queryable option graph used as pre-context for execution agents.

Raw CHUM-shaped session artifacts:

- [`docs/architecture/planning_sessions/2026-02-25-execution-option-catalog-simulated/README.md`](./planning_sessions/2026-02-25-execution-option-catalog-simulated/README.md)
- [`docs/architecture/planning_sessions/2026-02-25-execution-option-catalog-simulated/planning_trace_events.ndjson`](./planning_sessions/2026-02-25-execution-option-catalog-simulated/planning_trace_events.ndjson)
- [`docs/architecture/planning_sessions/2026-02-25-execution-option-catalog-simulated/planning_state_snapshots.ndjson`](./planning_sessions/2026-02-25-execution-option-catalog-simulated/planning_state_snapshots.ndjson)
- [`docs/architecture/planning_sessions/2026-02-25-execution-option-catalog-simulated/planning_candidate_scores.ndjson`](./planning_sessions/2026-02-25-execution-option-catalog-simulated/planning_candidate_scores.ndjson)
- [`docs/architecture/planning_sessions/2026-02-25-execution-option-catalog-simulated/structured_plan.json`](./planning_sessions/2026-02-25-execution-option-catalog-simulated/structured_plan.json)
- [`docs/architecture/planning_sessions/2026-02-25-execution-option-catalog-simulated/task_create_requests.json`](./planning_sessions/2026-02-25-execution-option-catalog-simulated/task_create_requests.json)
- [`docs/architecture/planning_sessions/2026-02-25-execution-option-catalog-simulated/posthoc_negative_markers.ndjson`](./planning_sessions/2026-02-25-execution-option-catalog-simulated/posthoc_negative_markers.ndjson)
- [`docs/architecture/planning_sessions/2026-02-25-execution-option-catalog-simulated/negative_import_attempt_trace.ndjson`](./planning_sessions/2026-02-25-execution-option-catalog-simulated/negative_import_attempt_trace.ndjson)
- [`docs/architecture/planning_sessions/2026-02-25-execution-option-catalog-simulated/canonical_import.sql`](./planning_sessions/2026-02-25-execution-option-catalog-simulated/canonical_import.sql)

## Objective

Run the architecture planning ceremony for this initiative with three concrete outputs:

1. research feasible implementation options
2. make explicit architecture decisions with rationale
3. emit a structured implementation plan with dependencies and gates

## Phase 1: Option Research

### Candidate A — Docs-Only Catalog

- Description: keep options as markdown/yaml docs only, no resolver.
- Strengths:
  - fastest to start
  - low implementation risk
- Weaknesses:
  - not directly queryable at runtime
  - no deterministic pre-context handoff
- Fit: good for ideation, weak for enforcement.

### Candidate B — YAML Catalog + Runtime Resolver (Recommended)

- Description: store atomic/composite options in versioned YAML, validate and resolve into execution bundles before dispatch.
- Strengths:
  - auditable and code-reviewable source-of-truth
  - deterministic handoff (step order, gates, allowed tools)
  - easy bootstrap from existing proteins/molecules
- Weaknesses:
  - requires resolver and validation layer
  - requires migration discipline for option changes
- Fit: strongest balance of control, speed, and maintainability.

### Candidate C — DB-First Catalog in CHUM Store

- Description: treat options as primary DB objects with write APIs.
- Strengths:
  - runtime query performance
  - rich dynamic updates
- Weaknesses:
  - higher complexity and migration overhead
  - harder human review than Git-tracked files
- Fit: useful later if scale demands it.

### Candidate D — Protein-Only Inference

- Description: rely exclusively on existing proteins/molecules without explicit option ontology.
- Strengths:
  - reuses existing mechanism
  - minimal new abstractions
- Weaknesses:
  - weak discovery for non-protein paths
  - awkward for heterogeneous tool chains and checklists
- Fit: too narrow for cross-skill/query use cases.

## Phase 2: Architecture Decisions and Rationale

### AD-1: Use a Unified Option Ontology

- Decision: define `atomic` and `composite` option nodes with typed edges.
- Why:
  - supports both single bash scripts and multi-step checklists
  - enables one query model across skills/tool calls/pathways

### AD-2: Canonical Source in YAML, Not DB

- Decision: keep canonical option definitions in repo YAML; optionally compile to DB cache.
- Why:
  - Git history + code review on planning logic
  - easier rollback and diffability than ad hoc DB edits

### AD-3: Planner Emits a Bounded Pre-Context Contract

- Decision: resolver output is a strict handoff bundle (`selected_option`, ordered `steps`, `gates`, constraints).
- Why:
  - prevents agent-side re-planning drift
  - makes tool usage deterministic and inspectable

### AD-4: Reuse Planning Trace Event Taxonomy

- Decision: instrument option resolution/execution with existing planning trace event types.
- Why:
  - immediate compatibility with `planning_trace_events`
  - enables reward, rollback, and candidate quality feedback loops

### AD-5: Enforce Execution Boundaries

- Decision: executor may run only steps declared in bundle unless explicit replanning trigger fires.
- Why:
  - controls cost and blast radius
  - enforces ceremony outcomes as policy, not suggestion

## Phase 3: Structured Plan

## Workstream 1 — Option Schema and Validation

- Deliverables:
  - versioned YAML schema for option nodes/edges
  - validator with CI check
- DoD:
  - invalid graph fails CI
  - cycle and missing-node errors are surfaced with actionable messages

## Workstream 2 — Seed Catalog from Existing Assets

- Deliverables:
  - seed entries for current proteins/molecules
  - seed entries for core scripts (for example `scripts/dev/test-safe.sh`)
- DoD:
  - at least one `react preflight` composite and one `design review` composite resolvable end-to-end

## Workstream 3 — Resolver and Pre-Context Bundle

- Deliverables:
  - resolver API/function: `ResolveExecutionOption(task_class, constraints)`
  - deterministic output contract (ordered steps + gates + policy)
- DoD:
  - same input yields same bundle
  - alternatives are ranked with explicit rationale fields

## Workstream 4 — Dispatcher/Planner Integration

- Deliverables:
  - planning stage integration that calls resolver before execution
  - bundle attached to execution request and persisted in trace metadata
- DoD:
  - execution starts only when bundle exists and passes gate checks

## Workstream 5 — Trace-Based Feedback and Scoring

- Deliverables:
  - scoring loop from trace outcomes (success, retries, gate_fail, rollback)
  - option health report
- DoD:
  - planner can down-rank failing options and prefer stronger alternatives

## Dependency Order

1. Workstream 1
2. Workstream 2
3. Workstream 3
4. Workstream 4
5. Workstream 5

## Suggested DAG Emission (Tasks)

1. `Define option graph schema and validator`
2. `Seed option catalog from proteins and scripts`
3. `Implement resolver and pre-context bundle contract`
4. `Integrate resolver into planning/dispatch flow`
5. `Add trace-based option scoring`

## Ceremony Outcome

Recommended path: **Candidate B (YAML catalog + runtime resolver)** with staged rollout and trace-driven optimization.

This achieves:

1. agent-led option research in plan space
2. explicit architecture decisions with reasons
3. a structured, dependency-aware implementation plan ready for DAG execution
