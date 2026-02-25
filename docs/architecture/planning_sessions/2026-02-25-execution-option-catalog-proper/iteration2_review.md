# Iteration 2 Review: Execution Option Catalog

Date: 2026-02-25
Session: `planning-chum-20260225-execution-option-catalog-proper`

## Context

The agreed ceremony outcome is:

- Objective: Research-Heavy
- Evaluation mode: Hybrid (broad scan then deep drill)
- Architecture: Dual-Track
- Rollout: Two-Phase

This means we are optimizing for learning depth and decision quality while still delivering an execution-capable system.

## Plan Review

What is strong in the current plan:

- Clear canonical source (`YAML`) and runtime query model (`DB mirror`)
- Explicit resolver contract for bounded pre-context handoff
- Phase boundaries with exit gates
- Existing trace structures that can evaluate planning outcomes

What is currently underspecified:

- Exact schema semantics for edge types and validation behavior
- Determinism guarantees for resolver tie-breaking and ranking
- Drift-control mechanics between YAML canonical and DB mirror
- Guardrails for score poisoning/noisy planning traces

## Risk Register (Iteration 2)

1. Dual-track drift (`YAML` vs `DB mirror`)  
Impact: high. Likelihood: medium.  
Mitigation: deterministic mirror build + parity CI gate + semantic hash checks.

2. Resolver bypass in execution paths  
Impact: high. Likelihood: medium-high.  
Mitigation: dispatch preflight must require resolver bundle presence.

3. Non-deterministic resolver output  
Impact: medium-high. Likelihood: medium.  
Mitigation: stable sorting and explicit tie-break keys; deterministic tests.

4. Score poisoning from poor trace quality  
Impact: medium-high. Likelihood: medium.  
Mitigation: weighted scoring, confidence thresholds, and bounded score deltas.

5. Catalog modeling ambiguity  
Impact: medium. Likelihood: medium.  
Mitigation: schema docs with normative examples and validator lint rules.

## Highest-Value Focus

Value is maximized by front-loading components that reduce execution waste and tool drift:

1. Resolver contract + dispatch gate enforcement (directly controls runtime behavior)
2. Schema + validator (prevents bad graph definitions entering system)
3. Minimal seeded catalog for real use paths (react preflight + design review)

DB mirror and scoring remain high-value, but after execution-control basics are enforced.

## Suggested Path (Recommended)

`Path V1: Value-first with guarded dual-track`

1. Implement canonical schema/validator and deterministic resolver baseline.
2. Enforce resolver bundle gate in planning/dispatch before broad mirror adoption.
3. Introduce DB mirror and parity checks immediately after baseline passes.
4. Enable trace-driven scoring only after parity and trace quality checks pass.

Rationale: preserves the chosen dual-track direction while avoiding early coupling failure.

## Options

1. Option A: Value-first (Recommended)
- Sequence: schema -> resolver -> dispatch gate -> mirror/parity -> scoring.
- Best when immediate execution quality is priority.

2. Option B: Drift-first
- Sequence: schema -> mirror/parity -> resolver -> scoring.
- Best when data consistency is primary concern, slower execution impact.

3. Option C: Parallel split
- Track 1: schema/resolver/gates.
- Track 2: mirror/parity/scoring scaffolding.
- Best with enough capacity; higher coordination overhead.

## Initial Morsel Decomposition

See:

- `iteration2_morsels.json` for graph-ready decomposition
- `iteration2_task_payloads.json` for `POST /tasks` payload candidates
