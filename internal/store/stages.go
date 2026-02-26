package store

import (
	"database/sql"
	"fmt"
	"time"
)

// StageHistoryEntry tracks per-stage lifecycle for a morsel workflow.
type StageHistoryEntry struct {
	Stage       string     `json:"stage"`
	Status      string     `json:"status"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	DispatchID  int64      `json:"dispatch_id,omitempty"`
}

// MorselStage is the persisted workflow stage state for a morsel in a project.
type MorselStage struct {
	ID           int64
	Project      string
	MorselID     string
	Workflow     string
	CurrentStage string
	StageIndex   int
	TotalStages  int
	StageHistory []StageHistoryEntry
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// GetMorselStage retrieves the stage state for a specific morsel in a project.
func (s *Store) GetMorselStage(project, morselID string) (*MorselStage, error) {
	var stage MorselStage
	var historyJSON string

	err := s.db.QueryRow(`
		SELECT id, project, morsel_id, workflow, current_stage, stage_index, 
		       total_stages, stage_history, created_at, updated_at 
		FROM morsel_stages 
		WHERE project = ? AND morsel_id = ?`,
		project, morselID,
	).Scan(
		&stage.ID, &stage.Project, &stage.MorselID, &stage.Workflow,
		&stage.CurrentStage, &stage.StageIndex, &stage.TotalStages,
		&historyJSON, &stage.CreatedAt, &stage.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("store: morsel stage not found for project=%s, morsel=%s", project, morselID)
		}
		return nil, fmt.Errorf("store: get morsel stage: %w", err)
	}

	return &stage, nil
}

// UpsertMorselStage creates or updates a morsel stage using composite project+morsel_id key.
func (s *Store) UpsertMorselStage(stage *MorselStage) error {
	historyJSON := "[]" // Placeholder for stage history JSON serialization

	_, err := s.db.Exec(`
		INSERT INTO morsel_stages (project, morsel_id, workflow, current_stage, stage_index, 
		                        total_stages, stage_history, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT (project, morsel_id) DO UPDATE SET
			workflow = excluded.workflow,
			current_stage = excluded.current_stage,
			stage_index = excluded.stage_index,
			total_stages = excluded.total_stages,
			stage_history = excluded.stage_history,
			updated_at = datetime('now')`,
		stage.Project, stage.MorselID, stage.Workflow, stage.CurrentStage,
		stage.StageIndex, stage.TotalStages, historyJSON,
	)

	if err != nil {
		return fmt.Errorf("store: upsert morsel stage: %w", err)
	}

	return nil
}

// UpdateMorselStageProgress advances a morsel to the next stage in its workflow.
func (s *Store) UpdateMorselStageProgress(project, morselID, newStage string, stageIndex, totalStages int, dispatchID int64) error {
	_, err := s.db.Exec(`
		UPDATE morsel_stages 
		SET current_stage = ?, stage_index = ?, total_stages = ?, updated_at = datetime('now')
		WHERE project = ? AND morsel_id = ?`,
		newStage, stageIndex, totalStages, project, morselID,
	)

	if err != nil {
		return fmt.Errorf("store: update morsel stage progress: %w", err)
	}

	return nil
}

// ListMorselStagesForProject retrieves all morsel stages for a specific project.
func (s *Store) ListMorselStagesForProject(project string) ([]*MorselStage, error) {
	rows, err := s.db.Query(`
		SELECT id, project, morsel_id, workflow, current_stage, stage_index, 
		       total_stages, stage_history, created_at, updated_at 
		FROM morsel_stages 
		WHERE project = ?
		ORDER BY updated_at DESC`,
		project,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list morsel stages for project: %w", err)
	}
	defer rows.Close()

	var stages []*MorselStage
	for rows.Next() {
		var stage MorselStage
		var historyJSON string

		err := rows.Scan(
			&stage.ID, &stage.Project, &stage.MorselID, &stage.Workflow,
			&stage.CurrentStage, &stage.StageIndex, &stage.TotalStages,
			&historyJSON, &stage.CreatedAt, &stage.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("store: scan morsel stage: %w", err)
		}

		stages = append(stages, &stage)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list morsel stages rows: %w", err)
	}

	return stages, nil
}

// DeleteMorselStage removes a morsel stage record for a specific project and morsel.
func (s *Store) DeleteMorselStage(project, morselID string) error {
	result, err := s.db.Exec(`
		DELETE FROM morsel_stages 
		WHERE project = ? AND morsel_id = ?`,
		project, morselID,
	)
	if err != nil {
		return fmt.Errorf("store: delete morsel stage: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("store: morsel stage not found for project=%s, morsel=%s", project, morselID)
	}

	return nil
}

// GetMorselStagesByMorselIDOnly is a legacy method that checks for cross-project ambiguity.
// Returns an error if multiple projects have the same morsel_id to prevent accidental overwrites.
func (s *Store) GetMorselStagesByMorselIDOnly(morselID string) ([]*MorselStage, error) {
	rows, err := s.db.Query(`
		SELECT id, project, morsel_id, workflow, current_stage, stage_index, 
		       total_stages, stage_history, created_at, updated_at 
		FROM morsel_stages 
		WHERE morsel_id = ?`,
		morselID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get morsel stages by morsel_id: %w", err)
	}
	defer rows.Close()

	var stages []*MorselStage
	projectsSeen := make(map[string]bool)

	for rows.Next() {
		var stage MorselStage
		var historyJSON string

		err := rows.Scan(
			&stage.ID, &stage.Project, &stage.MorselID, &stage.Workflow,
			&stage.CurrentStage, &stage.StageIndex, &stage.TotalStages,
			&historyJSON, &stage.CreatedAt, &stage.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("store: scan morsel stage: %w", err)
		}

		projectsSeen[stage.Project] = true
		stages = append(stages, &stage)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("store: get morsel stages by morsel_id rows: %w", err)
	}

	// Check for cross-project ambiguity
	if len(projectsSeen) > 1 {
		projects := make([]string, 0, len(projectsSeen))
		for project := range projectsSeen {
			projects = append(projects, project)
		}
		return nil, fmt.Errorf("store: ambiguous morsel_id=%s found in multiple projects: %v. Use project-specific lookup to avoid collisions", morselID, projects)
	}

	return stages, nil
}
