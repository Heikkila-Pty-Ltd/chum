// Package temporal implements the core CHUM Temporal workflows and activities.

package temporal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"go.temporal.io/sdk/activity"

	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/git"
	"github.com/antigravity-dev/chum/internal/graph"
	"github.com/antigravity-dev/chum/internal/store"
)

// Activities holds dependencies for Temporal activity methods.
type Activities struct {
	Store *store.Store
	Tiers config.Tiers
	DAG   *graph.DAG
}

// StructuredPlanActivity generates a structured plan from a task prompt.
// The plan is gated — it must pass Validate() to enter the coding engine.
func (a *Activities) StructuredPlanActivity(ctx context.Context, req TaskRequest) (*StructuredPlan, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(SharkPrefix+" Generating structured plan", "Agent", req.Agent, "TaskID", req.TaskID)

	prompt := fmt.Sprintf(`You are a senior engineering planner. Analyze this task and produce a structured execution plan.

TASK: %s

OUTPUT FORMAT: You MUST respond with ONLY a JSON object (no markdown, no commentary) with this exact structure:
{
  "summary": "one-line summary of the task",
  "steps": [{"description": "what to do", "file": "which file", "rationale": "why"}],
  "files_to_modify": ["file1.go", "file2.go"],
  "acceptance_criteria": ["criterion 1", "criterion 2"],
  "estimated_complexity": "low|medium|high",
  "risk_assessment": "what could go wrong"
}

Be thorough. Planning space is cheap — implementation is expensive.`, req.Prompt)

	cliResult, err := runAgent(ctx, req.Agent, prompt, req.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("plan generation failed: %w", err)
	}

	logger.Info(SharkPrefix+" Plan generation token usage",
		"InputTokens", cliResult.Tokens.InputTokens,
		"OutputTokens", cliResult.Tokens.OutputTokens,
		"CacheReadTokens", cliResult.Tokens.CacheReadTokens,
		"CacheCreationTokens", cliResult.Tokens.CacheCreationTokens,
		"CostUSD", cliResult.Tokens.CostUSD,
	)

	// Extract JSON from the output (agent might wrap it in markdown)
	jsonStr := extractJSON(cliResult.Output)
	if jsonStr == "" {
		return nil, fmt.Errorf("agent did not produce valid JSON plan. Output:\n%s", truncate(cliResult.Output, 500))
	}

	var plan StructuredPlan
	if err := json.Unmarshal([]byte(jsonStr), &plan); err != nil {
		return nil, fmt.Errorf("failed to parse plan JSON: %w\nRaw: %s", err, truncate(jsonStr, 500))
	}
	plan.TokenUsage = cliResult.Tokens

	// Gate: validate plan before it enters the coding engine
	if issues := plan.Validate(); len(issues) > 0 {
		return nil, fmt.Errorf("plan failed quality gate:\n- %s", strings.Join(issues, "\n- "))
	}

	logger.Info(SharkPrefix+" Plan generated and validated",
		"Summary", plan.Summary,
		"Steps", len(plan.Steps),
		"Files", len(plan.FilesToModify),
		"Criteria", len(plan.AcceptanceCriteria),
	)

	return &plan, nil
}

// ExecuteActivity runs the primary coding agent to implement the plan.
func (a *Activities) ExecuteActivity(ctx context.Context, plan StructuredPlan, req TaskRequest) (*ExecutionResult, error) {
	logger := activity.GetLogger(ctx)
	agent := req.Agent
	logger.Info(SharkPrefix+" Executing plan", "Agent", agent, "TaskID", req.TaskID)

	// Build a detailed execution prompt from the structured plan
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("TASK: %s\n\n", plan.Summary))
	sb.WriteString("PLAN:\n")
	for i, step := range plan.Steps {
		sb.WriteString(fmt.Sprintf("%d. [%s] %s\n   Rationale: %s\n", i+1, step.File, step.Description, step.Rationale))
	}
	sb.WriteString(fmt.Sprintf("\nFILES TO MODIFY: %s\n", strings.Join(plan.FilesToModify, ", ")))
	sb.WriteString("\nACCEPTANCE CRITERIA:\n")
	for _, c := range plan.AcceptanceCriteria {
		sb.WriteString(fmt.Sprintf("- %s\n", c))
	}

	if len(plan.PreviousErrors) > 0 {
		sb.WriteString(fmt.Sprintf("\nPREVIOUS ERRORS TO FIX:\n%s\n", strings.Join(plan.PreviousErrors, "\n")))
		sb.WriteString("\nCRITICAL: You are fixing a build/test failure or review rejection. Fix ONLY the specific errors reported. DO NOT restructure the code, do not engage in large refactors, and DO NOT create new files. Only modify existing files to make the errors pass.\n")
	}

	// --- Learner feedback loop: inject recent lessons and DoD patterns ---
	if a.Store != nil {
		// Recent project-level lessons (rules, patterns, antipatterns)
		lessons, err := a.Store.GetRecentLessons(req.Project, 5)
		if err == nil && len(lessons) > 0 {
			sb.WriteString("\nLESSONS FROM PAST FAILURES (apply these):\n")
			for i := range lessons {
				sb.WriteString(fmt.Sprintf("- [%s] %s\n", lessons[i].Category, lessons[i].Summary))
			}
		}

		// File-specific lessons (if plan has target files)
		if len(plan.FilesToModify) > 0 {
			fileLessons, searchErr := a.Store.SearchLessonsByFilePath(plan.FilesToModify, 3)
			if searchErr == nil && len(fileLessons) > 0 {
				sb.WriteString("\nFILE-SPECIFIC LESSONS (for files you're modifying):\n")
				for i := range fileLessons {
					sb.WriteString(fmt.Sprintf("- %s: %s\n", strings.Join(fileLessons[i].FilePaths, ", "), fileLessons[i].Summary))
				}
			}
		}

		// Recent DoD failures for this project (so the agent knows what checks will run)
		var recentFailures []string
		rows, err := a.Store.DB().Query(`
			SELECT DISTINCT failures FROM dod_results
			WHERE project = ? AND passed = 0 AND failures != ''
			ORDER BY checked_at DESC LIMIT 3`, req.Project)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var f string
				if rows.Scan(&f) == nil && f != "" {
					recentFailures = append(recentFailures, f)
				}
			}
		}
		if len(recentFailures) > 0 {
			sb.WriteString("\nRECENT DOD FAILURES IN THIS PROJECT (avoid repeating):\n")
			for _, f := range recentFailures {
				sb.WriteString(fmt.Sprintf("- %s\n", f))
			}
		}
	}

	sb.WriteString("\nImplement this plan now. Make all necessary code changes.")

	cliResult, err := runAgent(ctx, agent, sb.String(), req.WorkDir)

	// Auto-stage any untracked or modified files so the review agent can see them
	// and so DoD checks see the same committed state.
	addCmd := exec.CommandContext(ctx, "git", "add", ".")
	addCmd.Dir = req.WorkDir
	if addErr := addCmd.Run(); addErr != nil {
		logger.Warn(SharkPrefix+" Failed to auto-stage files", "error", addErr)
	}

	exitCode := 0
	if err != nil {
		exitCode = 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		// Don't fail the activity — we want to proceed to review even on non-zero exit
		logger.Warn(SharkPrefix+" Agent exited with error", "error", err)
	}

	logger.Info(SharkPrefix+" Execution token usage",
		"InputTokens", cliResult.Tokens.InputTokens,
		"OutputTokens", cliResult.Tokens.OutputTokens,
		"CostUSD", cliResult.Tokens.CostUSD,
	)

	return &ExecutionResult{
		ExitCode: exitCode,
		Output:   cliResult.Output,
		Agent:    agent,
		Tokens:   cliResult.Tokens,
	}, nil
}

// CodeReviewActivity runs a DIFFERENT agent to review the implementation.
// Claude reviews codex's work, codex reviews claude's. Cross-pollination catches blind spots.
func (a *Activities) CodeReviewActivity(ctx context.Context, plan StructuredPlan, execResult ExecutionResult, req TaskRequest) (*ReviewResult, error) {
	logger := activity.GetLogger(ctx)

	reviewer := req.Reviewer
	if reviewer == "" {
		reviewer = DefaultReviewer(execResult.Agent)
	}

	logger.Info(SharkPrefix+" Code review", "Reviewer", reviewer, "Author", execResult.Agent, "TaskID", req.TaskID)

	prompt := fmt.Sprintf(`You are a senior code reviewer. Another AI agent (%s) implemented the following plan.
Review their work against the acceptance criteria.

PLAN SUMMARY: %s

ACCEPTANCE CRITERIA:
%s

AGENT OUTPUT:
%s

Review the implementation. Respond with ONLY a JSON object:
{
  "approved": true/false,
  "issues": ["issue 1", "issue 2"],
  "suggestions": ["suggestion 1"]
}

Be rigorous. Quality enterprise-grade code only. Flag any: missing error handling, untested paths, race conditions, security issues.`,
		execResult.Agent,
		plan.Summary,
		formatCriteria(plan.AcceptanceCriteria),
		truncate(execResult.Output, 3000),
	)

	cliResult, err := runReviewAgent(ctx, reviewer, prompt, req.WorkDir)
	if err != nil {
		// Review failure is not fatal — log and approve with warning
		logger.Warn(SharkPrefix+" Review agent error, defaulting to approved with warning", "error", err)
		return &ReviewResult{
			Approved:      true,
			Issues:        []string{"Review agent failed: " + err.Error()},
			ReviewerAgent: reviewer,
			ReviewOutput:  cliResult.Output,
			Tokens:        cliResult.Tokens,
		}, nil
	}

	logger.Info(SharkPrefix+" Review token usage",
		"InputTokens", cliResult.Tokens.InputTokens,
		"OutputTokens", cliResult.Tokens.OutputTokens,
		"CostUSD", cliResult.Tokens.CostUSD,
	)

	jsonStr := extractJSON(cliResult.Output)
	if jsonStr == "" {
		// Can't parse review — approve with warning
		return &ReviewResult{
			Approved:      true,
			Issues:        []string{"Review output was not valid JSON"},
			ReviewerAgent: reviewer,
			ReviewOutput:  cliResult.Output,
			Tokens:        cliResult.Tokens,
		}, nil
	}

	result := parseReviewJSON(jsonStr, reviewer, cliResult)
	return &result, nil
}

// parseReviewJSON attempts to unmarshal review JSON. On failure, returns an
// approved review with a warning issue — graceful degradation so review
// infrastructure errors never block the pipeline.
func parseReviewJSON(jsonStr, reviewer string, cliResult CLIResult) ReviewResult {
	var result ReviewResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return ReviewResult{
			Approved:      true,
			Issues:        []string{"Failed to parse review JSON: " + err.Error()},
			ReviewerAgent: reviewer,
			ReviewOutput:  cliResult.Output,
			Tokens:        cliResult.Tokens,
		}
	}
	result.ReviewerAgent = reviewer
	result.ReviewOutput = cliResult.Output
	result.Tokens = cliResult.Tokens
	return result
}

// DoDVerifyActivity runs DoD checks (compile, test, lint) using git.RunPostMergeChecks.
// Uses cheap agent resources — no smart model needed to run tests.
func (a *Activities) DoDVerifyActivity(ctx context.Context, req TaskRequest) (*DoDResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(OrcaPrefix+" Running DoD checks", "TaskID", req.TaskID, "Checks", len(req.DoDChecks))

	checks := req.DoDChecks
	if len(checks) == 0 {
		// Default DoD: at minimum, the code must compile
		checks = []string{"go build ./..."}
	}

	gitResult, err := git.RunPostMergeChecks(req.WorkDir, checks)
	if err != nil {
		return nil, fmt.Errorf("DoD check execution failed: %w", err)
	}

	result := &DoDResult{
		Passed:   gitResult.Passed,
		Failures: gitResult.Failures,
	}

	for _, c := range gitResult.Checks {
		result.Checks = append(result.Checks, CheckResult{
			Command:    c.Command,
			ExitCode:   c.ExitCode,
			Output:     c.Output,
			Passed:     c.Passed,
			DurationMs: c.Duration.Milliseconds(),
		})
	}

	logger.Info(OrcaPrefix+" DoD result", "Passed", result.Passed, "Checks", len(result.Checks), "Failures", len(result.Failures))
	return result, nil
}

// RecordOutcomeActivity persists the workflow outcome to the store.
// This feeds the learner loop — learner runs on top to surface problems and inefficiencies.
func (a *Activities) RecordOutcomeActivity(ctx context.Context, outcome OutcomeRecord) error {
	logger := activity.GetLogger(ctx)
	logger.Info(OrcaPrefix+" Recording outcome", "TaskID", outcome.TaskID, "Status", outcome.Status)

	if a.Store == nil {
		logger.Warn(OrcaPrefix + " No store configured, skipping outcome recording")
		return nil
	}

	// Record dispatch
	dispatchID, err := a.Store.RecordDispatch(
		outcome.TaskID,
		outcome.Project,
		outcome.Agent,
		outcome.Provider,
		"temporal", // tier
		0,          // handle (not PID-based)
		"",         // session name
		"",         // prompt (stored in Temporal history)
		"",         // log path
		"",         // branch
		"temporal", // backend
	)
	if err != nil {
		logger.Error(OrcaPrefix+" Failed to record dispatch", "error", err)
		return err
	}

	// Update status
	if err := a.Store.UpdateDispatchStatus(dispatchID, outcome.Status, outcome.ExitCode, outcome.DurationS); err != nil {
		logger.Error(OrcaPrefix+" Failed to update dispatch status", "error", err)
	}

	// Record DoD result with check output for post-mortem diagnostics
	checkResultsJSON := ""
	if len(outcome.CheckResults) > 0 {
		if b, err := json.Marshal(outcome.CheckResults); err == nil {
			checkResultsJSON = string(b)
		}
	}
	if err := a.Store.RecordDoDResult(dispatchID, outcome.TaskID, outcome.Project, outcome.DoDPassed, outcome.DoDFailures, checkResultsJSON); err != nil {
		logger.Error(OrcaPrefix+" Failed to record DoD result", "error", err)
	}

	// Record aggregate token cost on the dispatch
	totalInput := outcome.TotalTokens.InputTokens
	totalOutput := outcome.TotalTokens.OutputTokens
	if err := a.Store.RecordDispatchCost(dispatchID, totalInput, totalOutput, outcome.TotalTokens.CostUSD); err != nil {
		logger.Error(OrcaPrefix+" Failed to record dispatch cost", "error", err)
	}

	// Record per-activity token breakdown for learner optimization.
	for _, at := range outcome.ActivityTokens {
		if err := a.Store.StoreTokenUsage(
			dispatchID,
			outcome.TaskID,
			outcome.Project,
			at.ActivityName,
			at.Agent,
			store.TokenUsage{
				InputTokens:         at.Tokens.InputTokens,
				OutputTokens:        at.Tokens.OutputTokens,
				CacheReadTokens:     at.Tokens.CacheReadTokens,
				CacheCreationTokens: at.Tokens.CacheCreationTokens,
				CostUSD:             at.Tokens.CostUSD,
			},
		); err != nil {
			logger.Error(OrcaPrefix+" Failed to store per-activity token usage", "error", err)
		} else {
			logger.Info(OrcaPrefix+" Activity token usage",
				"Activity", at.ActivityName,
				"Agent", at.Agent,
				"InputTokens", at.Tokens.InputTokens,
				"OutputTokens", at.Tokens.OutputTokens,
				"CacheReadTokens", at.Tokens.CacheReadTokens,
				"CacheCreationTokens", at.Tokens.CacheCreationTokens,
				"CostUSD", at.Tokens.CostUSD)
		}
	}

	// Record per-step metrics for pipeline observability.
	for _, sm := range outcome.StepMetrics {
		if err := a.Store.StoreStepMetric(
			dispatchID,
			outcome.TaskID,
			outcome.Project,
			sm.Name,
			sm.DurationS,
			sm.Status,
			sm.Slow,
		); err != nil {
			logger.Error(OrcaPrefix+" Failed to store step metric", "error", err, "Step", sm.Name)
		}
	}

	logger.Info(OrcaPrefix+" Outcome recorded", "DispatchID", dispatchID,
		"InputTokens", totalInput,
		"OutputTokens", totalOutput,
		"CacheReadTokens", outcome.TotalTokens.CacheReadTokens,
		"CacheCreationTokens", outcome.TotalTokens.CacheCreationTokens,
		"CostUSD", outcome.TotalTokens.CostUSD,
		"StepMetrics", len(outcome.StepMetrics))
	return nil
}

// EscalateActivity escalates a failed task to the chief/scrum-master with human in the loop.
// This is called when DoD fails after all retries — the task needs human intervention.
func (a *Activities) EscalateActivity(ctx context.Context, escalation EscalationRequest) error {
	logger := activity.GetLogger(ctx)
	logger.Error(OrcaPrefix+" ESCALATION: Task failed after all retries",
		"TaskID", escalation.TaskID,
		"Project", escalation.Project,
		"Attempts", escalation.AttemptCount,
		"Handoffs", escalation.HandoffCount,
		"Failures", strings.Join(escalation.Failures, "; "),
	)

	// Record health event for visibility
	if a.Store != nil {
		details := fmt.Sprintf("Task %s failed after %d attempts and %d handoffs. Failures: %s",
			escalation.TaskID, escalation.AttemptCount, escalation.HandoffCount,
			strings.Join(escalation.Failures, "; "))
		if recErr := a.Store.RecordHealthEvent("escalation_required", details); recErr != nil {
			logger.Warn(OrcaPrefix+" Failed to record health event", "error", recErr)
		}
	}

	// In V0, escalation is logged + stored. The human sees it via /health endpoint.
	// Future: trigger chief/scrum-master ceremony, Matrix notification, etc.
	return nil
}

// --- helpers ---

// sanitizeJSON cleans common LLM JSON corruption patterns.
// This is an antibody: LLMs frequently emit JSON with escaped backslashes,
// trailing commas, BOM markers, or control characters that break json.Unmarshal.
func sanitizeJSON(s string) string {
	// Strip UTF-8 BOM
	s = strings.TrimPrefix(s, "\xef\xbb\xbf")

	// Remove control characters (except \n, \r, \t which are valid in JSON strings)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			continue
		}
		b.WriteRune(r)
	}
	s = b.String()

	// Fix trailing commas before } or ] (e.g., {"a": 1,} → {"a": 1})
	for _, pair := range [][2]string{{",}", "}"}, {",]", "]"}} {
		for strings.Contains(s, pair[0]) {
			s = strings.ReplaceAll(s, pair[0], pair[1])
		}
	}
	// Also handle trailing comma with whitespace: , \n}
	for _, closer := range []string{"}", "]"} {
		for {
			idx := strings.Index(s, ",")
			if idx < 0 {
				break
			}
			// Look ahead past whitespace for the closer
			rest := strings.TrimLeft(s[idx+1:], " \t\n\r")
			if strings.HasPrefix(rest, closer) {
				s = s[:idx] + " " + rest
			} else {
				break
			}
		}
	}

	return s
}

// extractJSON finds the first JSON object in text (handles markdown code fences).
// Applies sanitizeJSON to clean common LLM output corruption.
func extractJSON(text string) string {
	// Try to find JSON between code fences first
	if idx := strings.Index(text, "```json"); idx >= 0 {
		start := idx + 7
		if end := strings.Index(text[start:], "```"); end >= 0 {
			return sanitizeJSON(strings.TrimSpace(text[start : start+end]))
		}
	}
	if idx := strings.Index(text, "```"); idx >= 0 {
		start := idx + 3
		// Skip optional language tag on same line
		if nl := strings.Index(text[start:], "\n"); nl >= 0 {
			start += nl + 1
		}
		if end := strings.Index(text[start:], "```"); end >= 0 {
			candidate := strings.TrimSpace(text[start : start+end])
			if candidate != "" && candidate[0] == '{' {
				return sanitizeJSON(candidate)
			}
		}
	}

	// Try to find raw JSON object
	start := strings.Index(text, "{")
	if start < 0 {
		return ""
	}
	// Find matching closing brace
	depth := 0
	for i := start; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return sanitizeJSON(text[start : i+1])
			}
		}
	}
	return ""
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// ResetWorkspaceActivity hard resets the codebase and cleans untracked files
// to give a backup agent a fresh slate when taking over a failed execution.
func (a *Activities) ResetWorkspaceActivity(ctx context.Context, workDir string) error {
	logger := activity.GetLogger(ctx)
	logger.Info(SharkPrefix+" Resetting workspace for fresh handoff", "WorkDir", workDir)

	cmd := exec.CommandContext(ctx, "git", "reset", "--hard", "HEAD")
	cmd.Dir = workDir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git reset: %w", err)
	}

	cleanCmd := exec.CommandContext(ctx, "git", "clean", "-fd")
	cleanCmd.Dir = workDir
	if err := cleanCmd.Run(); err != nil {
		return fmt.Errorf("git clean: %w", err)
	}

	return nil
}

func formatCriteria(criteria []string) string {
	var sb strings.Builder
	for _, c := range criteria {
		sb.WriteString(fmt.Sprintf("- %s\n", c))
	}
	return sb.String()
}
