---
title: "Split types.go into domain-specific type files"
status: open
priority: 3
type: refactor
labels:
  - technical-debt
  - maintainability
  - self-healing
estimate_minutes: 60
acceptance_criteria: |
  - types.go split into 3-4 domain-specific files
  - All 46 struct types moved to appropriate files
  - Shared types remain in types.go
  - All imports updated across temporal package
  - go build ./... succeeds
  - go test ./internal/temporal passes
  - No duplicate type definitions
design: |
  **Problem:** types.go contains 46 struct types (583 lines), making it hard to navigate
  and find the right type for a given purpose.

  **Current state:** 46 structs in one file covering:
  - Workflow requests/responses
  - Activity inputs/outputs
  - Dispatch/routing types
  - Review/DoD types
  - Token tracking types

  **Solution: Split by domain**

  1. **workflow_types.go** - Workflow-level types
     - TaskRequest
     - EscalationTier
     - StructuredPlan
     - PlanStep
     - TurtlePlanningRequest
     - CrabDecompositionRequest
     - PlanningRequest
     - LearnerRequest
     - etc.

  2. **activity_types.go** - Activity inputs/outputs
     - ExecutionResult
     - ReviewResult
     - DoDResult
     - NotifyRequest
     - InvestigationRequest
     - etc.

  3. **metrics_types.go** - Observability types
     - TokenUsage
     - ActivityTokenUsage
     - StepMetric
     - OrganismLog
     - etc.

  4. **types.go** - Keep only:
     - Shared constants (SharkPrefix, DefaultTaskQueue, etc.)
     - Helper functions used across domains (DefaultReviewer, normalizeSearchMetadata, etc.)
     - Core interfaces if any

  **Steps:**
  1. Analyze all 46 types and categorize by domain
  2. Create 3 new files with proper package declaration
  3. Move types to appropriate files
  4. Update imports in all temporal/*.go files (grep for type names)
  5. Verify no circular dependencies created
  6. Run go build ./... to catch import errors
  7. Run go test ./internal/temporal to verify correctness

  **Note:** All files stay in `package temporal`, so no import path changes needed.
depends_on: ["chum-refactor-01-split-activities"]
---

# Split types.go Domain Model

46 struct types in one file makes navigation difficult. Split into domain-specific
type files: workflow_types.go, activity_types.go, metrics_types.go.

Improves code organization and makes it easier to find relevant types.
