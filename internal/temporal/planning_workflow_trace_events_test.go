package temporal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

func TestPlanningWorkflowEmitsAdaptiveCeremonyTraceEvents(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	traceEvents := make([]PlanningTraceRecord, 0, 32)
	env.OnActivity(a.RecordPlanningTraceActivity, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		rec, ok := args.Get(1).(PlanningTraceRecord)
		if !ok {
			return
		}
		traceEvents = append(traceEvents, rec)
	}).Return(nil).Maybe()

	env.OnActivity(a.RecordPlanningSnapshotActivity, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity(a.IsPlanningActionBlacklistedActivity, mock.Anything, mock.Anything).Return(false, nil).Maybe()
	env.OnActivity(a.AddPlanningBlacklistEntryActivity, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity(a.GetLatestStablePlanningSnapshotActivity, mock.Anything, mock.Anything).Return((*PlanningSnapshotRecord)(nil), nil).Maybe()
	env.OnActivity(a.LoadPlanningCandidateScoresActivity, mock.Anything, mock.Anything).Return([]PlanningCandidateScoreRecord{}, nil).Maybe()
	env.OnActivity(a.AdjustPlanningCandidateScoreActivity, mock.Anything, mock.Anything).Return(nil).Maybe()

	env.OnActivity(a.GroomBacklogActivity, mock.Anything, mock.Anything).Return(&BacklogPresentation{
		Items: []BacklogItem{
			{ID: "morsel-1", Title: "Top slice", Impact: "high", Effort: "S", Recommended: true, Rationale: "most leverage"},
			{ID: "morsel-2", Title: "Alt slice", Impact: "medium", Effort: "M", Recommended: false, Rationale: "fallback"},
		},
		Rationale: "Prioritize highest leverage first.",
	}, nil)
	env.OnActivity(a.GenerateQuestionsActivity, mock.Anything, mock.Anything, mock.Anything).Return([]PlanningQuestion{
		{Question: "What exact behavior should change first?", Options: []string{"A", "B"}, Recommendation: "A"},
	}, nil)
	env.OnActivity(a.SummarizePlanActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&PlanSummary{
		What:      "Implement top slice behavior with deterministic checks",
		Why:       "Fastest path to validated planning improvement",
		Effort:    "S",
		DoDChecks: []string{"go test ./internal/temporal"},
	}, nil)

	env.OnWorkflow(CrabDecompositionWorkflow, mock.Anything, mock.Anything).Return(&CrabDecompositionResult{
		Status:         "completed",
		WhalesEmitted:  []string{"whale-1"},
		MorselsEmitted: []string{"morsel-1"},
	}, nil)
	env.OnActivity(a.CloseTaskActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("item-selected", "morsel-1")
		env.SignalWorkflow("answer", "A")
		env.SignalWorkflow("greenlight", "GO")
	}, 0)

	env.ExecuteWorkflow(PlanningCeremonyWorkflow, PlanningRequest{
		Project:       "test-project",
		Agent:         "claude",
		WorkDir:       "/tmp/test",
		CandidateTopK: 2,
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	hasEvent := func(eventType string) bool {
		for _, rec := range traceEvents {
			if rec.EventType == eventType {
				return true
			}
		}
		return false
	}

	require.True(t, hasEvent("goal_interpreted"), "expected goal_interpreted trace event")
	require.True(t, hasEvent("candidate_with_implications"), "expected candidate_with_implications trace event")
	require.True(t, hasEvent("behavior_contract"), "expected behavior_contract trace event")
	require.True(t, hasEvent("loop_decision"), "expected loop_decision trace event")
	require.True(t, hasEvent("ceremony_review"), "expected ceremony_review trace event")
	require.True(t, hasEvent("novel_pathway_candidate"), "expected novel_pathway_candidate trace event")
}

func TestPlanningWorkflowReviewsPoorOutcomeAndProposesAlternatives(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	traceEvents := make([]PlanningTraceRecord, 0, 64)
	scoreDeltas := make([]PlanningCandidateScoreDelta, 0, 16)
	env.OnActivity(a.RecordPlanningTraceActivity, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		rec, ok := args.Get(1).(PlanningTraceRecord)
		if !ok {
			return
		}
		traceEvents = append(traceEvents, rec)
	}).Return(nil).Maybe()

	env.OnActivity(a.RecordPlanningSnapshotActivity, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity(a.IsPlanningActionBlacklistedActivity, mock.Anything, mock.Anything).Return(false, nil).Maybe()
	env.OnActivity(a.AddPlanningBlacklistEntryActivity, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity(a.GetLatestStablePlanningSnapshotActivity, mock.Anything, mock.Anything).Return((*PlanningSnapshotRecord)(nil), nil).Maybe()
	env.OnActivity(a.LoadPlanningCandidateScoresActivity, mock.Anything, mock.Anything).Return([]PlanningCandidateScoreRecord{}, nil).Maybe()
	env.OnActivity(a.AdjustPlanningCandidateScoreActivity, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		delta, ok := args.Get(1).(PlanningCandidateScoreDelta)
		if !ok {
			return
		}
		scoreDeltas = append(scoreDeltas, delta)
	}).Return(nil).Maybe()

	env.OnActivity(a.GroomBacklogActivity, mock.Anything, mock.Anything).Return(&BacklogPresentation{
		Items: []BacklogItem{
			{ID: "morsel-1", Title: "Top slice", Impact: "high", Effort: "S", Recommended: true, Rationale: "most leverage"},
			{ID: "morsel-2", Title: "Alt slice", Impact: "medium", Effort: "M", Recommended: false, Rationale: "fallback"},
			{ID: "morsel-3", Title: "Third slice", Impact: "low", Effort: "L", Recommended: false, Rationale: "backup"},
		},
		Rationale: "Prioritize highest leverage first.",
	}, nil).Maybe()
	env.OnActivity(a.GenerateQuestionsActivity, mock.Anything, mock.Anything, mock.Anything).Return([]PlanningQuestion{
		{Question: "What exact behavior should change first?", Options: []string{"A", "B"}, Recommendation: "A"},
	}, nil).Maybe()
	env.OnActivity(a.SummarizePlanActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&PlanSummary{
		What:      "Implement top slice behavior with deterministic checks",
		Why:       "Fastest path to validated planning improvement",
		Effort:    "S",
		DoDChecks: []string{"go test ./internal/temporal"},
	}, nil).Maybe()

	env.OnWorkflow(CrabDecompositionWorkflow, mock.Anything, mock.Anything).Return(&CrabDecompositionResult{
		Status:         "completed",
		WhalesEmitted:  []string{"whale-1"},
		MorselsEmitted: []string{"morsel-1"},
	}, nil)
	env.OnActivity(a.CloseTaskActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	env.RegisterDelayedCallback(func() {
		// Cycle 1 ends with a non-GO decision (poor outcome).
		env.SignalWorkflow("item-selected", "morsel-1")
		env.SignalWorkflow("answer", "A")
		env.SignalWorkflow("greenlight", "NO")
		// Cycle 2 proceeds with promoted alternative and GO.
		env.SignalWorkflow("item-selected", "morsel-2")
		env.SignalWorkflow("answer", "A")
		env.SignalWorkflow("greenlight", "GO")
	}, 0)

	env.ExecuteWorkflow(PlanningCeremonyWorkflow, PlanningRequest{
		Project:       "test-project",
		Agent:         "claude",
		WorkDir:       "/tmp/test",
		CandidateTopK: 2,
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	hasEvent := func(eventType string) bool {
		for _, rec := range traceEvents {
			if rec.EventType == eventType {
				return true
			}
		}
		return false
	}

	require.True(t, hasEvent("trace_review"), "expected trace_review event after poor outcome")
	require.True(t, hasEvent("alternative_trace_candidate"), "expected alternative_trace_candidate proposals")
	require.True(t, hasEvent("ceremony_review"), "expected ceremony_review event at ceremony end")
	require.True(t, hasEvent("novel_pathway_candidate"), "expected novelty proposals at ceremony end")

	hasDelta := func(optionID string, predicate func(delta PlanningCandidateScoreDelta) bool) bool {
		for i := range scoreDeltas {
			if scoreDeltas[i].OptionID != optionID {
				continue
			}
			if predicate(scoreDeltas[i]) {
				return true
			}
		}
		return false
	}
	require.True(t, hasDelta("morsel-1", func(delta PlanningCandidateScoreDelta) bool {
		return delta.Delta < 0
	}), "expected persisted penalty for poor selected path")
	require.True(t, hasDelta("morsel-2", func(delta PlanningCandidateScoreDelta) bool {
		return delta.Delta > 0 && delta.Outcome == "success"
	}), "expected persisted positive reinforcement for agreed plan")
}

func TestPlanningWorkflowTreatsCrabEscalationAsValidRebound(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	env.OnActivity(a.RecordPlanningTraceActivity, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity(a.RecordPlanningSnapshotActivity, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity(a.IsPlanningActionBlacklistedActivity, mock.Anything, mock.Anything).Return(false, nil).Maybe()
	env.OnActivity(a.AddPlanningBlacklistEntryActivity, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity(a.GetLatestStablePlanningSnapshotActivity, mock.Anything, mock.Anything).Return((*PlanningSnapshotRecord)(nil), nil).Maybe()
	env.OnActivity(a.LoadPlanningCandidateScoresActivity, mock.Anything, mock.Anything).Return([]PlanningCandidateScoreRecord{}, nil).Maybe()
	env.OnActivity(a.AdjustPlanningCandidateScoreActivity, mock.Anything, mock.Anything).Return(nil).Maybe()

	env.OnActivity(a.GroomBacklogActivity, mock.Anything, mock.Anything).Return(&BacklogPresentation{
		Items: []BacklogItem{
			{ID: "morsel-1", Title: "Top slice", Impact: "high", Effort: "S", Recommended: true, Rationale: "most leverage"},
		},
		Rationale: "Prioritize highest leverage first.",
	}, nil)
	env.OnActivity(a.GenerateQuestionsActivity, mock.Anything, mock.Anything, mock.Anything).Return([]PlanningQuestion{
		{Question: "What exact behavior should change first?", Options: []string{"A", "B"}, Recommendation: "A"},
	}, nil)
	env.OnActivity(a.SummarizePlanActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&PlanSummary{
		What:      "Implement top slice behavior with deterministic checks",
		Why:       "Fastest path to validated planning improvement",
		Effort:    "S",
		DoDChecks: []string{"go test ./internal/temporal"},
	}, nil)

	env.OnWorkflow(CrabDecompositionWorkflow, mock.Anything, mock.Anything).Return(&CrabDecompositionResult{
		Status: "escalated",
	}, nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("item-selected", "morsel-1")
		env.SignalWorkflow("answer", "A")
		env.SignalWorkflow("greenlight", "GO")
	}, 0)

	env.ExecuteWorkflow(PlanningCeremonyWorkflow, PlanningRequest{
		Project:       "test-project",
		Agent:         "claude",
		WorkDir:       "/tmp/test",
		CandidateTopK: 2,
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func TestPlanningWorkflowTimesOutWaitingForSelection(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	traceEvents := make([]PlanningTraceRecord, 0, 16)
	env.OnActivity(a.RecordPlanningTraceActivity, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		rec, ok := args.Get(1).(PlanningTraceRecord)
		if !ok {
			return
		}
		traceEvents = append(traceEvents, rec)
	}).Return(nil).Maybe()
	env.OnActivity(a.RecordPlanningSnapshotActivity, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity(a.IsPlanningActionBlacklistedActivity, mock.Anything, mock.Anything).Return(false, nil).Maybe()
	env.OnActivity(a.AddPlanningBlacklistEntryActivity, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity(a.GetLatestStablePlanningSnapshotActivity, mock.Anything, mock.Anything).Return((*PlanningSnapshotRecord)(nil), nil).Maybe()
	env.OnActivity(a.LoadPlanningCandidateScoresActivity, mock.Anything, mock.Anything).Return([]PlanningCandidateScoreRecord{}, nil).Maybe()
	env.OnActivity(a.AdjustPlanningCandidateScoreActivity, mock.Anything, mock.Anything).Return(nil).Maybe()

	env.OnActivity(a.GroomBacklogActivity, mock.Anything, mock.Anything).Return(&BacklogPresentation{
		Items: []BacklogItem{
			{ID: "morsel-1", Title: "Top slice", Impact: "high", Effort: "S", Recommended: true, Rationale: "most leverage"},
		},
		Rationale: "Prioritize highest leverage first.",
	}, nil)

	env.ExecuteWorkflow(PlanningCeremonyWorkflow, PlanningRequest{
		Project:        "test-project",
		Agent:          "claude",
		WorkDir:        "/tmp/test",
		CandidateTopK:  2,
		SignalTimeout:  time.Second,
		SessionTimeout: 10 * time.Minute,
	})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	require.Contains(t, env.GetWorkflowError().Error(), "planning signal timeout")

	hasTimeout := false
	for i := range traceEvents {
		if traceEvents[i].EventType == "planning_signal_timeout" {
			hasTimeout = true
			break
		}
	}
	require.True(t, hasTimeout, "expected planning_signal_timeout trace event")
}
