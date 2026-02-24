package temporal

import (
	"fmt"
	"strconv"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// PlanningCeremonyWorkflow implements interactive sprint planning.
//
// Planning happens BEFORE any code is written. The chief grooms the backlog,
// presents options one at a time, asks sequential clarifying questions (each
// depending on the previous answer), then summarizes what/why/effort.
// Only after greenlight does it produce a TaskRequest for the execution workflow.
//
// Supports up to 5 planning cycles — iterate, improve, explore ideas, find best options.
// Nothing goes to the sharks until it's chum.
func PlanningCeremonyWorkflow(ctx workflow.Context, req PlanningRequest) (*TaskRequest, error) {
	logger := workflow.GetLogger(ctx)

	if req.Agent == "" {
		req.Agent = "claude"
	}
	if req.Tier == "" {
		req.Tier = "fast"
	}
	if req.SlowStepThreshold <= 0 {
		req.SlowStepThreshold = defaultSlowStepThreshold
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		HeartbeatTimeout:    30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var a *Activities

	const maxPlanningCycles = 5

	for cycle := 0; cycle < maxPlanningCycles; cycle++ {
		logger.Info("Planning cycle", "Cycle", cycle+1, "MaxCycles", maxPlanningCycles)

		// ===== PHASE 1: GROOM BACKLOG =====
		logger.Info("Planning: grooming backlog", "Project", req.Project)

		var backlog BacklogPresentation
		if err := workflow.ExecuteActivity(ctx, a.GroomBacklogActivity, req).Get(ctx, &backlog); err != nil {
			return nil, fmt.Errorf("backlog grooming failed: %w", err)
		}

		logger.Info("Planning: backlog ready", "Items", len(backlog.Items))

		// ===== PHASE 2: ITEM SELECTION =====
		logger.Info("Planning: waiting for item selection")

		selectChan := workflow.GetSignalChannel(ctx, "item-selected")
		var selectedID string
		selectChan.Receive(ctx, &selectedID)

		var selectedItem *BacklogItem
		for i := range backlog.Items {
			if backlog.Items[i].ID == selectedID {
				selectedItem = &backlog.Items[i]
				break
			}
		}
		if selectedItem == nil {
			selectedItem = &BacklogItem{ID: "custom", Title: selectedID}
		}

		logger.Info("Planning: item selected", "Title", selectedItem.Title)

		// ===== PHASE 3: SEQUENTIAL QUESTIONS =====
		var questions []PlanningQuestion
		if err := workflow.ExecuteActivity(ctx, a.GenerateQuestionsActivity, req, *selectedItem).Get(ctx, &questions); err != nil {
			return nil, fmt.Errorf("question generation failed: %w", err)
		}

		answerChan := workflow.GetSignalChannel(ctx, "answer")
		answers := make(map[string]string)

		for i := range questions {
			q := &questions[i]
			q.Number = i + 1
			q.Total = len(questions)

			if i > 0 {
				prevA := answers[strconv.Itoa(i)]
				q.Context = fmt.Sprintf("Based on Q%d answer: %s", i, prevA)
			}

			logger.Info("Planning: question", "N", q.Number, "Of", q.Total, "Q", q.Question)

			var answer string
			answerChan.Receive(ctx, &answer)
			answers[strconv.Itoa(i+1)] = answer

			logger.Info("Planning: answered", "Q", q.Number, "A", answer)
		}

		// ===== PHASE 4: SUMMARY =====
		var summary PlanSummary
		if err := workflow.ExecuteActivity(ctx, a.SummarizePlanActivity, req, *selectedItem, answers).Get(ctx, &summary); err != nil {
			return nil, fmt.Errorf("plan summary failed: %w", err)
		}

		logger.Info("Planning: summary", "What", summary.What, "Effort", summary.Effort)

		// ===== PHASE 5: GREENLIGHT =====
		logger.Info("Planning: waiting for greenlight", "Cycle", cycle+1)

		greenlightChan := workflow.GetSignalChannel(ctx, "greenlight")
		var decision string
		greenlightChan.Receive(ctx, &decision)

		if decision == "GO" {
			// ===== PRODUCE TASK REQUEST =====
			taskReq := &TaskRequest{
				TaskID:            selectedItem.ID,
				Project:           req.Project,
				Prompt:            summary.What,
				Agent:             req.Agent,
				Reviewer:          DefaultReviewer(req.Agent),
				WorkDir:           req.WorkDir,
				DoDChecks:         summary.DoDChecks,
				SlowStepThreshold: req.SlowStepThreshold,
			}

			logger.Info("Planning: GREENLIT — throwing to the sharks",
				"TaskID", taskReq.TaskID,
				"Cycle", cycle+1,
				"What", summary.What,
				"Effort", summary.Effort,
			)

			// Launch execution as a child workflow.
			// "How do you build a coding elephant? One piece of chum at a time."
			childOpts := workflow.ChildWorkflowOptions{
				WorkflowID: fmt.Sprintf("exec-%s-%d", taskReq.TaskID, workflow.Now(ctx).Unix()),
				TaskQueue:  ResolvedTaskQueue,
			}
			childCtx := workflow.WithChildOptions(ctx, childOpts)

			future := workflow.ExecuteChildWorkflow(childCtx, ChumAgentWorkflow, *taskReq)

			logger.Info("Planning: execution workflow launched",
				"ExecutionWorkflowID", childOpts.WorkflowID,
			)

			// Wait for execution to complete (or fail/escalate)
			var execErr error
			if err := future.Get(ctx, nil); err != nil {
				execErr = err
				logger.Warn("Planning: execution failed — sharks couldn't finish",
					"TaskID", taskReq.TaskID,
					"Error", err,
				)
			} else {
				logger.Info("Planning: execution COMPLETED",
					"TaskID", taskReq.TaskID,
				)
			}

			// Return the task request regardless — it's what was planned
			if execErr != nil {
				return taskReq, fmt.Errorf("planned and greenlit but execution failed: %w", execErr)
			}
			return taskReq, nil
		}

		// REALIGN — loop back, re-groom with fresh perspective
		logger.Info("Planning: realigning", "Cycle", cycle+1, "Remaining", maxPlanningCycles-cycle-1)
	}

	return nil, fmt.Errorf("planning exhausted %d cycles without greenlight", maxPlanningCycles)
}
