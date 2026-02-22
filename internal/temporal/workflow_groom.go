package temporal

import (
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// TacticalGroomWorkflow runs after every morsel completion to tidy the backlog.
// Spawned as a fire-and-forget child workflow (ParentClosePolicy: ABANDON).
// Uses fast/cheap LLM tier.
func TacticalGroomWorkflow(ctx workflow.Context, req TacticalGroomRequest) error {
	logger := workflow.GetLogger(ctx)
	logger.Info(RemoraPrefix+" TacticalGroom starting", "TaskID", req.TaskID, "Project", req.Project)

	if req.Tier == "" {
		req.Tier = "fast"
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 3 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts:    2,
			InitialInterval:    5 * time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    30 * time.Second,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var a *Activities
	var result GroomResult
	if err := workflow.ExecuteActivity(ctx, a.MutateTasksActivity, req).Get(ctx, &result); err != nil {
		logger.Warn(RemoraPrefix+" TacticalGroom failed (non-fatal)", "error", err)
		return nil
	}

	logger.Info(RemoraPrefix+" TacticalGroom complete", "Applied", result.MutationsApplied, "Failed", result.MutationsFailed)

	// Fire-and-forget notification.
	notifyOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	nCtx := workflow.WithActivityOptions(ctx, notifyOpts)
	_ = workflow.ExecuteActivity(nCtx, a.NotifyActivity, NotifyRequest{
		Event: "groom", TaskID: req.TaskID,
		Extra: map[string]string{"applied": fmt.Sprintf("%d", result.MutationsApplied)},
	}).Get(ctx, nil)

	return nil
}

// StrategicGroomWorkflow runs daily at 5:00 AM via CronSchedule.
// Uses premium LLM tier for deep analysis.
//
// Pipeline: GenerateRepoMap -> GetMorselState -> StrategicAnalysis -> ApplyMutations -> MorningBriefing
func StrategicGroomWorkflow(ctx workflow.Context, req StrategicGroomRequest) error {
	logger := workflow.GetLogger(ctx)
	logger.Info(RemoraPrefix+" StrategicGroom starting", "Project", req.Project)

	if req.Tier == "" {
		req.Tier = "premium"
	}

	shortAO := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	}
	longAO := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		HeartbeatTimeout:    30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	}

	var a *Activities

	// Step 1: Generate repo map (quick, subprocess-only)
	repoMapCtx := workflow.WithActivityOptions(ctx, shortAO)
	var repoMap RepoMap
	if err := workflow.ExecuteActivity(repoMapCtx, a.GenerateRepoMapActivity, req).Get(ctx, &repoMap); err != nil {
		return fmt.Errorf("repo map generation failed: %w", err)
	}

	// Step 2: Get compressed morsel state summary
	morselStateCtx := workflow.WithActivityOptions(ctx, shortAO)
	var morselStateSummary string
	if err := workflow.ExecuteActivity(morselStateCtx, a.GetMorselStateSummaryActivity, req).Get(ctx, &morselStateSummary); err != nil {
		logger.Warn(RemoraPrefix+" Failed to get morsel state, continuing with empty", "error", err)
		morselStateSummary = "(morsel state unavailable)"
	}

	// Step 3: Strategic analysis (premium LLM, may be slow)
	analysisCtx := workflow.WithActivityOptions(ctx, longAO)
	var analysis StrategicAnalysis
	if err := workflow.ExecuteActivity(analysisCtx, a.StrategicAnalysisActivity, req, &repoMap, morselStateSummary).Get(ctx, &analysis); err != nil {
		return fmt.Errorf("strategic analysis failed: %w", err)
	}

	// Step 4: Apply pre-normalized strategic mutations directly (no re-invocation of LLM).
	mutations := normalizeStrategicMutations(analysis.Mutations)
	if len(mutations) > 0 {
		if len(mutations) > 5 {
			mutations = mutations[:5]
		}

		mutateCtx := workflow.WithActivityOptions(ctx, shortAO)
		var mutResult GroomResult
		if err := workflow.ExecuteActivity(mutateCtx, a.ApplyStrategicMutationsActivity, req.Project, mutations).Get(ctx, &mutResult); err != nil {
			logger.Warn(RemoraPrefix+" Strategic mutations failed (non-fatal)", "error", err)
		} else {
			logger.Info(RemoraPrefix+" Strategic mutations applied", "Applied", mutResult.MutationsApplied, "Failed", mutResult.MutationsFailed)
		}
	}

	// Step 5: Generate morning briefing
	briefingCtx := workflow.WithActivityOptions(ctx, shortAO)
	var briefing MorningBriefing
	if err := workflow.ExecuteActivity(briefingCtx, a.GenerateMorningBriefingActivity, req, &analysis).Get(ctx, &briefing); err != nil {
		logger.Warn(RemoraPrefix+" Morning briefing failed (non-fatal)", "error", err)
	}

	logger.Info(RemoraPrefix+" StrategicGroom complete",
		"Project", req.Project,
		"Priorities", len(analysis.Priorities),
		"Risks", len(analysis.Risks),
	)
	return nil
}

func normalizeStrategicMutations(mutations []MorselMutation) []MorselMutation {
	if len(mutations) == 0 {
		return nil
	}

	out := make([]MorselMutation, 0, len(mutations))
	for idx := range mutations {
		m := mutations[idx]
		if strings.TrimSpace(m.StrategicSource) == "" {
			m.StrategicSource = StrategicMutationSource
		}

		m.Title = normalizeMutationTitle(m.Title)

		if m.Action != "create" {
			out = append(out, m)
			continue
		}

		// Any strategic create that lacks full actionable fields is deferred.
		// This catches both explicit deferred flags and model outputs that drift
		// from the prompt contract (e.g. vague decomposition suggestions without
		// acceptance/design/estimate).
		if m.Deferred || !isStrategicCreateActionable(m) {
			m.Deferred = true
		}

		if m.Deferred {
			if strings.TrimSpace(m.Title) == "" {
				m.Title = "Strategic deferred suggestion"
			}
			if strings.TrimSpace(m.Description) == "" {
				m.Description = "Deferred strategic recommendation pending breakdown."
			}
			if strings.TrimSpace(m.Acceptance) == "" {
				m.Acceptance = "This is deferred strategy guidance. Review and expand before execution."
			}
			if strings.TrimSpace(m.Design) == "" {
				m.Design = "Clarify design and acceptance criteria before creating executable subtasks."
			}
			if m.EstimateMinutes <= 0 {
				m.EstimateMinutes = 30
			}
			m.Priority = intPtrCopy(4)
			out = append(out, m)
			continue
		}

		if isStrategicCreateActionable(m) {
			out = append(out, m)
		}
	}
	return out
}

func isStrategicCreateActionable(m MorselMutation) bool {
	return strings.TrimSpace(m.Title) != "" &&
		strings.TrimSpace(m.Description) != "" &&
		strings.TrimSpace(m.Acceptance) != "" &&
		strings.TrimSpace(m.Design) != "" &&
		m.EstimateMinutes > 0
}

func intPtrCopy(v int) *int {
	value := v
	return &value
}
