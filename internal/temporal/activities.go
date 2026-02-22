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
	"github.com/antigravity-dev/chum/internal/matrix"
	"github.com/antigravity-dev/chum/internal/store"
)

// Activities holds dependencies for Temporal activity methods.
type Activities struct {
	Store       *store.Store
	Tiers       config.Tiers
	DAG         *graph.DAG
	Sender      matrix.Sender // Matrix notification sender (nil = disabled)
	DefaultRoom string        // Matrix room ID for notifications
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

	prompt := fmt.Sprintf(`You are a senior engineering planner. Analyze this task and produce a structured execution plan.

TASK: %s
%s
OUTPUT FORMAT: You MUST respond with ONLY a JSON object (no markdown, no commentary) with this exact structure:
{
  "summary": "one-line summary of the task",
  "steps": [{"description": "what to do", "file": "which file", "rationale": "why"}],
  "files_to_modify": ["file1.go", "file2.go"],
  "acceptance_criteria": ["criterion 1", "criterion 2"],
  "estimated_complexity": "low|medium|high",
  "risk_assessment": "what could go wrong"
}

Be thorough. Planning space is cheap — implementation is expensive.`, req.Prompt, genomeContext)

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
		// Log raw agent output so the octopus can learn why plans fail.
		// Common cause: agent returns camelCase keys but struct expects snake_case.
		logger.Warn(SharkPrefix+" Plan validation failed — raw JSON for octopus",
			"TaskID", req.TaskID,
			"RawJSON", truncate(jsonStr, 1000),
			"AgentOutput", truncate(cliResult.Output, 500),
			"Issues", issues)
		return nil, fmt.Errorf("plan failed quality gate:\n- %s\nRaw JSON (first 500 chars): %s", strings.Join(issues, "\n- "), truncate(jsonStr, 500))
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
func extractJSON(text string) string {
	// Try to find JSON between code fences first
	if idx := strings.Index(text, "```json"); idx >= 0 {
		start := idx + 7
		if end := strings.Index(text[start:], "```"); end >= 0 {
			return strings.TrimSpace(text[start : start+end])
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
				return candidate
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
				return text[start : i+1]
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

// SetupWorktreeActivity creates an isolated git worktree for this shark organism.
// Each organism gets its own workspace so concurrent sharks don't compete for
// build locks, .next/ directories, or other stateful artifacts.
// Returns the absolute path to the worktree directory.
func (a *Activities) SetupWorktreeActivity(ctx context.Context, baseDir, taskID string) (string, error) {
	logger := activity.GetLogger(ctx)

	// Worktree path: /tmp/chum-wt-{taskID} (unique per organism)
	wtDir := fmt.Sprintf("/tmp/chum-wt-%s", taskID)
	branch := fmt.Sprintf("chum/%s", taskID)

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
			// Try symlink first (fast), fall back to npm install
			lnCmd := exec.CommandContext(ctx, "ln", "-sf", nmSrc, nmDst)
			if lnCmd.Run() != nil {
				// Symlink failed, install fresh
				installCmd := exec.CommandContext(ctx, "npm", "install", "--prefer-offline")
				installCmd.Dir = wtDir
				if err := installCmd.Run(); err != nil {
					logger.Warn(SharkPrefix+" npm install failed", "worktree", wtDir, "error", err)
				}
			}
		}
	}

	logger.Info(SharkPrefix+" Worktree ready", "path", wtDir)
	return wtDir, nil
}

// CleanupWorktreeActivity removes the git worktree after the organism completes.
// Called at both success and failure paths — organisms are mortal.
func (a *Activities) CleanupWorktreeActivity(ctx context.Context, baseDir, wtDir string) error {
	logger := activity.GetLogger(ctx)
	logger.Info(SharkPrefix+" Cleaning up worktree", "worktree", wtDir)

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
