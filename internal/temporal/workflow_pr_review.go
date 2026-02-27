package temporal

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// PRReviewWorkflow fetches a PR diff, sends it to a cross-model reviewer via
// an authed CLI, and posts the review as a GitHub PR comment. Fire-and-forget:
// failures are logged but never block the parent workflow.
func PRReviewWorkflow(ctx workflow.Context, req PRReviewRequest) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("PR review workflow started", "pr", req.PRNumber, "author", req.Author)

	reviewOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	reviewCtx := workflow.WithActivityOptions(ctx, reviewOpts)

	var a *Activities
	var result PRReviewResult
	if err := workflow.ExecuteActivity(reviewCtx, a.ReviewPRActivity, req).Get(ctx, &result); err != nil {
		logger.Warn("PR review activity failed", "pr", req.PRNumber, "error", err)
		return fmt.Errorf("pr review failed for #%d: %w", req.PRNumber, err)
	}

	verdict := "approved"
	if !result.Approved {
		verdict = "changes_requested"
	}
	logger.Info("PR review complete",
		"pr", req.PRNumber,
		"reviewer", result.ReviewerAgent,
		"verdict", verdict,
		"issues", len(result.Issues),
		"suggestions", len(result.Suggestions),
	)

	// Notify via Matrix (fire-and-forget)
	notifyOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	nCtx := workflow.WithActivityOptions(ctx, notifyOpts)

	extra := map[string]string{
		"pr":       fmt.Sprintf("%d", req.PRNumber),
		"reviewer": result.ReviewerAgent,
		"verdict":  verdict,
	}
	_ = workflow.ExecuteActivity(nCtx, a.NotifyActivity, NotifyRequest{
		Event: "pr_review",
		Extra: extra,
	}).Get(ctx, nil)

	return nil
}
