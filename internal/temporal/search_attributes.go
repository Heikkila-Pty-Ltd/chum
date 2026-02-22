package temporal

import (
	"context"
	"errors"
	"fmt"
	"strings"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/grpc"
)

const (
	SearchAttributeProject      = "project"
	SearchAttributePriority     = "priority"
	SearchAttributeAgent        = "agent"
	SearchAttributeCurrentStage = "current_stage"
	SearchAttributeTaskTitle    = "task_title"

	// Signal names used by admin operations.
	ChumAgentDrainSignalName  = "admin-drain"
	ChumAgentResumeSignalName = "admin-resume"

	chumWorkflowStatusPlan      = "plan"
	chumWorkflowStatusRunning   = "running"
	chumWorkflowStatusGate      = "gate"
	chumWorkflowStatusExecute   = "execute"
	chumWorkflowStatusReview    = "review"
	chumWorkflowStatusDoD       = "dod"
	chumWorkflowStatusCompleted = "completed"
	chumWorkflowStatusEscalated = "escalated"
)

var upsertChumSearchAttributesFn = workflow.UpsertSearchAttributes //nolint:staticcheck

func chumSearchAttributeDefs() map[string]enumspb.IndexedValueType {
	return map[string]enumspb.IndexedValueType{
		SearchAttributeProject:      enumspb.INDEXED_VALUE_TYPE_KEYWORD,
		SearchAttributePriority:     enumspb.INDEXED_VALUE_TYPE_INT,
		SearchAttributeAgent:        enumspb.INDEXED_VALUE_TYPE_KEYWORD,
		SearchAttributeCurrentStage: enumspb.INDEXED_VALUE_TYPE_KEYWORD,
		SearchAttributeTaskTitle:    enumspb.INDEXED_VALUE_TYPE_TEXT,
	}
}

// ChumAgentRunningVisibilityQuery returns the visibility query for running Chum
// agent workflows across all projects.
func ChumAgentRunningVisibilityQuery() string {
	return ChumAgentRunningVisibilityQueryForProject("")
}

// ChumAgentRunningVisibilityQueryForProject returns the visibility query for
// running Chum agent workflows, optionally filtered by project.
func ChumAgentRunningVisibilityQueryForProject(project string) string {
	return ChumAgentRunningVisibilityQueryForProjectAndAgent(project, "")
}

// ChumAgentRunningVisibilityQueryForProjectAndAgent returns the visibility query for
// running Chum agent workflows, optionally filtered by project and/or agent.
func ChumAgentRunningVisibilityQueryForProjectAndAgent(project, agent string) string {
	activeStatuses := []string{
		chumWorkflowStatusRunning,
		chumWorkflowStatusPlan,
		chumWorkflowStatusGate,
		chumWorkflowStatusExecute,
		chumWorkflowStatusReview,
		chumWorkflowStatusDoD,
	}

	queryClauses := make([]string, 0, len(activeStatuses))
	for _, status := range activeStatuses {
		queryClauses = append(queryClauses, fmt.Sprintf("%s = '%s'", SearchAttributeCurrentStage, status))
	}

	visibilityQuery := fmt.Sprintf(
		"WorkflowType = 'ChumAgentWorkflow' AND ExecutionStatus = 'Running' AND (%s)",
		strings.Join(queryClauses, " OR "),
	)

	project = strings.TrimSpace(project)
	if project != "" {
		visibilityQuery = fmt.Sprintf("%s AND %s = '%s'", visibilityQuery, SearchAttributeProject, quoteSearchAttributeValue(project))
	}

	agent = strings.TrimSpace(agent)
	if agent != "" {
		visibilityQuery = fmt.Sprintf("%s AND %s = '%s'", visibilityQuery, SearchAttributeAgent, quoteSearchAttributeValue(agent))
	}

	return visibilityQuery
}

func upsertChumWorkflowSearchAttributes(ctx workflow.Context, req TaskRequest, status string) error {
	searchMetadata := normalizeSearchMetadataForVisibility(req)
	attrs := map[string]interface{}{
		SearchAttributeProject:      searchMetadata.Project,
		SearchAttributePriority:     searchMetadata.Priority,
		SearchAttributeAgent:        searchMetadata.Agent,
		SearchAttributeCurrentStage: status,
		SearchAttributeTaskTitle:    searchMetadata.TaskTitle,
	}
	return upsertChumSearchAttributesFn(ctx, attrs)
}

func normalizeSearchMetadataForVisibility(req TaskRequest) TaskRequest {
	req.Agent = strings.TrimSpace(req.Agent)
	if req.Agent == "" {
		req.Agent = "claude"
	}
	req.Project = strings.TrimSpace(req.Project)
	if req.Project == "" {
		req.Project = "unknown"
	}
	req.TaskTitle = normalizeTaskTitle(req.TaskID, req.TaskTitle, req.Prompt)
	req.Priority = normalizePriority(req.Priority)
	return req
}

// RegisterChumSearchAttributes is idempotent on existing attribute registrations.
func RegisterChumSearchAttributes(ctx context.Context, c client.Client) error {
	return RegisterChumSearchAttributesWithNamespace(ctx, c, client.DefaultNamespace)
}

// RegisterSearchAttributes provides an explicit namespace option for startup registration.
func RegisterSearchAttributes(ctx context.Context, c client.Client, namespace string) error {
	return RegisterChumSearchAttributesWithNamespace(ctx, c, namespace)
}

func RegisterChumSearchAttributesWithNamespace(ctx context.Context, c client.Client, namespace string) error {
	if c == nil {
		return fmt.Errorf("temporal client is nil")
	}

	ns := strings.TrimSpace(namespace)
	if ns == "" {
		ns = client.DefaultNamespace
	}

	if err := registerSearchAttributes(ctx, c.OperatorService(), ns); err != nil {
		return err
	}
	return nil
}

func registerSearchAttributes(ctx context.Context, reg searchAttributeRegistrar, namespace string) error {
	_, err := reg.AddSearchAttributes(ctx, &operatorservice.AddSearchAttributesRequest{
		Namespace:        namespace,
		SearchAttributes: chumSearchAttributeDefs(),
	})
	if err == nil {
		return nil
	}

	if isChumSearchAttributeAlreadyExistsError(err) {
		return nil
	}
	return err
}

type searchAttributeRegistrar interface {
	AddSearchAttributes(context.Context, *operatorservice.AddSearchAttributesRequest, ...grpc.CallOption) (*operatorservice.AddSearchAttributesResponse, error)
}

func quoteSearchAttributeValue(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func isChumSearchAttributeAlreadyExistsError(err error) bool {
	if err == nil {
		return false
	}

	var alreadyExists *serviceerror.AlreadyExists
	if errors.As(err, &alreadyExists) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "already exists")
}

func normalizePriority(priority int) int {
	if priority < 0 {
		return 0
	}
	if priority > 4 {
		return 4
	}
	return priority
}
