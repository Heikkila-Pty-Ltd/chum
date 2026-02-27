package temporal

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// stubPostMortemActivities registers mocks for all activities the
// PostMortemWorkflow calls, so tests don't panic on unregistered activities.
func stubPostMortemActivities(env *testsuite.TestWorkflowEnvironment) {
	env.OnActivity(a.RecordHealthEventActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity(a.NotifyActivity, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity(a.RecordOrganismLogActivity, mock.Anything, mock.Anything).Return(nil).Maybe()
}

func TestPostMortemWorkflowInvestigatesAndFilesAntibody(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	stubPostMortemActivities(env)

	var investigateCalled, antibodyCalled bool

	env.OnActivity(a.InvestigateFailureActivity, mock.Anything, mock.Anything).Run(func(_ mock.Arguments) {
		investigateCalled = true
	}).Return(&PostMortemInvestigation{
		RootCause:     "missing DB column provider_genes",
		Severity:      "high",
		ProposedFix:   "Add provider_genes column to genomes table migration",
		AffectedFiles: []string{"internal/store/genomes.go"},
		Category:      "logic",
		Antibodies:    []string{"always migrate new columns"},
	}, nil)

	env.OnActivity(a.FileAntibodyActivity, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		antibodyCalled = true
		req := args.Get(1).(FileAntibodyRequest)
		require.Equal(t, "missing DB column provider_genes", req.Investigation.RootCause)
		require.Equal(t, "test-project", req.Project)
	}).Return("task-antibody-1", nil)

	req := PostMortemRequest{
		Failure: FailureContext{
			WorkflowID:   "chum-agent-task-42-1234567890",
			RunID:        "run-abc",
			ErrorMessage: "no such column: provider_genes",
			TaskID:       "task-42",
		},
		Project: "test-project",
		Tier:    "fast",
	}

	env.ExecuteWorkflow(PostMortemWorkflow, req)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.True(t, investigateCalled, "should call InvestigateFailureActivity")
	require.True(t, antibodyCalled, "should call FileAntibodyActivity for high severity")
}

func TestPostMortemWorkflowSkipsAntibodyForLowSeverity(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	stubPostMortemActivities(env)

	env.OnActivity(a.InvestigateFailureActivity, mock.Anything, mock.Anything).Return(&PostMortemInvestigation{
		RootCause: "transient network timeout",
		Severity:  "low",
		Category:  "infrastructure",
	}, nil)

	// FileAntibodyActivity should NOT be called for low severity.
	antibodyCalled := false
	env.OnActivity(a.FileAntibodyActivity, mock.Anything, mock.Anything).Run(func(_ mock.Arguments) {
		antibodyCalled = true
	}).Return("", nil).Maybe()

	req := PostMortemRequest{
		Failure: FailureContext{
			WorkflowID:   "chum-agent-task-99-1234567890",
			RunID:        "run-low",
			ErrorMessage: "context deadline exceeded",
		},
		Project: "chum",
	}

	env.ExecuteWorkflow(PostMortemWorkflow, req)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.False(t, antibodyCalled, "should NOT file antibody for low severity")
}

func TestPostMortemWorkflowContinuesWhenInvestigationFails(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	stubPostMortemActivities(env)

	// Investigation fails — workflow should still complete.
	env.OnActivity(a.InvestigateFailureActivity, mock.Anything, mock.Anything).
		Return(nil, errors.New("LLM unavailable"))

	// FileAntibodyActivity should NOT be called when investigation fails.
	env.OnActivity(a.FileAntibodyActivity, mock.Anything, mock.Anything).Return("", nil).Maybe()

	req := PostMortemRequest{
		Failure: FailureContext{
			WorkflowID:   "chum-agent-task-55-1234567890",
			RunID:        "run-err",
			ErrorMessage: "something broke",
		},
		Project: "chum",
	}

	env.ExecuteWorkflow(PostMortemWorkflow, req)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError(), "workflow should complete even if investigation fails")
}

func TestPostMortemWorkflowRecordsHealthEventAndNotifies(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var healthEventRecorded, notifyRecorded bool

	env.OnActivity(a.RecordHealthEventActivity, mock.Anything, mock.Anything, mock.Anything).Run(func(_ mock.Arguments) {
		healthEventRecorded = true
	}).Return(nil)

	env.OnActivity(a.NotifyActivity, mock.Anything, mock.Anything).Run(func(_ mock.Arguments) {
		notifyRecorded = true
	}).Return(nil)

	env.OnActivity(a.RecordOrganismLogActivity, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity(a.InvestigateFailureActivity, mock.Anything, mock.Anything).Return(&PostMortemInvestigation{
		RootCause: "test error",
		Severity:  "low",
		Category:  "logic",
	}, nil)
	env.OnActivity(a.FileAntibodyActivity, mock.Anything, mock.Anything).Return("", nil).Maybe()

	req := PostMortemRequest{
		Failure: FailureContext{
			WorkflowID:   "chum-agent-task-42-1234567890",
			RunID:        "run-abc",
			ErrorMessage: "activity timeout on ExecuteActivity",
			TaskID:       "task-42",
		},
		Project: "test-project",
	}

	env.ExecuteWorkflow(PostMortemWorkflow, req)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.True(t, healthEventRecorded, "should record health event")
	require.True(t, notifyRecorded, "should send notification")
}

func TestDispatcherSpawnsPostMortemForFailedWorkflows(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var da *DispatchActivities
	var postMortemSpawned bool

	env.OnActivity(da.ScanCandidatesActivity, mock.Anything).Return(&ScanCandidatesResult{
		Candidates: nil,
		Running:    0,
		MaxTotal:   3,
	}, nil)

	env.OnActivity(da.CheckFailedWorkflowsActivity, mock.Anything).Return([]FailedWorkflow{{
		WorkflowID: "chum-agent-task-77-1234567890",
		RunID:      "run-failed",
		CloseTime:  "2026-02-27T08:00:00Z",
		ErrorMsg:   "activity StartToClose timeout",
	}}, nil)

	env.OnActivity(da.FetchFailureContextActivity, mock.Anything, mock.Anything).Return(&FailureContext{
		WorkflowID:   "chum-agent-task-77-1234567890",
		RunID:        "run-failed",
		ErrorMessage: "activity StartToClose timeout",
		TaskID:       "task-77",
	}, nil)

	env.OnWorkflow(PostMortemWorkflow, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		postMortemSpawned = true
		req := args.Get(1).(PostMortemRequest)
		require.Equal(t, "chum-agent-task-77-1234567890", req.Failure.WorkflowID)
		require.Equal(t, "task-77", req.Failure.TaskID)
	}).Return(nil)

	stubInfraActivities(env)

	env.ExecuteWorkflow(DispatcherWorkflow, struct{}{})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.True(t, postMortemSpawned, "dispatcher should spawn PostMortemWorkflow for failed workflows")
}

func TestDispatcherContinuesWhenFailureCheckFails(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var da *DispatchActivities

	env.OnActivity(da.ScanCandidatesActivity, mock.Anything).Return(&ScanCandidatesResult{
		Candidates: nil,
		Running:    0,
		MaxTotal:   3,
	}, nil)

	stubInfraActivities(env)

	env.ExecuteWorkflow(DispatcherWorkflow, struct{}{})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError(), "dispatcher should complete even if failure check fails")
}

func TestFetchFailureContextExtractsTaskID(t *testing.T) {
	tests := []struct {
		name       string
		workflowID string
		wantTaskID string
	}{
		{
			name:       "standard format",
			workflowID: "chum-agent-fix-db-migration-1234567890",
			wantTaskID: "fix-db-migration-1234567890",
		},
		{
			name:       "short id",
			workflowID: "chum-agent-task42",
			wantTaskID: "task42",
		},
		{
			name:       "too short",
			workflowID: "chum-agent",
			wantTaskID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			taskID := splitWorkflowID(tt.workflowID)
			require.Equal(t, tt.wantTaskID, taskID)
		})
	}
}

// splitWorkflowID extracts the task ID portion from a workflow ID.
// Format: "chum-agent-<taskID>" where taskID is everything after the second dash.
func splitWorkflowID(wfID string) string {
	var count int
	for i, c := range wfID {
		if c == '-' {
			count++
			if count == 2 {
				if i+1 < len(wfID) {
					return wfID[i+1:]
				}
				return ""
			}
		}
	}
	return ""
}

func TestAntibodyDedupKey(t *testing.T) {
	key1 := antibodyDedupKey("missing column", []string{"store.go"})
	key2 := antibodyDedupKey("missing column", []string{"store.go"})
	key3 := antibodyDedupKey("different error", []string{"store.go"})

	require.Equal(t, key1, key2, "same inputs should produce same key")
	require.NotEqual(t, key1, key3, "different root cause should produce different key")
	require.Len(t, key1, 16, "key should be 16 hex chars")
}

func TestSeverityToPriority(t *testing.T) {
	require.Equal(t, 0, severityToPriority("critical"))
	require.Equal(t, 1, severityToPriority("high"))
	require.Equal(t, 2, severityToPriority("medium"))
	require.Equal(t, 3, severityToPriority("low"))
	require.Equal(t, 3, severityToPriority("unknown"))
}
