---
title: "Antibodies for silent empty sentinel values"
status: ready
priority: 2
type: task
labels:
  - whale:temporal
  - reliability
estimate_minutes: 15
acceptance_criteria: |
  - Validation that strips empty strings from PreviousErrors before dispatch
  - Test: TaskRequest with previous_errors: [""] is treated as empty (no PREVIOUS ERRORS TO FIX prompt block)
  - Broader pattern: audit all []string fields on TaskRequest/DispatchCandidate for empty-string sentinels
design: |
  Root cause: previous_errors: [""] passes `len() > 0` check but contains no useful info.
  The agent gets a "PREVIOUS ERRORS TO FIX:" block with an empty line — wastes tokens and misleads.

  Fix: Add a sanitizer in ExecuteActivity (or in the dispatcher) that filters empty strings:
    errs := make([]string, 0, len(plan.PreviousErrors))
    for _, e := range plan.PreviousErrors { if strings.TrimSpace(e) != "" { errs = append(errs, e) } }
    plan.PreviousErrors = errs

  Also add a unit test that asserts previous_errors: [""] produces no PREVIOUS ERRORS block in the prompt.
depends_on: []
---

Meta: why was `previous_errors: [""]` not caught? The empty-string sentinel
passed `len() > 0` silently. Need input validation + test coverage for this
class of bug across all string-slice fields.
