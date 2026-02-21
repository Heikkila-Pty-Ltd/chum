package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/antigravity-dev/chum/internal/graph"
)

// BacklogBead represents a task in the backlog with metadata for sprint planning.
type BacklogBead struct {
	*graph.Task
	StageInfo       *BeadStage `json:"stage_info,omitempty"`
	LastDispatchAt  *time.Time `json:"last_dispatch_at,omitempty"`
	DispatchCount   int        `json:"dispatch_count"`
	FailureCount    int        `json:"failure_count"`
	IsBlocked       bool       `json:"is_blocked"`
	BlockingReasons []string   `json:"blocking_reasons,omitempty"`
}

// SprintContext provides comprehensive context for sprint planning decisions.
type SprintContext struct {
	BacklogBeads      []*BacklogBead  `json:"backlog_beads"`
	InProgressBeads   []*BacklogBead  `json:"in_progress_beads"`
	RecentCompletions []*BacklogBead  `json:"recent_completions"`
	DependencyGraph   *graph.DepGraph `json:"dependency_graph"`
	SprintBoundary    *SprintBoundary `json:"current_sprint,omitempty"`
	TotalBeadCount    int             `json:"total_bead_count"`
	ReadyBeadCount    int             `json:"ready_bead_count"`
	BlockedBeadCount  int             `json:"blocked_bead_count"`
}

// DependencyNode represents a node in the dependency graph with additional metadata.
type DependencyNode struct {
	BeadID          string   `json:"bead_id"`
	Title           string   `json:"title"`
	Priority        int      `json:"priority"`
	Stage           string   `json:"stage,omitempty"`
	DependsOn       []string `json:"depends_on"`
	Blocks          []string `json:"blocks"`
	IsReady         bool     `json:"is_ready"`
	EstimateMinutes int      `json:"estimate_minutes"`
}

// SprintPlanningRecord tracks automatic sprint planning trigger execution.
type SprintPlanningRecord struct {
	ID          int64     `json:"id"`
	Project     string    `json:"project"`
	Trigger     string    `json:"trigger"`
	Backlog     int       `json:"backlog"`
	Threshold   int       `json:"threshold"`
	Result      string    `json:"result"`
	Details     string    `json:"details,omitempty"`
	TriggeredAt time.Time `json:"triggered_at"`
}

// GetBacklogBeads retrieves all tasks that are in the backlog (no stage or stage:backlog).
func (s *Store) GetBacklogBeads(ctx context.Context, dag *graph.DAG, project string) ([]*BacklogBead, error) {
	allTasks, err := dag.ListTasks(ctx, project, "open")
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	var backlogBeads []*BacklogBead

	for i := range allTasks {
		task := &allTasks[i]

		// Check if task has stage label indicating it's not in backlog
		hasStageLabel := false
		isBacklog := false

		for _, label := range task.Labels {
			if label == "stage:backlog" {
				isBacklog = true
				hasStageLabel = true
				break
			}
			if len(label) > 6 && label[:6] == "stage:" && label != "stage:backlog" {
				hasStageLabel = true
				break
			}
		}

		// Include in backlog if: no stage label OR explicitly stage:backlog
		if !hasStageLabel || isBacklog {
			backlogBead := &BacklogBead{
				Task: task,
			}

			// Enrich with store data - don't skip if enrichment fails
			s.enrichBacklogBead(project, backlogBead) // ignore errors

			backlogBeads = append(backlogBeads, backlogBead)
		}
	}

	return backlogBeads, nil
}

// GetSprintContext gathers comprehensive context for sprint planning.
func (s *Store) GetSprintContext(ctx context.Context, dag *graph.DAG, project string, daysBack int) (*SprintContext, error) {
	// Get backlog tasks
	backlogBeads, err := s.GetBacklogBeads(ctx, dag, project)
	if err != nil {
		return nil, fmt.Errorf("failed to get backlog beads: %w", err)
	}

	// Get in-progress tasks
	inProgressBeads, err := s.getInProgressBeads(ctx, dag, project)
	if err != nil {
		return nil, fmt.Errorf("failed to get in-progress beads: %w", err)
	}

	// Get recent completions
	recentCompletions, err := s.getRecentCompletions(ctx, dag, project, daysBack)
	if err != nil {
		return nil, fmt.Errorf("failed to get recent completions: %w", err)
	}

	// Build dependency graph
	var allTasks []graph.Task
	for _, bb := range backlogBeads {
		allTasks = append(allTasks, *bb.Task)
	}
	for _, bb := range inProgressBeads {
		allTasks = append(allTasks, *bb.Task)
	}
	for _, bb := range recentCompletions {
		allTasks = append(allTasks, *bb.Task)
	}

	depGraph := graph.BuildDepGraph(allTasks)

	// Get current sprint boundary
	currentSprint, _ := s.GetCurrentSprintBoundary()

	// Calculate counts
	readyCount, blockedCount := s.calculateReadinessStats(backlogBeads, depGraph)

	return &SprintContext{
		BacklogBeads:      backlogBeads,
		InProgressBeads:   inProgressBeads,
		RecentCompletions: recentCompletions,
		DependencyGraph:   depGraph,
		SprintBoundary:    currentSprint,
		TotalBeadCount:    len(backlogBeads),
		ReadyBeadCount:    readyCount,
		BlockedBeadCount:  blockedCount,
	}, nil
}

// Helper functions

func (s *Store) enrichBacklogBead(project string, backlogBead *BacklogBead) {
	// Get stage info - best effort
	stageInfo, err := s.GetBeadStage(project, backlogBead.ID)
	if err == nil {
		backlogBead.StageInfo = stageInfo
	}

	// Get dispatch statistics - best effort
	dispatches, err := s.GetDispatchesByBead(backlogBead.ID)
	if err != nil {
		backlogBead.DispatchCount = 0
		backlogBead.FailureCount = 0
		return
	}

	backlogBead.DispatchCount = len(dispatches)

	var lastDispatch *time.Time
	failureCount := 0

	for _, dispatch := range dispatches {
		if lastDispatch == nil || dispatch.DispatchedAt.After(*lastDispatch) {
			lastDispatch = &dispatch.DispatchedAt
		}
		if dispatch.Status == "failed" {
			failureCount++
		}
	}

	backlogBead.LastDispatchAt = lastDispatch
	backlogBead.FailureCount = failureCount
}

func (s *Store) getInProgressBeads(ctx context.Context, dag *graph.DAG, project string) ([]*BacklogBead, error) {
	allTasks, err := dag.ListTasks(ctx, project, "open")
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	var inProgressBeads []*BacklogBead

	for i := range allTasks {
		task := &allTasks[i]

		isInProgress := false
		for _, label := range task.Labels {
			if label == "stage:in_progress" || label == "stage:review" ||
				label == "stage:testing" || label == "stage:development" {
				isInProgress = true
				break
			}
		}

		if isInProgress {
			backlogBead := &BacklogBead{Task: task}
			s.enrichBacklogBead(project, backlogBead)
			inProgressBeads = append(inProgressBeads, backlogBead)
		}
	}

	return inProgressBeads, nil
}

func (s *Store) getRecentCompletions(ctx context.Context, dag *graph.DAG, project string, daysBack int) ([]*BacklogBead, error) {
	allTasks, err := dag.ListTasks(ctx, project, "closed")
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	cutoff := time.Now().AddDate(0, 0, -daysBack)
	var recentCompletions []*BacklogBead

	for i := range allTasks {
		task := &allTasks[i]
		if task.UpdatedAt.After(cutoff) {
			backlogBead := &BacklogBead{Task: task}
			s.enrichBacklogBead(project, backlogBead)
			recentCompletions = append(recentCompletions, backlogBead)
		}
	}

	return recentCompletions, nil
}

func (s *Store) calculateReadinessStats(backlogBeads []*BacklogBead, depGraph *graph.DepGraph) (readyCount, blockedCount int) {
	for _, bead := range backlogBeads {
		if s.isBeadBlocked(bead, depGraph) {
			blockedCount++
			bead.IsBlocked = true
			bead.BlockingReasons = s.getBlockingReasons(bead, depGraph)
		} else {
			readyCount++
		}
	}

	return readyCount, blockedCount
}

func (s *Store) isBeadBlocked(bead *BacklogBead, depGraph *graph.DepGraph) bool {
	if depGraph == nil {
		return len(bead.DependsOn) > 0
	}

	for _, depID := range bead.DependsOn {
		if dep, exists := depGraph.Nodes()[depID]; exists {
			if dep.Status != "closed" {
				return true
			}
		} else {
			return true
		}
	}
	return false
}

func (s *Store) getBlockingReasons(bead *BacklogBead, depGraph *graph.DepGraph) []string {
	if depGraph == nil {
		return bead.DependsOn
	}

	var blockingReasons []string
	for _, depID := range bead.DependsOn {
		if dep, exists := depGraph.Nodes()[depID]; exists {
			if dep.Status != "closed" {
				blockingReasons = append(blockingReasons, depID)
			}
		} else {
			blockingReasons = append(blockingReasons, depID+" (missing)")
		}
	}
	return blockingReasons
}

// GetCurrentSprintBoundary returns the current sprint boundary if one exists.
func (s *Store) GetCurrentSprintBoundary() (*SprintBoundary, error) {
	query := `SELECT id, sprint_number, sprint_start, sprint_end, created_at
			 FROM sprint_boundaries
			 WHERE sprint_start <= datetime('now') AND sprint_end >= datetime('now')
			 ORDER BY sprint_start DESC LIMIT 1`

	var sb SprintBoundary
	err := s.db.QueryRow(query).Scan(
		&sb.ID, &sb.SprintNumber, &sb.SprintStart, &sb.SprintEnd, &sb.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get current sprint boundary: %w", err)
	}

	return &sb, nil
}

// RecordSprintPlanning stores a sprint planning trigger record for auditing and deduplication.
func (s *Store) RecordSprintPlanning(project, trigger string, backlogSize, threshold int, result, details string) error {
	if err := s.ensureSprintPlanningTable(); err != nil {
		return err
	}

	_, err := s.db.Exec(
		`INSERT INTO sprint_planning_runs
			(project, trigger_type, backlog_size, backlog_threshold, result, details, triggered_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		project,
		trigger,
		backlogSize,
		threshold,
		result,
		details,
		time.Now().UTC().Format(time.DateTime),
	)
	if err != nil {
		return fmt.Errorf("record sprint planning: %w", err)
	}
	return nil
}

// GetLastSprintPlanning retrieves the most recent sprint planning record for a project.
func (s *Store) GetLastSprintPlanning(project string) (*SprintPlanningRecord, error) {
	if err := s.ensureSprintPlanningTable(); err != nil {
		return nil, err
	}

	var (
		record      SprintPlanningRecord
		triggeredAt string
	)
	err := s.db.QueryRow(
		`SELECT id, project, trigger_type, backlog_size, backlog_threshold, result, details, triggered_at
		 FROM sprint_planning_runs
		 WHERE project = ?
		 ORDER BY triggered_at DESC
		 LIMIT 1`,
		project,
	).Scan(
		&record.ID,
		&record.Project,
		&record.Trigger,
		&record.Backlog,
		&record.Threshold,
		&record.Result,
		&record.Details,
		&triggeredAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get last sprint planning: %w", err)
	}

	parsed, err := time.ParseInLocation(time.DateTime, triggeredAt, time.UTC)
	if err != nil {
		parsed, err = parseSQLiteTime(triggeredAt)
		if err != nil {
			return nil, fmt.Errorf("parse last sprint planning timestamp: %w", err)
		}
	}
	record.TriggeredAt = parsed

	return &record, nil
}

func (s *Store) ensureSprintPlanningTable() error {
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS sprint_planning_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project TEXT NOT NULL,
			trigger_type TEXT NOT NULL,
			backlog_size INTEGER NOT NULL DEFAULT 0,
			backlog_threshold INTEGER NOT NULL DEFAULT 0,
			result TEXT NOT NULL DEFAULT '',
			details TEXT NOT NULL DEFAULT '',
			triggered_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`); err != nil {
		return fmt.Errorf("ensure sprint_planning_runs table: %w", err)
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_sprint_planning_project_time ON sprint_planning_runs(project, triggered_at)`); err != nil {
		return fmt.Errorf("ensure sprint_planning_runs index: %w", err)
	}
	return nil
}

func parseSQLiteTime(value string) (time.Time, error) {
	layouts := []string{
		time.DateTime,
		time.RFC3339Nano,
		time.RFC3339,
	}
	var lastErr error
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed.UTC(), nil
		}
		lastErr = err
	}
	return time.Time{}, lastErr
}
