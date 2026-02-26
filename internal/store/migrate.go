package store

import (
	"database/sql"
	"fmt"
)

// addColumnIfNotExists checks whether a column exists on a table and adds it
// using the supplied DDL fragment when it is missing.
func addColumnIfNotExists(db *sql.DB, table, column, ddl string) error {
	var count int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('` + table + `') WHERE name = '` + column + `'`,
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("check %s.%s column: %w", table, column, err)
	}
	if count == 0 {
		if _, err := db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + ddl); err != nil {
			return fmt.Errorf("add %s.%s column: %w", table, column, err)
		}
	}
	return nil
}

// migrate applies incremental schema migrations for existing databases.
func migrate(db *sql.DB) error {
	// Backfill columns added after the initial schema.
	dispatchColumns := []struct {
		column string
		ddl    string
	}{
		{"session_name", "session_name TEXT NOT NULL DEFAULT ''"},
		{"input_tokens", "input_tokens INTEGER NOT NULL DEFAULT 0"},
		{"output_tokens", "output_tokens INTEGER NOT NULL DEFAULT 0"},
		{"cost_usd", "cost_usd REAL NOT NULL DEFAULT 0"},
		{"failure_category", "failure_category TEXT NOT NULL DEFAULT ''"},
		{"failure_summary", "failure_summary TEXT NOT NULL DEFAULT ''"},
		{"log_path", "log_path TEXT NOT NULL DEFAULT ''"},
		{"branch", "branch TEXT NOT NULL DEFAULT ''"},
		{"backend", "backend TEXT NOT NULL DEFAULT ''"},
		{"stage", "stage TEXT NOT NULL DEFAULT 'dispatched'"},
		{"labels", "labels TEXT NOT NULL DEFAULT ''"},
		{"pr_url", "pr_url TEXT NOT NULL DEFAULT ''"},
		{"pr_number", "pr_number INTEGER NOT NULL DEFAULT 0"},
		{"next_retry_at", "next_retry_at DATETIME"},
	}
	for _, col := range dispatchColumns {
		if err := addColumnIfNotExists(db, "dispatches", col.column, col.ddl); err != nil {
			return err
		}
	}

	providerUsageColumns := []struct {
		column string
		ddl    string
	}{
		{"input_tokens", "input_tokens INTEGER NOT NULL DEFAULT 0"},
		{"output_tokens", "output_tokens INTEGER NOT NULL DEFAULT 0"},
	}
	for _, col := range providerUsageColumns {
		if err := addColumnIfNotExists(db, "provider_usage", col.column, col.ddl); err != nil {
			return err
		}
	}

	if err := addColumnIfNotExists(db, "genomes", "provider_genes", "provider_genes TEXT NOT NULL DEFAULT '[]'"); err != nil {
		return err
	}
	if err := addColumnIfNotExists(db, "genomes", "total_cost_usd", "total_cost_usd REAL NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := addColumnIfNotExists(db, "genomes", "hibernating", "hibernating BOOLEAN NOT NULL DEFAULT 0"); err != nil {
		return err
	}

	healthEventColumns := []struct {
		column string
		ddl    string
	}{
		{"dispatch_id", "dispatch_id INTEGER NOT NULL DEFAULT 0"},
		{"morsel_id", "morsel_id TEXT NOT NULL DEFAULT ''"},
	}
	for _, col := range healthEventColumns {
		if err := addColumnIfNotExists(db, "health_events", col.column, col.ddl); err != nil {
			return err
		}
	}

	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_health_events_dispatch ON health_events(dispatch_id)`); err != nil {
		return fmt.Errorf("create health_events dispatch index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_health_events_morsel ON health_events(morsel_id)`); err != nil {
		return fmt.Errorf("create health_events morsel index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_health_events_created_at ON health_events(created_at)`); err != nil {
		return fmt.Errorf("create health_events created_at index: %w", err)
	}

	if err := migrateSchedulingTables(db); err != nil {
		return err
	}

	if err := migrateMorselStagesTable(db); err != nil {
		return err
	}

	if err := migrateLessonsTable(db); err != nil {
		return err
	}

	if err := migrateTokenUsageTable(db); err != nil {
		return err
	}

	// Ensure stingray persistence is available for both fresh and upgraded DBs during startup migration.
	if err := migrateStingrayTables(db); err != nil {
		return fmt.Errorf("migrate stingray tables: %w", err)
	}

	if err := migrateProviderEscalations(db); err != nil {
		return err
	}

	if err := migrateCalcifiedScripts(db); err != nil {
		return fmt.Errorf("migrate calcified scripts: %w", err)
	}
	if err := migrateMCTSTables(db); err != nil {
		return fmt.Errorf("migrate mcts tables: %w", err)
	}
	if err := migratePlanningTraceTables(db); err != nil {
		return fmt.Errorf("migrate planning trace tables: %w", err)
	}
	if err := migratePlanningControlTables(db); err != nil {
		return fmt.Errorf("migrate planning control tables: %w", err)
	}

	if err := migrateOrganismLogs(db); err != nil {
		return fmt.Errorf("migrate organism logs: %w", err)
	}

	if err := migrateTraceTables(db); err != nil {
		return fmt.Errorf("migrate trace tables: %w", err)
	}

	return nil
}

// migrateSchedulingTables creates scheduling tables in the migration path
// for databases that predate their inclusion in the DDL schema.
func migrateSchedulingTables(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS claim_leases (
			morsel_id TEXT PRIMARY KEY,
			project TEXT NOT NULL,
			morsels_dir TEXT NOT NULL DEFAULT '',
			agent_id TEXT NOT NULL DEFAULT '',
			dispatch_id INTEGER NOT NULL DEFAULT 0,
			claimed_at DATETIME NOT NULL DEFAULT (datetime('now')),
			heartbeat_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`); err != nil {
		return fmt.Errorf("create claim_leases table: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_claim_leases_project ON claim_leases(project)`); err != nil {
		return fmt.Errorf("create claim_leases project index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_claim_leases_heartbeat ON claim_leases(heartbeat_at)`); err != nil {
		return fmt.Errorf("create claim_leases heartbeat index: %w", err)
	}

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS sprint_boundaries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			sprint_number INTEGER NOT NULL UNIQUE,
			sprint_start DATETIME NOT NULL,
			sprint_end DATETIME NOT NULL,
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`); err != nil {
		return fmt.Errorf("create sprint_boundaries table: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_sprint_boundaries_start ON sprint_boundaries(sprint_start)`); err != nil {
		return fmt.Errorf("create sprint_boundaries start index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_sprint_boundaries_end ON sprint_boundaries(sprint_end)`); err != nil {
		return fmt.Errorf("create sprint_boundaries end index: %w", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS execution_plan_gate (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			active_plan_id TEXT NOT NULL DEFAULT '',
			approved_by TEXT NOT NULL DEFAULT '',
			approved_at DATETIME,
			activated_at DATETIME,
			updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`); err != nil {
		return fmt.Errorf("create execution_plan_gate table: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_execution_plan_gate_active ON execution_plan_gate(active_plan_id)`); err != nil {
		return fmt.Errorf("create execution_plan_gate active index: %w", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS overflow_queue (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			morsel_id TEXT NOT NULL,
			project TEXT NOT NULL,
			role TEXT NOT NULL,
			agent_id TEXT NOT NULL,
			priority INTEGER NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			enqueued_at DATETIME NOT NULL DEFAULT (datetime('now')),
			attempts INTEGER NOT NULL DEFAULT 0
		)`); err != nil {
		return fmt.Errorf("create overflow_queue table: %w", err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_overflow_queue_morsel_role ON overflow_queue(morsel_id, role)`); err != nil {
		return fmt.Errorf("create overflow_queue morsel+role index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_overflow_queue_priority_enqueued_at ON overflow_queue(priority, enqueued_at)`); err != nil {
		return fmt.Errorf("create overflow_queue priority index: %w", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS safety_blocks (
			scope TEXT NOT NULL,
			block_type TEXT NOT NULL,
			blocked_until DATETIME NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			metadata TEXT NOT NULL DEFAULT '{}',
			created_at DATETIME NOT NULL DEFAULT (datetime('now')),
			updated_at DATETIME NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY(scope, block_type)
		)`); err != nil {
		return fmt.Errorf("create safety_blocks table: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_safety_blocks_scope_type ON safety_blocks(scope, block_type)`); err != nil {
		return fmt.Errorf("create safety blocks scope index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_safety_blocks_blocked_until ON safety_blocks(blocked_until)`); err != nil {
		return fmt.Errorf("create safety blocks blocked_until index: %w", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS quality_scores (
			dispatch_id INTEGER PRIMARY KEY NOT NULL REFERENCES dispatches(id),
			provider TEXT NOT NULL DEFAULT '',
			role TEXT NOT NULL DEFAULT '',
			overall REAL NOT NULL DEFAULT 0,
			tests_passed INTEGER,
			morsel_closed INTEGER NOT NULL DEFAULT 0,
			commit_made INTEGER NOT NULL DEFAULT 0,
			files_changed INTEGER NOT NULL DEFAULT 0,
			lines_changed INTEGER NOT NULL DEFAULT 0,
			duration REAL NOT NULL DEFAULT 0,
			recorded_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`); err != nil {
		return fmt.Errorf("create quality_scores table: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_quality_scores_provider_role ON quality_scores(provider, role, recorded_at)`); err != nil {
		return fmt.Errorf("create quality_scores provider+role index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_quality_scores_role ON quality_scores(role, recorded_at)`); err != nil {
		return fmt.Errorf("create quality_scores role index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_quality_scores_provider ON quality_scores(provider, recorded_at)`); err != nil {
		return fmt.Errorf("create quality_scores provider index: %w", err)
	}

	return nil
}

// migrateTraceTables creates execution_traces, trace_events, and crystal_candidates
// for databases that predate their inclusion in the DDL schema.
func migrateTraceTables(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS execution_traces (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL,
			species TEXT NOT NULL DEFAULT '',
			goal_signature TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'running',
			outcome TEXT NOT NULL DEFAULT '',
			attempt_count INTEGER NOT NULL DEFAULT 0,
			success_count INTEGER NOT NULL DEFAULT 0,
			support_count INTEGER NOT NULL DEFAULT 0,
			success_rate REAL NOT NULL DEFAULT 0,
			started_at DATETIME NOT NULL DEFAULT (datetime('now')),
			completed_at DATETIME,
			created_at DATETIME NOT NULL DEFAULT (datetime('now')),
			updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`); err != nil {
		return fmt.Errorf("create execution_traces table: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_execution_traces_task ON execution_traces(task_id)`); err != nil {
		return fmt.Errorf("create execution_traces task index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_execution_traces_species ON execution_traces(species)`); err != nil {
		return fmt.Errorf("create execution_traces species index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_execution_traces_status ON execution_traces(status)`); err != nil {
		return fmt.Errorf("create execution_traces status index: %w", err)
	}

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS trace_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			trace_id INTEGER NOT NULL REFERENCES execution_traces(id),
			stage TEXT NOT NULL,
			step TEXT NOT NULL,
			tool TEXT NOT NULL,
			command TEXT NOT NULL,
			input_summary TEXT NOT NULL DEFAULT '',
			output_summary TEXT NOT NULL DEFAULT '',
			duration_ms INTEGER NOT NULL DEFAULT 0,
			success INTEGER NOT NULL DEFAULT 0,
			error_context TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`); err != nil {
		return fmt.Errorf("create trace_events table: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_trace_events_trace_id ON trace_events(trace_id)`); err != nil {
		return fmt.Errorf("create trace_events trace_id index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_trace_events_stage ON trace_events(stage)`); err != nil {
		return fmt.Errorf("create trace_events stage index: %w", err)
	}

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS crystal_candidates (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			species TEXT NOT NULL DEFAULT '',
			goal_signature TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			template_json TEXT NOT NULL DEFAULT '{}',
			support_count INTEGER NOT NULL DEFAULT 0,
			attempt_count INTEGER NOT NULL DEFAULT 0,
			success_count INTEGER NOT NULL DEFAULT 0,
			success_rate REAL NOT NULL DEFAULT 0,
			preconditions TEXT NOT NULL DEFAULT '[]',
			ordered_steps TEXT NOT NULL DEFAULT '[]',
			verification_checks TEXT NOT NULL DEFAULT '[]',
			required_inputs TEXT NOT NULL DEFAULT '[]',
			last_seen_at DATETIME NOT NULL DEFAULT (datetime('now')),
			created_at DATETIME NOT NULL DEFAULT (datetime('now')),
			updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`); err != nil {
		return fmt.Errorf("create crystal_candidates table: %w", err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_crystal_candidates_species_goal_status ON crystal_candidates(species, goal_signature, status)`); err != nil {
		return fmt.Errorf("create crystal_candidates species+goal+status index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_crystal_candidates_status ON crystal_candidates(status)`); err != nil {
		return fmt.Errorf("create crystal_candidates status index: %w", err)
	}

	return nil
}

// migrateBeadToMorsel renames bead_id → morsel_id columns and the bead_stages
// table. Must run BEFORE schema DDL since the new DDL references morsel_id.
// Safe to call repeatedly — skips tables/columns that are already renamed.
func migrateBeadToMorsel(db *sql.DB) error {
	// Rename bead_stages table → morsel_stages (if it exists)
	var beadStagesExists int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='bead_stages'`).Scan(&beadStagesExists); err != nil {
		return fmt.Errorf("check bead_stages: %w", err)
	}
	if beadStagesExists > 0 {
		// Drop the morsel_stages table if schema DDL created it empty alongside old bead_stages
		if _, err := db.Exec(`DROP TABLE IF EXISTS morsel_stages`); err != nil {
			return fmt.Errorf("drop empty morsel_stages: %w", err)
		}
		if _, err := db.Exec(`ALTER TABLE bead_stages RENAME TO morsel_stages`); err != nil {
			return fmt.Errorf("rename bead_stages: %w", err)
		}
	}

	// Rename bead_id → morsel_id in all affected tables
	renames := []struct {
		table  string
		oldCol string
		newCol string
	}{
		{"dispatches", "bead_id", "morsel_id"},
		{"provider_usage", "bead_id", "morsel_id"},
		{"health_events", "bead_id", "morsel_id"},
		{"morsel_stages", "bead_id", "morsel_id"},
		{"dod_results", "bead_id", "morsel_id"},
		{"overflow_queue", "bead_id", "morsel_id"},
		{"lessons", "bead_id", "morsel_id"},
		{"token_usage", "bead_id", "morsel_id"},
		{"step_metrics", "bead_id", "morsel_id"},
		{"stingray_findings", "bead_id", "morsel_id"},
		{"provider_escalations", "bead_id", "morsel_id"},
		// claim_leases: bead_id → morsel_id (PK)
		{"claim_leases", "bead_id", "morsel_id"},
		// claim_leases: beads_dir → morsels_dir
		{"claim_leases", "beads_dir", "morsels_dir"},
	}

	for _, r := range renames {
		// Check if old column exists
		var hasOld int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('`+r.table+`') WHERE name = ?`, r.oldCol,
		).Scan(&hasOld); err != nil {
			// Table might not exist yet — skip silently
			continue
		}
		if hasOld == 0 {
			continue // Already renamed or table doesn't have this column
		}
		// Check if new column already exists (partial migration recovery)
		var hasNew int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('`+r.table+`') WHERE name = ?`, r.newCol,
		).Scan(&hasNew); err != nil {
			continue
		}
		if hasNew > 0 {
			// Both old and new columns exist — partial migration. Skip rename.
			continue
		}
		if _, err := db.Exec(`ALTER TABLE ` + r.table + ` RENAME COLUMN ` + r.oldCol + ` TO ` + r.newCol); err != nil {
			return fmt.Errorf("rename %s.%s → %s: %w", r.table, r.oldCol, r.newCol, err)
		}
	}

	// Rename indexes that reference bead_id
	for _, idx := range []string{
		"idx_morsel_stages_morsel",
		"idx_bead_stages_morsel",
		"idx_bead_stages_project_morsel",
		"idx_bead_stages_project_stage",
	} {
		_, _ = db.Exec(`DROP INDEX IF EXISTS ` + idx) // best-effort legacy index cleanup
	}

	return nil
}

// migrateMorselStagesTable ensures morsel_stages uses project+morsel keying and indexes.
func migrateMorselStagesTable(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS morsel_stages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			morsel_id TEXT NOT NULL,
			project TEXT NOT NULL,
			workflow TEXT NOT NULL,
			current_stage TEXT NOT NULL,
			stage_index INTEGER NOT NULL DEFAULT 0,
			total_stages INTEGER NOT NULL,
			stage_history TEXT NOT NULL DEFAULT '[]',
			created_at DATETIME NOT NULL DEFAULT (datetime('now')),
			updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)
	`); err != nil {
		return fmt.Errorf("create morsel_stages table: %w", err)
	}

	// Remove legacy morsel-only uniqueness to avoid cross-project collisions.
	if _, err := db.Exec(`DROP INDEX IF EXISTS idx_morsel_stages_morsel`); err != nil {
		return fmt.Errorf("drop legacy morsel_stages morsel-only index: %w", err)
	}

	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_morsel_stages_project_morsel ON morsel_stages(project, morsel_id)`); err != nil {
		return fmt.Errorf("create morsel_stages project_morsel index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_morsel_stages_project_stage ON morsel_stages(project, current_stage)`); err != nil {
		return fmt.Errorf("create morsel_stages project_stage index: %w", err)
	}

	// Add recurring_failures column to paleontology_runs (for systemic DoD failure detection)
	if err := addColumnIfNotExists(db, "paleontology_runs", "recurring_failures", "recurring_failures INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}

	return nil
}
