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
	maxDoDRetries = 3 // maximum DoD retry attempts
	maxHandoffs   = 3 // maximum cross-model review handoffs

	// defaultSlowStepThreshold is used when no config override is provided.
	defaultSlowStepThreshold = 2 * time.Minute
)

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

	// --- Search attributes for Temporal UI visibility ---
	upsertStage := func(stage string) {
		_ = workflow.UpsertTypedSearchAttributes(ctx,
			temporal.NewSearchAttributeKeyKeyword(SACurrentStage).ValueSet(stage),
		)
	}
	if err := workflow.UpsertTypedSearchAttributes(ctx,
		temporal.NewSearchAttributeKeyKeyword(SAProject).ValueSet(req.Project),
		temporal.NewSearchAttributeKeyInt64(SAPriority).ValueSet(int64(req.Priority)),
		temporal.NewSearchAttributeKeyKeyword(SAAgent).ValueSet(req.Agent),
		temporal.NewSearchAttributeKeyKeyword(SACurrentStage).ValueSet("plan"),
		temporal.NewSearchAttributeKeyString(SATaskTitle).ValueSet(req.Title),
	); err != nil {
		logger.Warn(SharkPrefix+" Failed to set search attributes", "error", err)
	}

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

	// Assign reviewer if not specified
	if req.Reviewer == "" {
		req.Reviewer = DefaultReviewer(req.Agent)
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

	var a *Activities

	// ===== PHASE 1: PLAN =====
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

	// ===== PHASE 3-6: EXECUTE → REVIEW → DOD LOOP =====
	handoffCount := 0

	for attempt := 0; attempt < maxDoDRetries; attempt++ {
		logger.Info(SharkPrefix+" Execution attempt", "Attempt", attempt+1, "Agent", currentAgent)

		// Reset token tracking to plan baseline for each attempt.
		// Only the last attempt's costs are reported in the outcome.
		resetAttemptTokens()

		// --- EXECUTE ---
		upsertStage("execute")
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

		// --- CROSS-MODEL REVIEW LOOP ---
		upsertStage("review")
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
				reviewPassed = true
				reviewStatus = "ok"
				break
			}

			// Review failed — swap agents and re-execute with feedback
			handoffCount++
			logger.Info(SharkPrefix+" Code review rejected, swapping agents",
				"Reviewer", currentReviewer,
				"Issues", strings.Join(review.Issues, "; "),
				"Handoff", handoffCount,
			)

			// Feed review issues back into the plan with context
			plan.PreviousErrors = append(plan.PreviousErrors,
				fmt.Sprintf("The previous agent (%s) attempted to implement the plan but failed code review. Their changes were reverted to give you a clean slate. Review by %s found issues: %s", currentAgent, review.ReviewerAgent, strings.Join(review.Issues, "; ")))

			// Swap: the reviewer becomes the implementer, and vice versa
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

		// --- SEMGREP PRE-FILTER ---
		// Run custom .semgrep/ rules first. Free and fast — catches known
		// antipatterns before we pay for compile/test/lint.
		upsertStage("semgrep")
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

		// --- DOD VERIFICATION ---
		upsertStage("dod")
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

			// ===== SUCCESS — RECORD OUTCOME =====
			upsertStage("completed")
			logger.Info(SharkPrefix+" DoD PASSED — recording outcome",
				"TotalInputTokens", totalTokens.InputTokens,
				"TotalOutputTokens", totalTokens.OutputTokens,
				"TotalCacheReadTokens", totalTokens.CacheReadTokens,
				"TotalCacheCreationTokens", totalTokens.CacheCreationTokens,
				"TotalCostUSD", totalTokens.CostUSD,
			)
			recordOutcome(ctx, recordOpts, a, req, "completed", 0,
				handoffCount, true, "", startTime, totalTokens, activityTokens, stepMetrics)

			// ===== CHUM LOOP — spawn async learner + groomer =====
			spawnCHUMWorkflows(ctx, logger, req, plan)

			return nil
		}

		// DoD failed — feed failures back into plan
		recordStep(fmt.Sprintf("dod[%d]", attempt+1), dodStart, "failed")
		failureMsg := strings.Join(dodResult.Failures, "; ")
		allFailures = append(allFailures, fmt.Sprintf("Attempt %d DoD failed: %s", attempt+1, failureMsg))
		plan.PreviousErrors = append(plan.PreviousErrors, "DoD check failures: "+failureMsg)

		logger.Warn(SharkPrefix+" DoD failed, retrying", "Attempt", attempt+1, "Failures", failureMsg)
	}

	// ===== ESCALATE — all retries exhausted =====
	upsertStage("escalated")
	escalateStart := workflow.Now(ctx)
	logger.Error(SharkPrefix + " All attempts exhausted, escalating to chief")

	escalateCtx := workflow.WithActivityOptions(ctx, recordOpts)
	if err := workflow.ExecuteActivity(escalateCtx, a.EscalateActivity, EscalationRequest{
		TaskID:       req.TaskID,
		Project:      req.Project,
		PlanSummary:  plan.Summary,
		Failures:     allFailures,
		AttemptCount: maxDoDRetries,
		HandoffCount: handoffCount,
	}).Get(ctx, nil); err != nil {
		logger.Warn(SharkPrefix+" Escalation activity failed (best-effort)", "error", err)
	}
	recordStep("escalate", escalateStart, "ok")

	recordOutcome(ctx, recordOpts, a, req, "escalated", 1,
		handoffCount, false, strings.Join(allFailures, "\n"), startTime, totalTokens, activityTokens, stepMetrics)

	return fmt.Errorf("task escalated after %d attempts: %s", maxDoDRetries, strings.Join(allFailures, "; "))
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
