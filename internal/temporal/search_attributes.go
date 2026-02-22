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
		SearchAttributeTaskTitle:    enumspb.INDEXED_VALUE_TYPE_KEYWORD,
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
	if project == "" {
		return visibilityQuery
	}

	return fmt.Sprintf("%s AND %s = '%s'", visibilityQuery, SearchAttributeProject, strings.ReplaceAll(project, "'", "''"))
}

func upsertChumWorkflowSearchAttributes(ctx workflow.Context, req TaskRequest, status string) error {
	req.Priority = normalizePriority(req.Priority)
	attrs := map[string]interface{}{
		SearchAttributeProject:      req.Project,
		SearchAttributePriority:     req.Priority,
		SearchAttributeAgent:        req.Agent,
		SearchAttributeCurrentStage: status,
		SearchAttributeTaskTitle:    req.TaskTitle,
	}
	return upsertChumSearchAttributesFn(ctx, attrs)
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

	_, err := c.OperatorService().AddSearchAttributes(ctx, &operatorservice.AddSearchAttributesRequest{
		Namespace:        ns,
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
