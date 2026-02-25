package api

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/antigravity-dev/chum/internal/beadsfork"
)

// TaskMirror mirrors API-created tasks to an external planning system.
// Implementations must be best-effort safe; failures should not block core task creation.
type TaskMirror interface {
	MirrorTaskCreate(ctx context.Context, taskID string, req CreateTaskRequest) (externalID string, err error)
}

type beadsForkClient interface {
	Create(ctx context.Context, title string, req beadsfork.CreateRequest) (beadsfork.Issue, error)
	AddDependency(ctx context.Context, issueID, dependsOnID, depType string) error
}

// BeadsForkTaskMirror mirrors CHUM task creation into a bd workspace.
// It keeps an in-memory task-id mapping to attach dependency edges for tasks
// created during the current server lifetime.
type BeadsForkTaskMirror struct {
	client beadsForkClient

	mu          sync.RWMutex
	taskToIssue map[string]string
}

// NewBeadsForkTaskMirror builds a mirror backed by the beadsfork scaffold client.
func NewBeadsForkTaskMirror(client *beadsfork.Client) *BeadsForkTaskMirror {
	return NewBeadsForkTaskMirrorWithClient(client)
}

// NewBeadsForkTaskMirrorWithClient allows testing with client fakes.
func NewBeadsForkTaskMirrorWithClient(client beadsForkClient) *BeadsForkTaskMirror {
	return &BeadsForkTaskMirror{
		client:      client,
		taskToIssue: map[string]string{},
	}
}

// MirrorTaskCreate mirrors a newly-created CHUM task into bd.
func (m *BeadsForkTaskMirror) MirrorTaskCreate(ctx context.Context, taskID string, req CreateTaskRequest) (string, error) {
	if m == nil || m.client == nil {
		return "", fmt.Errorf("beads mirror client is not configured")
	}

	issueType := normalizeBeadsIssueType(req.Type)
	priority := normalizeBeadsPriority(req.Priority)
	labels := dedupeLabels(req.Labels, "source:chum", "mirror:beads")
	if taskID != "" {
		labels = append(labels, "chum-task:"+taskID)
	}

	created, err := m.client.Create(ctx, req.Title, beadsfork.CreateRequest{
		Description: buildBeadsDescription(taskID, req),
		Priority:    priority,
		IssueType:   issueType,
		Labels:      labels,
	})
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	if taskID != "" {
		m.taskToIssue[taskID] = created.ID
	}
	m.mu.Unlock()

	// Best-effort dependency mirror for dependencies created in current process lifetime.
	for _, depTaskID := range req.DependsOn {
		depTaskID = strings.TrimSpace(depTaskID)
		if depTaskID == "" {
			continue
		}
		if depIssueID, ok := m.lookup(depTaskID); ok {
			_ = m.client.AddDependency(ctx, created.ID, depIssueID, "blocks") //nolint:errcheck // best-effort
		}
	}

	return created.ID, nil
}

func (m *BeadsForkTaskMirror) lookup(taskID string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	issueID, ok := m.taskToIssue[taskID]
	return issueID, ok
}

func normalizeBeadsIssueType(taskType string) string {
	switch strings.ToLower(strings.TrimSpace(taskType)) {
	case "bug", "feature", "task", "epic", "chore", "merge-request":
		return strings.ToLower(strings.TrimSpace(taskType))
	default:
		return "task"
	}
}

func normalizeBeadsPriority(priority int) int {
	if priority < 0 || priority > 4 {
		return 2
	}
	return priority
}

func dedupeLabels(labels []string, extras ...string) []string {
	out := make([]string, 0, len(labels)+len(extras))
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if slices.Contains(out, v) {
			return
		}
		out = append(out, v)
	}
	for _, label := range labels {
		add(label)
	}
	for _, extra := range extras {
		add(extra)
	}
	return out
}

func buildBeadsDescription(taskID string, req CreateTaskRequest) string {
	parts := make([]string, 0, 5)
	if desc := strings.TrimSpace(req.Description); desc != "" {
		parts = append(parts, desc)
	}

	meta := make([]string, 0, 3)
	if taskID != "" {
		meta = append(meta, "CHUM Task ID: "+taskID)
	}
	if project := strings.TrimSpace(req.Project); project != "" {
		meta = append(meta, "Project: "+project)
	}
	if len(meta) > 0 {
		parts = append(parts, strings.Join(meta, "\n"))
	}

	if acceptance := strings.TrimSpace(req.Acceptance); acceptance != "" {
		parts = append(parts, "Acceptance Criteria:\n"+acceptance)
	}
	if design := strings.TrimSpace(req.Design); design != "" {
		parts = append(parts, "Design Notes:\n"+design)
	}

	if len(parts) == 0 {
		return "(mirrored from CHUM /tasks)"
	}
	return strings.Join(parts, "\n\n")
}
