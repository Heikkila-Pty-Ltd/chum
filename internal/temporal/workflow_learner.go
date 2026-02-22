package temporal

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// ContinuousLearnerWorkflow runs after every morsel completion to extract and store lessons.
// Spawned as a fire-and-forget child workflow (ParentClosePolicy: ABANDON).
//
// Pipeline: ExtractLessons -> StoreLessons -> GenerateSemgrepRules -> SynthesizeCLAUDE.md -> CalcifyPattern
//
// All steps are non-fatal. Learner failure never blocks the main execution loop.
func ContinuousLearnerWorkflow(ctx workflow.Context, req LearnerRequest) error {
	logger := workflow.GetLogger(ctx)
	logger.Info(OctopusPrefix+" ContinuousLearner starting", "TaskID", req.TaskID)

	if req.Tier == "" {
		req.Tier = "fast"
	}

	var a *Activities

	// Step 1: Extract lessons from the completed morsel
	extractOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 3 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts:    2,
			InitialInterval:    5 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
		},
	}
	extractCtx := workflow.WithActivityOptions(ctx, extractOpts)

	var lessons []Lesson
	if err := workflow.ExecuteActivity(extractCtx, a.ExtractLessonsActivity, req).Get(ctx, &lessons); err != nil {
		logger.Warn(OctopusPrefix+" Lesson extraction failed (non-fatal)", "error", err)
		return nil
	}

	if len(lessons) == 0 {
		logger.Info(OctopusPrefix+" No lessons extracted", "TaskID", req.TaskID)
		return nil
	}

	// Step 2: Store lessons in FTS5
	storeOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	}
	storeCtx := workflow.WithActivityOptions(ctx, storeOpts)
	if err := workflow.ExecuteActivity(storeCtx, a.StoreLessonActivity, lessons).Get(ctx, nil); err != nil {
		logger.Warn(OctopusPrefix+" Lesson storage failed (non-fatal)", "error", err)
		// Continue to rule generation even if storage fails
	}

	// Step 3: Generate semgrep rules for enforceable lessons
	ruleOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 3 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	ruleCtx := workflow.WithActivityOptions(ctx, ruleOpts)
	var rules []SemgrepRule
	if err := workflow.ExecuteActivity(ruleCtx, a.GenerateSemgrepRuleActivity, req, lessons).Get(ctx, &rules); err != nil {
		logger.Warn(OctopusPrefix+" Semgrep rule generation failed (non-fatal)", "error", err)
	}

	// Step 4: Synthesize CLAUDE.md from accumulated lessons
	// This is the long-term memory — not just what failed last time, but everything
	// the project has learned. Both Claude CLI and Codex CLI auto-read CLAUDE.md.
	synthesizeOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	}
	synthesizeCtx := workflow.WithActivityOptions(ctx, synthesizeOpts)
	if err := workflow.ExecuteActivity(synthesizeCtx, a.SynthesizeCLAUDEmdActivity, req).Get(ctx, nil); err != nil {
		logger.Warn(OctopusPrefix+" CLAUDE.md synthesis failed (non-fatal)", "error", err)
	}

	// Step 5: Calcify Repeated Patterns into Deterministic Scripts (Margin Protection)
	// If the LLM has successfully solved this morsel's type enough consecutive times,
	// generate a hardcoded script to bypass the LLM entirely on the next run.
	// This is the stochastic→deterministic migration: we fire the LLM.
	calcifyOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 3 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	calcifyCtx := workflow.WithActivityOptions(ctx, calcifyOpts)
	var calcified bool
	if err := workflow.ExecuteActivity(calcifyCtx, a.CalcifyPatternActivity, req).Get(ctx, &calcified); err != nil {
		logger.Warn(OctopusPrefix+" Pattern calcification failed (non-fatal)", "error", err)
	} else if calcified {
		logger.Info(OctopusPrefix+" Pattern calcified into shadow script", "TaskID", req.TaskID)
	}

	logger.Info(OctopusPrefix+" ContinuousLearner complete",
		"TaskID", req.TaskID,
		"Lessons", len(lessons),
		"Rules", len(rules),
	)

	// Fire-and-forget notification.
	notifyOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	nCtx := workflow.WithActivityOptions(ctx, notifyOpts)
	_ = workflow.ExecuteActivity(nCtx, a.NotifyActivity, NotifyRequest{
		Event: "learner", TaskID: req.TaskID,
		Extra: map[string]string{"lessons": fmt.Sprintf("%d", len(lessons))},
	}).Get(ctx, nil)

	return nil
}
