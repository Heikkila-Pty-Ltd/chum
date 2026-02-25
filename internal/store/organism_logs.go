package store

import "database/sql"

// RecordOrganismLog persists a structured log entry for any non-shark organism
// (turtle, crab, learner, groomer, dispatcher, explosion, etc.).
// This feeds the learner loop and paleontologist with data from all pipeline
// stages, not just shark execution outcomes.
func (s *Store) RecordOrganismLog(organismType, workflowID, taskID, project, status string,
	durationS float64, details string, steps int, errMsg string) error {

	_, err := s.db.Exec(
		`INSERT INTO organism_logs
			(organism_type, workflow_id, task_id, project, status, duration_s, details, steps, error)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		organismType, workflowID, taskID, project, status, durationS, details, steps, errMsg,
	)
	return err
}

// migrateOrganismLogs creates the organism_logs table for existing databases.
func migrateOrganismLogs(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS organism_logs (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			organism_type  TEXT NOT NULL,
			workflow_id    TEXT NOT NULL DEFAULT '',
			task_id        TEXT NOT NULL DEFAULT '',
			project        TEXT NOT NULL DEFAULT '',
			status         TEXT NOT NULL DEFAULT '',
			duration_s     REAL NOT NULL DEFAULT 0,
			details        TEXT NOT NULL DEFAULT '',
			steps          INTEGER NOT NULL DEFAULT 0,
			error          TEXT NOT NULL DEFAULT '',
			created_at     DATETIME NOT NULL DEFAULT (datetime('now'))
		)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_organism_logs_type ON organism_logs(organism_type, created_at)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_organism_logs_project ON organism_logs(project, created_at)`); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_organism_logs_task ON organism_logs(task_id)`); err != nil {
		return err
	}
	return nil
}
