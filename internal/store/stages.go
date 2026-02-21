package store

import (
	"database/sql"
	"fmt"
	"time"
)


// StageHistoryEntry tracks per-stage lifecycle for a bead workflow.
type StageHistoryEntry struct {
	Stage       string     `json:"stage"`
	Status      string     `json:"status"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	DispatchID  int64      `json:"dispatch_id,omitempty"`
}

// BeadStage is the persisted workflow stage state for a bead in a project.
type BeadStage struct {
	ID           int64
	Project      string
	BeadID       string
	Workflow     string
	CurrentStage string
	StageIndex   int
	TotalStages  int
	StageHistory []StageHistoryEntry
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
// GetBeadStage retrieves the stage state for a specific bead in a project.
func (s *Store) GetBeadStage(project, beadID string) (*BeadStage, error) {
	var stage BeadStage
	var historyJSON string

	err := s.db.QueryRow(`
		SELECT id, project, bead_id, workflow, current_stage, stage_index, 
		       total_stages, stage_history, created_at, updated_at 
		FROM bead_stages 
		WHERE project = ? AND bead_id = ?`,
		project, beadID,
	).Scan(
		&stage.ID, &stage.Project, &stage.BeadID, &stage.Workflow,
		&stage.CurrentStage, &stage.StageIndex, &stage.TotalStages,
		&historyJSON, &stage.CreatedAt, &stage.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("store: bead stage not found for project=%s, bead=%s", project, beadID)
		}
		return nil, fmt.Errorf("store: get bead stage: %w", err)
	}

	// Parse stage history JSON
	if historyJSON != "" && historyJSON != "[]" {
		// For simplicity, we'll store history as JSON string - proper JSON unmarshaling would be added in production
		// This is a placeholder for the stage history parsing
	}

	return &stage, nil
}

// UpsertBeadStage creates or updates a bead stage using composite project+bead_id key.
func (s *Store) UpsertBeadStage(stage *BeadStage) error {
	historyJSON := "[]" // Placeholder for stage history JSON serialization

	_, err := s.db.Exec(`
		INSERT INTO bead_stages (project, bead_id, workflow, current_stage, stage_index, 
		                        total_stages, stage_history, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT (project, bead_id) DO UPDATE SET
			workflow = excluded.workflow,
			current_stage = excluded.current_stage,
			stage_index = excluded.stage_index,
			total_stages = excluded.total_stages,
			stage_history = excluded.stage_history,
			updated_at = datetime('now')`,
		stage.Project, stage.BeadID, stage.Workflow, stage.CurrentStage,
		stage.StageIndex, stage.TotalStages, historyJSON,
	)

	if err != nil {
		return fmt.Errorf("store: upsert bead stage: %w", err)
	}

	return nil
}

// UpdateBeadStageProgress advances a bead to the next stage in its workflow.
func (s *Store) UpdateBeadStageProgress(project, beadID, newStage string, stageIndex, totalStages int, dispatchID int64) error {
	_, err := s.db.Exec(`
		UPDATE bead_stages 
		SET current_stage = ?, stage_index = ?, total_stages = ?, updated_at = datetime('now')
		WHERE project = ? AND bead_id = ?`,
		newStage, stageIndex, totalStages, project, beadID,
	)

	if err != nil {
		return fmt.Errorf("store: update bead stage progress: %w", err)
	}

	return nil
}

// ListBeadStagesForProject retrieves all bead stages for a specific project.
func (s *Store) ListBeadStagesForProject(project string) ([]*BeadStage, error) {
	rows, err := s.db.Query(`
		SELECT id, project, bead_id, workflow, current_stage, stage_index, 
		       total_stages, stage_history, created_at, updated_at 
		FROM bead_stages 
		WHERE project = ?
		ORDER BY updated_at DESC`,
		project,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list bead stages for project: %w", err)
	}
	defer rows.Close()

	var stages []*BeadStage
	for rows.Next() {
		var stage BeadStage
		var historyJSON string

		err := rows.Scan(
			&stage.ID, &stage.Project, &stage.BeadID, &stage.Workflow,
			&stage.CurrentStage, &stage.StageIndex, &stage.TotalStages,
			&historyJSON, &stage.CreatedAt, &stage.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("store: scan bead stage: %w", err)
		}

		stages = append(stages, &stage)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list bead stages rows: %w", err)
	}

	return stages, nil
}

// DeleteBeadStage removes a bead stage record for a specific project and bead.
func (s *Store) DeleteBeadStage(project, beadID string) error {
	result, err := s.db.Exec(`
		DELETE FROM bead_stages 
		WHERE project = ? AND bead_id = ?`,
		project, beadID,
	)
	if err != nil {
		return fmt.Errorf("store: delete bead stage: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("store: bead stage not found for project=%s, bead=%s", project, beadID)
	}

	return nil
}

// GetBeadStagesByBeadIDOnly is a legacy method that checks for cross-project ambiguity.
// Returns an error if multiple projects have the same bead_id to prevent accidental overwrites.
func (s *Store) GetBeadStagesByBeadIDOnly(beadID string) ([]*BeadStage, error) {
	rows, err := s.db.Query(`
		SELECT id, project, bead_id, workflow, current_stage, stage_index, 
		       total_stages, stage_history, created_at, updated_at 
		FROM bead_stages 
		WHERE bead_id = ?`,
		beadID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get bead stages by bead_id: %w", err)
	}
	defer rows.Close()

	var stages []*BeadStage
	projectsSeen := make(map[string]bool)

	for rows.Next() {
		var stage BeadStage
		var historyJSON string

		err := rows.Scan(
			&stage.ID, &stage.Project, &stage.BeadID, &stage.Workflow,
			&stage.CurrentStage, &stage.StageIndex, &stage.TotalStages,
			&historyJSON, &stage.CreatedAt, &stage.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("store: scan bead stage: %w", err)
		}

		projectsSeen[stage.Project] = true
		stages = append(stages, &stage)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("store: get bead stages by bead_id rows: %w", err)
	}

	// Check for cross-project ambiguity
	if len(projectsSeen) > 1 {
		projects := make([]string, 0, len(projectsSeen))
		for project := range projectsSeen {
			projects = append(projects, project)
		}
		return nil, fmt.Errorf("store: ambiguous bead_id=%s found in multiple projects: %v. Use project-specific lookup to avoid collisions", beadID, projects)
	}

	return stages, nil
}
