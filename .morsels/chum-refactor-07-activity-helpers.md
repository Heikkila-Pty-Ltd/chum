---
title: "Extract common activity patterns into activity_helpers.go"
status: open
priority: 3
type: refactor
labels:
  - technical-debt
  - code-deduplication
  - self-healing
estimate_minutes: 90
acceptance_criteria: |
  - New file created: internal/temporal/activity_helpers.go
  - Common patterns extracted: CLI invocation, result parsing, error classification
  - At least 3 helper functions created and used in 5+ places
  - Code duplication reduced by 200+ lines
  - All tests pass: go test ./internal/temporal
  - go build ./... succeeds
  - Helper functions have unit tests in activity_helpers_test.go
design: |
  **Problem:** Multiple activity files (crab_activities.go, turtle_activities.go,
  stingray_activities.go, protein_synthesis_activity.go) contain duplicated patterns
  for CLI invocation, output parsing, and error handling.

  **Common patterns to extract:**

  1. **CLI Invocation Pattern**
     - Build exec.Command with timeout
     - Capture stdout/stderr
     - Handle context cancellation
     - Extract exit code
     - Parse JSON output if present

  2. **Result Parsing Pattern**
     - Parse agent CLI JSON output
     - Extract token usage (input_tokens, output_tokens, cost_usd)
     - Handle malformed JSON with repair
     - Populate TokenUsage struct

  3. **Error Classification Pattern**
     - Distinguish infrastructure errors (timeout, OOM) from user errors (syntax, logic)
     - Map exit codes to error types
     - Build ActivityError with proper classification

  4. **Worktree Path Resolution**
     - Resolve relative paths in worktree
     - Validate worktree exists
     - Create worktree directory if needed

  **Proposed helpers:**

  ```go
  // InvokeCLI runs an agent CLI command with timeout and captures output
  func InvokeCLI(ctx context.Context, cfg CLIConfig, prompt string, workDir string) (*CLIResult, error) {
      // Build command
      // Set environment
      // Pipe stdin
      // Capture stdout/stderr
      // Handle timeout
      // Return structured result
  }

  // ParseAgentOutput extracts structured data from agent CLI JSON output
  func ParseAgentOutput(stdout, stderr string, agent string) (*AgentOutput, error) {
      // Try JSON parse
      // Fall back to json_repair if malformed
      // Extract token usage
      // Extract exit code
      // Return structured output
  }

  // ClassifyError determines if an error is infrastructure or user-caused
  func ClassifyError(exitCode int, stderr string, duration time.Duration) ErrorClass {
      // Check for timeout
      // Check for OOM (exit 137)
      // Check for known infrastructure signals
      // Default to user error
  }

  // ResolveWorktreePath validates and resolves a path within a worktree
  func ResolveWorktreePath(workDir, relativePath string) (string, error) {
      // Join paths
      // Validate within worktree bounds (prevent ../.. escapes)
      // Check exists if required
  }
  ```

  **Steps:**
  1. Identify all CLI invocations across activity files (grep for exec.Command)
  2. Analyze patterns and extract common structure
  3. Create activity_helpers.go with extracted functions
  4. Create activity_helpers_test.go with unit tests
  5. Refactor existing activities to use helpers (one file at a time)
  6. Verify tests pass after each refactoring
  7. Measure LOC reduction (aim for 200+ lines removed)

  **Success criteria:**
  - Each helper used in 5+ places
  - Code duplication reduced
  - Easier to add new activities (just use helpers)
depends_on: ["chum-refactor-01-split-activities"]
---

# Extract Common Activity Patterns

Multiple activity files duplicate CLI invocation, parsing, and error handling logic.
Extract common patterns into reusable helpers to reduce duplication and improve consistency.

Target: 200+ lines of code deduplication, making future activities easier to write.
