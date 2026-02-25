---
title: "Increase temporal package test coverage to 60%+"
status: ready
priority: 0
type: testing
labels:
  - technical-debt
  - reliability
  - self-healing
estimate_minutes: 180
acceptance_criteria: |
  - internal/temporal package test coverage reaches 60% minimum
  - New tests added for critical workflows: ChumAgentWorkflow, DispatcherWorkflow
  - New tests added for critical activities: StructuredPlanActivity, ExecuteAgentActivity, DoDVerifyActivity
  - All new tests pass
  - go test -cover ./internal/temporal shows >= 60% coverage
  - go test -race ./internal/temporal passes with no race conditions
design: |
  **Problem:** Current temporal package has only 22.3% test coverage despite containing
  the most critical business logic. This creates risk of regressions and makes refactoring dangerous.

  **Current state:**
  - workflow_test.go exists with 1,200 lines but only covers 22.3%
  - Many activities have no tests
  - Workflows have minimal happy-path tests

  **Target areas (prioritized by criticality):**

  1. **workflow.go** (ChumAgentWorkflow) - 50% coverage target
     - Test happy path: plan → execute → review → DoD → record
     - Test failure paths: DoD failure, review failure, execution timeout
     - Test escalation chain behavior
     - Test preflight abort on closed tasks
     - Test retry logic across tiers

  2. **workflow_dispatcher.go** (DispatcherWorkflow) - 60% coverage target
     - Test 3-lane routing: familiar/unfamiliar/complex
     - Test throttling behavior
     - Test agent rotation
     - Test child workflow spawning
     - Test empty candidate list

  3. **activities.go** - 50% coverage target
     - StructuredPlanActivity: test genome injection, semgrep context, failure context
     - ExecuteAgentActivity: test CLI invocation, output parsing, token extraction
     - DoDVerifyActivity: test pass/fail scenarios
     - RecordOutcomeActivity: test store persistence

  4. **types.go** - 80% coverage target
     - Test StructuredPlan.Validate() with various invalid plans
     - Test TokenUsage.Add() accumulation
     - Test DefaultReviewer() routing logic

  **Approach:**
  - Use Temporal's testsuite package for workflow tests
  - Use table-driven tests for activity logic
  - Mock store and external dependencies
  - Test error paths, not just happy paths

  **Verification:**
  ```bash
  go test -cover ./internal/temporal
  go test -race ./internal/temporal
  go test -coverprofile=coverage.out ./internal/temporal
  go tool cover -html=coverage.out  # verify visually
  ```
---

# Increase Temporal Test Coverage

The temporal package has only 22.3% test coverage despite being the core orchestration layer.
This is a critical reliability gap. Target: 60%+ coverage with focus on workflows and activities.

Without tests, we can't safely refactor. This unblocks all other refactoring work.
