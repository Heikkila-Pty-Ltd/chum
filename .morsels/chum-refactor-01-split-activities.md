---
title: "Split activities.go god object into domain-specific files"
status: ready
priority: 0
type: refactor
labels:
  - technical-debt
  - maintainability
  - self-healing
estimate_minutes: 90
acceptance_criteria: |
  - activities.go is split into 5 domain-specific files
  - All activity functions are moved to appropriate files
  - No duplicate code between files
  - All tests pass: go test ./internal/temporal/...
  - go build ./... succeeds
  - golangci-lint run passes
  - Activity registration in worker.go updated if needed
design: |
  **Problem:** activities.go is 1,844 lines with 37+ activity functions. This is a god object
  that violates SRP and makes navigation/maintenance difficult.

  **Solution:** Split into domain-specific activity files:

  1. `plan_activities.go` - Planning and genome injection
     - StructuredPlanActivity
     - LoadGenomeActivity (if exists)
     - classifySpecies helper
     - loadSemgrepContext helper

  2. `execute_activities.go` - Code execution
     - ExecuteAgentActivity
     - All CLI invocation logic

  3. `review_activities.go` - Cross-model review
     - ReviewActivity
     - ReviewResult parsing

  4. `dod_activities.go` - Definition of Done checks
     - DoDVerifyActivity
     - RunPostMergeChecks calls
     - Test/build verification

  5. `record_activities.go` - Outcome persistence
     - RecordOutcomeActivity
     - Store interactions for metrics
     - Genome updates

  6. `activities.go` - Keep only:
     - Activities struct definition
     - WorktreeDir helper
     - Shared utility functions that don't fit other categories

  **Steps:**
  1. Read activities.go completely to understand all functions
  2. Create 5 new files with proper package declaration and imports
  3. Move functions to appropriate files (use git mv logic - copy then verify)
  4. Update imports in each file
  5. Ensure Activities struct methods work across files (all files are same package)
  6. Run go build ./... after each file to catch import errors
  7. Run go test ./internal/temporal/... to verify no breakage
  8. Run golangci-lint to verify code quality

  **Note:** All files remain in `package temporal`, so method receivers work across files.
---

# Split activities.go God Object

The 1,844-line activities.go file has become unmaintainable. Split it into 5 domain-specific
files to improve navigability and reduce merge conflicts.

This is CHUM eating its own tail - using the refactoring pipeline to fix the pipeline itself.
