package temporal

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// PostMortemWorkflow investigates a failed workflow. Step 1 (td19a) fetches
// failure context and records it. Step 2 (td19b) will add LLM investigation
// and antibody filing.
func PostMortemWorkflow(ctx workflow.Context, req PostMortemRequest) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("Post-mortem started",
		"workflow_id", req.Failure.WorkflowID,
		"task_id", req.Failure.TaskID,
		"error", truncate(req.Failure.ErrorMessage, 200),
	)

	// Record health event for observability
	recordOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	recordCtx := workflow.WithActivityOptions(ctx, recordOpts)

	var a *Activities
	_ = workflow.ExecuteActivity(recordCtx, a.RecordHealthEventActivity,
		"postmortem_complete",
		fmt.Sprintf("wf=%s task=%s err=%s",
			req.Failure.WorkflowID,
			req.Failure.TaskID,
			truncate(req.Failure.ErrorMessage, 300)),
	).Get(ctx, nil)

	// Notify via Matrix
	notifyOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	nCtx := workflow.WithActivityOptions(ctx, notifyOpts)
	_ = workflow.ExecuteActivity(nCtx, a.NotifyActivity, NotifyRequest{
		Event: "postmortem",
		Extra: map[string]string{
			"workflow_id": req.Failure.WorkflowID,
			"task_id":     req.Failure.TaskID,
			"error":       truncate(req.Failure.ErrorMessage, 200),
		},
	}).Get(ctx, nil)

	logger.Info("Post-mortem complete",
		"workflow_id", req.Failure.WorkflowID,
		"task_id", req.Failure.TaskID,
	)

	recordOrganismLog(ctx, "postmortem", req.Failure.TaskID, req.Project, "completed",
		fmt.Sprintf("investigated failure: %s", truncate(req.Failure.ErrorMessage, 200)),
		workflow.Now(ctx), 1, "")

	return nil
}
