package temporal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/antigravity-dev/chum/internal/planner"
	"github.com/antigravity-dev/chum/internal/store"
)

const (
	plannerV2RootNodeKey       = "dispatch_lane_v2"
	plannerV2LaneDirect        = "direct"
	plannerV2LanePlanning      = "planning"
	plannerV2ExplorationWeight = 1.2
	plannerV2RewardHalfLife    = 72 * time.Hour
	plannerV2PruneMinVisits    = 2
	plannerV2PruneStaleAfter   = 14 * 24 * time.Hour
)

// PlannerV2Workflow selects a lane via UCT and dispatches the selected child workflow.
func PlannerV2Workflow(ctx workflow.Context, req PlannerV2Request) error {
	logger := workflow.GetLogger(ctx)
	now := workflow.Now(ctx)
	startedAt := now

	parentNodeKey := strings.TrimSpace(req.ParentNodeKey)
	if parentNodeKey == "" {
		parentNodeKey = plannerV2RootNodeKey
	}

	selectedLane, selectedScore, priors := selectPlannerV2Lane(req, now)
	logger.Info(SharkPrefix+" PlannerV2 lane selected",
		"task", req.Candidate.TaskID,
		"project", req.Candidate.Project,
		"species", req.Candidate.Species,
		"lane", selectedLane,
		"score", selectedScore,
	)

	timeout := workflowTimeout(req.Candidate.EstimateMinutes)
	wfID := fmt.Sprintf("%s-%s-%d", req.Candidate.TaskID, selectedLane, now.Unix())
	childOpts := workflow.ChildWorkflowOptions{
		WorkflowID:               wfID,
		TaskQueue:                DefaultTaskQueue,
		WorkflowExecutionTimeout: timeout,
		WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
		ParentClosePolicy:        enumspb.PARENT_CLOSE_POLICY_ABANDON,
	}
	childCtx := workflow.WithChildOptions(ctx, childOpts)

	var childFuture workflow.ChildWorkflowFuture
	switch selectedLane {
	case plannerV2LanePlanning:
		slowStep := req.Candidate.SlowStepThreshold
		if slowStep <= 0 {
			slowStep = defaultSlowStepThreshold
		}
		planningReq := seededPlanningRequestFromCandidate(
			req.Candidate,
			req.Task.Agent,
			slowStep,
			defaultPlanningSignalTimeout,
			defaultPlanningSessionTimeout,
		)
		childFuture = workflow.ExecuteChildWorkflow(childCtx, PlanningCeremonyWorkflow, planningReq)
	default:
		selectedLane = plannerV2LaneDirect
		childFuture = workflow.ExecuteChildWorkflow(childCtx, ChumAgentWorkflow, req.Task)
	}

	outcome := "started"
	reward := 1.0
	var childExec workflow.Execution
	if err := childFuture.GetChildWorkflowExecution().Get(ctx, &childExec); err != nil {
		outcome = "start_failed"
		reward = 0
		recordPlannerOutcome(ctx, req, parentNodeKey, selectedLane, outcome, reward, workflow.Now(ctx).Sub(startedAt).Seconds(), "", priors)
		return fmt.Errorf("planner v2 start child lane=%s: %w", selectedLane, err)
	}

	recordPlannerOutcome(ctx, req, parentNodeKey, selectedLane, outcome, reward, workflow.Now(ctx).Sub(startedAt).Seconds(), childExec.ID, priors)
	return nil
}

func recordPlannerOutcome(
	ctx workflow.Context,
	req PlannerV2Request,
	parentNodeKey, selectedLane, outcome string,
	reward, durationS float64,
	childWorkflowID string,
	priors map[string]float64,
) {
	logger := workflow.GetLogger(ctx)
	metadataJSON := "{}"
	if b, err := json.Marshal(map[string]any{
		"reward": reward,
		"priors": priors,
	}); err == nil {
		metadataJSON = string(b)
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	}
	actCtx := workflow.WithActivityOptions(ctx, ao)
	var da *DispatchActivities
	if err := workflow.ExecuteActivity(actCtx, da.RecordPlannerOutcomeActivity, PlannerOutcomeRecord{
		TaskID:         req.Candidate.TaskID,
		Project:        req.Candidate.Project,
		Species:        req.Candidate.Species,
		ParentNodeKey:  parentNodeKey,
		SelectedAction: selectedLane,
		Outcome:        outcome,
		Reward:         reward,
		DurationS:      durationS,
		ChildWorkflow:  childWorkflowID,
		MetadataJSON:   metadataJSON,
	}).Get(ctx, nil); err != nil {
		logger.Warn(SharkPrefix+" PlannerV2: failed to record planner outcome", "error", err)
	}
}

func selectPlannerV2Lane(req PlannerV2Request, now time.Time) (string, float64, map[string]float64) {
	priors := plannerV2LanePriors(req.Candidate)
	if isCrabEmittedCandidate(req.Candidate) {
		// Strict phase ordering: once crab emits a morsel, dispatch directly to sharks.
		return plannerV2LaneDirect, 0, priors
	}

	type laneArm struct {
		action string
		arm    planner.Arm
	}
	arms := make([]laneArm, 0, len(priors))
	indexByAction := make(map[string]int, len(priors))
	for action, prior := range priors {
		indexByAction[action] = len(arms)
		arms = append(arms, laneArm{
			action: action,
			arm: planner.Arm{
				Key:   action,
				Prior: prior,
			},
		})
	}

	for _, stat := range req.Candidate.PlannerEdgeStats {
		action := normalizePlannerV2LaneKey(stat.ActionKey)
		idx, ok := indexByAction[action]
		if !ok {
			continue
		}
		age := time.Duration(0)
		if !stat.UpdatedAt.IsZero() && now.After(stat.UpdatedAt) {
			age = now.Sub(stat.UpdatedAt)
		}
		arms[idx].arm.Visits = stat.Visits
		arms[idx].arm.TotalReward = planner.DecayByAge(stat.TotalReward, age, plannerV2RewardHalfLife)
		arms[idx].arm.LastSeen = stat.UpdatedAt
	}

	raw := make([]planner.Arm, 0, len(arms))
	for _, la := range arms {
		raw = append(raw, la.arm)
	}
	filtered := planner.PruneStaleArms(raw, now, plannerV2PruneMinVisits, plannerV2PruneStaleAfter)
	if len(filtered) == 0 {
		return plannerV2LaneDirect, 0, priors
	}

	selected, ok := planner.SelectUCT(filtered, plannerV2ExplorationWeight)
	if !ok || selected.Key == "" {
		return plannerV2LaneDirect, 0, priors
	}
	return selected.Key, selected.Score, priors
}

func plannerV2LanePriors(c DispatchCandidate) map[string]float64 {
	priors := map[string]float64{
		plannerV2LaneDirect:   0.8,
		plannerV2LanePlanning: 0.2,
	}
	if isCrabEmittedCandidate(c) {
		priors[plannerV2LaneDirect] += 3.0
		priors[plannerV2LanePlanning] -= 1.0
	}

	if c.Generation > 0 && c.Complexity < 50 && len(c.PreviousErrors) == 0 {
		priors[plannerV2LaneDirect] += 0.7
	}
	if c.Generation == 0 {
		priors[plannerV2LanePlanning] += 1.2
		priors[plannerV2LaneDirect] -= 0.3
	}
	if len(c.PreviousErrors) > 0 {
		priors[plannerV2LanePlanning] += 1.0
		priors[plannerV2LaneDirect] -= 0.2
	}
	if c.Complexity > 70 {
		priors[plannerV2LanePlanning] += 0.8
		priors[plannerV2LaneDirect] -= 0.3
	}
	return priors
}

func normalizePlannerV2LaneKey(action string) string {
	switch strings.TrimSpace(action) {
	case "turtle":
		// Backward compatibility with previously persisted lane stats.
		return plannerV2LanePlanning
	case "cambrian":
		// Cambrian lane has been retired; fold historic weight into planning.
		return plannerV2LanePlanning
	default:
		return strings.TrimSpace(action)
	}
}

// RecordPlannerOutcomeActivity persists one planner decision rollout.
func (da *DispatchActivities) RecordPlannerOutcomeActivity(ctx context.Context, outcome PlannerOutcomeRecord) error {
	logger := activity.GetLogger(ctx)
	if da.Store == nil {
		return nil
	}

	parentNodeKey := strings.TrimSpace(outcome.ParentNodeKey)
	if parentNodeKey == "" {
		parentNodeKey = plannerV2RootNodeKey
	}
	action := strings.TrimSpace(outcome.SelectedAction)
	if action == "" {
		return fmt.Errorf("record planner outcome: selected action is required")
	}

	rollout := store.MCTSRollout{
		TaskID:        strings.TrimSpace(outcome.TaskID),
		Project:       strings.TrimSpace(outcome.Project),
		Species:       strings.TrimSpace(outcome.Species),
		ParentNodeKey: parentNodeKey,
		ActionKey:     action,
		Outcome:       strings.TrimSpace(outcome.Outcome),
		Reward:        outcome.Reward,
		DurationS:     outcome.DurationS,
		MetadataJSON:  strings.TrimSpace(outcome.MetadataJSON),
	}

	if _, err := da.Store.RecordMCTSRollout(rollout); err != nil {
		return fmt.Errorf("record planner rollout: %w", err)
	}

	logger.Info(SharkPrefix+" PlannerV2 outcome recorded",
		"task", outcome.TaskID,
		"project", outcome.Project,
		"species", outcome.Species,
		"action", action,
		"outcome", outcome.Outcome,
		"child_workflow", outcome.ChildWorkflow,
	)
	return nil
}
