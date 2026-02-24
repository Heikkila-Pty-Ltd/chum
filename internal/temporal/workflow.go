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
//  4. REVIEW      — Different agent reviews (claude↔codex cross-pollination)
//  5. HANDOFF     — If review fails, swap agents and re-execute (up to 3 handoffs)
//  6. DOD         — Compile/test/lint verification via git.RunPostMergeChecks
//  7. RECORD      — Persist outcome to store (feeds learner loop)
//  8. ESCALATE    — If DoD fails after retries, escalate to chief + human
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
		StartToCloseTimeout: 15 * time.Minute,
		HeartbeatTimeout:    30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1}, // no auto-retry, we handle it
	}
	reviewOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	dodOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
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

		maxRetries := retriesForTier(tier.Tier, req.MaxRetriesOverride)

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

			// --- CROSS-MODEL REVIEW LOOP ---
			updateSearchAttributes(chumWorkflowStatusReview)

			reviewStart := workflow.Now(ctx)
			reviewPassed := false
			reviewStatus := "failed"
			effectiveMaxHandoffs := maxHandoffs
			if req.MaxHandoffsOverride > 0 {
				effectiveMaxHandoffs = req.MaxHandoffsOverride
			}
			for handoff := 0; handoff < effectiveMaxHandoffs; handoff++ {
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

				if req.ExplosionID != "" {
					retainWorktree = true
					// Record outcome even in explosion mode — the parent needs
					// this data for winner scoring and the learner needs patterns.
					recordOutcome(ctx, recordOpts, a, req, "completed", 0,
						handoffCount, true, "", startTime, totalTokens, activityTokens, stepMetrics)
					return nil // CambrianExplosionWorkflow will handle task closing, pushing, and merging
				}

				// Close the task — it's done. New work = new morsel.
				closeCtx := workflow.WithActivityOptions(ctx, recordOpts)
				_ = workflow.ExecuteActivity(closeCtx, a.CloseTaskActivity, req.TaskID, "completed").Get(ctx, nil)

				recordOutcome(ctx, recordOpts, a, req, "completed", 0,
					handoffCount, true, "", startTime, totalTokens, activityTokens, stepMetrics)

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
				if err := workflow.ExecuteActivity(mergeCtx, a.MergeToMainActivity,
					baseWorkDir, featureBranch, plan.Summary).Get(ctx, nil); err != nil {
					logger.Warn(SharkPrefix+" Merge to main failed — branch pushed but not merged",
						"error", err, "branch", featureBranch)
					// If conflict, mark task as conflict status instead of completed.
					notify("conflict", map[string]string{"branch": featureBranch, "error": err.Error()})
				}

				cleanupWorktree()
				return nil
			}

			// DoD failed — classify before feeding back to agent
			recordStep(fmt.Sprintf("dod[%d]", attempt+1), dodStart, "failed")
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
			logger.Warn(SharkPrefix+" DoD failed, retrying", "Attempt", attempt+1, "Failures", failureMsg)

			// --- FAILURE TRIAGE — read output, decide retry vs rescope ---
			// Every failure is analysed. The triage reads the agent's actual
			// output and decides: retry with guidance, or send to turtles.
			triageStart := workflow.Now(ctx)
			triageCtx := workflow.WithActivityOptions(ctx, triageOpts)
			var triageResult FailureTriageResult
			if err := workflow.ExecuteActivity(triageCtx, a.FailureTriageActivity, FailureTriageRequest{
				TaskID:      req.TaskID,
				Project:     req.Project,
				WorkDir:     req.WorkDir,
				Agent:       currentAgent,
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

	// ===== FILE INVESTIGATION TASK — pipeline eats its own failures =====
	investigateCtx := workflow.WithActivityOptions(ctx, recordOpts)
	failureSummary := truncate(strings.Join(allFailures, "; "), 200)
	_ = workflow.ExecuteActivity(investigateCtx, a.FileInvestigationTaskActivity, InvestigationRequest{
		Category:    "escalation",
		Title:       fmt.Sprintf("Investigate repeated failure: %s — %s", req.TaskID, truncate(plan.Summary, 60)),
		Description: fmt.Sprintf("Task `%s` (project: %s) failed after %d attempts across all provider tiers.\n\nPlan: %s\n\nFailures:\n%s", req.TaskID, req.Project, escalationAttempt, plan.Summary, failureSummary),
		Source:      workflow.GetInfo(ctx).WorkflowExecution.ID,
		Project:     req.Project,
		Severity:    "warning",
	}).Get(ctx, nil)

	// ===== SPAWN LEARNER ON FAILURE =====
	// The octopus learns MORE from failures than successes.
	// Failures carry antibodies: what went wrong, which files are risky,
	// which errors repeated. This is the richest evolutionary data.
	failureLearnerReq := LearnerRequest{
		TaskID:         req.TaskID,
		Project:        req.Project,
		WorkDir:        baseWorkDir,
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

	// ===== GENOME EVOLUTION — escalation feeds antibodies =====
	// The organism failed. Its approach becomes an antibody in the species genome.
	// If this antibody appears 3+ times, it auto-promotes to fossil (EXTINCT).
	species := classifySpecies(req.TaskID, req.Prompt, plan.FilesToModify)
	genomeEntry := store.GenomeEntry{
		Pattern: plan.Summary,
		Reason:  fmt.Sprintf("escalated after %d attempts: %s", escalationAttempt, truncate(strings.Join(allFailures, "; "), 200)),
		Files:   plan.FilesToModify,
		Agent:   req.Agent,
	}
	genomeCtx := workflow.WithActivityOptions(ctx, recordOpts)
	if err := workflow.ExecuteActivity(genomeCtx, a.EvolveGenomeActivity,
		species, false, genomeEntry).Get(ctx, nil); err != nil {
		logger.Warn(SharkPrefix+" Genome evolution failed (non-fatal)", "species", species, "error", err)
	} else {
		logger.Info(SharkPrefix+" Genome evolved — antibody added",
			"Species", species, "Antibody", truncate(plan.Summary, 80))
	}

	// Flag species as hibernating (unless override)
	// User requested golf-directory to never hibernate so it keeps trying.
	if req.Project == "golf-directory" {
		logger.Info(SharkPrefix+" Skipping hibernation for golf-directory (user override)", "Species", species)
	} else {
		hibernateCtx := workflow.WithActivityOptions(ctx, recordOpts)
		if err := workflow.ExecuteActivity(hibernateCtx, a.HibernateGenomeActivity, species).Get(ctx, nil); err != nil {
			logger.Warn(SharkPrefix+" Genome hibernation failed (non-fatal)", "species", species, "error", err)
		}
	}

	// ===== TURTLE RESCUE — beached shark → turtle investigation =====
	// Instead of letting the task rot, spawn a turtle ceremony to investigate
	// WHY it failed and decompose it into better, smaller morsels.
	// The shark dies, but the turtles carry its failure memory into new plans.
	var failureContext strings.Builder
	failureContext.WriteString(fmt.Sprintf("BEACHED SHARK INVESTIGATION: Task `%s` failed after %d attempts across all provider tiers.\n\n", req.TaskID, escalationAttempt))
	failureContext.WriteString(fmt.Sprintf("ORIGINAL PLAN: %s\n\n", plan.Summary))
	failureContext.WriteString("FAILURE HISTORY:\n")
	for i, f := range allFailures {
		failureContext.WriteString(fmt.Sprintf("  %d. %s\n", i+1, truncate(f, 300)))
	}
	failureContext.WriteString("\nINSTRUCTION: Investigate why this task failed repeatedly. Consider:\n")
	failureContext.WriteString("- Is the task scope too broad? Decompose into smaller, achievable morsels.\n")
	failureContext.WriteString("- Are there missing prerequisites or dependencies?\n")
	failureContext.WriteString("- Should the acceptance criteria be revised?\n")
	failureContext.WriteString("- Is this task fundamentally blocked by infrastructure issues?\n")

	turtleReq := TurtlePlanningRequest{
		TaskID:      fmt.Sprintf("rescue-%s", req.TaskID),
		Project:     req.Project,
		WorkDir:     baseWorkDir,
		Description: failureContext.String(),
		Context:     plan.FilesToModify,
		Tier:        "balanced", // quality matters for rescue planning
	}
	turtleOpts := workflow.ChildWorkflowOptions{
		ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_ABANDON,
		WorkflowID:        fmt.Sprintf("turtle-rescue-%s-%d", req.TaskID, workflow.Now(ctx).Unix()),
	}
	turtleCtx := workflow.WithChildOptions(ctx, turtleOpts)
	turtleFut := workflow.ExecuteChildWorkflow(turtleCtx, TurtleToCrabWorkflow, turtleReq)
	if err := turtleFut.GetChildWorkflowExecution().Get(ctx, nil); err != nil {
		logger.Warn(SharkPrefix+" Turtle rescue ceremony failed to start", "error", err)
	} else {
		logger.Info(SharkPrefix+" 🐢 Turtle rescue spawned — beached shark will be investigated and rescoped",
			"TaskID", req.TaskID, "TurtleWorkflowID", fmt.Sprintf("turtle-rescue-%s", req.TaskID))
		notify("turtle_rescue", map[string]string{
			"task":     req.TaskID,
			"attempts": fmt.Sprintf("%d", escalationAttempt),
		})
	}

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
