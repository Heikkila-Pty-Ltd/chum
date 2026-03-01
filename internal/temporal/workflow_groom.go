package temporal

import (
	"fmt"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/antigravity-dev/chum/internal/graph"
)

// TacticalGroomWorkflow runs after every morsel completion to tidy the backlog.
// Spawned as a fire-and-forget child workflow (ParentClosePolicy: ABANDON).
// Uses fast/cheap LLM tier.
func TacticalGroomWorkflow(ctx workflow.Context, req TacticalGroomRequest) error {
	startTime := workflow.Now(ctx)
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

	recordOrganismLog(ctx, "groomer", req.TaskID, req.Project, "completed",
		fmt.Sprintf("tactical: %d applied, %d failed", result.MutationsApplied, result.MutationsFailed),
		startTime, 1, "")

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
	startTime := workflow.Now(ctx)
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

	// Step 4.5: Detect and decompose whales
	whaleCtx := workflow.WithActivityOptions(ctx, shortAO)
	var whales []graph.Task
	if err := workflow.ExecuteActivity(whaleCtx, a.DetectWhalesActivity, req.Project).Get(ctx, &whales); err != nil {
		logger.Warn(RemoraPrefix+" Whale detection failed (non-fatal)", "error", err)
	}

	if len(whales) > 0 {
		logger.Info(RemoraPrefix+" Decomposing whales", "Count", len(whales))

		// Launch all crab decompositions concurrently — each involves LLM calls
		// and can take minutes, so parallel execution saves significant wall time.
		type pendingWhale struct {
			whale  graph.Task
			future workflow.ChildWorkflowFuture
		}
		pending := make([]pendingWhale, 0, len(whales))
		for i := range whales {
			whale := &whales[i]
			planMD := buildWhalePlanMarkdown(whale)

			crabReq := CrabDecompositionRequest{
				PlanID:                  whale.ID,
				Project:                 req.Project,
				WorkDir:                 req.WorkDir,
				PlanMarkdown:            planMD,
				Tier:                    "premium",
				RequireHumanReview:      false,
				DisableTurtleEscalation: true,
			}

			childOpts := workflow.ChildWorkflowOptions{
				WorkflowID:            fmt.Sprintf("crab-from-groom-%s-%d", whale.ID, workflow.Now(ctx).Unix()),
				WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
				ParentClosePolicy:     enumspb.PARENT_CLOSE_POLICY_ABANDON,
			}
			childCtx := workflow.WithChildOptions(ctx, childOpts)
			future := workflow.ExecuteChildWorkflow(childCtx, CrabDecompositionWorkflow, crabReq)
			pending = append(pending, pendingWhale{whale: *whale, future: future})
		}

		// Collect results from all concurrent decompositions.
		notifyOpts := workflow.ActivityOptions{
			StartToCloseTimeout: 5 * time.Second,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
		}
		for _, p := range pending {
			var crabResult CrabDecompositionResult
			summary := WhaleDecompositionSummary{
				WhaleID:    p.whale.ID,
				WhaleTitle: p.whale.Title,
			}

			if err := p.future.Get(ctx, &crabResult); err != nil {
				logger.Warn(RemoraPrefix+" Whale decomposition failed", "WhaleID", p.whale.ID, "error", err)
				summary.Status = "failed"
			} else {
				summary.MorselsEmitted = crabResult.MorselsEmitted
				summary.Status = crabResult.Status

				// Close the parent whale after successful decomposition.
				if crabResult.Status == "completed" && len(crabResult.MorselsEmitted) > 0 {
					closeCtx := workflow.WithActivityOptions(ctx, shortAO)
					if closeErr := workflow.ExecuteActivity(closeCtx, a.CloseTaskActivity, p.whale.ID, "completed").Get(ctx, nil); closeErr != nil {
						logger.Warn(RemoraPrefix+" Failed to close whale after decomposition", "WhaleID", p.whale.ID, "error", closeErr)
					}
				}
			}

			analysis.WhalesDecomposed = append(analysis.WhalesDecomposed, summary)

			// Fire-and-forget notification.
			nCtx := workflow.WithActivityOptions(ctx, notifyOpts)
			_ = workflow.ExecuteActivity(nCtx, a.NotifyActivity, NotifyRequest{
				Event:  "whale_sliced",
				TaskID: p.whale.ID,
				Extra: map[string]string{
					"title":   p.whale.Title,
					"morsels": fmt.Sprintf("%d", len(summary.MorselsEmitted)),
				},
			}).Get(ctx, nil)
		}
	}

	// Step 5: Generate morning briefing
	briefingCtx := workflow.WithActivityOptions(ctx, shortAO)
	var briefing MorningBriefing
	if err := workflow.ExecuteActivity(briefingCtx, a.GenerateMorningBriefingActivity, req, &analysis).Get(ctx, &briefing); err != nil {
		logger.Warn(RemoraPrefix+" Morning briefing failed (non-fatal)", "error", err)
	}

	// Step 6: UBS baseline scan — scan the trunk for bugs and create morsels
	ubsCtx := workflow.WithActivityOptions(ctx, shortAO)
	var ubsMorselsCreated int
	if err := workflow.ExecuteActivity(ubsCtx, a.UBSBaselineScanActivity, req.Project, req.WorkDir).Get(ctx, &ubsMorselsCreated); err != nil {
		logger.Warn(RemoraPrefix+" UBS baseline scan failed (non-fatal)", "error", err)
	} else if ubsMorselsCreated > 0 {
		logger.Info(RemoraPrefix+" UBS baseline created morsels", "Count", ubsMorselsCreated)
	}

	logger.Info(RemoraPrefix+" StrategicGroom complete",
		"Project", req.Project,
		"Priorities", len(analysis.Priorities),
		"Risks", len(analysis.Risks),
		"UBSMorsels", ubsMorselsCreated,
		"WhalesSliced", len(analysis.WhalesDecomposed),
	)

	recordOrganismLog(ctx, "groomer", "", req.Project, "completed",
		fmt.Sprintf("strategic: %d priorities, %d risks, %d UBS morsels, %d whales sliced",
			len(analysis.Priorities), len(analysis.Risks), ubsMorselsCreated, len(analysis.WhalesDecomposed)),
		startTime, 7, "")

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

// buildWhalePlanMarkdown constructs a plan markdown from a whale task's fields
// for use as input to CrabDecompositionWorkflow.
func buildWhalePlanMarkdown(t *graph.Task) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s\n\n", t.Title))
	if t.Description != "" {
		sb.WriteString(t.Description)
		sb.WriteString("\n\n")
	}
	if t.Acceptance != "" {
		sb.WriteString("## Acceptance Criteria\n\n")
		sb.WriteString(t.Acceptance)
		sb.WriteString("\n\n")
	}
	if t.Design != "" {
		sb.WriteString("## Design\n\n")
		sb.WriteString(t.Design)
		sb.WriteString("\n\n")
	}
	return sb.String()
}
