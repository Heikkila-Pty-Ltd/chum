package temporal

import (
	"context"
	"fmt"
	"strings"
	"time"

	batchv1 "go.temporal.io/api/batch/v1"
	commonv1 "go.temporal.io/api/common/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc"

	emptypb "google.golang.org/protobuf/types/known/emptypb"
)

const (
	adminBatchOperationReasonPrefix = "chum admin"
	adminBatchOperationIdentity     = "chum-admin"
)

type batchService interface {
	StartBatchOperation(context.Context, *workflowservice.StartBatchOperationRequest, ...grpc.CallOption) (*workflowservice.StartBatchOperationResponse, error)
}

func batchOperationJobID(operation string) string {
	return fmt.Sprintf("chum-admin-%s-%d", operation, time.Now().UnixNano())
}

func startVisibilityBatchOperation(ctx context.Context, svc batchService, namespace, query, opType, opReason string, op any) (string, error) {
	if svc == nil {
		return "", fmt.Errorf("temporal batch service is nil")
	}

	q := strings.TrimSpace(query)
	if q == "" {
		return "", fmt.Errorf("visibility query is required")
	}

	ns := strings.TrimSpace(namespace)
	if ns == "" {
		ns = client.DefaultNamespace
	}

	req := &workflowservice.StartBatchOperationRequest{
		Namespace:       ns,
		VisibilityQuery: q,
		JobId:           batchOperationJobID(opType),
		Reason:          fmt.Sprintf("%s %s", adminBatchOperationReasonPrefix, opReason),
	}

	switch typed := op.(type) {
	case *workflowservice.StartBatchOperationRequest_SignalOperation:
		req.Operation = typed
	case *workflowservice.StartBatchOperationRequest_ResetOperation:
		req.Operation = typed
	case *workflowservice.StartBatchOperationRequest_TerminationOperation:
		req.Operation = typed
	default:
		return "", fmt.Errorf("unsupported batch operation type %T", op)
	}

	if _, err := svc.StartBatchOperation(ctx, req); err != nil {
		return "", err
	}

	return req.JobId, nil
}

func StartDrainAgentWorkflows(ctx context.Context, svc batchService, namespace, query string) (string, error) {
	signal := &workflowservice.StartBatchOperationRequest_SignalOperation{
		SignalOperation: &batchv1.BatchOperationSignal{
			Signal:   ChumAgentDrainSignalName,
			Identity: adminBatchOperationIdentity,
		},
	}
	return startVisibilityBatchOperation(
		ctx,
		svc,
		namespace,
		query,
		"drain",
		"drain",
		signal,
	)
}

func StartResumeAgentWorkflows(ctx context.Context, svc batchService, namespace, query string) (string, error) {
	signal := &workflowservice.StartBatchOperationRequest_SignalOperation{
		SignalOperation: &batchv1.BatchOperationSignal{
			Signal:   ChumAgentResumeSignalName,
			Identity: adminBatchOperationIdentity,
		},
	}
	return startVisibilityBatchOperation(
		ctx,
		svc,
		namespace,
		query,
		"resume",
		"resume",
		signal,
	)
}

func StartResetAgentWorkflows(ctx context.Context, svc batchService, namespace, query string) (string, error) {
	reset := &workflowservice.StartBatchOperationRequest_ResetOperation{
		ResetOperation: &batchv1.BatchOperationReset{
			Identity: adminBatchOperationIdentity,
			Options: &commonv1.ResetOptions{
				Target: &commonv1.ResetOptions_LastWorkflowTask{
					LastWorkflowTask: &emptypb.Empty{},
				},
			},
		},
	}
	return startVisibilityBatchOperation(
		ctx,
		svc,
		namespace,
		query,
		"reset",
		"reset",
		reset,
	)
}

func StartTerminateAgentWorkflows(ctx context.Context, svc batchService, namespace, query string) (string, error) {
	termination := &workflowservice.StartBatchOperationRequest_TerminationOperation{
		TerminationOperation: &batchv1.BatchOperationTermination{
			Identity: adminBatchOperationIdentity,
		},
	}
	return startVisibilityBatchOperation(
		ctx,
		svc,
		namespace,
		query,
		"terminate",
		"terminate",
		termination,
	)
}
