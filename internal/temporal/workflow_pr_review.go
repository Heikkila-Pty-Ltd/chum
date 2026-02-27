package temporal

import (
	"fmt"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
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
		StartToCloseTimeout: 10 * time.Minute,
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

// PRReviewPollerWorkflow scans for open PRs that haven't been reviewed by CHUM
// and spawns PRReviewWorkflow for each. Runs on a Temporal Schedule.
func PRReviewPollerWorkflow(ctx workflow.Context, req PRReviewPollerRequest) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("PR review poller: scanning for unreviewed PRs", "workspace", req.Workspace)

	scanOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 1 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	scanCtx := workflow.WithActivityOptions(ctx, scanOpts)

	var a *Activities
	var prs []UnreviewedPR
	if err := workflow.ExecuteActivity(scanCtx, a.ScanOpenPRsActivity, req).Get(ctx, &prs); err != nil {
		logger.Warn("PR review poller: scan failed", "error", err)
		return fmt.Errorf("scan open PRs failed: %w", err)
	}

	if len(prs) == 0 {
		logger.Info("PR review poller: no unreviewed PRs found")
		return nil
	}

	logger.Info("PR review poller: found unreviewed PRs", "count", len(prs))

	// Spawn a PRReviewWorkflow for each unreviewed PR
	for _, pr := range prs {
		reviewReq := PRReviewRequest{
			PRNumber:  pr.Number,
			Workspace: req.Workspace,
			Author:    pr.Author,
		}

		childOpts := workflow.ChildWorkflowOptions{
			WorkflowID:        fmt.Sprintf("pr-review-%d-poll-%d", pr.Number, workflow.Now(ctx).Unix()),
			ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_ABANDON,
		}
		childCtx := workflow.WithChildOptions(ctx, childOpts)
		fut := workflow.ExecuteChildWorkflow(childCtx, PRReviewWorkflow, reviewReq)

		// Wait for child to start (not complete) so ABANDON policy applies.
		var childExec workflow.Execution
		if err := fut.GetChildWorkflowExecution().Get(ctx, &childExec); err != nil {
			logger.Warn("PR review poller: failed to start review", "pr", pr.Number, "error", err)
			continue
		}
		logger.Info("PR review poller: review spawned", "pr", pr.Number, "workflow_id", childExec.ID)
	}

	return nil
}
