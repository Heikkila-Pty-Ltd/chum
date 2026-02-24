// Package temporal implements the core CHUM Temporal workflows and activities.

package temporal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"

	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/git"
	"github.com/antigravity-dev/chum/internal/graph"
	"github.com/antigravity-dev/chum/internal/matrix"
	"github.com/antigravity-dev/chum/internal/store"
)

// Activities holds dependencies for Temporal activity methods.
type Activities struct {
	Store       *store.Store
	Tiers       config.Tiers
	DAG         *graph.DAG
	Sender      matrix.Sender // Matrix notification sender (nil = disabled)
	DefaultRoom string        // Matrix room ID for standard notifications
	AdminRoom   string        // Matrix room ID for critical escalations (DM)
	TurtleRoom  string        // Matrix room for turtle deliberation (3-agent conversation)
}

// WorktreeDir returns the worktree directory path for a task/explosion pair.
// Uses os.TempDir() so paths are portable across systems with different $TMPDIR.
func WorktreeDir(taskID, explosionID string) string {
	if explosionID != "" {
		return filepath.Join(os.TempDir(), fmt.Sprintf("chum-wt-%s-%s", taskID, explosionID))
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("chum-wt-%s", taskID))
}

// StructuredPlanActivity generates a structured plan from a task prompt.
// The plan is gated — it must pass Validate() to enter the coding engine.
func (a *Activities) StructuredPlanActivity(ctx context.Context, req TaskRequest) (*StructuredPlan, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(SharkPrefix+" Generating structured plan", "Agent", req.Agent, "TaskID", req.TaskID)

	// Inject species genome — the accumulated evolutionary memory.
	// The genome IS the real product; the plan is just metabolism.
	var genomeContext string
	if a.Store != nil {
		species := classifySpecies(req.TaskID, req.Prompt, nil)
		if genome, err := a.Store.GetGenomeForPrompt(species); err == nil && genome != "" {
			genomeContext = "\n" + genome + "\n"
			logger.Info(SharkPrefix+" Genome injected into planning prompt", "Species", species)
		}
	}

	semgrepContext := loadSemgrepContext(req.WorkDir)
	if semgrepContext != "" {
		logger.Info(SharkPrefix+" Semgrep gates injected into planning prompt")
	}

	// Inject previous failures — the scent of death.
	// A new shark is born with the memory of the one that came before.
	var failureContext string
	if len(req.PreviousErrors) > 0 {
		var validErrors []string
		for _, errStr := range req.PreviousErrors {
			if strings.TrimSpace(errStr) != "" {
				validErrors = append(validErrors, errStr)
			}
		}
		if len(validErrors) > 0 {
			failureContext = "\nPREVIOUS FAILURES (avoid these mistakes):\n---\n" + strings.Join(validErrors, "\n---\n") + "\n"
			logger.Info(SharkPrefix+" Previous failures injected into planning prompt", "Count", len(validErrors))
		}
	}

	prompt := fmt.Sprintf(`You are a senior engineering planner. Analyze this task and produce a structured execution plan.

TASK: %s
%s%s%s
OUTPUT FORMAT: You MUST respond with ONLY a JSON object (no markdown, no commentary) with this exact structure:
{
  "summary": "one-line summary of the task",
  "steps": [{"description": "what to do", "file": "which file", "rationale": "why"}],
  "files_to_modify": ["file1.go", "file2.go"],
  "acceptance_criteria": ["criterion 1", "criterion 2"],
  "estimated_complexity": "low|medium|high",
  "risk_assessment": "what could go wrong"
}

Be thorough. Planning space is cheap — implementation is expensive.`, req.Prompt, genomeContext, semgrepContext, failureContext)

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
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("agent did not produce valid JSON plan. Output:\n%s", truncate(cliResult.Output, 500)),
			"PLAN_NO_JSON", nil)
	}

	plan, err := flexUnmarshalPlan(jsonStr)
	if err != nil {
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("failed to parse plan JSON: %v\nRaw: %s", err, truncate(jsonStr, 500)),
			"PLAN_PARSE_FAILED", err)
	}
	plan.TokenUsage = cliResult.Tokens

	// Gate: validate plan before it enters the coding engine
	if issues := plan.Validate(); len(issues) > 0 {
		logger.Warn(SharkPrefix+" Plan validation failed — orca killing shark",
			"TaskID", req.TaskID,
			"Agent", req.Agent,
			"RawJSON", truncate(jsonStr, 1000),
			"Issues", issues)
		// Non-retryable: the agent's JSON schema is wrong, retrying won't fix it.
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("plan failed quality gate (orca kill):\n- %s\nRaw JSON: %s", strings.Join(issues, "\n- "), truncate(jsonStr, 500)),
			"PLAN_QUALITY_GATE", nil)
	}

	logger.Info(SharkPrefix+" Plan generated and validated",
		"Summary", plan.Summary,
		"Steps", len(plan.Steps),
		"Files", len(plan.FilesToModify),
		"Criteria", len(plan.AcceptanceCriteria),
	)

	return plan, nil
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

	// Record DoD result
	if err := a.Store.RecordDoDResult(dispatchID, outcome.TaskID, outcome.Project, outcome.DoDPassed, outcome.DoDFailures, ""); err != nil {
		logger.Error(OrcaPrefix+" Failed to record DoD result", "error", err)
	}

	// Close task in DAG when DoD passes — this ungates downstream dependencies.
	// When DoD fails, the task stays "ready" — the organism dies but the substrate
	// persists for the next attempt. Task mortality never ungates dependencies.
	if outcome.DoDPassed && a.DAG != nil {
		if err := a.DAG.CloseTask(ctx, outcome.TaskID); err != nil {
			logger.Error(OrcaPrefix+" Failed to close task in DAG", "error", err, "TaskID", outcome.TaskID)
		} else {
			logger.Info(OrcaPrefix+" Task closed in DAG — downstream dependencies ungated",
				"TaskID", outcome.TaskID, "Project", outcome.Project)
		}
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
// Sends a detailed escalation message to the Matrix admin room for the Hex agent to review.
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

	// Send escalation to Matrix admin room for Hex agent to review and act on.
	if a.Sender != nil && a.AdminRoom != "" {
		// Build a concise failure summary (last 3 failures max)
		failureSummary := escalation.Failures
		if len(failureSummary) > 3 {
			failureSummary = failureSummary[len(failureSummary)-3:]
		}

		msg := fmt.Sprintf(
			"🚨 **ESCALATION REQUIRED** — `%s` (`%s`)\n\n"+
				"All %d attempts exhausted across the entire escalation chain.\n\n"+
				"**Recent failures:**\n```\n%s\n```\n\n"+
				"**Plan summary:** %s\n\n"+
				"**Action needed:** This task likely requires human intervention "+
				"(e.g. manual credential rotation, external service config, infrastructure change). "+
				"Please review the failures above, diagnose the root cause, and either:\n"+
				"1. Fix the underlying issue and re-queue the morsel\n"+
				"2. Close the morsel if it's no longer relevant\n"+
				"3. Decompose it into smaller tasks that the sharks can handle",
			escalation.TaskID,
			escalation.Project,
			escalation.AttemptCount,
			strings.Join(failureSummary, "\n"),
			truncate(escalation.PlanSummary, 200),
		)

		if sendErr := a.Sender.SendMessage(ctx, a.AdminRoom, msg); sendErr != nil {
			logger.Warn(OrcaPrefix+" Failed to send escalation to Matrix", "error", sendErr)
		} else {
			logger.Info(OrcaPrefix+" Escalation sent to Matrix admin room", "task", escalation.TaskID)
		}
	}

	return nil
}

// CloseTaskActivity marks a task as closed in the graph DAG. Called when the
// workflow completes (success or failure). A closed task never re-enters the
// dispatcher queue. New work = new morsel. The ocean does not allow weakness.
func (a *Activities) CloseTaskActivity(ctx context.Context, taskID, finalStatus string) error {
	logger := activity.GetLogger(ctx)
	logger.Info(SharkPrefix+" Closing task in DAG", "TaskID", taskID, "FinalStatus", finalStatus)

	if a.DAG == nil {
		logger.Warn(SharkPrefix + " No DAG configured, skipping task closure")
		return nil
	}

	// Update status to the final status (completed/failed/escalated)
	if err := a.DAG.UpdateTask(ctx, taskID, map[string]any{"status": finalStatus}); err != nil {
		logger.Warn(SharkPrefix+" Failed to update task status (best-effort)", "error", err)
		// Don't fail the workflow over this — best-effort closure
	}

	return nil
}

// RecordHealthEventActivity records a health event to the store from within a
// workflow. This makes crabs, grooming, and other workflows visible to the
// octopus and stingray observability system.
func (a *Activities) RecordHealthEventActivity(ctx context.Context, eventType, details string) error {
	if a.Store == nil {
		return nil
	}
	return a.Store.RecordHealthEvent(eventType, details)
}

// --- helpers ---

// extractJSON finds the first JSON object in text (handles markdown code fences).
// Applies sanitizeJSON to fix common LLM output issues (invalid escapes, trailing commas).
func extractJSON(text string) string {
	var raw string

	// Try to find JSON between code fences first
	if idx := strings.Index(text, "```json"); idx >= 0 {
		start := idx + 7
		if end := strings.Index(text[start:], "```"); end >= 0 {
			raw = strings.TrimSpace(text[start : start+end])
		}
	}
	if raw == "" {
		if idx := strings.Index(text, "```"); idx >= 0 {
			start := idx + 3
			// Skip optional language tag on same line
			if nl := strings.Index(text[start:], "\n"); nl >= 0 {
				start += nl + 1
			}
			if end := strings.Index(text[start:], "```"); end >= 0 {
				candidate := strings.TrimSpace(text[start : start+end])
				if candidate != "" && candidate[0] == '{' {
					raw = candidate
				}
			}
		}
	}

	if raw == "" {
		// Try to find raw JSON object
		start := strings.Index(text, "{")
		if start < 0 {
			return ""
		}
		// Find matching closing brace (skip braces inside strings)
		depth := 0
		inString := false
		escaped := false
		for i := start; i < len(text); i++ {
			ch := text[i]
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' && inString {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = !inString
				continue
			}
			if inString {
				continue
			}
			switch ch {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					raw = text[start : i+1]
					break
				}
			}
			if raw != "" {
				break
			}
		}
	}

	if raw == "" {
		return ""
	}
	return sanitizeJSON(raw)
}

// sanitizeJSON fixes common issues in LLM-generated JSON that cause
// json.Unmarshal to fail:
//  1. Invalid backslash escapes (\n literal outside strings → \\n)
//  2. Trailing commas before } or ] (common LLM mistake)
//  3. Unescaped control characters in string values
func sanitizeJSON(s string) string {
	// First, try to parse as-is — fast path for valid JSON.
	if json.Valid([]byte(s)) {
		return s
	}

	// Walk through the string and fix issues inside JSON string values.
	var buf strings.Builder
	buf.Grow(len(s))
	inString := false
	i := 0
	for i < len(s) {
		ch := s[i]
		if !inString {
			if ch == '"' {
				inString = true
			}
			buf.WriteByte(ch)
			i++
			continue
		}
		// Inside a JSON string value
		if ch == '"' {
			// End of string
			inString = false
			buf.WriteByte(ch)
			i++
			continue
		}
		if ch == '\\' && i+1 < len(s) {
			next := s[i+1]
			// Valid JSON escapes: " \ / b f n r t u
			switch next {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't', 'u':
				buf.WriteByte(ch)
				buf.WriteByte(next)
				i += 2
				continue
			default:
				// Invalid escape — double-escape the backslash
				buf.WriteString("\\\\")
				i++
				continue
			}
		}
		// Control characters (0x00-0x1F) must be escaped in JSON strings
		if ch < 0x20 {
			switch ch {
			case '\n':
				buf.WriteString("\\n")
			case '\r':
				buf.WriteString("\\r")
			case '\t':
				buf.WriteString("\\t")
			default:
				buf.WriteString(fmt.Sprintf("\\u%04x", ch))
			}
			i++
			continue
		}
		buf.WriteByte(ch)
		i++
	}

	result := buf.String()

	// Fix trailing commas: ,} → } and ,] → ]
	result = trailingCommaRe.ReplaceAllString(result, "$1")

	return result
}

var trailingCommaRe = regexp.MustCompile(`,\s*([}\]])`)

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// SetupWorktreeActivity creates an isolated git worktree for this shark organism.
// Each organism gets its own workspace so concurrent sharks don't compete for
// build locks, .next/ directories, or other stateful artifacts.
// Returns the absolute path to the worktree directory.
func (a *Activities) SetupWorktreeActivity(ctx context.Context, baseDir, taskID, explosionID string) (string, error) {
	logger := activity.GetLogger(ctx)

	// Worktree path: $TMPDIR/chum-wt-{taskID}[-{explosionID}] (unique per organism)
	wtDir := WorktreeDir(taskID, "")
	branch := fmt.Sprintf("chum/%s", taskID)
	if explosionID != "" {
		wtDir = WorktreeDir(taskID, explosionID)
		branch = fmt.Sprintf("chum/%s-%s", taskID, explosionID)
	}

	logger.Info(SharkPrefix+" Setting up worktree", "base", baseDir, "worktree", wtDir, "branch", branch)

	// Remove stale worktree if exists (from a dead organism)
	rmCmd := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", wtDir)
	rmCmd.Dir = baseDir
	if err := rmCmd.Run(); err != nil {
		logger.Debug(SharkPrefix+" No stale worktree to remove", "worktree", wtDir, "error", err)
	}

	// Delete stale branch if exists
	delBranch := exec.CommandContext(ctx, "git", "branch", "-D", branch)
	delBranch.Dir = baseDir
	if err := delBranch.Run(); err != nil {
		logger.Debug(SharkPrefix+" No stale branch to delete", "branch", branch, "error", err)
	}

	// Create fresh worktree with a new branch from HEAD
	addCmd := exec.CommandContext(ctx, "git", "worktree", "add", "-b", branch, wtDir)
	addCmd.Dir = baseDir
	if out, err := addCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git worktree add failed: %w\n%s", err, string(out))
	}

	// If the base project has node_modules but the worktree doesn't, symlink them.
	// This avoids expensive `npm install` per organism.
	nmSrc := baseDir + "/node_modules"
	nmDst := wtDir + "/node_modules"
	if _, err := exec.LookPath("node"); err == nil {
		// Check if package.json exists and node_modules doesn't in worktree
		pkgCmd := exec.CommandContext(ctx, "test", "-f", wtDir+"/package.json")
		nmCheck := exec.CommandContext(ctx, "test", "-d", nmDst)
		if pkgCmd.Run() == nil && nmCheck.Run() != nil {
			// Copy node_modules instead of symlinking to avoid Next.js Turbopack symlink-out-of-root errors
			cpCmd := exec.CommandContext(ctx, "cp", "-a", nmSrc, nmDst)
			if cpCmd.Run() != nil {
				// Symlink failed, install fresh
				installCmd := exec.CommandContext(ctx, "npm", "install", "--prefer-offline")
				installCmd.Dir = wtDir
				if err := installCmd.Run(); err != nil {
					logger.Warn(SharkPrefix+" npm install failed", "worktree", wtDir, "error", err)
				}
			}
		}
	}

	// Copy .env* files from base repo — git worktrees don't include .gitignore'd files.
	// Without .env.local, Next.js/Supabase builds fail because NEXT_PUBLIC_* vars are missing at build time.
	envGlob, _ := filepath.Glob(filepath.Join(baseDir, ".env*"))
	for _, envFile := range envGlob {
		baseName := filepath.Base(envFile)
		dst := filepath.Join(wtDir, baseName)
		// Only copy if not already present (don't overwrite if the branch has its own)
		if _, err := os.Stat(dst); err != nil {
			src, readErr := os.ReadFile(envFile)
			if readErr == nil {
				if writeErr := os.WriteFile(dst, src, 0600); writeErr == nil {
					logger.Info(SharkPrefix+" Copied env file to worktree", "file", baseName)
				}
			}
		}
	}

	logger.Info(SharkPrefix+" Worktree ready", "path", wtDir)
	return wtDir, nil
}

// CleanupWorktreeActivity removes the git worktree after the organism completes.
// Called at both success and failure paths — organisms are mortal.

// flexUnmarshalPlan tries to parse agent output into a StructuredPlan.
// Handles multiple agent output formats:
//  1. Direct JSON: {"summary":"...", "steps":[...]} — codex does this
//  2. Gemini envelope: {"session_id":"...", "response":"{\"summary\":\"...\"}"} —
//     gemini wraps the plan in a session envelope where "response" is a JSON *string*
//  3. camelCase keys: {"filesToModify":[...]} — normalize to snake_case
func flexUnmarshalPlan(jsonStr string) (*StructuredPlan, error) {
	var plan StructuredPlan
	if err := json.Unmarshal([]byte(jsonStr), &plan); err != nil {
		return nil, err
	}

	// If standard unmarshal populated fields, we're done (codex path).
	if plan.Summary != "" || len(plan.Steps) > 0 || len(plan.AcceptanceCriteria) > 0 || len(plan.FilesToModify) > 0 {
		return &plan, nil
	}

	// Standard unmarshal gave us empty fields. Check for gemini envelope.
	var envelope struct {
		SessionID string `json:"session_id"`
		Response  string `json:"response"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &envelope); err == nil && envelope.Response != "" {
		// Gemini wraps plan as a JSON string inside "response".
		// The response value is the actual plan JSON.
		var innerPlan StructuredPlan
		if err := json.Unmarshal([]byte(envelope.Response), &innerPlan); err == nil {
			if innerPlan.Summary != "" || len(innerPlan.Steps) > 0 {
				return &innerPlan, nil
			}
		}
		// Inner parse failed or still empty — try key normalization on inner JSON
		if normalized, err := normalizeJSONKeys(envelope.Response); err == nil {
			var innerPlan2 StructuredPlan
			if err := json.Unmarshal(normalized, &innerPlan2); err == nil {
				if innerPlan2.Summary != "" || len(innerPlan2.Steps) > 0 {
					return &innerPlan2, nil
				}
			}
		}
	}

	// Last resort: try key normalization on the original JSON.
	if normalized, err := normalizeJSONKeys(jsonStr); err == nil {
		var plan2 StructuredPlan
		if err := json.Unmarshal(normalized, &plan2); err == nil {
			return &plan2, nil
		}
	}

	return &plan, nil // return the empty plan — Validate() will catch it
}

// normalizeJSONKeys converts camelCase keys to snake_case for known plan fields.
func normalizeJSONKeys(jsonStr string) ([]byte, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return nil, err
	}

	keyMap := map[string]string{
		"filesToModify":       "files_to_modify",
		"acceptanceCriteria":  "acceptance_criteria",
		"estimatedComplexity": "estimated_complexity",
		"riskAssessment":      "risk_assessment",
		"previousErrors":      "previous_errors",
	}

	normalized := make(map[string]json.RawMessage, len(raw))
	for k, v := range raw {
		if mapped, ok := keyMap[k]; ok {
			normalized[mapped] = v
		} else {
			normalized[k] = v
		}
	}

	return json.Marshal(normalized)
}

// PushWorktreeActivity pushes the organism's code branch to the remote origin.
func (a *Activities) PushWorktreeActivity(ctx context.Context, wtDir string) error {
	logger := activity.GetLogger(ctx)
	logger.Info(SharkPrefix+" Pushing worktree branch to remote", "worktree", wtDir)

	pushCmd := exec.CommandContext(ctx, "git", "push", "origin", "HEAD")
	pushCmd.Dir = wtDir
	if out, err := pushCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git push origin HEAD failed: %w\n%s", err, string(out))
	}

	logger.Info(SharkPrefix+" Worktree branch pushed successfully")
	return nil
}

// MergeToMainActivity squash-merges a feature branch into the base branch (default: main),
// pushes the result, and cleans up the feature branch (local + remote).
// If a merge conflict is detected, returns git.ErrMergeConflict so the workflow can escalate.
func (a *Activities) MergeToMainActivity(ctx context.Context, baseDir, featureBranch, taskSummary string) error {
	logger := activity.GetLogger(ctx)
	logger.Info(SharkPrefix+" Merging feature branch into main",
		"baseDir", baseDir, "featureBranch", featureBranch)

	// Pull latest main to minimize stale-base conflicts.
	pullCmd := exec.CommandContext(ctx, "git", "checkout", "main")
	pullCmd.Dir = baseDir
	if out, err := pullCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("checkout main failed: %w\n%s", err, string(out))
	}

	pullOrigin := exec.CommandContext(ctx, "git", "pull", "--rebase", "origin", "main")
	pullOrigin.Dir = baseDir
	if out, err := pullOrigin.CombinedOutput(); err != nil {
		logger.Warn(SharkPrefix+" pull --rebase origin main failed (proceeding anyway)", "error", err, "output", string(out))
	}

	// Squash-merge the feature branch into main.
	if err := git.MergeBranchIntoBase(baseDir, featureBranch, "main", "squash"); err != nil {
		if errors.Is(err, git.ErrMergeConflict) {
			logger.Warn(SharkPrefix+" Merge conflict detected — escalating",
				"featureBranch", featureBranch, "error", err)
			return err
		}
		return fmt.Errorf("merge failed: %w", err)
	}

	// The squash merge stages changes but git.MergeBranchIntoBase already commits
	// for squash strategy. Push main to remote.
	pushCmd := exec.CommandContext(ctx, "git", "push", "origin", "main")
	pushCmd.Dir = baseDir
	if out, err := pushCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("push main failed: %w\n%s", err, string(out))
	}

	logger.Info(SharkPrefix+" Feature branch merged into main and pushed", "featureBranch", featureBranch)

	// Cleanup: delete the local and remote feature branch.
	delLocal := exec.CommandContext(ctx, "git", "branch", "-D", featureBranch)
	delLocal.Dir = baseDir
	if err := delLocal.Run(); err != nil {
		logger.Debug(SharkPrefix+" Failed to delete local branch (best-effort)", "branch", featureBranch, "error", err)
	}

	delRemote := exec.CommandContext(ctx, "git", "push", "origin", "--delete", featureBranch)
	delRemote.Dir = baseDir
	if err := delRemote.Run(); err != nil {
		logger.Debug(SharkPrefix+" Failed to delete remote branch (best-effort)", "branch", featureBranch, "error", err)
	}

	return nil
}

// ExplosionCandidate holds data about a single explosion candidate for senior review.
type ExplosionCandidate struct {
	Provider    string
	ExplosionID string
	Diff        string // git diff output
	ElapsedS    float64
}

// ReviewExplosionCandidatesActivity uses a senior model to compare multiple DoD-passing
// implementations and pick the best one. Returns the index of the winner (0-based).
func (a *Activities) ReviewExplosionCandidatesActivity(ctx context.Context, taskID string, candidates []ExplosionCandidate) (int, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(SharkPrefix+" Senior review of explosion candidates",
		"TaskID", taskID, "Candidates", len(candidates))

	if len(candidates) == 1 {
		return 0, nil // Only one candidate — it wins by default
	}

	// Build a comparison prompt with all diffs
	var promptBuilder strings.Builder
	promptBuilder.WriteString(fmt.Sprintf("You are a senior engineering lead. %d AI agents all attempted the same task and passed their build checks.\n", len(candidates)))
	promptBuilder.WriteString("Compare their implementations and pick the BEST one.\n\n")
	promptBuilder.WriteString("Evaluate on: code quality, simplicity, correctness, maintainability, and efficiency.\n\n")

	for i, c := range candidates {
		promptBuilder.WriteString(fmt.Sprintf("=== CANDIDATE %d: %s (completed in %.0fs) ===\n", i+1, c.Provider, c.ElapsedS))
		diff := c.Diff
		if len(diff) > 4000 {
			diff = diff[:4000] + "\n... [truncated]"
		}
		promptBuilder.WriteString(diff)
		promptBuilder.WriteString("\n\n")
	}

	promptBuilder.WriteString(`Respond with ONLY a JSON object:
{
  "winner": <1-based candidate number>,
  "rationale": "brief explanation of why this implementation is best",
  "patterns": {
    "good": ["pattern 1 from winner", "pattern 2"],
    "bad": ["anti-pattern from losers", "issue found"]
  }
}`)

	// Use gemini as the senior reviewer for explosion comparison
	reviewer := "gemini"
	cliResult, err := runReviewAgent(ctx, reviewer, promptBuilder.String(), "")
	if err != nil {
		logger.Warn(SharkPrefix+" Senior review failed — falling back to fastest candidate", "error", err)
		return 0, nil // Fall back to first candidate (fastest)
	}

	jsonStr := extractJSON(cliResult.Output)
	if jsonStr == "" {
		logger.Warn(SharkPrefix+" Senior review output was not valid JSON — falling back to fastest")
		return 0, nil
	}

	// Parse the winner index
	var reviewResult struct {
		Winner    int      `json:"winner"`
		Rationale string   `json:"rationale"`
		Patterns  struct {
			Good []string `json:"good"`
			Bad  []string `json:"bad"`
		} `json:"patterns"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &reviewResult); err != nil {
		logger.Warn(SharkPrefix+" Failed to parse review JSON", "error", err)
		return 0, nil
	}

	winnerIdx := reviewResult.Winner - 1 // convert 1-based to 0-based
	if winnerIdx < 0 || winnerIdx >= len(candidates) {
		logger.Warn(SharkPrefix+" Invalid winner index from reviewer", "winner", reviewResult.Winner)
		return 0, nil
	}

	logger.Info(SharkPrefix+" Senior review complete",
		"Winner", candidates[winnerIdx].Provider,
		"Rationale", reviewResult.Rationale,
		"GoodPatterns", len(reviewResult.Patterns.Good),
		"BadPatterns", len(reviewResult.Patterns.Bad))

	return winnerIdx, nil
}

// GetWorktreeDiffActivity returns the git diff of a worktree against its base branch.
// Used by the explosion workflow to get diffs for senior reviewer comparison.
func (a *Activities) GetWorktreeDiffActivity(ctx context.Context, wtDir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "main", "--stat", "--patch")
	cmd.Dir = wtDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git diff in %s failed: %w\n%s", wtDir, err, string(out))
	}
	diff := string(out)
	if len(diff) > 8000 {
		diff = diff[:8000] + "\n... [truncated]"
	}
	return diff, nil
}

func (a *Activities) CleanupWorktreeActivity(ctx context.Context, baseDir, wtDir string) error {
	logger := activity.GetLogger(ctx)
	logger.Info(SharkPrefix+" Cleaning up worktree", "worktree", wtDir)

	// Detect the branch name before removal so we can cleanup remote.
	branchCmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	branchCmd.Dir = wtDir
	branchOut, _ := branchCmd.Output()
	branchName := strings.TrimSpace(string(branchOut))

	// Remove the worktree
	rmCmd := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", wtDir)
	rmCmd.Dir = baseDir
	if out, err := rmCmd.CombinedOutput(); err != nil {
		logger.Warn(SharkPrefix+" Worktree removal failed (best-effort)", "error", err, "output", string(out))
	}

	// Prune stale worktree entries
	pruneCmd := exec.CommandContext(ctx, "git", "worktree", "prune")
	pruneCmd.Dir = baseDir
	if err := pruneCmd.Run(); err != nil {
		logger.Warn(SharkPrefix+" git worktree prune failed", "error", err)
	}

	// Delete the remote branch to prevent stale chum/* branches accumulating.
	if branchName != "" && branchName != "HEAD" && strings.HasPrefix(branchName, "chum/") {
		delRemote := exec.CommandContext(ctx, "git", "push", "origin", "--delete", branchName)
		delRemote.Dir = baseDir
		if err := delRemote.Run(); err != nil {
			logger.Debug(SharkPrefix+" Failed to delete remote branch (best-effort)", "branch", branchName, "error", err)
		}

		delLocal := exec.CommandContext(ctx, "git", "branch", "-D", branchName)
		delLocal.Dir = baseDir
		if err := delLocal.Run(); err != nil {
			logger.Debug(SharkPrefix+" Failed to delete local branch (best-effort)", "branch", branchName, "error", err)
		}
	}

	return nil
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

// EscalationEvent is the payload for recording a tier escalation.
type EscalationEvent struct {
	MorselID       string
	Project        string
	FailedProvider string
	FailedTier     string
	EscalatedTo    string
	EscalatedTier  string
}

// RecordEscalationActivity persists an escalation event to the store.
func (a *Activities) RecordEscalationActivity(ctx context.Context, evt EscalationEvent) error {
	if a.Store == nil {
		return nil
	}
	return a.Store.RecordEscalation(store.ProviderEscalation{
		MorselID:        evt.MorselID,
		Project:         evt.Project,
		FailedProvider:  evt.FailedProvider,
		FailedTier:      evt.FailedTier,
		FailureReason:   "exhausted_retries",
		EscalatedTo:     evt.EscalatedTo,
		EscalatedTier:   evt.EscalatedTier,
		EscalatedResult: "pending",
	})
}

// RecordFailureActivity persists failure scent (errors) to the task's error_log.
// Future sharks inheriting this task will use this context to avoid previous mistakes.
func (a *Activities) RecordFailureActivity(ctx context.Context, taskID string, failures []string) error {
	logger := activity.GetLogger(ctx)
	if len(failures) == 0 {
		return nil
	}

	scent := strings.Join(failures, "\n---\n")
	logger.Info(OrcaPrefix+" Recording failure scent", "TaskID", taskID, "Errors", len(failures))

	if a.DAG == nil {
		logger.Warn(OrcaPrefix + " No DAG configured, cannot record failure scent")
		return nil
	}

	// Update the task's error_log field.
	return a.DAG.UpdateTask(ctx, taskID, map[string]any{
		"error_log": scent,
	})
}

// AutoFixResult holds the outcome of an automatic lint-fix pass.
type AutoFixResult struct {
	FilesFixed int      `json:"files_fixed"`
	ToolsRun   []string `json:"tools_run"`
	Output     string   `json:"output"`
}

// AutoFixLintActivity runs deterministic auto-fix tools (gofmt, goimports)
// to clean up common formatting issues before the next agent retry.
// This is cheap and avoids wasting a retry on trivially fixable lint errors.
func (a *Activities) AutoFixLintActivity(ctx context.Context, workDir string) (*AutoFixResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(OrcaPrefix+" Running auto-fix lint", "workDir", workDir)

	result := &AutoFixResult{}
	var allOutput strings.Builder

	// List of auto-fix tools to run. Each is: [command, description].
	// Only run tools that are installed and applicable.
	fixTools := []struct {
		cmd  string
		args []string
		name string
	}{
		{"gofmt", []string{"-w", "."}, "gofmt"},
		{"goimports", []string{"-w", "."}, "goimports"},
	}

	for _, tool := range fixTools {
		path, err := exec.LookPath(tool.cmd)
		if err != nil {
			continue // tool not installed, skip
		}
		_ = path

		cmd := exec.CommandContext(ctx, tool.cmd, tool.args...)
		cmd.Dir = workDir
		out, err := cmd.CombinedOutput()
		allOutput.WriteString(fmt.Sprintf("--- %s ---\n%s\n", tool.name, string(out)))
		if err != nil {
			logger.Warn(OrcaPrefix+" Auto-fix tool failed (non-fatal)", "tool", tool.name, "error", err)
			continue
		}
		result.ToolsRun = append(result.ToolsRun, tool.name)
	}

	// Count files changed by git
	cmd := exec.CommandContext(ctx, "git", "diff", "--name-only")
	cmd.Dir = workDir
	diffOut, err := cmd.Output()
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(diffOut)), "\n")
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				result.FilesFixed++
			}
		}
	}

	// Stage and commit auto-fixes if any
	if result.FilesFixed > 0 {
		stageCmd := exec.CommandContext(ctx, "git", "add", "-A")
		stageCmd.Dir = workDir
		if err := stageCmd.Run(); err != nil {
			logger.Warn(OrcaPrefix+" git add failed during auto-fix", "error", err)
		} else {
			commitCmd := exec.CommandContext(ctx, "git", "commit", "-m", "chore: auto-fix formatting (gofmt/goimports)", "--no-verify")
			commitCmd.Dir = workDir
			if err := commitCmd.Run(); err != nil {
				logger.Warn(OrcaPrefix+" git commit failed during auto-fix", "error", err)
			}
		}
	}

	result.Output = allOutput.String()
	return result, nil
}

// loadSemgrepContext reads .semgrep/*.yml rules from the workspace and formats
// them for prompt injection. The organism sees the environment it must survive in.
// A system optimizing for survival doesn't set organisms up for failure.
func loadSemgrepContext(workDir string) string {
	semgrepDir := filepath.Join(workDir, ".semgrep")
	entries, err := os.ReadDir(semgrepDir)
	if err != nil || len(entries) == 0 {
		return ""
	}

	var rules []string
	totalLen := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yml" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(semgrepDir, entry.Name()))
		if err != nil {
			continue
		}
		rule := string(data)
		if totalLen+len(rule) > 2000 {
			break // cap context to avoid prompt bloat
		}
		rules = append(rules, rule)
		totalLen += len(rule)
	}

	if len(rules) == 0 {
		return ""
	}

	result := "\nENFORCED CODE RULES (your output will be scanned by these — plan accordingly):\n"
	for _, r := range rules {
		result += "---\n" + r + "\n"
	}
	return result
}

// ===== PALEONTOLOGIST ACTIVITIES =====

// AnalyzeProviderFitnessActivity queries dispatch outcomes and updates genome
// provider_genes where success rates have shifted significantly.
// Returns the number of genome mutations applied.
func (a *Activities) AnalyzeProviderFitnessActivity(ctx context.Context, req PaleontologistRequest) (int, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(PaleontologistPrefix+" Analyzing provider fitness")

	since := time.Now().UTC().Add(-time.Duration(req.LookbackH) * time.Hour)
	rates, err := a.Store.GetProviderSuccessRates(since)
	if err != nil {
		return 0, fmt.Errorf("get provider success rates: %w", err)
	}

	mutations := 0
	for _, rate := range rates {
		if rate.Successes+rate.Failures < 2 {
			continue // not enough data to be meaningful
		}

		genome, err := a.Store.GetGenome(rate.Species)
		if err != nil || genome == nil {
			continue
		}

		// Check if the provider's fitness has shifted significantly.
		// If success rate < 30% and the genome still prefers this provider, evolve.
		if rate.SuccessRate < 0.3 && rate.Failures >= 3 {
			entry := store.GenomeEntry{
				Pattern:     fmt.Sprintf("Provider %s has %.0f%% success rate on this species", rate.Provider, rate.SuccessRate*100),
				Alternative: "Consider using a different provider for this species type",
				Agent:       "paleontologist",
			}
			if err := a.Store.EvolveGenomeWithCost(rate.Species, false, entry, rate.Provider, rate.AvgCostUSD); err != nil {
				logger.Warn(PaleontologistPrefix+" Failed to evolve genome", "species", rate.Species, "error", err)
			} else {
				mutations++
				logger.Info(PaleontologistPrefix+" Provider fitness mutation applied",
					"Species", rate.Species, "Provider", rate.Provider, "SuccessRate", rate.SuccessRate)
			}
		}
	}

	return mutations, nil
}

// DiscoverAntibodiesActivity finds recurring UBS patterns and creates genome antibodies.
// Returns the number of antibodies created.
func (a *Activities) DiscoverAntibodiesActivity(ctx context.Context, req PaleontologistRequest) (int, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(PaleontologistPrefix+" Discovering antibodies from UBS patterns")

	patterns, err := a.Store.GetRepeatingUBSPatterns(3)
	if err != nil {
		return 0, fmt.Errorf("get repeating UBS patterns: %w", err)
	}

	antibodies := 0
	for _, p := range patterns {
		entry := store.GenomeEntry{
			Pattern:     fmt.Sprintf("UBS %s: %s in %s", p.RuleID, p.Message, p.FilePath),
			Alternative: fmt.Sprintf("Check %s — this pattern has appeared %d times", p.FilePath, p.Count),
			Count:       p.Count,
			Agent:       "paleontologist",
			Files:       []string{p.FilePath},
		}
		if err := a.Store.EvolveGenomeWithCost(p.Species, false, entry, "", 0); err != nil {
			logger.Warn(PaleontologistPrefix+" Failed to create antibody", "species", p.Species, "error", err)
		} else {
			antibodies++
			logger.Info(PaleontologistPrefix+" Antibody created",
				"Species", p.Species, "RuleID", p.RuleID, "Count", p.Count)
		}
	}

	return antibodies, nil
}

// ScanProteinCandidatesActivity finds species ready for proteinisation.
// Returns the number of proteins nominated.
func (a *Activities) ScanProteinCandidatesActivity(ctx context.Context, req PaleontologistRequest) (int, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(PaleontologistPrefix+" Scanning for proteinisation candidates")

	candidates, err := a.Store.GetProteinCandidates(5)
	if err != nil {
		return 0, fmt.Errorf("get protein candidates: %w", err)
	}

	nominated := 0
	for _, c := range candidates {
		if c.HasProtein {
			continue // already proteinised
		}

		logger.Info(PaleontologistPrefix+" Protein candidate found",
			"Species", c.Species, "Successes", c.TotalSuccesses,
			"TopPattern", c.TopPattern, "FittestProvider", c.FittestProvider)
		nominated++
		// Note: actual protein creation requires the CalcifyPatternActivity
		// from the learner workflow. We log the nomination here for the
		// strategic groomer to pick up and action.
	}

	return nominated, nil
}

// AuditSpeciesHealthActivity checks genomes for anomalies and takes action:
// escalating stuck species/hibernators and bootstrapping orphans.
// Returns the number of species audited.
func (a *Activities) AuditSpeciesHealthActivity(ctx context.Context, req PaleontologistRequest) (int, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(PaleontologistPrefix+" Auditing species health")

	audited := 0

	// 1. Check for stale hibernators (hibernating > 24h)
	hibernators, err := a.Store.GetStaleHibernators(24 * time.Hour)
	if err != nil {
		logger.Warn(PaleontologistPrefix+" Failed to get stale hibernators", "error", err)
	} else {
		for _, h := range hibernators {
			audited++
			logger.Info(PaleontologistPrefix+" Stale hibernator detected",
				"Species", h.Species, "Generation", h.Generation,
				"Issue", h.Issue, "Antibodies", h.AntibodyCount, "LastEvolved", h.LastEvolved)
			
			if a.Sender != nil {
				targetRoom := a.AdminRoom
				if targetRoom == "" {
					targetRoom = a.DefaultRoom
				}
				msg := fmt.Sprintf("⚠️ **Stale Hibernator Detected**\nSpecies `%s` has been hibernating for >24h. It may need higher-level LLM intervention or manual review.", h.Species)
				_ = a.Sender.SendMessage(ctx, targetRoom, msg)
			}
		}
	}

	// 2. Check for stuck species (high antibodies, 0 fossils)
	stuck, err := a.Store.GetStuckSpecies(10) // 10 antibodies threshold
	if err != nil {
		logger.Warn(PaleontologistPrefix+" Failed to get stuck species", "error", err)
	} else {
		for _, s := range stuck {
			audited++
			logger.Info(PaleontologistPrefix+" Stuck species detected",
				"Species", s.Species, "Antibodies", s.AntibodyCount)
			
			if a.Sender != nil {
				targetRoom := a.AdminRoom
				if targetRoom == "" {
					targetRoom = a.DefaultRoom
				}
				msg := fmt.Sprintf("⚠️ **Stuck Species Detected**\nSpecies `%s` has %d antibodies but 0 fossils. The agent keeps failing but cannot consolidate the learnings. Please review the failures.", s.Species, s.AntibodyCount)
				_ = a.Sender.SendMessage(ctx, targetRoom, msg)
			}
		}
	}

	// 3. Check for species without genomes (orphans)
	orphans, err := a.Store.GetSpeciesWithoutGenome()
	if err != nil {
		logger.Warn(PaleontologistPrefix+" Failed to get orphan species", "error", err)
	} else {
		for _, species := range orphans {
			audited++
			logger.Info(PaleontologistPrefix+" Bootstrapping empty genome for orphan species", "Species", species)
			if err := a.Store.CreateEmptyGenome(species); err != nil {
				logger.Warn(PaleontologistPrefix+" Failed to bootstrap genome", "species", species, "error", err)
			}
		}
	}

	return audited, nil
}

// AnalyzeCostTrendsActivity compares cost-per-success between current and previous
// time windows to detect cost spikes.
// Returns the number of cost alerts generated.
func (a *Activities) AnalyzeCostTrendsActivity(ctx context.Context, req PaleontologistRequest) (int, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(PaleontologistPrefix+" Analyzing cost trends")

	current, previous, err := a.Store.GetCostTrends(req.LookbackH)
	if err != nil {
		return 0, fmt.Errorf("get cost trends: %w", err)
	}

	// Build map of previous costs for comparison
	prevCosts := make(map[string]float64)
	for _, t := range previous {
		key := t.Provider + "/" + t.Agent
		prevCosts[key] = t.CostPerSuccess
	}

	alerts := 0
	for _, t := range current {
		key := t.Provider + "/" + t.Agent
		prevCost, hasPrev := prevCosts[key]
		if !hasPrev || prevCost <= 0 {
			continue
		}
		// Alert if cost-per-success increased by > 50%
		if t.CostPerSuccess > prevCost*1.5 {
			alerts++
			logger.Warn(PaleontologistPrefix+" Cost spike detected",
				"Provider", t.Provider, "Agent", t.Agent,
				"PrevCostPerSuccess", prevCost, "CurrentCostPerSuccess", t.CostPerSuccess,
				"Increase", fmt.Sprintf("%.0f%%", (t.CostPerSuccess/prevCost-1)*100))
		}
	}

	return alerts, nil
}

// DiscoverRecurringDoDFailuresActivity detects DoD failure patterns that appear
// across multiple dispatches and raises alerts for systemic issues.
// Returns the number of recurring failure patterns detected.
func (a *Activities) DiscoverRecurringDoDFailuresActivity(ctx context.Context, req PaleontologistRequest) (int, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(PaleontologistPrefix+" Discovering recurring DoD failures")

	since := time.Now().UTC().Add(-time.Duration(req.LookbackH) * time.Hour)
	patterns, err := a.Store.GetRecurringDoDFailures(3, since) // threshold: 3+ occurrences
	if err != nil {
		return 0, fmt.Errorf("get recurring DoD failures: %w", err)
	}

	detected := 0
	for _, p := range patterns {
		detected++

		// Truncate failure message for logging
		failureSnippet := p.Failures
		if len(failureSnippet) > 200 {
			failureSnippet = failureSnippet[:200] + "..."
		}

		logger.Warn(PaleontologistPrefix+" RECURRING DOD FAILURE DETECTED",
			"Count", p.Count,
			"Projects", strings.Join(p.Projects, ", "),
			"MorselIDs", strings.Join(p.MorselIDs, ", "),
			"FirstSeen", p.FirstSeenAt,
			"LastSeen", p.LastSeenAt,
			"Failures", failureSnippet)

		// Send Matrix alert for high-frequency failures (5+ occurrences)
		if p.Count >= 5 && a.Sender != nil {
			targetRoom := a.AdminRoom
			if targetRoom == "" {
				targetRoom = a.DefaultRoom
			}

			// Build affected morsels list (max 5 for brevity)
			morselList := p.MorselIDs
			if len(morselList) > 5 {
				morselList = append(morselList[:5], "...")
			}

			msg := fmt.Sprintf(
				"🚨 **SYSTEMIC BUILD FAILURE DETECTED** 🚨\n\n"+
					"**Pattern:** Same DoD failure across **%d morsels** in the last %dh\n\n"+
					"**Affected projects:** %s\n\n"+
					"**Affected morsels:**\n%s\n\n"+
					"**Failure:**\n```\n%s\n```\n\n"+
					"**Action required:** This is a systemic issue, not an individual morsel problem. "+
					"Investigate the root cause (e.g., broken dependency, missing env var, infrastructure issue) "+
					"before dispatching more morsels. Fix the underlying issue to unblock the pipeline.",
				p.Count,
				req.LookbackH,
				strings.Join(p.Projects, ", "),
				"- `"+strings.Join(morselList, "`\n- `")+"`",
				truncate(p.Failures, 500),
			)

			if sendErr := a.Sender.SendMessage(ctx, targetRoom, msg); sendErr != nil {
				logger.Warn(PaleontologistPrefix+" Failed to send recurring failure alert", "error", sendErr)
			} else {
				logger.Info(PaleontologistPrefix+" Recurring failure alert sent to Matrix",
					"count", p.Count, "projects", len(p.Projects))
			}
		}

		// Record health event for visibility in observability tools
		if a.Store != nil {
			details := fmt.Sprintf("Recurring DoD failure (%d occurrences): %s", p.Count, truncate(p.Failures, 200))
			if recErr := a.Store.RecordHealthEvent("recurring_dod_failure", details); recErr != nil {
				logger.Warn(PaleontologistPrefix+" Failed to record health event", "error", recErr)
			}
		}
	}

	return detected, nil
}

// AnalyzeFailureRateTrendsActivity compares current vs previous failure rates
// and maintains a "doomsday clock" that escalates warnings to Hex.
// NOT a hard gate - Hex decides whether to pause based on the clock.
func (a *Activities) AnalyzeFailureRateTrendsActivity(ctx context.Context, req PaleontologistRequest) error {
	logger := activity.GetLogger(ctx)
	logger.Info(PaleontologistPrefix+" Analyzing failure rate trends")

	// Analyze overall failure rate delta (all projects)
	delta, err := a.Store.GetFailureRateDelta("", req.LookbackH)
	if err != nil {
		return fmt.Errorf("get overall failure rate delta: %w", err)
	}

	logger.Info(PaleontologistPrefix+" Failure rate trend",
		"Trend", delta.Trend,
		"CurrentRate", fmt.Sprintf("%.1f%%", delta.CurrentRate),
		"PreviousRate", fmt.Sprintf("%.1f%%", delta.PreviousRate),
		"Delta", fmt.Sprintf("%+.1f%%", delta.Delta),
		"CurrentDispatches", delta.CurrentDispatches,
		"PreviousDispatches", delta.PreviousDispatches)

	// Record health event FIRST (feeds doomsday clock)
	if delta.Trend != "stable" {
		details := fmt.Sprintf("Failure rate %s: %.1f%% → %.1f%% (%+.1f%% points)",
			delta.Trend, delta.PreviousRate, delta.CurrentRate, delta.Delta)
		if recErr := a.Store.RecordHealthEvent("failure_rate_"+delta.Trend, details); recErr != nil {
			logger.Warn(PaleontologistPrefix+" Failed to record health event", "error", recErr)
		}
	}

	// Calculate doomsday clock (system health score)
	healthScore, err := a.Store.GetSystemHealthScore()
	if err != nil {
		logger.Warn(PaleontologistPrefix+" Failed to calculate health score", "error", err)
		healthScore = &store.SystemHealthScore{
			Score:          50,
			AlertLevel:     "yellow",
			MeteorStatus:   "Unknown",
			MeteorDistance: "🌍...........☄️",
		}
	}

	logger.Info(PaleontologistPrefix+" Meteor tracking",
		"Score", healthScore.Score,
		"AlertLevel", healthScore.AlertLevel,
		"MeteorStatus", healthScore.MeteorStatus,
		"DegradationStreak", healthScore.DegradationStreak,
		"ImprovementStreak", healthScore.ImprovementStreak)

	// Send to Hex via Matrix with escalating urgency
	if a.Sender != nil && delta.CurrentDispatches >= 10 {
		targetRoom := a.AdminRoom // Always send to Hex
		if targetRoom == "" {
			targetRoom = a.DefaultRoom
		}

		var emoji, header string
		switch healthScore.AlertLevel {
		case "green":
			emoji = "🌍"
			header = "**ECOSYSTEM THRIVING**"
		case "yellow":
			emoji = "☄️"
			header = "**METEOR DETECTED - Approaching**"
		case "orange":
			emoji = "⚠️"
			header = "**METEOR INCOMING - Impact Risk**"
		case "red":
			if healthScore.Score < 15 {
				emoji = "💥"
				header = "**☠️ EXTINCTION EVENT IN PROGRESS**"
			} else {
				emoji = "🚨"
				header = "**METEOR NEAR IMPACT - Critical**"
			}
		}

		msg := fmt.Sprintf(
			"%s %s — Meteor Tracking Report\n\n"+
				"☄️ **Meteor Status:** %s\n"+
				"📏 **Distance:** `%s`\n"+
				"📊 **Ecosystem Health:** %d/100 (%s)\n"+
				"📉 **Degradation Streak:** %d consecutive impact warnings\n"+
				"📈 **Recovery Streak:** %d consecutive improvements\n\n"+
				"**Current Species Mortality Rate:** %.1f%% (%d extinct / %d organisms)\n"+
				"**Previous Mortality Rate:** %.1f%% (%d extinct / %d organisms)\n"+
				"**Atmospheric Change:** %+.1f%% points (%s)\n\n"+
				"**Paleontologist Assessment for Hex:**\n%s\n\n"+
				"**Analysis Window:** Last %dh vs previous %dh\n"+
				"**Next Scan:** 30 minutes",
			emoji, header,
			healthScore.MeteorStatus,
			healthScore.MeteorDistance,
			healthScore.Score, healthScore.AlertLevel,
			healthScore.DegradationStreak,
			healthScore.ImprovementStreak,
			delta.CurrentRate,
			int(float64(delta.CurrentDispatches)*(delta.CurrentRate/100)),
			delta.CurrentDispatches,
			delta.PreviousRate,
			int(float64(delta.PreviousDispatches)*(delta.PreviousRate/100)),
			delta.PreviousDispatches,
			delta.Delta,
			delta.Trend,
			healthScore.Recommendation,
			req.LookbackH, req.LookbackH,
		)

		if sendErr := a.Sender.SendMessage(ctx, targetRoom, msg); sendErr != nil {
			logger.Warn(PaleontologistPrefix+" Failed to send doomsday clock report to Hex", "error", sendErr)
		} else {
			logger.Info(PaleontologistPrefix+" Meteor tracking report sent to Hex",
				"AlertLevel", healthScore.AlertLevel,
				"MeteorStatus", healthScore.MeteorStatus,
				"Score", healthScore.Score)
		}
	}

	return nil
}

// RecordPaleontologyRunActivity records a paleontologist analysis run in the audit table.
func (a *Activities) RecordPaleontologyRunActivity(ctx context.Context,
	antibodies, genes, proteins, audited, alerts, recurringFailures int, summary string) error {
	return a.Store.RecordPaleontologyRun(store.PaleontologyRunResult{
		AntibodiesDiscovered: antibodies,
		GenesMutated:         genes,
		ProteinsNominated:    proteins,
		SpeciesAudited:       audited,
		CostAlerts:           alerts,
		RecurringFailures:    recurringFailures,
		Summary:              summary,
	})
}

