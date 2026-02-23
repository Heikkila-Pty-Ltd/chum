package temporal

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// JanitorWorkflow runs on an hourly schedule to clean up stale git worktrees,
// branches, and other ephemeral artifacts left behind by completed or crashed
// shark organisms.
//
// Pipeline:
//  1. PruneWorktreesActivity — remove /tmp/chum-wt-* dirs with no active workflow
//  2. PruneBranchesActivity — delete stale chum/* branches with no worktree
//
// Best-effort — failures never block the main execution loop.
func JanitorWorkflow(ctx workflow.Context, workspaces []string) error {
	logger := workflow.GetLogger(ctx)
	logger.Info(JanitorPrefix + " Janitorial sweep starting")

	var a *Activities

	pruneOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	pruneCtx := workflow.WithActivityOptions(ctx, pruneOpts)

	// Step 1: Prune stale worktrees and branches across all projects.
	var result JanitorResult
	if err := workflow.ExecuteActivity(pruneCtx, a.JanitorSweepActivity, workspaces).Get(ctx, &result); err != nil {
		logger.Warn(JanitorPrefix+" Sweep failed", "error", err)
		return nil // best-effort
	}

	logger.Info(JanitorPrefix+" Sweep complete",
		"worktrees_pruned", result.WorktreesPruned,
		"branches_pruned", result.BranchesPruned,
		"dirs_cleaned", result.DirsCleaned,
		"errors", result.Errors,
	)
	return nil
}

// JanitorResult collects metrics from the janitorial sweep.
type JanitorResult struct {
	WorktreesPruned int      `json:"worktrees_pruned"`
	BranchesPruned  int      `json:"branches_pruned"`
	DirsCleaned     int      `json:"dirs_cleaned"`
	Errors          []string `json:"errors,omitempty"`
}
