---
title: "Extract dispatcher routing logic into testable module"
status: open
priority: 2
type: refactor
labels:
  - technical-debt
  - testability
  - self-healing
estimate_minutes: 120
acceptance_criteria: |
  - New file created: internal/temporal/routing.go
  - RouteSelector type with SelectWorkflow method
  - All routing conditions extracted from workflow_dispatcher.go lines 145-170
  - Comprehensive unit tests for all routing branches in routing_test.go
  - All edge cases tested: Gen 0, complexity thresholds, crab seal status
  - Integration test verifying DispatcherWorkflow uses RouteSelector
  - go test ./internal/temporal passes
  - go test -cover ./internal/temporal/routing.go shows >= 85% coverage
design: |
  **Problem:** The 3-lane routing logic in DispatcherWorkflow (lines 145-170) is complex
  with multiple conditions (Generation, Complexity, HasCrabSeal, EscalationTiers). It's
  embedded in workflow code and hard to test in isolation.

  **Current routing logic (workflow_dispatcher.go:145-170):**
  ```go
  if c.Complexity > 70 || !c.HasCrabSeal {
      // Lane 3: Complex → Turtle→Crab Pipeline
  } else if c.Generation == 0 && len(result.EscalationTiers) > 1 {
      // Lane 2: Unfamiliar → Cambrian Explosion
  } else {
      // Lane 1: Familiar → Direct Assign
  }
  ```

  **Solution: Extract to routing.go**

  ```go
  package temporal

  // WorkflowRoute identifies which workflow type to execute
  type WorkflowRoute int

  const (
      RouteChumAgent WorkflowRoute = iota  // Lane 1: Familiar/simple
      RouteCambrianExplosion                // Lane 2: Unfamiliar
      RouteTurtleToCrab                     // Lane 3: Complex
  )

  // RoutingDecision contains the selected route and reasoning
  type RoutingDecision struct {
      Route  WorkflowRoute
      Reason string  // human-readable explanation for observability
  }

  // RouteSelector encapsulates workflow routing logic
  type RouteSelector struct {
      ComplexityThreshold int  // default: 70
  }

  // SelectWorkflow determines which workflow to execute based on candidate properties
  func (rs *RouteSelector) SelectWorkflow(candidate ScanCandidate, escalationTiers []EscalationTier) RoutingDecision {
      // Lane 3: Complex or un-decomposed
      if candidate.Complexity > rs.ComplexityThreshold {
          return RoutingDecision{
              Route:  RouteTurtleToCrab,
              Reason: fmt.Sprintf("complexity=%d exceeds threshold=%d", candidate.Complexity, rs.ComplexityThreshold),
          }
      }
      if !candidate.HasCrabSeal {
          return RoutingDecision{
              Route:  RouteTurtleToCrab,
              Reason: "task not yet decomposed (missing crab seal)",
          }
      }

      // Lane 2: Unfamiliar (Gen 0) with multiple providers available
      if candidate.Generation == 0 && len(escalationTiers) > 1 {
          return RoutingDecision{
              Route:  RouteCambrianExplosion,
              Reason: fmt.Sprintf("gen=%d (unfamiliar) with %d escalation tiers available", candidate.Generation, len(escalationTiers)),
          }
      }

      // Lane 1: Familiar/simple - default path
      return RoutingDecision{
          Route:  RouteChumAgent,
          Reason: fmt.Sprintf("gen=%d (familiar), complexity=%d (simple)", candidate.Generation, candidate.Complexity),
      }
  }
  ```

  **Test coverage (routing_test.go):**
  - Lane 1: Gen > 0, low complexity → RouteChumAgent
  - Lane 2: Gen = 0, multiple tiers → RouteCambrianExplosion
  - Lane 2: Gen = 0, single tier → RouteChumAgent (fallback)
  - Lane 3: Complexity = 71 → RouteTurtleToCrab
  - Lane 3: Complexity = 70 → RouteChumAgent (boundary)
  - Lane 3: HasCrabSeal = false → RouteTurtleToCrab
  - Edge: Gen = 0, complexity = 100, multiple tiers → RouteTurtleToCrab (complexity trumps gen)

  **Steps:**
  1. Create internal/temporal/routing.go with above structure
  2. Create internal/temporal/routing_test.go with table-driven tests
  3. Update workflow_dispatcher.go to use RouteSelector
  4. Run tests to verify behavior unchanged
  5. Add observability: log routing decision reason in dispatcher

  **Benefits:**
  - Routing logic testable in isolation
  - Easy to add new routing conditions
  - Clear reasoning for workflow selection (helps debugging)
  - Complexity threshold configurable (not hardcoded 70)
depends_on: ["chum-refactor-04-workflow-call-graph"]
---

# Extract Dispatcher Routing Logic

The 3-lane routing logic is embedded in workflow code and hard to test. Extract it
into a dedicated, testable routing module with comprehensive unit tests.

This makes the routing logic explicit, testable, and easier to evolve.
