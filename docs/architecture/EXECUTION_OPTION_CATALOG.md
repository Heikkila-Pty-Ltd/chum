# Execution Option Catalog

## Goal

Document atomic skills/tool calls/scripts and composite checklists in one queryable graph so agents receive a pre-structured execution plan before writing code.

This reduces prompt drift and avoids re-deriving tool strategy inside every agent run.

This is intentionally not "spec-only". The catalog is meant to be produced and refined through planning ceremonies that capture options, tradeoffs, and decision rationale.

## Scope

This model covers:

- atomic options (single bash script, single tool call, single skill action)
- composite options (ordered checklists/workflows that chain atomic options)
- pre-context handoff payloads for planners/executors

## Core Model

### 1) Option Node

Every executable unit is a node.

```yaml
id: react-build-check
kind: atomic # atomic | composite
action: script # script | skill | tool_call | prompt | human_gate
title: Run project build
description: Runs package build to catch compile-level regressions early
command: npm run build
inputs:
  - repo_root
outputs:
  - build_log
guards:
  - must_run_from_workspace: true
  - fail_on_nonzero_exit: true
tags: [react, preflight, verification]
```

### 2) Option Edge

Edges define pathway semantics:

- `depends_on` (hard prerequisite)
- `follows` (recommended order)
- `alternative_to` (same goal, different path/cost)
- `expands_to` (composite node expands into children)

### 3) Option Graph

A queryable directed graph of nodes + edges:

- atomics can be executed directly
- composites expand into ordered atomics
- alternatives can be selected by policy (cost, speed, risk)

## Mapping to Existing CHUM Primitives

CHUM already has most of this:

- `proteins` = composite workflows
- `molecules` = ordered steps (often atomic)
- `planning_trace_events` = execution/decision trace graph with node and parent links

Practical mapping:

- treat each molecule as an `atomic` node
- treat each protein as a `composite` node with `expands_to` edges
- use planning trace event types (`tool_call`, `tool_result`, `gate_pass`, `gate_fail`, `rollback_applied`) as runtime observability for option quality

## Catalog File Shape

Recommended source-of-truth format (YAML in repo, compiled to DB if needed):

```yaml
version: 1
options:
  - id: react-preflight-v1
    kind: composite
    title: React component preflight checklist
    expands_to:
      - read-types
      - build-component
      - run-build
      - run-audit
    policy:
      max_parallelism: 1
      fail_fast: true

  - id: read-types
    kind: atomic
    action: script
    command: rg -n "type|interface" lib types src
    tags: [react, types]

  - id: run-audit
    kind: atomic
    action: skill
    skill: /audit
    tags: [design, quality]

edges:
  - from: read-types
    to: build-component
    type: follows
  - from: build-component
    to: run-build
    type: depends_on
  - from: run-build
    to: run-audit
    type: depends_on
```

## Example: Your Two Patterns

### A) Individual Atomic Skills/Scripts

- Example: `scripts/dev/test-safe.sh`
- Represent as one `atomic` node with explicit input/output contract
- Reusable in many composites

### B) Complex Checklists

- Example: React preflight
  - composite node expanding to ordered atomics (read types -> implement -> build -> audit)
- Example: design review checklist
  - composite node expanding to ordered design atoms (critique -> normalize -> harden -> audit)

This allows direct graph queries like:

- "show all atomics tagged `design`"
- "show all composites that include `/audit`"
- "find shortest verified path for `react component preflight`"

## Agent Pre-Context Handoff Contract

Before execution, planner resolves candidate options and hands executor a bounded plan:

```json
{
  "task_class": "react-component",
  "selected_option": "react-preflight-v1",
  "steps": [
    {"id": "read-types", "action": "script", "command": "rg -n \"type|interface\" lib types src"},
    {"id": "build-component", "action": "prompt", "instruction_ref": "protein:component-v1:build-component"},
    {"id": "run-build", "action": "script", "command": "npm run build"},
    {"id": "run-audit", "action": "skill", "skill": "/audit"}
  ],
  "gates": [
    {"name": "build_passes", "required": true},
    {"name": "dod_passes", "required": true}
  ]
}
```

Executor behavior should be constrained to:

- run provided steps in order unless policy allows parallelism
- emit trace events per step
- request replanning only on gate failure, missing dependency, or unavailable tool

## Query Modes

Support at least three query modes:

- `by_task_class`: pick best composite for a species/task class
- `by_capability`: discover available atomics by tag/action/tool
- `by_constraint`: filter by speed/cost/risk policy before selecting pathway

## Incremental Rollout

1. Start with documentation-only catalog for existing proteins + key scripts.
2. Add loader that validates YAML and writes normalized nodes/edges.
3. Add planner resolver to return pre-context handoff bundles.
4. Score options using planning trace outcomes (success rate, gate failures, retries, token cost).

## Success Criteria

- agents spend fewer tokens on tool-selection reasoning
- lower gate-fail and rollback rate in planning traces
- faster time-to-first-valid-plan for recurring task classes
- easier human inspection: "what options exist and why this one was chosen"

## Ceremony-Driven Planning

Use this catalog through a recurring architecture planning ceremony, not as static config.

- Ceremony outputs should include:
  - option research (`candidate_set_evaluated` and option events)
  - explicit architecture decisions with reasons (`decision` events)
  - implementation-ready structured plan (`plan_summary_result` + behavior contract)
- Reference session artifact:
  - [`docs/architecture/EXECUTION_OPTION_CATALOG_CEREMONY_SESSION.md`](./EXECUTION_OPTION_CATALOG_CEREMONY_SESSION.md)
