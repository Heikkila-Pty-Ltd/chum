package temporal

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	workflowservice "go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc"
)

type fakeBatchService struct {
	lastRequest *workflowservice.StartBatchOperationRequest
	startErr    error
}

func (f *fakeBatchService) StartBatchOperation(_ context.Context, req *workflowservice.StartBatchOperationRequest, _ ...grpc.CallOption) (*workflowservice.StartBatchOperationResponse, error) {
	f.lastRequest = req
	if f.startErr != nil {
		return nil, f.startErr
	}
	return &workflowservice.StartBatchOperationResponse{}, nil
}

func assertVisibilityQueryAndNamespace(t *testing.T, req *workflowservice.StartBatchOperationRequest, namespace, query string) {
	t.Helper()
	require.Equal(t, query, req.GetVisibilityQuery())
	require.Equal(t, namespace, req.GetNamespace())
	require.NotEmpty(t, req.GetReason())
	require.NotEmpty(t, req.GetJobId())
}

func assertSignalOp(t *testing.T, req *workflowservice.StartBatchOperationRequest, signal string) {
	t.Helper()
	signalOp := req.GetSignalOperation()
	require.NotNil(t, signalOp)
	require.Equal(t, signal, signalOp.GetSignal())
	require.Equal(t, "chum-admin", signalOp.GetIdentity())
}

func assertResetOp(t *testing.T, req *workflowservice.StartBatchOperationRequest) {
	t.Helper()
	resetOp := req.GetResetOperation()
	require.NotNil(t, resetOp)
	require.Equal(t, "chum-admin", resetOp.GetIdentity())
	require.NotNil(t, resetOp.GetOptions())
	require.NotNil(t, resetOp.GetOptions().GetLastWorkflowTask())
}

func assertTerminationOp(t *testing.T, req *workflowservice.StartBatchOperationRequest) {
	t.Helper()
	terminationOp := req.GetTerminationOperation()
	require.NotNil(t, terminationOp)
	require.Equal(t, "chum-admin", terminationOp.GetIdentity())
}

func TestStartDrainAgentWorkflowsStartsSignalBatchOperation(t *testing.T) {
	svc := &fakeBatchService{}
	operationID, err := StartDrainAgentWorkflows(t.Context(), svc, "", "WorkflowType = 'ChumAgentWorkflow'")
	require.NoError(t, err)
	require.NotEmpty(t, operationID)
	require.Contains(t, operationID, "chum-admin-drain")
	require.NotNil(t, svc.lastRequest)

	assertVisibilityQueryAndNamespace(t, svc.lastRequest, client.DefaultNamespace, "WorkflowType = 'ChumAgentWorkflow'")
	assertSignalOp(t, svc.lastRequest, ChumAgentDrainSignalName)
}

func TestStartResumeAgentWorkflowsStartsSignalBatchOperation(t *testing.T) {
	svc := &fakeBatchService{}
	operationID, err := StartResumeAgentWorkflows(t.Context(), svc, "ns", "ExecutionStatus = 'Running'")
	require.NoError(t, err)
	require.NotEmpty(t, operationID)
	require.Contains(t, operationID, "chum-admin-resume")
	require.NotNil(t, svc.lastRequest)

	assertVisibilityQueryAndNamespace(t, svc.lastRequest, "ns", "ExecutionStatus = 'Running'")
	assertSignalOp(t, svc.lastRequest, ChumAgentResumeSignalName)
}

func TestStartResetAgentWorkflowsStartsResetBatchOperation(t *testing.T) {
	svc := &fakeBatchService{}
	operationID, err := StartResetAgentWorkflows(t.Context(), svc, "", "Project = 'api'")
	require.NoError(t, err)
	require.NotEmpty(t, operationID)
	require.Contains(t, operationID, "chum-admin-reset")
	require.NotNil(t, svc.lastRequest)

	assertVisibilityQueryAndNamespace(t, svc.lastRequest, client.DefaultNamespace, "Project = 'api'")
	assertResetOp(t, svc.lastRequest)
}

func TestStartTerminateAgentWorkflowsStartsTerminationBatchOperation(t *testing.T) {
	svc := &fakeBatchService{}
	operationID, err := StartTerminateAgentWorkflows(t.Context(), svc, "custom-ns", "CurrentStage = 'running'")
	require.NoError(t, err)
	require.NotEmpty(t, operationID)
	require.Contains(t, operationID, "chum-admin-terminate")
	require.NotNil(t, svc.lastRequest)

	assertVisibilityQueryAndNamespace(t, svc.lastRequest, "custom-ns", "CurrentStage = 'running'")
	assertTerminationOp(t, svc.lastRequest)
}

func TestStartAgentWorkflowsRejectsMissingQuery(t *testing.T) {
	svc := &fakeBatchService{}
	_, err := StartResetAgentWorkflows(t.Context(), svc, "", "   ")
	require.Error(t, err)

	_, err = StartTerminateAgentWorkflows(t.Context(), svc, "", "\t")
	require.Error(t, err)
}
