package store

import (
	"database/sql"
	"fmt"
	"time"
)

// CalcifiedScript represents a deterministic script that replaces an LLM dispatch.
type CalcifiedScript struct {
	ID               int64
	MorselType       string
	Project          string
	ScriptPath       string
	Status           string // "shadow", "active", "quarantined"
	SHA256           string
	ShadowMatches    int
	ShadowFailures   int
	CreatedAt        time.Time
	PromotedAt       *time.Time
	QuarantinedAt    *time.Time
	QuarantineReason string
}

// migrateCalcifiedScripts ensures the calcified_scripts table exists.
func migrateCalcifiedScripts(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS calcified_scripts (
			id                INTEGER PRIMARY KEY AUTOINCREMENT,
			morsel_type       TEXT NOT NULL,
			project           TEXT NOT NULL DEFAULT '',
			script_path       TEXT NOT NULL,
			status            TEXT NOT NULL DEFAULT 'shadow',
			sha256            TEXT NOT NULL,
			shadow_matches    INTEGER NOT NULL DEFAULT 0,
			shadow_failures   INTEGER NOT NULL DEFAULT 0,
			created_at        DATETIME NOT NULL DEFAULT (datetime('now')),
			promoted_at       DATETIME,
			quarantined_at    DATETIME,
			quarantine_reason TEXT NOT NULL DEFAULT ''
		)`); err != nil {
		return fmt.Errorf("create calcified_scripts table: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_calcified_scripts_type_status ON calcified_scripts(morsel_type, status)`); err != nil {
		return fmt.Errorf("create calcified_scripts type+status index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_calcified_scripts_project ON calcified_scripts(project)`); err != nil {
		return fmt.Errorf("create calcified_scripts project index: %w", err)
	}
	return nil
}

// RecordCalcifiedScript inserts a new calcified script record.
func (s *Store) RecordCalcifiedScript(morselType, project, scriptPath, sha256 string) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO calcified_scripts (morsel_type, project, script_path, sha256) VALUES (?, ?, ?, ?)`,
		morselType, project, scriptPath, sha256,
	)
	if err != nil {
		return 0, fmt.Errorf("record calcified script: %w", err)
	}
	return res.LastInsertId()
}

// GetActiveScriptForType returns the active (promoted) script for a morsel type, or nil.
func (s *Store) GetActiveScriptForType(morselType string) (*CalcifiedScript, error) {
	return s.getScriptByStatus(morselType, "active")
}

// GetShadowScriptForType returns the shadow (validation) script for a morsel type, or nil.
func (s *Store) GetShadowScriptForType(morselType string) (*CalcifiedScript, error) {
	return s.getScriptByStatus(morselType, "shadow")
}

func (s *Store) getScriptByStatus(morselType, status string) (*CalcifiedScript, error) {
	row := s.db.QueryRow(
		`SELECT id, morsel_type, project, script_path, status, sha256,
		        shadow_matches, shadow_failures, created_at, promoted_at,
		        quarantined_at, quarantine_reason
		 FROM calcified_scripts
		 WHERE morsel_type = ? AND status = ?
		 ORDER BY created_at DESC LIMIT 1`,
		morselType, status,
	)
	cs := &CalcifiedScript{}
	err := row.Scan(
		&cs.ID, &cs.MorselType, &cs.Project, &cs.ScriptPath, &cs.Status, &cs.SHA256,
		&cs.ShadowMatches, &cs.ShadowFailures, &cs.CreatedAt, &cs.PromotedAt,
		&cs.QuarantinedAt, &cs.QuarantineReason,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get %s script for type %s: %w", status, morselType, err)
	}
	return cs, nil
}

// UpdateScriptShadowCounts increments shadow match or failure counters.
func (s *Store) UpdateScriptShadowCounts(id int64, matchDelta, failureDelta int) error {
	_, err := s.db.Exec(
		`UPDATE calcified_scripts
		 SET shadow_matches = shadow_matches + ?,
		     shadow_failures = shadow_failures + ?
		 WHERE id = ?`,
		matchDelta, failureDelta, id,
	)
	if err != nil {
		return fmt.Errorf("update shadow counts for script %d: %w", id, err)
	}
	return nil
}

// PromoteScript transitions a script from shadow to active.
func (s *Store) PromoteScript(id int64) error {
	_, err := s.db.Exec(
		`UPDATE calcified_scripts SET status = 'active', promoted_at = datetime('now') WHERE id = ?`,
		id,
	)
	if err != nil {
		return fmt.Errorf("promote script %d: %w", id, err)
	}
	return nil
}

// QuarantineScript marks a broken script as quarantined with a reason.
func (s *Store) QuarantineScript(id int64, reason string) error {
	_, err := s.db.Exec(
		`UPDATE calcified_scripts
		 SET status = 'quarantined', quarantined_at = datetime('now'), quarantine_reason = ?
		 WHERE id = ?`,
		reason, id,
	)
	if err != nil {
		return fmt.Errorf("quarantine script %d: %w", id, err)
	}
	return nil
}

// GetConsecutiveSuccessfulDispatches counts how many of the most recent dispatches
// for a given morsel_id pattern (used as morsel type) completed successfully
// in an unbroken streak. Stops counting at the first non-completed dispatch.
func (s *Store) GetConsecutiveSuccessfulDispatches(morselType, project string) (int, error) {
	rows, err := s.db.Query(
		`SELECT status FROM dispatches
		 WHERE morsel_id LIKE ? AND project = ? AND status IN ('completed', 'failed', 'escalated')
		 ORDER BY id DESC
		 LIMIT 50`,
		morselType+"%", project,
	)
	if err != nil {
		return 0, fmt.Errorf("query consecutive successes: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			return count, err
		}
		if status != "completed" {
			break
		}
		count++
	}
	return count, rows.Err()
}

// GetCalcifiedScriptByID returns a script by its database ID.
func (s *Store) GetCalcifiedScriptByID(id int64) (*CalcifiedScript, error) {
	row := s.db.QueryRow(
		`SELECT id, morsel_type, project, script_path, status, sha256,
		        shadow_matches, shadow_failures, created_at, promoted_at,
		        quarantined_at, quarantine_reason
		 FROM calcified_scripts WHERE id = ?`, id,
	)
	cs := &CalcifiedScript{}
	err := row.Scan(
		&cs.ID, &cs.MorselType, &cs.Project, &cs.ScriptPath, &cs.Status, &cs.SHA256,
		&cs.ShadowMatches, &cs.ShadowFailures, &cs.CreatedAt, &cs.PromotedAt,
		&cs.QuarantinedAt, &cs.QuarantineReason,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get calcified script %d: %w", id, err)
	}
	return cs, nil
}
