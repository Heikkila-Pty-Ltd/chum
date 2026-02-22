package temporal

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// CalcificationWorkflow runs on a schedule to detect morsel types eligible
// for calcification and generate deterministic replacement scripts.
//
// Pipeline:
//  1. DetectCalcificationCandidates — find morsel types with enough consecutive successes
//  2. For each candidate: CompileCalcifiedScript — generate shadow script via premium LLM
//  3. Notify — report new scripts to the coordination channel
//
// This workflow is non-blocking — calcification failures never affect the main execution loop.
func CalcificationWorkflow(ctx workflow.Context, project string) error {
	logger := workflow.GetLogger(ctx)
	logger.Info(OctopusPrefix + " CalcificationWorkflow starting")

	var a *Activities

	// Step 1: Detect candidates
	detectOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 1 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts:  2,
			InitialInterval: 5 * time.Second,
		},
	}
	detectCtx := workflow.WithActivityOptions(ctx, detectOpts)

	var candidates []CalcificationCandidate
	if err := workflow.ExecuteActivity(detectCtx, a.DetectCalcificationCandidatesActivity, project).Get(ctx, &candidates); err != nil {
		logger.Warn(OctopusPrefix+" Candidate detection failed", "error", err)
		return nil
	}

	if len(candidates) == 0 {
		logger.Info(OctopusPrefix + " No calcification candidates found")
		return nil
	}

	logger.Info(OctopusPrefix+" Found candidates", "count", len(candidates))

	// Step 2: Compile each candidate
	compileOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	compileCtx := workflow.WithActivityOptions(ctx, compileOpts)

	compiled := 0
	for _, candidate := range candidates {
		var result CalcifiedScriptResult
		if err := workflow.ExecuteActivity(compileCtx, a.CompileCalcifiedScriptActivity, candidate).Get(ctx, &result); err != nil {
			logger.Warn(OctopusPrefix+" Script compilation failed",
				"type", candidate.MorselType, "error", err)
			continue
		}
		compiled++
		logger.Info(OctopusPrefix+" Script compiled",
			"type", candidate.MorselType, "path", result.ScriptPath)
	}

	// Step 3: Notify
	notifyOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	nCtx := workflow.WithActivityOptions(ctx, notifyOpts)
	_ = workflow.ExecuteActivity(nCtx, a.NotifyActivity, NotifyRequest{
		Event: "calcification",
		Extra: map[string]string{
			"candidates": fmt.Sprintf("%d", len(candidates)),
			"compiled":   fmt.Sprintf("%d", compiled),
		},
	}).Get(ctx, nil)

	logger.Info(OctopusPrefix+" CalcificationWorkflow complete",
		"candidates", len(candidates), "compiled", compiled)
	return nil
}
