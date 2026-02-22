package temporal

import (
	"fmt"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	maxHandoffs = 3 // maximum cross-model review handoffs

	// defaultSlowStepThreshold is used when no config override is provided.
	defaultSlowStepThreshold = 2 * time.Minute
)

// tierForIndex maps chain index to tier name.
func tierForIndex(idx int) string {
	switch idx {
	case 0:
		return "fast"
	case 1:
		return "balanced"
	default:
		return "premium"
	}
}

// retriesForTier returns the max retry attempts per tier level.
// Cheap models get more retries; expensive models fewer.
func retriesForTier(tier string) int {
	switch strings.ToLower(tier) {
	case "fast", "":
		return 3
	case "balanced":
		return 2
	case "premium":
		return 1
	}
	return 2
}

func normalizeTaskTitle(taskID, title, prompt string) string {
	if strings.TrimSpace(title) != "" {
		return strings.TrimSpace(title)
	}
	if strings.TrimSpace(taskID) != "" {
		return strings.TrimSpace(taskID)
	}
	if strings.TrimSpace(prompt) != "" {
		return strings.TrimSpace(prompt)
	}
	return "untitled-task"
}

// ChumAgentWorkflow implements the LeSS/SCRUM loop:
//
//  1. PLAN        — StructuredPlanActivity generates a structured plan with acceptance criteria
//  2. GATE        — Human approval signal (nothing enters the coding engine un-parceled)
//  3. EXECUTE     — Primary agent implements the plan
//  4. REVIEW      — Different agent reviews (claude↔codex cross-pollination)
//  5. HANDOFF     — If review fails, swap agents and re-execute (up to 3 handoffs)
//  6. DOD         — Compile/test/lint verification via git.RunPostMergeChecks
//  7. RECORD      — Persist outcome to store (feeds learner loop)
//  8. ESCALATE    — If DoD fails after retries, escalate to chief + human
func ChumAgentWorkflow(ctx workflow.Context, req TaskRequest) error {
	startTime := workflow.Now(ctx)
	logger := workflow.GetLogger(ctx)

	slowThreshold := defaultSlowStepThreshold
	if req.SlowStepThreshold > 0 {
		slowThreshold = req.SlowStepThreshold
	}

	var stepMetrics []StepMetric
	recordStep := func(name string, stepStart time.Time, status string) {
		dur := workflow.Now(ctx).Sub(stepStart)
		slow := dur >= slowThreshold
		stepMetrics = append(stepMetrics, StepMetric{
			Name:      name,
			DurationS: dur.Seconds(),
			Status:    status,
			Slow:      slow,
		})
		if slow {
			logger.Warn(SharkPrefix+" SLOW STEP",
				"Step", name, "DurationS", dur.Seconds(), "Threshold", slowThreshold.String(), "Status", status)
		} else {
			logger.Info(SharkPrefix+" Step complete",
				"Step", name, "DurationS", dur.Seconds(), "Status", status)
		}
	}

	// Normalize once at workflow entry so every stage upsert has stable indexed fields.
	req = normalizeSearchMetadataForVisibility(req)
	if req.Reviewer == "" {
		req.Reviewer = DefaultReviewer(req.Agent)
	}

	updateSearchAttributes := func(stage string) {
		if err := upsertChumWorkflowSearchAttributes(ctx, req, stage); err != nil {
			logger.Warn(SharkPrefix+" Failed to set workflow search attributes", "stage", stage, "error", err)
		}
	}

	updateSearchAttributes(chumWorkflowStatusPlan)
	drainSignal := workflow.GetSignalChannel(ctx, ChumAgentDrainSignalName)
	resumeSignal := workflow.GetSignalChannel(ctx, ChumAgentResumeSignalName)

	isDrained := false
	awaitResumeGate := func() {
		for {
			sel := workflow.NewSelector(ctx)
			sel.AddReceive(drainSignal, func(c workflow.ReceiveChannel, _ bool) {
				var payload any
				c.Receive(ctx, &payload)
				isDrained = true
				logger.Info("received admin-drain signal; workflow will wait before next step")
			})
			sel.AddReceive(resumeSignal, func(c workflow.ReceiveChannel, _ bool) {
				var payload any
				c.Receive(ctx, &payload)
				isDrained = false
				logger.Info("received admin-resume signal; workflow resuming")
			})

			if !isDrained {
				sel.AddDefault(func() {})
			}

			sel.Select(ctx)
			if !isDrained {
				return
			}
		}
	}

	// --- Activity options ---
	planOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	}
	execOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 15 * time.Minute,
		HeartbeatTimeout:    30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1}, // no auto-retry, we handle it
	}
	reviewOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	}
	dodOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	recordOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	}
	notifyOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}

	var a *Activities

	// === WORKTREE ISOLATION ===
	// Each organism gets its own git worktree so concurrent sharks don't
	// compete for .next/lock, build artifacts, or stateful directories.
	baseWorkDir := req.WorkDir
	worktreeOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	}
	wtCtx := workflow.WithActivityOptions(ctx, worktreeOpts)
	var worktreePath string
	if err := workflow.ExecuteActivity(wtCtx, a.SetupWorktreeActivity, baseWorkDir, req.TaskID).Get(ctx, &worktreePath); err != nil {
		logger.Warn(SharkPrefix+" Worktree setup failed, falling back to shared workspace", "error", err)
		worktreePath = "" // signal: no worktree, use shared workspace
	} else {
		req.WorkDir = worktreePath
		logger.Info(SharkPrefix+" Worktree isolated", "path", worktreePath)
	}

	// cleanupWorktree removes the worktree on any exit path.
	cleanupWorktree := func() {
		if worktreePath == "" {
			return // no worktree to clean
		}
		cleanCtx := workflow.WithActivityOptions(ctx, worktreeOpts)
		if err := workflow.ExecuteActivity(cleanCtx, a.CleanupWorktreeActivity, baseWorkDir, worktreePath).Get(ctx, nil); err != nil {
			logger.Warn(SharkPrefix+" Worktree cleanup failed (best-effort)", "error", err)
		}
	}

	// notify is a fire-and-forget helper — errors never block the pipeline.
	notify := func(event string, extra map[string]string) {
		nCtx := workflow.WithActivityOptions(ctx, notifyOpts)
		_ = workflow.ExecuteActivity(nCtx, a.NotifyActivity, NotifyRequest{
			Event: event, TaskID: req.TaskID, Extra: extra,
		}).Get(ctx, nil)
	}

	// ===== PHASE 1: PLAN =====
	awaitResumeGate()
	planStart := workflow.Now(ctx)
	var plan StructuredPlan
	logger.Info(SharkPrefix + " Phase 1: Generating structured plan via LLM")
	planCtx := workflow.WithActivityOptions(ctx, planOpts)

	if err := workflow.ExecuteActivity(planCtx, a.StructuredPlanActivity, req).Get(ctx, &plan); err != nil {
		recordStep("plan", planStart, "failed")
		return fmt.Errorf("plan generation failed: %w", err)
	}
	if plan.TokenUsage.InputTokens > 0 || plan.TokenUsage.OutputTokens > 0 || plan.TokenUsage.CostUSD > 0 ||
		plan.TokenUsage.CacheReadTokens > 0 || plan.TokenUsage.CacheCreationTokens > 0 {
		logger.Info(SharkPrefix+" Plan tokens recorded in workflow",
			"InputTokens", plan.TokenUsage.InputTokens,
			"OutputTokens", plan.TokenUsage.OutputTokens,
			"CacheReadTokens", plan.TokenUsage.CacheReadTokens,
			"CacheCreationTokens", plan.TokenUsage.CacheCreationTokens,
			"CostUSD", plan.TokenUsage.CostUSD,
		)
	}
	recordStep("plan", planStart, "ok")

	logger.Info(SharkPrefix+" Plan ready",
		"Summary", truncate(plan.Summary, 120),
		"Steps", len(plan.Steps),
		"Files", len(plan.FilesToModify),
	)
	notify("plan", map[string]string{"title": plan.Summary, "agent": req.Agent})
	updateSearchAttributes(chumWorkflowStatusGate)

	// ===== PHASE 2: HUMAN GATE =====
	// Pre-planned work (has acceptance criteria) skips the gate.
	// "If CHUM is in the water, feed."

	currentAgent := req.Agent
	currentReviewer := req.Reviewer
	var allFailures []string
	var totalTokens TokenUsage
	var activityTokens []ActivityTokenUsage

	// Helper: reset per-attempt token tracking with plan tokens as baseline.
	planHasTokens := plan.TokenUsage.InputTokens > 0 || plan.TokenUsage.OutputTokens > 0 || plan.TokenUsage.CostUSD > 0 ||
		plan.TokenUsage.CacheReadTokens > 0 || plan.TokenUsage.CacheCreationTokens > 0
	resetAttemptTokens := func() {
		totalTokens = TokenUsage{}
		totalTokens.Add(plan.TokenUsage)
		activityTokens = nil
		if planHasTokens {
			activityTokens = append(activityTokens, ActivityTokenUsage{
				ActivityName: "plan",
				Agent:        req.Agent,
				Tokens:       plan.TokenUsage,
			})
		}
	}
	resetAttemptTokens()

	// ===== PHASE 2: (gate removed — ready status IS the approval) =====

	// ===== PHASE 3-6: EXECUTE → REVIEW → DOD LOOP (with tier escalation) =====
	handoffCount := 0
	escalationAttempt := 0

	// Build escalation chain — either from TaskRequest or fallback to single provider.
	chain := req.EscalationChain
	if len(chain) == 0 {
		chain = []EscalationTier{{
			ProviderKey: req.Provider,
			CLI:         req.Agent,
			Model:       req.Model,
			Tier:        "fast",
			Enabled:     true,
		}}
	}

	// Track the previous failed tier for escalation learning.
	var lastFailedProvider, lastFailedTier string

	for _, tier := range chain {
		if !tier.Enabled {
			logger.Info(SharkPrefix+" Skipping gated provider", "Provider", tier.ProviderKey, "Tier", tier.Tier)
			continue
		}

		maxRetries := retriesForTier(tier.Tier)

		// Override agent to use this tier's CLI+model
		currentAgent = tier.CLI
		req.Agent = tier.CLI
		req.Model = tier.Model
		req.Reviewer = DefaultReviewer(tier.CLI)
		currentReviewer = req.Reviewer

		logger.Info(SharkPrefix+" Tier escalation",
			"Tier", tier.Tier, "Provider", tier.ProviderKey, "CLI", tier.CLI, "Model", tier.Model,
			"MaxRetries", maxRetries)

		// Record escalation from previous tier
		if lastFailedProvider != "" {
			recordEscalation(ctx, logger, a, req.TaskID, req.Project,
				lastFailedProvider, lastFailedTier, tier.ProviderKey, tier.Tier)
		}

		for attempt := 0; attempt < maxRetries; attempt++ {
			escalationAttempt++
			logger.Info(SharkPrefix+" Execution attempt", "Attempt", attempt+1, "Agent", currentAgent)
			notify("execute", map[string]string{"agent": currentAgent, "attempt": fmt.Sprintf("%d", attempt+1)})

			// Reset token tracking to plan baseline for each attempt.
			// Only the last attempt's costs are reported in the outcome.
			resetAttemptTokens()

			awaitResumeGate()

			// --- EXECUTE ---
			updateSearchAttributes(chumWorkflowStatusExecute)

			execStart := workflow.Now(ctx)
			execCtx := workflow.WithActivityOptions(ctx, execOpts)
			var execResult ExecutionResult
			if err := workflow.ExecuteActivity(execCtx, a.ExecuteActivity, plan, req).Get(ctx, &execResult); err != nil {
				recordStep(fmt.Sprintf("execute[%d]", attempt+1), execStart, "failed")
				allFailures = append(allFailures, fmt.Sprintf("Attempt %d execute error: %s", attempt+1, err.Error()))
				continue
			}
			totalTokens.Add(execResult.Tokens)
			activityTokens = append(activityTokens, ActivityTokenUsage{
				ActivityName: "execute", Agent: execResult.Agent, Tokens: execResult.Tokens,
			})
			recordStep(fmt.Sprintf("execute[%d]", attempt+1), execStart, "ok")

			awaitResumeGate()

			// --- CROSS-MODEL REVIEW LOOP ---
			updateSearchAttributes(chumWorkflowStatusReview)

			reviewStart := workflow.Now(ctx)
			reviewPassed := false
			reviewStatus := "failed"
			for handoff := 0; handoff < maxHandoffs; handoff++ {
				reviewCtx := workflow.WithActivityOptions(ctx, reviewOpts)
				var review ReviewResult

				// Override the agent for this execution so the reviewer field is correct
				reviewReq := req
				reviewReq.Reviewer = currentReviewer

				if err := workflow.ExecuteActivity(reviewCtx, a.CodeReviewActivity, plan, execResult, reviewReq).Get(ctx, &review); err != nil {
					logger.Warn(SharkPrefix+" Review activity failed", "error", err)
					reviewPassed = true // don't block on review infrastructure failures
					reviewStatus = "failed"
					break
				}

				totalTokens.Add(review.Tokens)
				activityTokens = append(activityTokens, ActivityTokenUsage{
					ActivityName: "review", Agent: review.ReviewerAgent, Tokens: review.Tokens,
				})

				if review.Approved {
					logger.Info(SharkPrefix+" Code review approved", "Reviewer", review.ReviewerAgent, "Handoff", handoff)
					notify("review_approved", map[string]string{"reviewer": review.ReviewerAgent})
					reviewPassed = true
					reviewStatus = "ok"
					break
				}

				// Review failed — swap agents and re-execute with feedback
				handoffCount++
				notify("handoff", map[string]string{"from": currentAgent, "to": currentReviewer, "handoff": fmt.Sprintf("%d", handoffCount)})
				logger.Info(SharkPrefix+" Code review rejected, swapping agents",
					"Reviewer", currentReviewer,
					"Issues", strings.Join(review.Issues, "; "),
					"Handoff", handoffCount,
				)

				// Feed review issues back into the plan with context
				plan.PreviousErrors = append(plan.PreviousErrors,
					fmt.Sprintf("The previous agent (%s) attempted to implement the plan but failed code review. Their changes were reverted to give you a clean slate. Review by %s found issues: %s", currentAgent, review.ReviewerAgent, strings.Join(review.Issues, "; ")))

				// Swap: the reviewer becomes the implementer, and vice versa
				updateSearchAttributes(chumWorkflowStatusExecute)
				awaitResumeGate()

				currentAgent, currentReviewer = currentReviewer, currentAgent
				req.Agent = currentAgent

				// Reset workspace for the new agent so they have a fresh slate
				resetStart := workflow.Now(ctx)
				resetCtx := workflow.WithActivityOptions(ctx, execOpts) // Use the longer execOpts timeout for git commands
				if err := workflow.ExecuteActivity(resetCtx, a.ResetWorkspaceActivity, req.WorkDir).Get(ctx, nil); err != nil {
					logger.Warn(SharkPrefix+" Failed to reset workspace for fresh agent", "error", err)
				}
				recordStep(fmt.Sprintf("handoff-reset[%d]", handoffCount), resetStart, "ok")

				// Re-execute with the swapped agent
				handoffExecStart := workflow.Now(ctx)
				var reExecResult ExecutionResult
				if err := workflow.ExecuteActivity(execCtx, a.ExecuteActivity, plan, req).Get(ctx, &reExecResult); err != nil {
					recordStep(fmt.Sprintf("handoff-execute[%d]", handoffCount), handoffExecStart, "failed")
					allFailures = append(allFailures, fmt.Sprintf("Handoff %d execute error: %s", handoffCount, err.Error()))
					break
				}
				totalTokens.Add(reExecResult.Tokens)
				activityTokens = append(activityTokens, ActivityTokenUsage{
					ActivityName: "execute", Agent: reExecResult.Agent, Tokens: reExecResult.Tokens,
				})
				recordStep(fmt.Sprintf("handoff-execute[%d]", handoffCount), handoffExecStart, "ok")
				execResult = reExecResult
			}

			if !reviewPassed {
				recordStep(fmt.Sprintf("review[%d]", attempt+1), reviewStart, "failed")
				allFailures = append(allFailures, fmt.Sprintf("Attempt %d: review not passed after %d handoffs", attempt+1, handoffCount))
				continue
			}
			recordStep(fmt.Sprintf("review[%d]", attempt+1), reviewStart, reviewStatus)

			awaitResumeGate()

			// --- SEMGREP PRE-FILTER ---
			// Run custom .semgrep/ rules first. Free and fast — catches known
			// antipatterns before we pay for compile/test/lint.
			semgrepStart := workflow.Now(ctx)
			semgrepOpts := workflow.ActivityOptions{
				StartToCloseTimeout: 1 * time.Minute,
				RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
			}
			semgrepCtx := workflow.WithActivityOptions(ctx, semgrepOpts)
			var semgrepResult SemgrepScanResult
			if err := workflow.ExecuteActivity(semgrepCtx, a.RunSemgrepScanActivity, req.WorkDir).Get(ctx, &semgrepResult); err != nil {
				logger.Warn(SharkPrefix+" Semgrep scan failed (non-fatal, proceeding to DoD)", "error", err)
				recordStep(fmt.Sprintf("semgrep[%d]", attempt+1), semgrepStart, "skipped")
			} else if !semgrepResult.Passed {
				recordStep(fmt.Sprintf("semgrep[%d]", attempt+1), semgrepStart, "failed")
				plan.PreviousErrors = append(plan.PreviousErrors,
					fmt.Sprintf("Semgrep found %d issues: %s", semgrepResult.Findings, truncate(semgrepResult.Output, 500)))
				allFailures = append(allFailures,
					fmt.Sprintf("Attempt %d: Semgrep found %d issues", attempt+1, semgrepResult.Findings))
				logger.Warn(SharkPrefix+" Semgrep pre-filter failed, skipping expensive DoD", "Findings", semgrepResult.Findings)
				continue
			} else {
				recordStep(fmt.Sprintf("semgrep[%d]", attempt+1), semgrepStart, "ok")
			}

			awaitResumeGate()

			// --- DOD VERIFICATION ---
			updateSearchAttributes(chumWorkflowStatusDoD)

			dodStart := workflow.Now(ctx)
			logger.Info(SharkPrefix + " Running DoD checks")
			dodCtx := workflow.WithActivityOptions(ctx, dodOpts)
			var dodResult DoDResult
			if err := workflow.ExecuteActivity(dodCtx, a.DoDVerifyActivity, req).Get(ctx, &dodResult); err != nil {
				recordStep(fmt.Sprintf("dod[%d]", attempt+1), dodStart, "failed")
				allFailures = append(allFailures, fmt.Sprintf("Attempt %d DoD error: %s", attempt+1, err.Error()))
				continue
			}

			if dodResult.Passed {
				recordStep(fmt.Sprintf("dod[%d]", attempt+1), dodStart, "ok")

				duration := workflow.Now(ctx).Sub(startTime)
				notify("dod_pass", map[string]string{"duration": fmtDuration(duration), "cost": fmtCost(totalTokens.CostUSD)})
				notify("complete", map[string]string{"duration": fmtDuration(duration), "cost": fmtCost(totalTokens.CostUSD)})

				// ===== SUCCESS — CLOSE TASK + RECORD OUTCOME =====
				logger.Info(SharkPrefix+" DoD PASSED — closing task and recording outcome",
					"TotalInputTokens", totalTokens.InputTokens,
					"TotalOutputTokens", totalTokens.OutputTokens,
					"TotalCacheReadTokens", totalTokens.CacheReadTokens,
					"TotalCacheCreationTokens", totalTokens.CacheCreationTokens,
					"TotalCostUSD", totalTokens.CostUSD,
				)

				updateSearchAttributes(chumWorkflowStatusCompleted)

				// Close the task — it's done. New work = new morsel.
				closeCtx := workflow.WithActivityOptions(ctx, recordOpts)
				_ = workflow.ExecuteActivity(closeCtx, a.CloseTaskActivity, req.TaskID, "completed").Get(ctx, nil)

				recordOutcome(ctx, recordOpts, a, req, "completed", 0,
					handoffCount, true, "", startTime, totalTokens, activityTokens, stepMetrics)

				// ===== CHUM LOOP — spawn async learner + groomer =====
				spawnCHUMWorkflows(ctx, logger, req, plan)

				cleanupWorktree()
				return nil
			}

			// DoD failed — feed detailed check output back to agent
			recordStep(fmt.Sprintf("dod[%d]", attempt+1), dodStart, "failed")
			failureMsg := strings.Join(dodResult.Failures, "; ")
			allFailures = append(allFailures, fmt.Sprintf("Attempt %d DoD failed: %s", attempt+1, failureMsg))

			// Build detailed feedback with per-check output so the agent
			// knows exactly which commands failed and what the errors were.
			var detailedFeedback strings.Builder
			detailedFeedback.WriteString("DoD check failures:\n")
			for _, check := range dodResult.Checks {
				if !check.Passed {
					detailedFeedback.WriteString(fmt.Sprintf("\n--- FAILED: %s (exit %d) ---\n", check.Command, check.ExitCode))
					detailedFeedback.WriteString(truncate(check.Output, 2000))
					detailedFeedback.WriteString("\n")
				}
			}
			plan.PreviousErrors = append(plan.PreviousErrors, detailedFeedback.String())

			notify("dod_fail", map[string]string{"failures": failureMsg, "attempt": fmt.Sprintf("%d", attempt+1)})
			logger.Warn(SharkPrefix+" DoD failed, retrying", "Attempt", attempt+1, "Failures", failureMsg)

			// --- AUTO-FIX: run gofmt + goimports before next retry ---
			// Cheap and deterministic — fixes formatting issues that agents
			// commonly introduce, saving a full retry attempt.
			autoFixStart := workflow.Now(ctx)
			autoFixOpts := workflow.ActivityOptions{
				StartToCloseTimeout: 1 * time.Minute,
				RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
			}
			autoFixCtx := workflow.WithActivityOptions(ctx, autoFixOpts)
			var autoFixResult AutoFixResult
			if err := workflow.ExecuteActivity(autoFixCtx, a.AutoFixLintActivity, req.WorkDir).Get(ctx, &autoFixResult); err != nil {
				logger.Warn(SharkPrefix+" Auto-fix lint failed (non-fatal)", "error", err)
			} else if autoFixResult.FilesFixed > 0 {
				logger.Info(SharkPrefix+" Auto-fix applied",
					"FilesFixed", autoFixResult.FilesFixed, "Tools", autoFixResult.ToolsRun)
			}
			recordStep(fmt.Sprintf("autofix[%d]", attempt+1), autoFixStart, "ok")
		}

		// All retries exhausted for this tier — record failure and try next tier
		lastFailedProvider = tier.ProviderKey
		lastFailedTier = tier.Tier
		logger.Warn(SharkPrefix+" Tier exhausted, escalating",
			"Tier", tier.Tier, "Provider", tier.ProviderKey, "Attempts", maxRetries)
	} // end tier loop

	// ===== ESCALATE — all tiers exhausted =====
	awaitResumeGate()
	updateSearchAttributes(chumWorkflowStatusEscalated)

	escalateStart := workflow.Now(ctx)
	notify("escalate", map[string]string{"attempts": fmt.Sprintf("%d", escalationAttempt)})
	logger.Error(SharkPrefix + " All attempts exhausted, escalating to chief")

	escalateCtx := workflow.WithActivityOptions(ctx, recordOpts)
	if err := workflow.ExecuteActivity(escalateCtx, a.EscalateActivity, EscalationRequest{
		TaskID:       req.TaskID,
		Project:      req.Project,
		PlanSummary:  plan.Summary,
		Failures:     allFailures,
		AttemptCount: escalationAttempt,
		HandoffCount: handoffCount,
	}).Get(ctx, nil); err != nil {
		logger.Warn(SharkPrefix+" Escalation activity failed (best-effort)", "error", err)
	}
	recordStep("escalate", escalateStart, "ok")

	// Close the task — it failed. New work = new morsel.
	closeCtx := workflow.WithActivityOptions(ctx, recordOpts)
	_ = workflow.ExecuteActivity(closeCtx, a.CloseTaskActivity, req.TaskID, "escalated").Get(ctx, nil)

	recordOutcome(ctx, recordOpts, a, req, "escalated", 1,
		handoffCount, false, strings.Join(allFailures, "\n"), startTime, totalTokens, activityTokens, stepMetrics)

	// ===== SPAWN LEARNER ON FAILURE =====
	// The octopus learns MORE from failures than successes.
	// Failures carry antibodies: what went wrong, which files are risky,
	// which errors repeated. This is the richest evolutionary data.
	failureLearnerReq := LearnerRequest{
		TaskID:         req.TaskID,
		Project:        req.Project,
		WorkDir:        req.WorkDir,
		Agent:          req.Agent,
		DoDPassed:      false,
		FilesChanged:   plan.FilesToModify,
		PreviousErrors: allFailures,
		Tier:           "fast",
	}
	failureLearnerOpts := workflow.ChildWorkflowOptions{
		ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_ABANDON,
		WorkflowID:        fmt.Sprintf("learner-%s-%d", req.TaskID, workflow.Now(ctx).Unix()),
	}
	failureLearnerCtx := workflow.WithChildOptions(ctx, failureLearnerOpts)
	failureLearnerFut := workflow.ExecuteChildWorkflow(failureLearnerCtx, ContinuousLearnerWorkflow, failureLearnerReq)
	_ = failureLearnerFut.GetChildWorkflowExecution().Get(ctx, nil)
	logger.Info(SharkPrefix+" Spawned failure learner — octopus will extract antibodies", "TaskID", req.TaskID)

	cleanupWorktree()
	return fmt.Errorf("task escalated after %d attempts: %s", escalationAttempt, strings.Join(allFailures, "; "))
}

// recordOutcome is a helper to persist the workflow outcome via RecordOutcomeActivity.
func recordOutcome(ctx workflow.Context, opts workflow.ActivityOptions, a *Activities,
	req TaskRequest, status string, exitCode, handoffs int,
	dodPassed bool, dodFailures string, startTime time.Time,
	tokens TokenUsage, activityTokens []ActivityTokenUsage, steps []StepMetric) {

	logger := workflow.GetLogger(ctx)
	recordCtx := workflow.WithActivityOptions(ctx, opts)
	duration := workflow.Now(ctx).Sub(startTime).Seconds()

	if err := workflow.ExecuteActivity(recordCtx, a.RecordOutcomeActivity, OutcomeRecord{
		TaskID:         req.TaskID,
		Project:        req.Project,
		Agent:          req.Agent,
		Reviewer:       req.Reviewer,
		Provider:       req.Provider,
		Status:         status,
		ExitCode:       exitCode,
		DurationS:      duration,
		DoDPassed:      dodPassed,
		DoDFailures:    dodFailures,
		Handoffs:       handoffs,
		TotalTokens:    tokens,
		ActivityTokens: activityTokens,
		StepMetrics:    steps,
	}).Get(ctx, nil); err != nil {
		logger.Warn(SharkPrefix+" RecordOutcome activity failed (best-effort)", "error", err)
	}
}

// spawnCHUMWorkflows fires off the ContinuousLearner and TacticalGroom as
// detached child workflows. They run completely async — the parent returns
// immediately and the children survive even after it completes.
func spawnCHUMWorkflows(ctx workflow.Context, logger log.Logger, req TaskRequest, plan StructuredPlan) {
	chumOpts := workflow.ChildWorkflowOptions{
		ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_ABANDON,
	}

	// --- Spawn ContinuousLearnerWorkflow ---
	learnerReq := LearnerRequest{
		TaskID:         req.TaskID,
		Project:        req.Project,
		WorkDir:        req.WorkDir,
		Agent:          req.Agent,
		DoDPassed:      true,
		FilesChanged:   plan.FilesToModify,
		PreviousErrors: plan.PreviousErrors,
		Tier:           "fast",
	}
	learnerOpts := chumOpts
	learnerOpts.WorkflowID = fmt.Sprintf("learner-%s-%d", req.TaskID, workflow.Now(ctx).Unix())
	learnerCtx := workflow.WithChildOptions(ctx, learnerOpts)
	learnerFut := workflow.ExecuteChildWorkflow(learnerCtx, ContinuousLearnerWorkflow, learnerReq)

	// --- Spawn TacticalGroomWorkflow ---
	groomReq := TacticalGroomRequest{
		TaskID:  req.TaskID,
		Project: req.Project,
		WorkDir: req.WorkDir,
		Tier:    "fast",
	}
	groomOpts := chumOpts
	groomOpts.WorkflowID = fmt.Sprintf("groom-%s-%d", req.TaskID, workflow.Now(ctx).Unix())
	groomCtx := workflow.WithChildOptions(ctx, groomOpts)
	groomFut := workflow.ExecuteChildWorkflow(groomCtx, TacticalGroomWorkflow, groomReq)

	// CRITICAL: Wait for both children to actually start before the parent returns.
	// Without this, Temporal kills the children when the parent completes — the
	// ABANDON policy only protects children that have already started executing.
	var learnerExec, groomExec workflow.Execution
	if err := learnerFut.GetChildWorkflowExecution().Get(ctx, &learnerExec); err != nil {
		logger.Warn(SharkPrefix+" CHUM: Learner failed to start", "error", err)
	} else {
		logger.Info(SharkPrefix+" CHUM: Learner started", "WorkflowID", learnerExec.ID, "RunID", learnerExec.RunID)
	}
	if err := groomFut.GetChildWorkflowExecution().Get(ctx, &groomExec); err != nil {
		logger.Warn(SharkPrefix+" CHUM: TacticalGroom failed to start", "error", err)
	} else {
		logger.Info(SharkPrefix+" CHUM: TacticalGroom started", "WorkflowID", groomExec.ID, "RunID", groomExec.RunID)
	}
}

// recordEscalation logs an escalation event to the store (best-effort).
func recordEscalation(ctx workflow.Context, logger log.Logger, a *Activities,
	taskID, project, failedProvider, failedTier, escalatedTo, escalatedTier string) {

	// a is a Temporal typed nil — can't access fields on it.
	// The Store check was panicking because a itself is nil.
	if a == nil {
		logger.Warn(SharkPrefix + " recordEscalation: activities pointer is nil, skipping")
		return
	}
	if a.Store == nil {
		return
	}
	logger.Info(SharkPrefix+" Recording escalation",
		"From", failedProvider, "FromTier", failedTier,
		"To", escalatedTo, "ToTier", escalatedTier)

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	actCtx := workflow.WithActivityOptions(ctx, ao)
	_ = workflow.ExecuteActivity(actCtx, a.RecordEscalationActivity, EscalationEvent{
		MorselID:       taskID,
		Project:        project,
		FailedProvider: failedProvider,
		FailedTier:     failedTier,
		EscalatedTo:    escalatedTo,
		EscalatedTier:  escalatedTier,
	}).Get(ctx, nil)
}
