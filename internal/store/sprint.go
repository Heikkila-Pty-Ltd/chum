package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/antigravity-dev/chum/internal/graph"
)

// BacklogMorsel represents a task in the backlog with metadata for sprint planning.
type BacklogMorsel struct {
	*graph.Task
	StageInfo       *MorselStage `json:"stage_info,omitempty"`
	LastDispatchAt  *time.Time   `json:"last_dispatch_at,omitempty"`
	DispatchCount   int          `json:"dispatch_count"`
	FailureCount    int          `json:"failure_count"`
	IsBlocked       bool         `json:"is_blocked"`
	BlockingReasons []string     `json:"blocking_reasons,omitempty"`
}

// SprintContext provides comprehensive context for sprint planning decisions.
type SprintContext struct {
	BacklogMorsels     []*BacklogMorsel `json:"backlog_morsels"`
	InProgressMorsels  []*BacklogMorsel `json:"in_progress_morsels"`
	RecentCompletions  []*BacklogMorsel `json:"recent_completions"`
	DependencyGraph    *graph.DepGraph  `json:"dependency_graph"`
	SprintBoundary     *SprintBoundary  `json:"current_sprint,omitempty"`
	TotalMorselCount   int              `json:"total_morsel_count"`
	ReadyMorselCount   int              `json:"ready_morsel_count"`
	BlockedMorselCount int              `json:"blocked_morsel_count"`
}

// DependencyNode represents a node in the dependency graph with additional metadata.
type DependencyNode struct {
	MorselID        string   `json:"morsel_id"`
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

// GetBacklogMorsels retrieves all tasks that are in the backlog (no stage or stage:backlog).
func (s *Store) GetBacklogMorsels(ctx context.Context, dag *graph.DAG, project string) ([]*BacklogMorsel, error) {
	allTasks, err := dag.ListTasks(ctx, project, "open")
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	var backlogMorsels []*BacklogMorsel

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
			backlogMorsel := &BacklogMorsel{
				Task: task,
			}

			// Enrich with store data - don't skip if enrichment fails
			s.enrichBacklogMorsel(project, backlogMorsel) // ignore errors

			backlogMorsels = append(backlogMorsels, backlogMorsel)
		}
	}

	return backlogMorsels, nil
}

// GetSprintContext gathers comprehensive context for sprint planning.
func (s *Store) GetSprintContext(ctx context.Context, dag *graph.DAG, project string, daysBack int) (*SprintContext, error) {
	// Get backlog tasks
	backlogMorsels, err := s.GetBacklogMorsels(ctx, dag, project)
	if err != nil {
		return nil, fmt.Errorf("failed to get backlog morsels: %w", err)
	}

	// Get in-progress tasks
	inProgressMorsels, err := s.getInProgressMorsels(ctx, dag, project)
	if err != nil {
		return nil, fmt.Errorf("failed to get in-progress morsels: %w", err)
	}

	// Get recent completions
	recentCompletions, err := s.getRecentCompletions(ctx, dag, project, daysBack)
	if err != nil {
		return nil, fmt.Errorf("failed to get recent completions: %w", err)
	}

	// Build dependency graph
	allTasks := make([]graph.Task, 0, len(backlogMorsels)+len(inProgressMorsels)+len(recentCompletions))
	for _, bb := range backlogMorsels {
		allTasks = append(allTasks, *bb.Task)
	}
	for _, bb := range inProgressMorsels {
		allTasks = append(allTasks, *bb.Task)
	}
	for _, bb := range recentCompletions {
		allTasks = append(allTasks, *bb.Task)
	}

	depGraph := graph.BuildDepGraph(allTasks)

	// Get current sprint boundary
	currentSprint, sprintErr := s.GetCurrentSprintBoundary()
	if sprintErr != nil {
		currentSprint = nil
	}

	// Calculate counts
	readyCount, blockedCount := s.calculateReadinessStats(backlogMorsels, depGraph)

	return &SprintContext{
		BacklogMorsels:     backlogMorsels,
		InProgressMorsels:  inProgressMorsels,
		RecentCompletions:  recentCompletions,
		DependencyGraph:    depGraph,
		SprintBoundary:     currentSprint,
		TotalMorselCount:   len(backlogMorsels),
		ReadyMorselCount:   readyCount,
		BlockedMorselCount: blockedCount,
	}, nil
}

// Helper functions

func (s *Store) enrichBacklogMorsel(project string, backlogMorsel *BacklogMorsel) {
	// Get stage info - best effort
	stageInfo, err := s.GetMorselStage(project, backlogMorsel.ID)
	if err == nil {
		backlogMorsel.StageInfo = stageInfo
	}

	// Get dispatch statistics - best effort
	dispatches, err := s.GetDispatchesByMorsel(backlogMorsel.ID)
	if err != nil {
		backlogMorsel.DispatchCount = 0
		backlogMorsel.FailureCount = 0
		return
	}

	backlogMorsel.DispatchCount = len(dispatches)

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

	backlogMorsel.LastDispatchAt = lastDispatch
	backlogMorsel.FailureCount = failureCount
}

func (s *Store) getInProgressMorsels(ctx context.Context, dag *graph.DAG, project string) ([]*BacklogMorsel, error) {
	allTasks, err := dag.ListTasks(ctx, project, "open")
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	var inProgressMorsels []*BacklogMorsel

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
			backlogMorsel := &BacklogMorsel{Task: task}
			s.enrichBacklogMorsel(project, backlogMorsel)
			inProgressMorsels = append(inProgressMorsels, backlogMorsel)
		}
	}

	return inProgressMorsels, nil
}

func (s *Store) getRecentCompletions(ctx context.Context, dag *graph.DAG, project string, daysBack int) ([]*BacklogMorsel, error) {
	allTasks, err := dag.ListTasks(ctx, project, "closed")
	if err != nil {
		return nil, fmt.Errorf("failed to list tasks: %w", err)
	}

	cutoff := time.Now().AddDate(0, 0, -daysBack)
	var recentCompletions []*BacklogMorsel

	for i := range allTasks {
		task := &allTasks[i]
		if task.UpdatedAt.After(cutoff) {
			backlogMorsel := &BacklogMorsel{Task: task}
			s.enrichBacklogMorsel(project, backlogMorsel)
			recentCompletions = append(recentCompletions, backlogMorsel)
		}
	}

	return recentCompletions, nil
}

func (s *Store) calculateReadinessStats(backlogMorsels []*BacklogMorsel, depGraph *graph.DepGraph) (readyCount, blockedCount int) {
	for _, morsel := range backlogMorsels {
		if s.isMorselBlocked(morsel, depGraph) {
			blockedCount++
			morsel.IsBlocked = true
			morsel.BlockingReasons = s.getBlockingReasons(morsel, depGraph)
		} else {
			readyCount++
		}
	}

	return readyCount, blockedCount
}

func (s *Store) isMorselBlocked(morsel *BacklogMorsel, depGraph *graph.DepGraph) bool {
	if depGraph == nil {
		return len(morsel.DependsOn) > 0
	}

	for _, depID := range morsel.DependsOn {
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

func (s *Store) getBlockingReasons(morsel *BacklogMorsel, depGraph *graph.DepGraph) []string {
	if depGraph == nil {
		return morsel.DependsOn
	}

	var blockingReasons []string
	for _, depID := range morsel.DependsOn {
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

	if errors.Is(err, sql.ErrNoRows) {
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
	if errors.Is(err, sql.ErrNoRows) {
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
