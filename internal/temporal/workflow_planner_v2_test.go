package temporal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

func TestDispatcherRoutesThroughPlannerV2WhenEnabled(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var da *DispatchActivities
	var captured PlannerV2Request
	var capturedSet bool

	env.OnActivity(da.ScanCandidatesActivity, mock.Anything).Return(&ScanCandidatesResult{
		EnablePlannerV2: true,
		Candidates: []DispatchCandidate{{
			TaskID:            "morsel-planner",
			Title:             "Planner task",
			TaskTitle:         "Planner task",
			Project:           "project-1",
			WorkDir:           "/tmp/test",
			Prompt:            "Implement planner route",
			Species:           "species-planner",
			Provider:          "codex",
			SlowStepThreshold: 30 * time.Second,
			Priority:          2,
			EstimateMinutes:   30,
		}},
		Running:  0,
		MaxTotal: 3,
	}, nil)

	env.OnWorkflow(PlannerV2Workflow, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		req, ok := args.Get(1).(PlannerV2Request)
		if ok {
			captured = req
			capturedSet = true
		}
	}).Return(nil)

	env.ExecuteWorkflow(DispatcherWorkflow, struct{}{})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.True(t, capturedSet, "dispatcher should dispatch PlannerV2Workflow")
	require.Equal(t, "morsel-planner", captured.Candidate.TaskID)
	require.Equal(t, plannerV2RootNodeKey, captured.ParentNodeKey)
}

func TestPlannerV2WorkflowChoosesPlanningForComplexTask(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var da *DispatchActivities
	var captured PlanningRequest
	var capturedOutcome PlannerOutcomeRecord
	var outcomeSet bool

	env.OnWorkflow(PlanningCeremonyWorkflow, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		req, ok := args.Get(1).(PlanningRequest)
		if ok {
			captured = req
		}
	}).Return(&TaskRequest{TaskID: "morsel-complex"}, nil)

	env.OnActivity(da.RecordPlannerOutcomeActivity, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		req, ok := args.Get(1).(PlannerOutcomeRecord)
		if ok {
			capturedOutcome = req
			outcomeSet = true
		}
	}).Return(nil)

	env.ExecuteWorkflow(PlannerV2Workflow, PlannerV2Request{
		Candidate: DispatchCandidate{
			TaskID:          "morsel-complex",
			Project:         "project-1",
			WorkDir:         "/tmp/test",
			Prompt:          "Complex architectural migration",
			Species:         "species-complex",
			EstimateMinutes: 120,
			Generation:      2,
			Complexity:      95,
		},
		Task: TaskRequest{
			TaskID:    "morsel-complex",
			Project:   "project-1",
			TaskTitle: "Complex migration",
			Prompt:    "Complex architectural migration",
			Agent:     "codex",
			WorkDir:   "/tmp/test",
			Provider:  "codex",
		},
		EscalationTiers: []EscalationTier{{ProviderKey: "codex", CLI: "codex", Tier: "fast", Enabled: true}},
		ParentNodeKey:   plannerV2RootNodeKey,
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, "morsel-complex", captured.SeedTaskID)
	require.Equal(t, "project-1", captured.Project)
	require.True(t, outcomeSet, "planner outcome should be recorded")
	require.Equal(t, plannerV2LanePlanning, capturedOutcome.SelectedAction)
	require.Equal(t, "started", capturedOutcome.Outcome)
}

func TestSelectPlannerV2LaneUsesEdgeStats(t *testing.T) {
	now := time.Now().UTC()
	lane, _, _ := selectPlannerV2Lane(PlannerV2Request{
		Candidate: DispatchCandidate{
			Generation: 2,
			Complexity: 20,
			PlannerEdgeStats: []PlannerEdgeStat{
				{ActionKey: plannerV2LaneDirect, Visits: 20, TotalReward: 18, UpdatedAt: now},
				{ActionKey: "cambrian", Visits: 20, TotalReward: 2, UpdatedAt: now},
				{ActionKey: plannerV2LanePlanning, Visits: 20, TotalReward: 1, UpdatedAt: now},
			},
		},
	}, now)

	require.Equal(t, plannerV2LaneDirect, lane)
}

func TestSelectPlannerV2LaneForCrabEmittedCandidateIsDirect(t *testing.T) {
	now := time.Now().UTC()
	lane, _, _ := selectPlannerV2Lane(PlannerV2Request{
		Candidate: DispatchCandidate{
			Generation: 0,
			Complexity: 95,
			Labels:     []string{"source:crab", "plan:plan-1"},
			PlannerEdgeStats: []PlannerEdgeStat{
				{ActionKey: plannerV2LanePlanning, Visits: 50, TotalReward: 49, UpdatedAt: now},
			},
		},
	}, now)

	require.Equal(t, plannerV2LaneDirect, lane)
}
