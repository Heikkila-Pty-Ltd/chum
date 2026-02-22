package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Store provides SQLite-backed persistence for CHUM state.
type Store struct {
	db                  *sql.DB
	dispatchPersistHook func(point string) error
}

const schema = `
CREATE TABLE IF NOT EXISTS dispatches (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	morsel_id TEXT NOT NULL,
	project TEXT NOT NULL,
	agent_id TEXT NOT NULL,
	provider TEXT NOT NULL,
	tier TEXT NOT NULL,
	pid INTEGER NOT NULL DEFAULT 0,
	session_name TEXT NOT NULL DEFAULT '',
	stage TEXT NOT NULL DEFAULT 'dispatched',
	labels TEXT NOT NULL DEFAULT '',
	prompt TEXT NOT NULL,
	dispatched_at DATETIME NOT NULL DEFAULT (datetime('now')),
	completed_at DATETIME,
	next_retry_at DATETIME,
	status TEXT NOT NULL DEFAULT 'running',
	exit_code INTEGER NOT NULL DEFAULT 0,
	duration_s REAL NOT NULL DEFAULT 0,
	retries INTEGER NOT NULL DEFAULT 0,
	escalated_from_tier TEXT NOT NULL DEFAULT '',
	pr_url TEXT NOT NULL DEFAULT '',
	pr_number INTEGER NOT NULL DEFAULT 0,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	cost_usd REAL NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS provider_usage (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	provider TEXT NOT NULL,
	agent_id TEXT NOT NULL,
	morsel_id TEXT NOT NULL,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	dispatched_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

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
);

CREATE TABLE IF NOT EXISTS health_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	event_type TEXT NOT NULL,
	details TEXT NOT NULL DEFAULT '',
	dispatch_id INTEGER NOT NULL DEFAULT 0,
	morsel_id TEXT NOT NULL DEFAULT '',
	created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS tick_metrics (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	tick_at DATETIME NOT NULL DEFAULT (datetime('now')),
	project TEXT NOT NULL,
	morsels_open INTEGER NOT NULL DEFAULT 0,
	morsels_ready INTEGER NOT NULL DEFAULT 0,
	dispatched INTEGER NOT NULL DEFAULT 0,
	completed INTEGER NOT NULL DEFAULT 0,
	failed INTEGER NOT NULL DEFAULT 0,
	stuck INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS dod_results (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	dispatch_id INTEGER NOT NULL REFERENCES dispatches(id),
	morsel_id TEXT NOT NULL,
	project TEXT NOT NULL,
	checked_at DATETIME NOT NULL DEFAULT (datetime('now')),
	passed BOOLEAN NOT NULL DEFAULT 0,
	failures TEXT NOT NULL DEFAULT '',
	check_results TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS dispatch_output (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	dispatch_id INTEGER NOT NULL REFERENCES dispatches(id),
	captured_at DATETIME NOT NULL DEFAULT (datetime('now')),
	output TEXT NOT NULL,
	output_tail TEXT NOT NULL,
	output_bytes INTEGER NOT NULL
);

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
);

CREATE TABLE IF NOT EXISTS morsel_stages (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	project TEXT NOT NULL,
	morsel_id TEXT NOT NULL,
	workflow TEXT NOT NULL,
	current_stage TEXT NOT NULL,
	stage_index INTEGER NOT NULL DEFAULT 0,
	total_stages INTEGER NOT NULL,
	stage_history TEXT NOT NULL DEFAULT '[]',
	created_at DATETIME NOT NULL DEFAULT (datetime('now')),
	updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS claim_leases (
	morsel_id TEXT PRIMARY KEY,
	project TEXT NOT NULL,
	morsels_dir TEXT NOT NULL DEFAULT '',
	agent_id TEXT NOT NULL DEFAULT '',
	dispatch_id INTEGER NOT NULL DEFAULT 0,
	claimed_at DATETIME NOT NULL DEFAULT (datetime('now')),
	heartbeat_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS safety_blocks (
	scope TEXT NOT NULL,
	block_type TEXT NOT NULL,
	blocked_until DATETIME NOT NULL,
	reason TEXT NOT NULL DEFAULT '',
	metadata TEXT NOT NULL DEFAULT '{}',
	created_at DATETIME NOT NULL DEFAULT (datetime('now')),
	updated_at DATETIME NOT NULL DEFAULT (datetime('now')),
	PRIMARY KEY(scope, block_type)
);

CREATE TABLE IF NOT EXISTS sprint_boundaries (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	sprint_number INTEGER NOT NULL UNIQUE,
	sprint_start DATETIME NOT NULL,
	sprint_end DATETIME NOT NULL,
	created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS execution_plan_gate (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	active_plan_id TEXT NOT NULL DEFAULT '',
	approved_by TEXT NOT NULL DEFAULT '',
	approved_at DATETIME,
	activated_at DATETIME,
	updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_morsel_stages_project_morsel ON morsel_stages(project, morsel_id);
CREATE INDEX IF NOT EXISTS idx_morsel_stages_project_stage ON morsel_stages(project, current_stage);
CREATE INDEX IF NOT EXISTS idx_dispatches_status ON dispatches(status);
CREATE INDEX IF NOT EXISTS idx_dispatches_morsel ON dispatches(morsel_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_overflow_queue_morsel_role ON overflow_queue(morsel_id, role);
CREATE INDEX IF NOT EXISTS idx_overflow_queue_priority_enqueued_at ON overflow_queue(priority, enqueued_at);
CREATE INDEX IF NOT EXISTS idx_claim_leases_project ON claim_leases(project);
CREATE INDEX IF NOT EXISTS idx_claim_leases_heartbeat ON claim_leases(heartbeat_at);
CREATE INDEX IF NOT EXISTS idx_safety_blocks_scope_type ON safety_blocks(scope, block_type);
CREATE INDEX IF NOT EXISTS idx_safety_blocks_blocked_until ON safety_blocks(blocked_until);
CREATE INDEX IF NOT EXISTS idx_sprint_boundaries_start ON sprint_boundaries(sprint_start);
CREATE INDEX IF NOT EXISTS idx_sprint_boundaries_end ON sprint_boundaries(sprint_end);
CREATE INDEX IF NOT EXISTS idx_execution_plan_gate_active ON execution_plan_gate(active_plan_id);
CREATE INDEX IF NOT EXISTS idx_usage_provider ON provider_usage(provider, dispatched_at);
CREATE INDEX IF NOT EXISTS idx_dispatch_output_dispatch ON dispatch_output(dispatch_id);
CREATE INDEX IF NOT EXISTS idx_quality_scores_provider_role ON quality_scores(provider, role, recorded_at);
CREATE INDEX IF NOT EXISTS idx_quality_scores_role ON quality_scores(role, recorded_at);
CREATE INDEX IF NOT EXISTS idx_quality_scores_provider ON quality_scores(provider, recorded_at);

CREATE TABLE IF NOT EXISTS token_usage (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	dispatch_id INTEGER NOT NULL DEFAULT 0,
	morsel_id TEXT NOT NULL DEFAULT '',
	project TEXT NOT NULL DEFAULT '',
	activity_name TEXT NOT NULL DEFAULT '',
	agent TEXT NOT NULL DEFAULT '',
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	cache_read_tokens INTEGER NOT NULL DEFAULT 0,
	cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
	cost_usd REAL NOT NULL DEFAULT 0,
	recorded_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_token_usage_dispatch ON token_usage(dispatch_id);
CREATE INDEX IF NOT EXISTS idx_token_usage_project ON token_usage(project, recorded_at);
CREATE INDEX IF NOT EXISTS idx_token_usage_agent ON token_usage(agent, recorded_at);

CREATE TABLE IF NOT EXISTS step_metrics (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	dispatch_id INTEGER NOT NULL DEFAULT 0,
	morsel_id TEXT NOT NULL DEFAULT '',
	project TEXT NOT NULL DEFAULT '',
	step_name TEXT NOT NULL DEFAULT '',
	duration_s REAL NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT '',
	slow INTEGER NOT NULL DEFAULT 0,
	recorded_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_step_metrics_dispatch ON step_metrics(dispatch_id);
CREATE INDEX IF NOT EXISTS idx_step_metrics_project ON step_metrics(project, recorded_at);

`

// Open creates or opens a SQLite database at the given path and ensures the schema exists.
func Open(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", dbPath, err)
	}

	// Rename bead→morsel columns in existing databases BEFORE schema DDL runs.
	// The schema uses morsel_id but pre-rename databases have bead_id.
	if err := migrateBeadToMorsel(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrate bead→morsel: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: create schema: %w", err)
	}

	// Run migrations for existing databases
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}

	s := &Store{db: db}

	// Ensure evolutionary genome table exists
	if err := s.ensureGenomesTable(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: genomes table: %w", err)
	}

	return s, nil
}

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
		db.Exec(`DROP INDEX IF EXISTS ` + idx)
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
	return nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB returns the underlying sql.DB for advanced queries.
func (s *Store) DB() *sql.DB {
	return s.db
}
