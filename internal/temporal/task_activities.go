package temporal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"go.temporal.io/sdk/activity"

	"github.com/antigravity-dev/chum/internal/store"
)

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

	// Classify failure for systemic pattern detection (paleontologist, genome evolution).
	if !outcome.DoDPassed && outcome.DoDFailures != "" {
		category, summary := classifyFailure(outcome.DoDFailures)
		if category != "" {
			if diagErr := a.Store.UpdateFailureDiagnosis(dispatchID, category, summary); diagErr != nil {
				logger.Error(OrcaPrefix+" Failed to record failure diagnosis", "error", diagErr)
			} else {
				logger.Info(OrcaPrefix+" Failure classified",
					"DispatchID", dispatchID,
					"Category", category,
					"Summary", summary)
			}
		}
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

// MarkMorselDoneActivity updates the morsel .md file on disk from status: ready
// to status: done and commits the change. This prevents the dispatcher from
// re-dispatching completed tasks. Non-fatal: returns nil on any failure.
func (a *Activities) MarkMorselDoneActivity(ctx context.Context, workDir, taskID string) error {
	logger := activity.GetLogger(ctx)

	morselPath := filepath.Join(workDir, ".morsels", taskID+".md")
	data, err := os.ReadFile(morselPath)
	if err != nil {
		logger.Warn(SharkPrefix+" Morsel file not found (non-fatal)", "path", morselPath, "error", err)
		return nil
	}

	// Replace status: ready with status: done in YAML frontmatter.
	// The frontmatter is delimited by --- lines at the top of the file.
	re := regexp.MustCompile(`(?m)^status:\s*ready\b.*$`)
	updated := re.ReplaceAllString(string(data), "status: done")
	if updated == string(data) {
		logger.Info(SharkPrefix+" Morsel already marked done or has non-ready status", "task", taskID)
		return nil
	}

	if err := os.WriteFile(morselPath, []byte(updated), 0o644); err != nil {
		logger.Warn(SharkPrefix+" Failed to write morsel file (non-fatal)", "path", morselPath, "error", err)
		return nil
	}

	// Git add + commit
	addCmd := exec.CommandContext(ctx, "git", "add", morselPath)
	addCmd.Dir = workDir
	if err := addCmd.Run(); err != nil {
		logger.Warn(SharkPrefix+" git add morsel failed (non-fatal)", "error", err)
		return nil
	}

	commitMsg := fmt.Sprintf("chore: mark morsel %s as done", taskID)
	commitCmd := exec.CommandContext(ctx, "git", "commit", "-m", commitMsg)
	commitCmd.Dir = workDir
	if out, err := commitCmd.CombinedOutput(); err != nil {
		logger.Warn(SharkPrefix+" git commit morsel failed (non-fatal)", "error", err, "output", string(out))
		return nil
	}

	logger.Info(SharkPrefix+" Morsel marked as done", "task", taskID)
	return nil
}

// UnblockDependentsActivity checks downstream dependents of a completed task
// and auto-promotes any whose dependencies are all satisfied from open to ready.
// It also updates the corresponding morsel .md files and commits changes.
// Non-fatal: returns nil on any failure.
func (a *Activities) UnblockDependentsActivity(ctx context.Context, workDir, completedTaskID string) ([]string, error) {
	logger := activity.GetLogger(ctx)

	if a.DAG == nil {
		logger.Warn(SharkPrefix + " DAG not configured, skipping auto-unblock")
		return nil, nil
	}

	promoted, err := a.DAG.AutoUnblockDependents(ctx, completedTaskID)
	if err != nil {
		logger.Warn(SharkPrefix+" Auto-unblock failed (non-fatal)", "task", completedTaskID, "error", err)
		return nil, nil
	}

	if len(promoted) == 0 {
		return nil, nil
	}

	// Update morsel .md files from open/blocked to ready.
	re := regexp.MustCompile(`(?m)^status:\s*(open|blocked)\b.*$`)
	gitPaths := make([]string, 0, len(promoted))
	for _, taskID := range promoted {
		morselPath := filepath.Join(workDir, ".morsels", taskID+".md")
		data, readErr := os.ReadFile(morselPath)
		if readErr != nil {
			logger.Warn(SharkPrefix+" Morsel file not found for unblock (non-fatal)", "path", morselPath)
			continue
		}
		updated := re.ReplaceAllString(string(data), "status: ready")
		if updated == string(data) {
			continue
		}
		if writeErr := os.WriteFile(morselPath, []byte(updated), 0o644); writeErr != nil {
			logger.Warn(SharkPrefix+" Failed to write unblocked morsel (non-fatal)", "path", morselPath)
			continue
		}
		gitPaths = append(gitPaths, morselPath)
	}

	// Single git commit for all unblocked morsels.
	if len(gitPaths) > 0 {
		addArgs := append([]string{"add"}, gitPaths...)
		addCmd := exec.CommandContext(ctx, "git", addArgs...)
		addCmd.Dir = workDir
		if addErr := addCmd.Run(); addErr != nil {
			logger.Warn(SharkPrefix+" git add unblocked morsels failed (non-fatal)", "error", addErr)
			return promoted, nil
		}

		commitMsg := fmt.Sprintf("chore: auto-unblock %d morsels after %s completed", len(gitPaths), completedTaskID)
		commitCmd := exec.CommandContext(ctx, "git", "commit", "-m", commitMsg)
		commitCmd.Dir = workDir
		if out, commitErr := commitCmd.CombinedOutput(); commitErr != nil {
			logger.Warn(SharkPrefix+" git commit unblock failed (non-fatal)", "error", commitErr, "output", string(out))
		}
	}

	logger.Info(SharkPrefix+" Auto-unblocked dependents", "trigger", completedTaskID, "promoted", promoted)
	return promoted, nil
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
