package temporal

import (
	"context"
	"errors"
	"testing"

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

	require.Len(t, defs, 5)
	require.Equal(t, enumspb.INDEXED_VALUE_TYPE_KEYWORD, defs[SearchAttributeProject])
	require.Equal(t, enumspb.INDEXED_VALUE_TYPE_INT, defs[SearchAttributePriority])
	require.Equal(t, enumspb.INDEXED_VALUE_TYPE_KEYWORD, defs[SearchAttributeAgent])
	require.Equal(t, enumspb.INDEXED_VALUE_TYPE_KEYWORD, defs[SearchAttributeCurrentStage])
	require.Equal(t, enumspb.INDEXED_VALUE_TYPE_TEXT, defs[SearchAttributeTaskTitle])
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
