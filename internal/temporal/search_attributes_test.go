package temporal

import (
	"context"
	"errors"
	"testing"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"

	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/api/serviceerror"
	"google.golang.org/grpc"
)

type fakeSearchAttributeRegistrar struct {
	requests []*operatorservice.AddSearchAttributesRequest
	err      error
}

func (f *fakeSearchAttributeRegistrar) AddSearchAttributes(
	_ context.Context,
	req *operatorservice.AddSearchAttributesRequest,
	_ ...grpc.CallOption,
) (*operatorservice.AddSearchAttributesResponse, error) {
	f.requests = append(f.requests, req)
	if f.err != nil {
		return nil, f.err
	}
	return &operatorservice.AddSearchAttributesResponse{}, nil
}

func TestChumSearchAttributeDefsHaveExactContract(t *testing.T) {
	defs := chumSearchAttributeDefs()

	require.Len(t, defs, 4)
	require.Equal(t, enumspb.INDEXED_VALUE_TYPE_KEYWORD, defs[SearchAttributeProject])
	require.Equal(t, enumspb.INDEXED_VALUE_TYPE_INT, defs[SearchAttributePriority])
	require.Equal(t, enumspb.INDEXED_VALUE_TYPE_KEYWORD, defs[SearchAttributeAgent])
	require.Equal(t, enumspb.INDEXED_VALUE_TYPE_KEYWORD, defs[SearchAttributeCurrentStage])
}

func TestRegisterSearchAttributesSkipsAlreadyExistsError(t *testing.T) {
	registrar := &fakeSearchAttributeRegistrar{
		err: serviceerror.NewAlreadyExists("search attribute already exists"),
	}

	err := registerSearchAttributes(t.Context(), registrar, "chum")
	require.NoError(t, err)
	require.Len(t, registrar.requests, 1)
	require.Equal(t, "chum", registrar.requests[0].Namespace)
}

func TestRegisterSearchAttributesDefaultsAndTrimsNamespace(t *testing.T) {
	registrar := &fakeSearchAttributeRegistrar{}
	err := registerSearchAttributes(t.Context(), registrar, "  ")
	require.NoError(t, err)
	require.Len(t, registrar.requests, 1)
	require.Equal(t, client.DefaultNamespace, registrar.requests[0].Namespace)
}

func TestRegisterSearchAttributesReturnsUnexpectedErrors(t *testing.T) {
	otherErr := errors.New("search attribute registration failed")
	registrar := &fakeSearchAttributeRegistrar{
		err: otherErr,
	}

	err := registerSearchAttributes(t.Context(), registrar, "chum")
	require.Error(t, err)
	require.EqualError(t, err, otherErr.Error())
}

func TestChumAgentRunningVisibilityQueryForProjectAndAgent(t *testing.T) {
	q := ChumAgentRunningVisibilityQueryForProjectAndAgent("alpha-proj", "gemini")

	require.Contains(t, q, "WorkflowType = 'ChumAgentWorkflow'")
	require.Contains(t, q, "ExecutionStatus = 'Running'")
	require.Contains(t, q, SearchAttributeProject+" = 'alpha-proj'")
	require.Contains(t, q, SearchAttributeAgent+" = 'gemini'")

	stages := []string{chumWorkflowStatusRunning, chumWorkflowStatusPlan, chumWorkflowStatusGate, chumWorkflowStatusExecute, chumWorkflowStatusReview, chumWorkflowStatusDoD}
	for _, stage := range stages {
		require.Contains(t, q, SearchAttributeCurrentStage+" = '"+stage+"'")
	}
}

func TestChumAgentRunningVisibilityQueryEscapesProject(t *testing.T) {
	q := ChumAgentRunningVisibilityQueryForProject("acme's")
	require.Contains(t, q, SearchAttributeProject+" = 'acme''s'")
}

func TestUpsertChumWorkflowSearchAttributesNormalizesRequest(t *testing.T) {
	var capturedAttrs map[string]interface{}
	original := upsertChumSearchAttributesFn
	var workflowCtx workflow.Context
	t.Cleanup(func() {
		upsertChumSearchAttributesFn = original
	})

	upsertChumSearchAttributesFn = func(_ workflow.Context, attrs map[string]interface{}) error {
		capturedAttrs = attrs
		return nil
	}

	err := upsertChumWorkflowSearchAttributes(workflowCtx, TaskRequest{
		Project:   "  ",
		Agent:     " ",
		TaskTitle: "",
		TaskID:    "task-42",
		Prompt:    "build search index",
		Priority:  99,
	}, chumWorkflowStatusPlan)
	require.NoError(t, err)
	require.NotNil(t, capturedAttrs)
	require.Equal(t, "unknown", capturedAttrs[SearchAttributeProject])
	require.Equal(t, 4, capturedAttrs[SearchAttributePriority])
	require.Equal(t, "claude", capturedAttrs[SearchAttributeAgent])
	require.Equal(t, chumWorkflowStatusPlan, capturedAttrs[SearchAttributeCurrentStage])
}
