package temporal

import (
	"context"
	"fmt"
	"strings"

	"go.temporal.io/sdk/activity"

	"github.com/antigravity-dev/chum/internal/graph"
)

// InvestigationRequest describes a system failure that should become a task
// for CHUM to investigate and harden against.
type InvestigationRequest struct {
	Category    string `json:"category"`    // "dispatcher", "sentinel", "infrastructure", "escalation"
	Title       string `json:"title"`       // concise failure title
	Description string `json:"description"` // full error context
	Source      string `json:"source"`      // workflow ID that triggered this
	Project     string `json:"project"`     // target project, or "chum" for self-referential
	Severity    string `json:"severity"`    // "critical" or "warning"
}

// FileInvestigationTaskActivity creates a bugfix task from a system failure.
// Deduplicates against existing open tasks to avoid flooding the DAG.
// The pipeline eats its own failures.
func (a *Activities) FileInvestigationTaskActivity(ctx context.Context, req InvestigationRequest) (string, error) {
	logger := activity.GetLogger(ctx)

	if a.DAG == nil {
		logger.Warn(OrcaPrefix + " No DAG configured, cannot file investigation task")
		return "", nil
	}

	project := req.Project
	if project == "" {
		project = "chum"
	}

	// Dedup: check for existing open tasks with similar title
	existing, err := a.DAG.ListTasks(ctx, project)
	if err != nil {
		logger.Warn(OrcaPrefix+" Failed to list tasks for dedup", "error", err)
		// Continue anyway — better to have a duplicate than miss a failure
	} else {
		titleLower := strings.ToLower(req.Title)
		for _, t := range existing {
			if t.Status == "completed" || t.Status == "closed" {
				continue
			}
			// Match on title similarity — either exact or substring
			existingLower := strings.ToLower(t.Title)
			if existingLower == titleLower ||
				strings.Contains(existingLower, titleLower) ||
				strings.Contains(titleLower, existingLower) {
				logger.Info(OrcaPrefix+" Investigation task already exists, skipping",
					"ExistingID", t.ID, "Title", t.Title)
				return "", nil
			}
			// Also match on self-heal label + same category
			for _, label := range t.Labels {
				if label == "self-heal" && strings.Contains(existingLower, req.Category) {
					logger.Info(OrcaPrefix+" Similar self-heal task exists, skipping",
						"ExistingID", t.ID, "Title", t.Title)
					return "", nil
				}
			}
		}
	}

	// Priority based on severity
	priority := 2
	if req.Severity == "critical" {
		priority = 0
	}

	// Estimate based on category
	estimate := 30
	switch req.Category {
	case "infrastructure":
		estimate = 45
	case "dispatcher":
		estimate = 30
	case "sentinel":
		estimate = 20
	case "escalation":
		estimate = 60
	}

	description := fmt.Sprintf("**Auto-filed by CHUM immune system**\n\n"+
		"Category: %s\nSeverity: %s\nSource: %s\n\n%s",
		req.Category, req.Severity, req.Source, req.Description)

	acceptance := fmt.Sprintf("- Root cause identified and documented\n"+
		"- Fix implemented and verified with `go build ./...`\n"+
		"- Regression prevented (add test or guard if applicable)\n"+
		"- No similar %s failures in subsequent dispatches",
		req.Category)

	taskID, err := a.DAG.CreateTask(ctx, graph.Task{
		Title:           req.Title,
		Description:     description,
		Acceptance:      acceptance,
		Type:            "bugfix",
		Priority:        priority,
		EstimateMinutes: estimate,
		Labels:          []string{"self-heal", req.Category},
		Project:         project,
	})
	if err != nil {
		logger.Error(OrcaPrefix+" Failed to create investigation task", "error", err)
		return "", fmt.Errorf("create investigation task: %w", err)
	}

	logger.Info(OrcaPrefix+" Investigation task filed",
		"TaskID", taskID, "Title", req.Title, "Category", req.Category, "Severity", req.Severity)
	return taskID, nil
}
