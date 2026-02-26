package temporal

import (
	"fmt"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// CambrianExplosionWorkflow runs when a species has 0 generation (meaning it encounters a task type it hasn't successfully solved before).
// It dispatches the same task to multiple Top-Tier models in parallel, scores them based on fitness (success + cost),
// updates the genetic lineage, and applies the winning changes to the master branch.
func CambrianExplosionWorkflow(ctx workflow.Context, req TaskRequest, providers []EscalationTier) error {
	logger := workflow.GetLogger(ctx)
	logger.Info(SharkPrefix+" Triggering Cambrian Explosion!", "TaskID", req.TaskID, "Providers", len(providers))
	startTime := workflow.Now(ctx)

	var a *Activities

	// Create future variables for each fork
	futures := make([]workflow.Future, 0, len(providers))
	explosionIDs := make([]string, 0, len(providers))

	// Launch parallel isolated sandboxes
	for i, tier := range providers {
		explosionID := fmt.Sprintf("exp-%d", i)
		explosionIDs = append(explosionIDs, explosionID)

		// Create isolated child workflow config
		childReq := req
		childReq.Agent = tier.CLI
		childReq.Model = tier.Model
		childReq.Provider = tier.ProviderKey
		childReq.ExplosionID = explosionID // PREVENTS worktree collision and DB mutations

		timeout := workflowTimeout(15) // default to 15m for explosion children
		wfID := fmt.Sprintf("exp-%s-%s-%d", req.TaskID, tier.ProviderKey, workflow.Now(ctx).Unix())

		childOpts := workflow.ChildWorkflowOptions{
			WorkflowID:               wfID,
			TaskQueue:                DefaultTaskQueue,
			WorkflowExecutionTimeout: timeout,
			WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
			ParentClosePolicy:        enumspb.PARENT_CLOSE_POLICY_ABANDON,
		}
		childCtx := workflow.WithChildOptions(ctx, childOpts)

		logger.Info(SharkPrefix+" Exploding organism into biome", "Provider", tier.ProviderKey, "ExplosionID", explosionID)
		fut := workflow.ExecuteChildWorkflow(childCtx, ChumAgentWorkflow, childReq)
		futures = append(futures, fut)
	}

	// Gather results — wait for ALL children to complete before scoring.
	type ExplosionResult struct {
		Provider    string
		DoDPassed   bool
		Error       string
		ExplosionID string
		ElapsedS    float64
	}

	results := make([]ExplosionResult, 0, len(futures))

	sel := workflow.NewSelector(ctx)
	remaining := len(futures)

	for i, fut := range futures {
		sel.AddFuture(fut, func(f workflow.Future) {
			provider := providers[i].ProviderKey
			explosionID := explosionIDs[i]

			err := f.Get(ctx, nil)
			passed := (err == nil)
			errMsg := ""
			if err != nil {
				errMsg = err.Error()
			}

			elapsed := workflow.Now(ctx).Sub(startTime).Seconds()
			results = append(results, ExplosionResult{
				Provider:    provider,
				DoDPassed:   passed,
				ExplosionID: explosionID,
				Error:       errMsg,
				ElapsedS:    elapsed,
			})

			status := "PASSED"
			if !passed {
				status = "FAILED"
			}
			logger.Info(SharkPrefix+" Explosion organism finished",
				"Provider", provider, "Status", status, "ElapsedS", elapsed)
		})
	}

	// Drain all futures — every organism must finish before we can compare.
	for remaining > 0 {
		sel.Select(ctx)
		remaining--
	}

	// === FOSSIL RECORD — log all results for paleontologist analysis ===
	passedCount := 0
	for _, res := range results {
		if res.DoDPassed {
			passedCount++
		}
	}
	logger.Info(SharkPrefix+" Cambrian Explosion complete",
		"TaskID", req.TaskID, "Total", len(results), "Passed", passedCount,
		"Failed", len(results)-passedCount)

	recordOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	}
	recordCtx := workflow.WithActivityOptions(ctx, recordOpts)

	// === WINNER SELECTION ===
	passingResults := make([]ExplosionResult, 0, len(results))
	for _, res := range results {
		if res.DoDPassed {
			passingResults = append(passingResults, res)
		}
	}

	if len(passingResults) == 0 {
		logger.Error(SharkPrefix + " Cambrian Explosion failed — all species variations went extinct")
		var allFailures []string
		for _, res := range results {
			if res.Error != "" {
				allFailures = append(allFailures, fmt.Sprintf("%s: %s", res.Provider, res.Error))
			}
		}
		if err := workflow.ExecuteActivity(recordCtx, a.EscalateActivity, req.TaskID, allFailures).Get(ctx, nil); err != nil {
			logger.Warn(SharkPrefix+" Failed to record escalation in explosion workflow", "error", err)
		}

		// Clean up all worktrees
		for _, res := range results {
			resDir := WorktreeDir(req.TaskID, res.ExplosionID)
			if err := workflow.ExecuteActivity(recordCtx, a.CleanupWorktreeActivity, req.WorkDir, resDir).Get(ctx, nil); err != nil {
				logger.Warn(SharkPrefix+" Failed cleanup worktree after explosion extinction", "error", err, "workdir", resDir)
			}
		}
		recordOrganismLog(ctx, "explosion", req.TaskID, req.Project, "failed",
			fmt.Sprintf("all %d providers extinct", len(results)),
			startTime, len(results), "all providers failed")

		return fmt.Errorf("all providers failed the explosion")
	}

	// Determine winner: if only 1 passed, it wins. If multiple, senior review.
	var winner *ExplosionResult

	if len(passingResults) == 1 {
		winner = &passingResults[0]
		logger.Info(SharkPrefix+" Single DoD-passing organism — auto-selected",
			"Winner", winner.Provider, "ElapsedS", winner.ElapsedS)
	} else {
		// Multiple candidates passed — get git diffs and call senior reviewer.
		reviewOpts := workflow.ActivityOptions{
			StartToCloseTimeout: 5 * time.Minute,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
		}
		reviewCtx := workflow.WithActivityOptions(ctx, reviewOpts)

		// Build candidates with their git diffs
		candidates := make([]ExplosionCandidate, 0, len(passingResults))
		for _, res := range passingResults {
			wtDir := WorktreeDir(req.TaskID, res.ExplosionID)
			// Get the diff for this candidate
			var diff string
			diffOpts := workflow.ActivityOptions{
				StartToCloseTimeout: 30 * time.Second,
				RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
			}
			diffCtx := workflow.WithActivityOptions(ctx, diffOpts)
			_ = workflow.ExecuteActivity(diffCtx, a.GetWorktreeDiffActivity, wtDir).Get(ctx, &diff)

			candidates = append(candidates, ExplosionCandidate{
				Provider:    res.Provider,
				ExplosionID: res.ExplosionID,
				Diff:        diff,
				ElapsedS:    res.ElapsedS,
			})
		}

		var winnerIdx int
		if err := workflow.ExecuteActivity(reviewCtx, a.ReviewExplosionCandidatesActivity,
			req.TaskID, candidates).Get(ctx, &winnerIdx); err != nil {
			logger.Warn(SharkPrefix+" Senior review failed — using fastest candidate", "error", err)
			winnerIdx = 0
		}
		winner = &passingResults[winnerIdx]
		logger.Info(SharkPrefix+" Senior review selected winner",
			"Winner", winner.Provider, "ElapsedS", winner.ElapsedS,
			"CandidatesReviewed", len(passingResults))
	}

	logger.Info(SharkPrefix+" Cambrian Explosion — pushing winner", "Winner", winner.Provider)

	// Push and merge the winner's branch.
	wtDir := WorktreeDir(req.TaskID, winner.ExplosionID)
	if err := workflow.ExecuteActivity(recordCtx, a.PushWorktreeActivity, wtDir).Get(ctx, nil); err != nil {
		logger.Warn(SharkPrefix+" Failed to push winner worktree", "error", err)
	}

	featureBranch := fmt.Sprintf("chum/%s-%s", req.TaskID, winner.ExplosionID)
	if err := workflow.ExecuteActivity(recordCtx, a.MergeToMainActivity,
		req.WorkDir, featureBranch, "Cambrian Explosion winner: "+winner.Provider).Get(ctx, nil); err != nil {
		logger.Warn(SharkPrefix+" Merge to main failed for explosion winner", "error", err, "branch", featureBranch)
	}

	// Close the task.
	if err := workflow.ExecuteActivity(recordCtx, a.CloseTaskActivity, req.TaskID, "completed").Get(ctx, nil); err != nil {
		logger.Warn(SharkPrefix+" Failed to close task after explosion winner", "error", err, "task", req.TaskID)
	}
	recordOutcome(ctx, recordOpts, a, req, "completed", 0, true, "", workflow.Now(ctx), TokenUsage{}, nil, nil)

	// Clean up ALL worktrees (including winner's, since it's merged now).
	for _, res := range results {
		resDir := WorktreeDir(req.TaskID, res.ExplosionID)
		if err := workflow.ExecuteActivity(recordCtx, a.CleanupWorktreeActivity, req.WorkDir, resDir).Get(ctx, nil); err != nil {
			logger.Warn(SharkPrefix+" Failed cleanup worktree after explosion", "error", err, "workdir", resDir)
		}
	}

	recordOrganismLog(ctx, "explosion", req.TaskID, req.Project, "completed",
		fmt.Sprintf("winner=%s, %d/%d passed", winner.Provider, passedCount, len(results)),
		startTime, len(results), "")

	return nil
}
