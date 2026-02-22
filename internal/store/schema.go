package store

import (
	"database/sql"
	"fmt"
)

const stingrayRunsTableSchema = `
CREATE TABLE IF NOT EXISTS stingray_runs (
	id INTEGER PRIMARY KEY,
	project TEXT NOT NULL,
	run_at DATETIME NOT NULL DEFAULT (datetime('now')),
	findings_total INTEGER NOT NULL DEFAULT 0,
	findings_new INTEGER NOT NULL DEFAULT 0,
	findings_resolved INTEGER NOT NULL DEFAULT 0,
	metrics_json TEXT NOT NULL DEFAULT '{}'
)`

const stingrayRunsIndexes = `
CREATE INDEX IF NOT EXISTS idx_stingray_runs_project ON stingray_runs(project);
CREATE INDEX IF NOT EXISTS idx_stingray_runs_run_at ON stingray_runs(run_at);
CREATE INDEX IF NOT EXISTS idx_stingray_runs_project_id ON stingray_runs(project, id);
`

const stingrayFindingsTableSchema = `
CREATE TABLE IF NOT EXISTS stingray_findings (
	id INTEGER PRIMARY KEY,
	run_id INTEGER NOT NULL REFERENCES stingray_runs(id),
	project TEXT NOT NULL,
	category TEXT NOT NULL,
	severity TEXT NOT NULL,
	title TEXT NOT NULL,
	detail TEXT NOT NULL,
	file_path TEXT NOT NULL DEFAULT '',
	evidence TEXT NOT NULL DEFAULT '',
	morsel_id TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'open',
	first_seen DATETIME NOT NULL DEFAULT (datetime('now')),
	last_seen DATETIME NOT NULL DEFAULT (datetime('now'))
)`

const stingrayFindingsIndexes = `
CREATE INDEX IF NOT EXISTS idx_stingray_findings_run ON stingray_findings(run_id);
CREATE INDEX IF NOT EXISTS idx_stingray_findings_project ON stingray_findings(project);
CREATE INDEX IF NOT EXISTS idx_stingray_findings_project_status_run ON stingray_findings(project, status, run_id);
CREATE INDEX IF NOT EXISTS idx_stingray_findings_status ON stingray_findings(status);
CREATE INDEX IF NOT EXISTS idx_stingray_findings_category ON stingray_findings(category);
CREATE INDEX IF NOT EXISTS idx_stingray_findings_project_status_title_file_path
	ON stingray_findings(project, status, title, file_path);
CREATE INDEX IF NOT EXISTS idx_stingray_findings_project_last_seen
	ON stingray_findings(project, last_seen, id);`

// migrateStingrayTables ensures stingray tables and indexes exist.
// It remains as part of the migration path for backward compatibility.
func migrateStingrayTables(db *sql.DB) error {
	return ensureStingraySchema(db)
}

// ensureStingraySchema ensures all Stingray persistence objects are present.
func ensureStingraySchema(db *sql.DB) error {
	if _, err := db.Exec(stingrayRunsTableSchema); err != nil {
		return fmt.Errorf("create stingray_runs table: %w", err)
	}
	if _, err := db.Exec(stingrayRunsIndexes); err != nil {
		return fmt.Errorf("create stingray_runs indexes: %w", err)
	}

	if _, err := db.Exec(stingrayFindingsTableSchema); err != nil {
		return fmt.Errorf("create stingray_findings table: %w", err)
	}
	if _, err := db.Exec(stingrayFindingsIndexes); err != nil {
		return fmt.Errorf("create stingray_findings indexes: %w", err)
	}

	return nil
}
