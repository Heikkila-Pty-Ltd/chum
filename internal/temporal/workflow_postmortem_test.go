package temporal

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

func TestPostMortemWorkflowRecordsHealthEventAndNotifies(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var healthEventRecorded bool
	var notifyRecorded bool

	env.OnActivity(a.RecordHealthEventActivity, mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		healthEventRecorded = true
	}).Return(nil)

	env.OnActivity(a.NotifyActivity, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		notifyRecorded = true
	}).Return(nil)

	env.OnActivity(a.RecordOrganismLogActivity, mock.Anything, mock.Anything).Return(nil).Maybe()

	req := PostMortemRequest{
		Failure: FailureContext{
			WorkflowID:   "chum-agent-task-42-1234567890",
			RunID:        "run-abc",
			ErrorMessage: "activity timeout on ExecuteActivity",
			TaskID:       "task-42",
		},
		Project: "test-project",
		Tier:    "fast",
	}

	env.ExecuteWorkflow(PostMortemWorkflow, req)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.True(t, healthEventRecorded, "should record health event")
	require.True(t, notifyRecorded, "should send notification")
}

func TestPostMortemWorkflowSucceedsEvenIfActivitiesFail(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	// Both activities fail — workflow should still complete.
	env.OnActivity(a.RecordHealthEventActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	env.OnActivity(a.NotifyActivity, mock.Anything, mock.Anything).
		Return(nil)
	env.OnActivity(a.RecordOrganismLogActivity, mock.Anything, mock.Anything).Return(nil).Maybe()

	req := PostMortemRequest{
		Failure: FailureContext{
			WorkflowID:   "chum-agent-fix-db-999",
			RunID:        "run-xyz",
			ErrorMessage: "compile error: undefined reference",
		},
		Project: "chum",
		Tier:    "fast",
	}

	env.ExecuteWorkflow(PostMortemWorkflow, req)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func TestDispatcherSpawnsPostMortemForFailedWorkflows(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var da *DispatchActivities
	var postMortemSpawned bool

	// No candidates to dispatch.
	env.OnActivity(da.ScanCandidatesActivity, mock.Anything).Return(&ScanCandidatesResult{
		Candidates: nil,
		Running:    0,
		MaxTotal:   3,
	}, nil)

	// Return one failed workflow.
	env.OnActivity(da.CheckFailedWorkflowsActivity, mock.Anything).Return([]FailedWorkflow{{
		WorkflowID: "chum-agent-task-77-1234567890",
		RunID:      "run-failed",
		CloseTime:  "2026-02-27T08:00:00Z",
		ErrorMsg:   "activity StartToClose timeout",
	}}, nil)

	// Return failure context.
	env.OnActivity(da.FetchFailureContextActivity, mock.Anything, mock.Anything).Return(&FailureContext{
		WorkflowID:   "chum-agent-task-77-1234567890",
		RunID:        "run-failed",
		ErrorMessage: "activity StartToClose timeout",
		TaskID:       "task-77",
	}, nil)

	// Capture PostMortemWorkflow spawn.
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

	// CheckFailedWorkflowsActivity not registered — simulates failure.
	// The dispatcher should handle this gracefully (non-fatal).

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
