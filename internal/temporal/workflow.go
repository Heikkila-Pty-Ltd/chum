package temporal

import (
	"fmt"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/antigravity-dev/chum/internal/store"
)

const (
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
// If override > 0, it takes precedence (e.g. higher-learning mode).
func retriesForTier(tier string, override int) int {
	if override > 0 {
		return override
	}
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
//  4. REVIEW      — Reviewer approves/rejects the output (no handoff swapping)
//  5. DOD         — Compile/test/lint verification via git.RunPostMergeChecks
//  6. RECORD      — Persist outcome to store (feeds learner loop)
//  7. ESCALATE    — If checks fail after minimal retries, escalate to chief + human
func ChumAgentWorkflow(ctx workflow.Context, req TaskRequest) (err error) {
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
	var allFailures []string
	infraRetries := make(map[string]bool) // tracks transient infra failures that got a free retry
	defer func() {
		// If the workflow is failing, record the "scent" in the task store.
		// Future sharks will use this to skip previous mistakes.
		if err != nil {
			recordOpts := workflow.ActivityOptions{
				StartToCloseTimeout: 10 * time.Second,
				RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
			}
			fCtx := workflow.WithActivityOptions(ctx, recordOpts)
			var a *Activities
			_ = workflow.ExecuteActivity(fCtx, a.RecordFailureActivity, req.TaskID, allFailures).Get(ctx, nil)
		}
	}()
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
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	execOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 45 * time.Minute,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1}, // no auto-retry, we handle it
	}
	reviewOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	dodOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 45 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	recordOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	triageOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 90 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	notifyOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}

	var a *Activities

	// rescopeTriggered is set to true when failure triage decides the task
	// needs turtle/crab intervention instead of more retries.
	var rescopeTriggered bool
	var rescopeReason string

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
	if err := workflow.ExecuteActivity(wtCtx, a.SetupWorktreeActivity, baseWorkDir, req.TaskID, req.ExplosionID).Get(ctx, &worktreePath); err != nil {
		logger.Warn(SharkPrefix+" Worktree setup failed, falling back to shared workspace", "error", err)
		worktreePath = "" // signal: no worktree, use shared workspace
	} else {
		req.WorkDir = worktreePath
		logger.Info(SharkPrefix+" Worktree isolated", "path", worktreePath)
	}

	var retainWorktree bool

	// cleanupWorktree removes the worktree on any exit path.
	cleanupWorktree := func() {
		if worktreePath == "" || retainWorktree {
			return // no worktree to clean, or explicitly retained for explosion winner
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

	// ===== GRAPH-BRAIN TRACE — record execution graph for pattern crystallization =====
	traceSessionID := req.TraceSessionID
	if traceSessionID == "" {
		traceSessionID = workflow.GetInfo(ctx).WorkflowExecution.ID
	}
	traceOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	var lastTraceEventID string
	// recordTrace is a fire-and-forget helper — trace errors never block the pipeline.
	recordTrace := func(phase, eventType string, reward float64, isTerminal bool, metadata string) {
		tCtx := workflow.WithActivityOptions(ctx, traceOpts)
		var eventID string
		if traceErr := workflow.ExecuteActivity(tCtx, a.RecordGraphTraceEventActivity, GraphTraceRequest{
			ParentEventID: lastTraceEventID,
			SessionID:     traceSessionID,
			EventType:     eventType,
			Phase:         phase,
			ModelName:     req.Model,
			Reward:        reward,
			IsTerminal:    isTerminal,
			Metadata:      metadata,
		}).Get(ctx, &eventID); traceErr != nil {
			logger.Warn(SharkPrefix+" graph trace failed (non-fatal)", "phase", phase, "error", traceErr)
		}
		if eventID != "" {
			lastTraceEventID = eventID
		}
	}
	backpropTrace := func(reward float64) {
		tCtx := workflow.WithActivityOptions(ctx, traceOpts)
		if traceErr := workflow.ExecuteActivity(tCtx, a.BackpropagateRewardActivity, BackpropagateRewardRequest{
			SessionID: traceSessionID,
			Reward:    reward,
		}).Get(ctx, nil); traceErr != nil {
			logger.Warn(SharkPrefix+" graph trace backprop failed (non-fatal)", "error", traceErr)
		}
	}

	// ===== BUG PRIMING — inject population-level bug data =====
	// Classify species early for priming (will be re-classified with plan data later).
	earlySpecies := classifySpecies(req.TaskID, req.Prompt, nil)
	var bugPriming string
	if req.Agent != "" {
		bugPrimingCtx := workflow.WithActivityOptions(ctx, recordOpts)
		_ = workflow.ExecuteActivity(bugPrimingCtx, a.GetBugPrimingActivity, req.Agent, earlySpecies).Get(ctx, &bugPriming)
		if bugPriming != "" {
			logger.Info(SharkPrefix+" Bug priming injected", "Provider", req.Agent, "Species", earlySpecies, "Len", len(bugPriming))
			req.Prompt = req.Prompt + "\n\n" + bugPriming
		}
	}

	// ===== PROTEIN INJECTION — deterministic workflow instructions =====
	var activeProteinID string
	var proteinInstructions string
	{
		proteinCtx := workflow.WithActivityOptions(ctx, recordOpts)
		_ = workflow.ExecuteActivity(proteinCtx, a.GetProteinInstructionsActivity, earlySpecies).Get(ctx, &proteinInstructions)
		if proteinInstructions != "" {
			logger.Info(SharkPrefix+" Protein protocol injected", "Species", earlySpecies)
			req.Prompt = req.Prompt + "\n\n" + proteinInstructions
			activeProteinID = earlySpecies // tracks which protein was used
		}
	}

	// ===== PHASE 1: PLAN =====
	awaitResumeGate()
	planStart := workflow.Now(ctx)
	var plan StructuredPlan
	logger.Info(SharkPrefix + " Phase 1: Generating structured plan via LLM")
	planCtx := workflow.WithActivityOptions(ctx, planOpts)
	plan.PreviousErrors = req.PreviousErrors

	if err := workflow.ExecuteActivity(planCtx, a.StructuredPlanActivity, req).Get(ctx, &plan); err != nil {
		recordStep("plan", planStart, "failed")
		// Close task to prevent infinite re-dispatch — a task that can't even plan
		// should not be retried every tick until the species hibernates.
		_ = workflow.ExecuteActivity(
			workflow.WithActivityOptions(ctx, recordOpts),
			a.CloseTaskActivity, req.TaskID, "plan_failed",
		).Get(ctx, nil)
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
	recordTrace("plan", "phase_boundary", 0.1, false, traceMetadataJSON(
		"agent", req.Agent, "summary", truncate(plan.Summary, 200),
		"tokens_in", plan.TokenUsage.InputTokens, "tokens_out", plan.TokenUsage.OutputTokens,
	))
	updateSearchAttributes(chumWorkflowStatusGate)

	// ===== PHASE 2: HUMAN GATE =====
	// Pre-planned work (has acceptance criteria) skips the gate.
	// "If CHUM is in the water, feed."

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

	// ===== PHASE 3-5: EXECUTE → REVIEW → CHECK LOOP =====
	escalationAttempt := 0

	// Build execution chain — either from TaskRequest or fallback to single provider.
	chain := req.EscalationChain
	if len(chain) == 0 {
		chain = []EscalationTier{{
			ProviderKey: req.Provider,
			CLI:         req.Agent,
			Model:       req.Model,
			Tier:        "balanced",
			Enabled:     true,
		}}
	}
	// Simplified routing: keep only the first tier in fast-loop mode.
	if len(chain) > 1 {
		chain = chain[:1]
	}

	for _, tier := range chain {
		if !tier.Enabled {
			logger.Info(SharkPrefix+" Skipping gated provider", "Provider", tier.ProviderKey, "Tier", tier.Tier)
			continue
		}

		maxRetries := retriesForTier(tier.Tier, req.MaxRetriesOverride)

		// Override agent to use this tier's CLI+model
		_ = tier.CLI // agent set via req.Agent below
		req.Agent = tier.CLI
		req.Model = tier.Model
		req.Reviewer = tier.Reviewer
		if req.Reviewer == "" {
			req.Reviewer = DefaultReviewer(tier.CLI)
		}

		logger.Info(SharkPrefix+" Tier escalation",
			"Tier", tier.Tier, "Provider", tier.ProviderKey, "CLI", tier.CLI, "Model", tier.Model,
			"MaxRetries", maxRetries)

		for attempt := 0; attempt < maxRetries; attempt++ {
			escalationAttempt++
			logger.Info(SharkPrefix+" Execution attempt", "Attempt", attempt+1, "Agent", req.Agent)
			notify("execute", map[string]string{"agent": req.Agent, "attempt": fmt.Sprintf("%d", attempt+1)})

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
			recordTrace("execute", "phase_boundary", 0.3, false, traceMetadataJSON(
				"agent", execResult.Agent, "exit_code", execResult.ExitCode,
				"tokens_in", execResult.Tokens.InputTokens, "tokens_out", execResult.Tokens.OutputTokens,
			))

			awaitResumeGate()

			// --- SENTINEL SCAN — detect execution drift ---
			sentinelStart := workflow.Now(ctx)
			sentinelOpts := workflow.ActivityOptions{
				StartToCloseTimeout: 1 * time.Minute,
				RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
			}
			sentinelCtx := workflow.WithActivityOptions(ctx, sentinelOpts)
			var sentinelResult SentinelResult
			if err := workflow.ExecuteActivity(sentinelCtx, a.SentinelScanActivity, SentinelScanRequest{
				WorktreePath:  worktreePath,
				ExpectedFiles: plan.FilesToModify,
				MorselID:      req.TaskID,
				Project:       req.Project,
				Attempt:       attempt + 1,
			}).Get(ctx, &sentinelResult); err != nil {
				logger.Warn(SharkPrefix+" Sentinel scan failed (non-fatal)", "error", err)
				recordStep(fmt.Sprintf("sentinel[%d]", attempt+1), sentinelStart, "skipped")
			} else {
				if len(sentinelResult.RevertedFiles) > 0 {
					plan.PreviousErrors = append(plan.PreviousErrors,
						fmt.Sprintf("SENTINEL: reverted %d out-of-scope file(s) that broke the build: %s. Stay within scope — only modify files listed in the plan.",
							len(sentinelResult.RevertedFiles),
							strings.Join(sentinelResult.RevertedFiles, ", ")))
					logger.Warn(SharkPrefix+" Sentinel reverted drift",
						"RevertedFiles", sentinelResult.RevertedFiles)
				} else if len(sentinelResult.OutOfScopeFiles) > 0 {
					logger.Info(SharkPrefix+" Sentinel: out-of-scope files detected but build OK",
						"OutOfScope", sentinelResult.OutOfScopeFiles)
				}
				status := "ok"
				if !sentinelResult.Passed {
					status = "failed"
				}
				recordStep(fmt.Sprintf("sentinel[%d]", attempt+1), sentinelStart, status)
			}

			// --- REVIEW ---
			updateSearchAttributes(chumWorkflowStatusReview)

			reviewStart := workflow.Now(ctx)
			reviewCtx := workflow.WithActivityOptions(ctx, reviewOpts)
			var review ReviewResult

			reviewReq := req
			if strings.TrimSpace(reviewReq.Reviewer) == "" {
				reviewReq.Reviewer = DefaultReviewer(req.Agent)
			}

			if err := workflow.ExecuteActivity(reviewCtx, a.CodeReviewActivity, plan, execResult, reviewReq).Get(ctx, &review); err != nil {
				logger.Warn(SharkPrefix+" Review activity failed (non-blocking)", "error", err)
				recordStep(fmt.Sprintf("review[%d]", attempt+1), reviewStart, "skipped")
			} else {
				totalTokens.Add(review.Tokens)
				activityTokens = append(activityTokens, ActivityTokenUsage{
					ActivityName: "review", Agent: review.ReviewerAgent, Tokens: review.Tokens,
				})
				if !review.Approved {
					recordStep(fmt.Sprintf("review[%d]", attempt+1), reviewStart, "failed")
					allFailures = append(allFailures, fmt.Sprintf("Attempt %d: review not approved: %s", attempt+1, strings.Join(review.Issues, "; ")))
					recordTrace("review", "phase_boundary", -0.1, false, traceMetadataJSON(
						"reviewer", review.ReviewerAgent, "approved", false,
						"issues", strings.Join(review.Issues, "; "),
					))
				} else {
					logger.Info(SharkPrefix+" Code review approved", "Reviewer", review.ReviewerAgent)
					notify("review_approved", map[string]string{"reviewer": review.ReviewerAgent})
					recordStep(fmt.Sprintf("review[%d]", attempt+1), reviewStart, "ok")
					recordTrace("review", "phase_boundary", 0.5, false, traceMetadataJSON(
						"reviewer", review.ReviewerAgent, "approved", true,
					))
				}
			}

			awaitResumeGate()

			// --- UBS PRE-FILTER ---
			// Run Ultimate Bug Scanner before DoD. Fast static analysis catches
			// known antipatterns. All findings logged to ubs_findings table.
			ubsStart := workflow.Now(ctx)
			ubsOpts := workflow.ActivityOptions{
				StartToCloseTimeout: 2 * time.Minute,
				RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
			}
			ubsCtx := workflow.WithActivityOptions(ctx, ubsOpts)
			ubsReq := UBSScanRequest{
				WorktreePath: worktreePath,
				MorselID:     req.TaskID,
				Project:      req.Project,
				Provider:     req.Agent,
				Species:      earlySpecies,
				Attempt:      attempt + 1,
			}
			var ubsResult UBSScanResult
			if err := workflow.ExecuteActivity(ubsCtx, a.RunUBSScanActivity, ubsReq).Get(ctx, &ubsResult); err != nil {
				logger.Warn(SharkPrefix+" UBS scan failed (non-fatal, proceeding to DoD)", "error", err)
				recordStep(fmt.Sprintf("ubs[%d]", attempt+1), ubsStart, "skipped")
			} else if !ubsResult.Passed {
				recordStep(fmt.Sprintf("ubs[%d]", attempt+1), ubsStart, "failed")
				plan.PreviousErrors = append(plan.PreviousErrors,
					fmt.Sprintf("UBS found %d critical issues (total %d findings)", ubsResult.Critical, ubsResult.TotalFindings))
				allFailures = append(allFailures,
					fmt.Sprintf("Attempt %d: UBS found %d critical issues", attempt+1, ubsResult.Critical))
				logger.Warn(SharkPrefix+" UBS pre-filter failed, skipping expensive DoD",
					"Critical", ubsResult.Critical, "Warnings", ubsResult.Warnings)
				continue
			} else {
				recordStep(fmt.Sprintf("ubs[%d]", attempt+1), ubsStart, "ok")
				recordTrace("ubs", "phase_boundary", 0.6, false, traceMetadataJSON(
					"critical", ubsResult.Critical, "total_findings", ubsResult.TotalFindings,
				))
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
				recordTrace("dod", "phase_boundary", 0.9, false, traceMetadataJSON(
					"passed", true, "checks", len(dodResult.Checks),
				))
				// Terminal success — record and backpropagate
				recordTrace("complete", "phase_boundary", 1.0, true, traceMetadataJSON(
					"cost_usd", totalTokens.CostUSD,
					"tokens_in", totalTokens.InputTokens, "tokens_out", totalTokens.OutputTokens,
				))
				backpropTrace(1.0)

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

				if req.ExplosionID != "" {
					retainWorktree = true
					// Record outcome even in explosion mode — the parent needs
					// this data for winner scoring and the learner needs patterns.
					recordOutcome(ctx, recordOpts, a, req, "completed", 0,
						true, "", startTime, totalTokens, activityTokens, stepMetrics)
					return nil // CambrianExplosionWorkflow will handle task closing, pushing, and merging
				}

				// Close the task — it's done. New work = new morsel.
				closeCtx := workflow.WithActivityOptions(ctx, recordOpts)
				_ = workflow.ExecuteActivity(closeCtx, a.CloseTaskActivity, req.TaskID, "completed").Get(ctx, nil)

				// Mark morsel file as done — prevents re-dispatch
				markCtx := workflow.WithActivityOptions(ctx, recordOpts)
				if err := workflow.ExecuteActivity(markCtx, a.MarkMorselDoneActivity, baseWorkDir, req.TaskID).Get(ctx, nil); err != nil {
					logger.Warn(SharkPrefix+" Failed to mark morsel done (non-fatal)", "error", err)
				}

				// Auto-unblock downstream morsels whose deps are now all satisfied
				unblockCtx := workflow.WithActivityOptions(ctx, recordOpts)
				var unblocked []string
				if err := workflow.ExecuteActivity(unblockCtx, a.UnblockDependentsActivity, baseWorkDir, req.TaskID).Get(ctx, &unblocked); err != nil {
					logger.Warn(SharkPrefix+" Failed to auto-unblock dependents (non-fatal)", "error", err)
				}
				if len(unblocked) > 0 {
					logger.Info(SharkPrefix+" Auto-unblocked downstream morsels", "count", len(unblocked), "ids", unblocked)
				}

				recordOutcome(ctx, recordOpts, a, req, "completed", 0,
					true, "", startTime, totalTokens, activityTokens, stepMetrics)

				// ===== CHUM LOOP — spawn async learner + groomer =====
				spawnCHUMWorkflows(ctx, logger, req, plan, baseWorkDir)

				// ===== PROTEIN FOLD — record execution result =====
				if activeProteinID != "" {
					retroReq := MoleculeRetroRequest{
						ProteinID:    activeProteinID,
						MorselID:     req.TaskID,
						DoDPassed:    true,
						BuildPassed:  true,
						AttemptCount: attempt + 1,
					}
					var retro store.MoleculeRetro
					retroCtx := workflow.WithActivityOptions(ctx, recordOpts)
					_ = workflow.ExecuteActivity(retroCtx, a.MoleculeRetroActivity, retroReq).Get(ctx, &retro)

					fold := store.ProteinFold{
						ProteinID: activeProteinID,
						Project:   req.Project,
						MorselID:  req.TaskID,
						Provider:  req.Agent,
						Success:   true,
						Retro:     FormatRetroJSON(&retro),
					}
					_ = workflow.ExecuteActivity(retroCtx, a.RecordProteinFoldActivity, fold).Get(ctx, nil)
				}

				// ===== GENOME EVOLUTION — DoD pass feeds DNA =====
				// The organism succeeded. Its approach becomes a pattern (DNA)
				// in the species genome. The organism dies. The gene lives.
				species := classifySpecies(req.TaskID, req.Prompt, plan.FilesToModify)
				genomeEntry := store.GenomeEntry{
					Pattern: plan.Summary,
					Reason:  "DoD passed",
					Files:   plan.FilesToModify,
					Agent:   req.Agent,
				}
				genomeCtx := workflow.WithActivityOptions(ctx, recordOpts)
				if err := workflow.ExecuteActivity(genomeCtx, a.EvolveGenomeActivity,
					species, true, genomeEntry).Get(ctx, nil); err != nil {
					logger.Warn(SharkPrefix+" Genome evolution failed (non-fatal)", "species", species, "error", err)
				} else {
					logger.Info(SharkPrefix+" Genome evolved — pattern added",
						"Species", species, "Pattern", truncate(plan.Summary, 80))
				}

				// ===== PROPAGATION — Push validated code to remote =====
				// The gene must propagate. A commit without a push is a dead gene.
				pushCtx := workflow.WithActivityOptions(ctx, worktreeOpts)
				if err := workflow.ExecuteActivity(pushCtx, a.PushWorktreeActivity, worktreePath).Get(ctx, nil); err != nil {
					logger.Warn(SharkPrefix+" Failed to push worktree branch", "error", err)
				}

				// ===== MERGE TO MAIN — squash-merge feature branch into main =====
				featureBranch := fmt.Sprintf("chum/%s", req.TaskID)
				mergeCtx := workflow.WithActivityOptions(ctx, worktreeOpts)
				var prNumber int
				if err := workflow.ExecuteActivity(mergeCtx, a.MergeToMainActivity,
					baseWorkDir, featureBranch, plan.Summary).Get(ctx, &prNumber); err != nil {
					logger.Warn(SharkPrefix+" Merge to main failed — branch pushed but not merged",
						"error", err, "branch", featureBranch)
					// If conflict, mark task as conflict status instead of completed.
					notify("conflict", map[string]string{"branch": featureBranch, "error": err.Error()})
				}

				// ===== PR REVIEW — spawn cross-model review as fire-and-forget child =====
				if prNumber > 0 {
					prReviewOpts := workflow.ChildWorkflowOptions{
						WorkflowID:        fmt.Sprintf("pr-review-%d-%s", prNumber, req.TaskID),
						ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_ABANDON,
					}
					prReviewCtx := workflow.WithChildOptions(ctx, prReviewOpts)
					prReviewFut := workflow.ExecuteChildWorkflow(prReviewCtx, PRReviewWorkflow, PRReviewRequest{
						PRNumber:  prNumber,
						Workspace: baseWorkDir,
						Author:    req.Agent,
					})
					var prReviewExec workflow.Execution
					if startErr := prReviewFut.GetChildWorkflowExecution().Get(ctx, &prReviewExec); startErr != nil {
						logger.Warn(SharkPrefix+" PR review workflow failed to start", "error", startErr, "pr", prNumber)
					} else {
						logger.Info(SharkPrefix+" PR review spawned", "WorkflowID", prReviewExec.ID, "PR", prNumber)
					}
				}

				cleanupWorktree()
				return nil
			}

			// DoD failed — classify before feeding back to agent
			recordStep(fmt.Sprintf("dod[%d]", attempt+1), dodStart, "failed")
			recordTrace("dod", "phase_boundary", -0.2, false, traceMetadataJSON(
				"passed", false, "failures", truncate(strings.Join(dodResult.Failures, "; "), 500),
			))
			failureMsg := strings.Join(dodResult.Failures, "; ")

			// JUDGEMENT LAYER: infrastructure failures are NOT the shark's fault.
			// Transient (parallel lock, git lock) → one free retry.
			// Persistent (disk full, tool missing) → rescue/escalate immediately.
			if isInfrastructureFailure(strings.ToLower(failureMsg)) {
				reason := extractInfraReason(strings.ToLower(failureMsg))
				logger.Warn(SharkPrefix+" DoD failed due to INFRASTRUCTURE",
					"Attempt", attempt+1, "Reason", reason,
					"Transient", isTransientInfraFailure(strings.ToLower(failureMsg)))

				if isTransientInfraFailure(strings.ToLower(failureMsg)) {
					// Transient: one free retry (don't burn attempt), but only once.
					// If we've already had an infra retry, escalate.
					infraKey := fmt.Sprintf("infra_retry_%s", reason)
					if _, seen := infraRetries[infraKey]; !seen {
						infraRetries[infraKey] = true
						notify("dod_infra", map[string]string{
							"reason": reason, "attempt": fmt.Sprintf("%d", attempt+1),
							"action": "retrying (transient)",
						})
						attempt-- // don't burn the attempt
						continue
					}
				}

				// Persistent, or transient that already retried → RESCUE.
				// Don't burn more tokens on something the shark can't fix.
				notify("rescue", map[string]string{
					"reason": fmt.Sprintf("Infrastructure failure: %s", reason),
					"task":   req.TaskID,
				})
				allFailures = append(allFailures,
					fmt.Sprintf("INFRASTRUCTURE RESCUE (attempt %d): %s", attempt+1, reason))
				logger.Error(SharkPrefix+" Infrastructure rescue — aborting retries",
					"TaskID", req.TaskID, "Reason", reason)
				break // exit retry loop — escalate to human
			}

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

			// Extract truncated build output for the Matrix notification
			var notifyDetail string
			for _, check := range dodResult.Checks {
				if !check.Passed && check.Output != "" {
					notifyDetail = truncate(check.Output, 500)
					break
				}
			}
			notify("dod_fail", map[string]string{"failures": failureMsg, "attempt": fmt.Sprintf("%d", attempt+1), "detail": notifyDetail})
			logger.Warn(SharkPrefix+" DoD failed", "Attempt", attempt+1, "Failures", failureMsg)

			// --- FAILURE TRIAGE — read output, decide retry vs rescope ---
			// Every failure is analyzed. The triage reads the agent's actual
			// output and decides: retry with guidance, or send to turtles.
			triageStart := workflow.Now(ctx)
			triageCtx := workflow.WithActivityOptions(ctx, triageOpts)
			var triageResult FailureTriageResult
			if err := workflow.ExecuteActivity(triageCtx, a.FailureTriageActivity, FailureTriageRequest{
				TaskID:      req.TaskID,
				Project:     req.Project,
				WorkDir:     req.WorkDir,
				Agent:       req.Agent,
				FailureType: "dod",
				Failures:    dodResult.Failures,
				AgentOutput: execResult.Output,
				Attempt:     attempt + 1,
				MaxRetries:  maxRetries,
				PlanSummary: plan.Summary,
				Tier:        tier.Tier,
			}).Get(ctx, &triageResult); err != nil {
				logger.Warn(SharkPrefix+" Failure triage failed (non-fatal, continuing)", "error", err)
				recordStep(fmt.Sprintf("triage[%d]", attempt+1), triageStart, "failed")
			} else {
				recordStep(fmt.Sprintf("triage[%d]", attempt+1), triageStart, "ok")

				if triageResult.Decision == "rescope" {
					logger.Info(SharkPrefix+" Triage decision: RESCOPE to turtles",
						"Reason", triageResult.RescopeReason, "Category", triageResult.Category)
					allFailures = append(allFailures,
						fmt.Sprintf("Triage rescope (attempt %d): %s", attempt+1, triageResult.RescopeReason))
					rescopeTriggered = true
					rescopeReason = triageResult.RescopeReason
					break // break retry loop → will also break tier loop below
				}

				// Decision: retry with guidance
				if triageResult.Guidance != "" {
					plan.PreviousErrors = append(plan.PreviousErrors,
						"TRIAGE GUIDANCE (from failure analysis): "+triageResult.Guidance)
					logger.Info(SharkPrefix+" Triage guidance injected for next attempt",
						"Category", triageResult.Category)
				}
			}

			// --- AUTO-FIX: run gofmt + goimports before next retry ---
			// Cheap and deterministic — fixes formatting issues that agents
			// commonly introduce, saving a full retry attempt.
			if attempt+1 < maxRetries {
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
		}

		// If triage said rescope, skip remaining tiers entirely
		if rescopeTriggered {
			logger.Info(SharkPrefix+" Triage rescope — skipping remaining tiers",
				"Reason", rescopeReason)
			break
		}

		logger.Warn(SharkPrefix+" Tier exhausted, escalating",
			"Tier", tier.Tier, "Provider", tier.ProviderKey, "Attempts", maxRetries)
	} // end tier loop

	// ===== ESCALATE — all tiers exhausted =====
	awaitResumeGate()
	updateSearchAttributes(chumWorkflowStatusEscalated)

	// Terminal failure — record and backpropagate
	recordTrace("escalate", "phase_boundary", -0.5, true, traceMetadataJSON(
		"attempts", escalationAttempt, "failures", len(allFailures),
	))
	backpropTrace(-0.5)

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
		HandoffCount: 0,
	}).Get(ctx, nil); err != nil {
		logger.Warn(SharkPrefix+" Escalation activity failed (best-effort)", "error", err)
	}
	recordStep("escalate", escalateStart, "ok")

	// Reset task to "ready" — it re-enters the top of the pipeline.
	// The dispatcher sees PreviousErrors and routes it through PlanningCeremonyWorkflow,
	// which replans, reslices via decomp, places into the DAG, and re-dispatches to sharks.
	closeCtx := workflow.WithActivityOptions(ctx, recordOpts)
	_ = workflow.ExecuteActivity(closeCtx, a.CloseTaskActivity, req.TaskID, "ready").Get(ctx, nil)
	logger.Info(SharkPrefix+" Task reset to ready — will be replanned and resliced",
		"TaskID", req.TaskID, "Attempts", escalationAttempt)

	// Record outcome even on escalation — the store needs the failure record
	// and tokens burned, and the learner extracts antibodies from failures.
	recordOutcome(ctx, recordOpts, a, req, "escalated", 1,
		false, strings.Join(allFailures, "; "),
		startTime, totalTokens, activityTokens, stepMetrics)

	// Spawn failure learner — extract antibodies from what went wrong.
	spawnFailureLearner(ctx, workflow.GetLogger(ctx), req, plan, baseWorkDir)

	cleanupWorktree()
	return fmt.Errorf("task escalated after %d attempts: %s", escalationAttempt, strings.Join(allFailures, "; "))
}

// recordOutcome is a helper to persist the workflow outcome via RecordOutcomeActivity.
func recordOutcome(ctx workflow.Context, opts workflow.ActivityOptions, a *Activities,
	req TaskRequest, status string, exitCode int,
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
		Handoffs:       0,
		TotalTokens:    tokens,
		ActivityTokens: activityTokens,
		StepMetrics:    steps,
	}).Get(ctx, nil); err != nil {
		logger.Warn(SharkPrefix+" RecordOutcome activity failed (best-effort)", "error", err)
	}
}

// recordOrganismLog is a fire-and-forget helper to persist organism logs from
// any non-shark workflow (turtle, crab, learner, groomer, dispatcher, explosion).
func recordOrganismLog(ctx workflow.Context, organismType, taskID, project, status, details string,
	startTime time.Time, steps int, errMsg string) {

	logger := workflow.GetLogger(ctx)
	opts := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	}
	actCtx := workflow.WithActivityOptions(ctx, opts)

	wfID := workflow.GetInfo(ctx).WorkflowExecution.ID
	duration := workflow.Now(ctx).Sub(startTime).Seconds()

	var a *Activities
	if err := workflow.ExecuteActivity(actCtx, a.RecordOrganismLogActivity, OrganismLog{
		OrganismType: organismType,
		WorkflowID:   wfID,
		TaskID:       taskID,
		Project:      project,
		Status:       status,
		DurationS:    duration,
		Details:      details,
		Steps:        steps,
		Error:        errMsg,
	}).Get(ctx, nil); err != nil {
		logger.Warn("Organism log recording failed (best-effort)", "error", err, "type", organismType)
	}
}

// spawnCHUMWorkflows fires off the ContinuousLearner and TacticalGroom as
// detached child workflows. They run completely async — the parent returns
// immediately and the children survive even after it completes.
func spawnCHUMWorkflows(ctx workflow.Context, logger log.Logger, req TaskRequest, plan StructuredPlan, baseWorkDir string) {
	chumOpts := workflow.ChildWorkflowOptions{
		ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_ABANDON,
	}

	// --- Spawn ContinuousLearnerWorkflow ---
	learnerReq := LearnerRequest{
		TaskID:         req.TaskID,
		Project:        req.Project,
		WorkDir:        baseWorkDir,
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
		TaskID:       req.TaskID,
		Project:      req.Project,
		WorkDir:      baseWorkDir,
		Tier:         "fast",
		FilesChanged: plan.FilesToModify,
		DiffSummary:  truncate(plan.Summary, 500),
		TaskTitle:    req.TaskTitle,
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

// spawnFailureLearner fires off the ContinuousLearner on failure paths.
// Unlike spawnCHUMWorkflows, it does NOT spawn TacticalGroom — failed work
// doesn't need grooming, but the learner still extracts antibodies.
func spawnFailureLearner(ctx workflow.Context, logger log.Logger, req TaskRequest, plan StructuredPlan, baseWorkDir string) {
	learnerOpts := workflow.ChildWorkflowOptions{
		ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_ABANDON,
		WorkflowID:        fmt.Sprintf("learner-fail-%s-%d", req.TaskID, workflow.Now(ctx).Unix()),
	}
	learnerCtx := workflow.WithChildOptions(ctx, learnerOpts)
	learnerReq := LearnerRequest{
		TaskID:         req.TaskID,
		Project:        req.Project,
		WorkDir:        baseWorkDir,
		Agent:          req.Agent,
		DoDPassed:      false,
		FilesChanged:   plan.FilesToModify,
		PreviousErrors: plan.PreviousErrors,
		Tier:           "fast",
	}
	learnerFut := workflow.ExecuteChildWorkflow(learnerCtx, ContinuousLearnerWorkflow, learnerReq)
	var learnerExec workflow.Execution
	if err := learnerFut.GetChildWorkflowExecution().Get(ctx, &learnerExec); err != nil {
		logger.Warn(SharkPrefix+" CHUM: Failure learner failed to start", "error", err)
	} else {
		logger.Info(SharkPrefix+" CHUM: Failure learner started", "WorkflowID", learnerExec.ID)
	}
}
