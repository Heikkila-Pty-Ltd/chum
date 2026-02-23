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

	var a *Activities

	// Create future variables for each fork
	var futures []workflow.Future
	var explosionIDs []string

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

	// Gather results
	type ExplosionResult struct {
		Provider       string
		DoDPassed      bool
		CostUSD        float64
		Error          string
		Worktree       string
		ExplosionID    string
	}

	var results []ExplosionResult
	for i, fut := range futures {
		provider := providers[i].ProviderKey
		explosionID := explosionIDs[i]

		// Execute child and wait for its completion logic
		// We expect the workflow to return nil if it passed, or an error if it escalated/failed.
		err := fut.Get(ctx, nil)
		passed := (err == nil)
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		}

		results = append(results, ExplosionResult{
			Provider:    provider,
			DoDPassed:   passed,
			ExplosionID: explosionID,
			Error:       errMsg,
		})
	}

	// We have the results; now pick the fittest!
	var winner *ExplosionResult

	// Score them out
	for i := range results {
		res := &results[i]
		if !res.DoDPassed {
			continue // Dead branches don't get selected
		}
		// In MVP we just pick the first one that passed DoD. 
		// Future: track costs from a shared store or return from workflow to compute true Fitness.
		if winner == nil {
			winner = res
		}
	}

	recordOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	}
	recordCtx := workflow.WithActivityOptions(ctx, recordOpts)

	if winner == nil {
		logger.Error(SharkPrefix + " Cambrian Explosion failed — all species variations went extinct")
		var allFailures []string
		for _, res := range results {
			if res.Error != "" {
				allFailures = append(allFailures, fmt.Sprintf("%s: %s", res.Provider, res.Error))
			}
		}
		workflow.ExecuteActivity(recordCtx, a.EscalateActivity, req.TaskID, allFailures).Get(ctx, nil)
		return fmt.Errorf("all providers failed the explosion")
	}

	logger.Info(SharkPrefix+" Cambrian Explosion selected winner", "Winner", winner.Provider)

	// Since ChumAgentWorkflow skipped merging/pushing, we have to push the winner's branch!
	// Re-construct the winner's worktree branch format
	wtDir := WorktreeDir(req.TaskID, winner.ExplosionID)
	if err := workflow.ExecuteActivity(recordCtx, a.PushWorktreeActivity, wtDir).Get(ctx, nil); err != nil {
		logger.Warn(SharkPrefix+" Failed to push winner worktree", "error", err)
	}

	// Update Task Status
	workflow.ExecuteActivity(recordCtx, a.CloseTaskActivity, req.TaskID, "completed").Get(ctx, nil)
	recordOutcome(ctx, recordOpts, a, req, "completed", 0, 0, true, "", workflow.Now(ctx), TokenUsage{}, nil, nil)

	// Clean up all worktrees created during the explosion
	for _, res := range results {
		resDir := WorktreeDir(req.TaskID, res.ExplosionID)
		workflow.ExecuteActivity(recordCtx, a.CleanupWorktreeActivity, req.WorkDir, resDir).Get(ctx, nil)
	}

	return nil
}
