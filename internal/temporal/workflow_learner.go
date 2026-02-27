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
// Pipeline: ExtractLessons -> StoreLessons -> GenerateSemgrepRules -> SynthesizeCLAUDE.md -> CalcifyPattern -> PR
//
// All steps are non-fatal. Learner failure never blocks the main execution loop.
func ContinuousLearnerWorkflow(ctx workflow.Context, req LearnerRequest) error {
	startTime := workflow.Now(ctx)
	logger := workflow.GetLogger(ctx)
	logger.Info(OctopusPrefix+" ContinuousLearner starting", "TaskID", req.TaskID)

	if req.Tier == "" {
		req.Tier = "fast"
	}

	var a *Activities

	// Create an isolated worktree for the learner so we don't commit directly to master
	worktreeOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	}
	wtCtx := workflow.WithActivityOptions(ctx, worktreeOpts)
	baseWorkDir := req.WorkDir
	var wtDir string
	if err := workflow.ExecuteActivity(wtCtx, a.SetupWorktreeActivity, baseWorkDir, "learner-"+req.TaskID, "").Get(ctx, &wtDir); err != nil || wtDir == "" {
		logger.Warn(OctopusPrefix+" Learner worktree setup failed", "error", err)
		return nil // abort if we can't get a worktree
	}
	defer func() {
		if wtDir != "" {
			_ = workflow.ExecuteActivity(wtCtx, a.CleanupWorktreeActivity, baseWorkDir, wtDir).Get(ctx, nil)
		}
	}()

	// Switch all subsequent activities to operate in the worktree
	req.WorkDir = wtDir

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

	// Always generate CLAUDE.md and PR even if 0 new lessons (might be recovering from earlier parse errors)
	// But only store/generate semantics if there are lessons.
	var rules []SemgrepRule
	if len(lessons) > 0 {
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
		if err := workflow.ExecuteActivity(ruleCtx, a.GenerateSemgrepRuleActivity, req, lessons).Get(ctx, &rules); err != nil {
			logger.Warn(OctopusPrefix+" Semgrep rule generation failed (non-fatal)", "error", err)
		}
	} else {
		logger.Info(OctopusPrefix+" No new lessons extracted", "TaskID", req.TaskID)
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

	// Step 6: Commit and PR all learner outputs
	// Without this, the learning is ephemeral and gets wiped out on the next checkout.
	commitOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 1 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	}
	commitCtx := workflow.WithActivityOptions(ctx, commitOpts)

	var committed bool
	if err := workflow.ExecuteActivity(commitCtx, a.CommitLearnerOutputsActivity, req.WorkDir, req.TaskID).Get(ctx, &committed); err != nil {
		logger.Warn(OctopusPrefix+" Failed to commit learner outputs", "error", err)
	}

	// Only push and PR if we actually committed new files
	if committed {
		pushCtx := workflow.WithActivityOptions(ctx, worktreeOpts)
		if err := workflow.ExecuteActivity(pushCtx, a.PushWorktreeActivity, req.WorkDir).Get(ctx, nil); err != nil {
			logger.Warn(OctopusPrefix+" Failed to push learner branch", "error", err)
		} else {
			mergeCtx := workflow.WithActivityOptions(ctx, worktreeOpts)
			featureBranch := fmt.Sprintf("chum/learner-%s", req.TaskID)
			prTitle := fmt.Sprintf("Octopus learning updates from task %s", req.TaskID)
			if err := workflow.ExecuteActivity(mergeCtx, a.MergeToMainActivity, baseWorkDir, featureBranch, prTitle).Get(ctx, nil); err != nil {
				logger.Warn(OctopusPrefix+" Failed to create learner PR", "error", err)
			}
		}
	}

	logger.Info(OctopusPrefix+" ContinuousLearner complete",
		"TaskID", req.TaskID,
		"Lessons", len(lessons),
		"Rules", len(rules),
	)

	recordOrganismLog(ctx, "learner", req.TaskID, req.Project, "completed",
		fmt.Sprintf("%d lessons, %d rules, calcified=%v", len(lessons), len(rules), calcified),
		startTime, 6, "")

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

