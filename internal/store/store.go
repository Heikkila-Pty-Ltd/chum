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
	bead_id TEXT NOT NULL,
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
	bead_id TEXT NOT NULL,
	input_tokens INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	dispatched_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS overflow_queue (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	bead_id TEXT NOT NULL,
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
	bead_id TEXT NOT NULL DEFAULT '',
	created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS tick_metrics (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	tick_at DATETIME NOT NULL DEFAULT (datetime('now')),
	project TEXT NOT NULL,
	beads_open INTEGER NOT NULL DEFAULT 0,
	beads_ready INTEGER NOT NULL DEFAULT 0,
	dispatched INTEGER NOT NULL DEFAULT 0,
	completed INTEGER NOT NULL DEFAULT 0,
	failed INTEGER NOT NULL DEFAULT 0,
	stuck INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS dod_results (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	dispatch_id INTEGER NOT NULL REFERENCES dispatches(id),
	bead_id TEXT NOT NULL,
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
	bead_closed INTEGER NOT NULL DEFAULT 0,
	commit_made INTEGER NOT NULL DEFAULT 0,
	files_changed INTEGER NOT NULL DEFAULT 0,
	lines_changed INTEGER NOT NULL DEFAULT 0,
	duration REAL NOT NULL DEFAULT 0,
	recorded_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS bead_stages (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	project TEXT NOT NULL,
	bead_id TEXT NOT NULL,
	workflow TEXT NOT NULL,
	current_stage TEXT NOT NULL,
	stage_index INTEGER NOT NULL DEFAULT 0,
	total_stages INTEGER NOT NULL,
	stage_history TEXT NOT NULL DEFAULT '[]',
	created_at DATETIME NOT NULL DEFAULT (datetime('now')),
	updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS claim_leases (
	bead_id TEXT PRIMARY KEY,
	project TEXT NOT NULL,
	beads_dir TEXT NOT NULL DEFAULT '',
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

CREATE UNIQUE INDEX IF NOT EXISTS idx_bead_stages_project_bead ON bead_stages(project, bead_id);
CREATE INDEX IF NOT EXISTS idx_bead_stages_project_stage ON bead_stages(project, current_stage);
CREATE INDEX IF NOT EXISTS idx_dispatches_status ON dispatches(status);
CREATE INDEX IF NOT EXISTS idx_dispatches_bead ON dispatches(bead_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_overflow_queue_bead_role ON overflow_queue(bead_id, role);
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
	bead_id TEXT NOT NULL DEFAULT '',
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
	bead_id TEXT NOT NULL DEFAULT '',
	project TEXT NOT NULL DEFAULT '',
	step_name TEXT NOT NULL DEFAULT '',
	duration_s REAL NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT '',
	slow INTEGER NOT NULL DEFAULT 0,
	recorded_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_step_metrics_dispatch ON step_metrics(dispatch_id);
CREATE INDEX IF NOT EXISTS idx_step_metrics_project ON step_metrics(project, recorded_at);

CREATE TABLE IF NOT EXISTS stingray_runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	project TEXT NOT NULL,
	run_at DATETIME NOT NULL DEFAULT (datetime('now')),
	findings_total INTEGER NOT NULL DEFAULT 0,
	findings_new INTEGER NOT NULL DEFAULT 0,
	findings_resolved INTEGER NOT NULL DEFAULT 0,
	metrics_json TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_stingray_runs_project ON stingray_runs(project);
CREATE INDEX IF NOT EXISTS idx_stingray_runs_run_at ON stingray_runs(run_at);

CREATE TABLE IF NOT EXISTS stingray_findings (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id INTEGER NOT NULL REFERENCES stingray_runs(id),
	project TEXT NOT NULL,
	category TEXT NOT NULL,
	severity TEXT NOT NULL,
	title TEXT NOT NULL,
	detail TEXT NOT NULL,
	file_path TEXT NOT NULL DEFAULT '',
	evidence TEXT NOT NULL DEFAULT '',
	bead_id TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'open',
	first_seen DATETIME NOT NULL DEFAULT (datetime('now')),
	last_seen DATETIME NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_stingray_findings_run ON stingray_findings(run_id);
CREATE INDEX IF NOT EXISTS idx_stingray_findings_project ON stingray_findings(project);
CREATE INDEX IF NOT EXISTS idx_stingray_findings_status ON stingray_findings(status);
CREATE INDEX IF NOT EXISTS idx_stingray_findings_category ON stingray_findings(category);
`

// Open creates or opens a SQLite database at the given path and ensures the schema exists.
func Open(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", dbPath, err)
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

	return &Store{db: db}, nil
}

// migrate applies incremental schema migrations for existing databases.
func migrate(db *sql.DB) error {
	// Add session_name column if it doesn't exist (for databases created before this field was added)
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('dispatches') WHERE name = 'session_name'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check session_name column: %w", err)
	}
	if count == 0 {
		if _, err := db.Exec(`ALTER TABLE dispatches ADD COLUMN session_name TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add session_name column: %w", err)
		}
	}

	// Add cost tracking columns if they don't exist
	err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('dispatches') WHERE name = 'input_tokens'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check input_tokens column: %w", err)
	}
	if count == 0 {
		if _, err := db.Exec(`ALTER TABLE dispatches ADD COLUMN input_tokens INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add input_tokens column: %w", err)
		}
	}

	err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('dispatches') WHERE name = 'output_tokens'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check output_tokens column: %w", err)
	}
	if count == 0 {
		if _, err := db.Exec(`ALTER TABLE dispatches ADD COLUMN output_tokens INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add output_tokens column: %w", err)
		}
	}

	err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('dispatches') WHERE name = 'cost_usd'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check cost_usd column: %w", err)
	}
	if count == 0 {
		if _, err := db.Exec(`ALTER TABLE dispatches ADD COLUMN cost_usd REAL NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add cost_usd column: %w", err)
		}
	}

	// Add failure diagnosis columns if they don't exist
	err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('dispatches') WHERE name = 'failure_category'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check failure_category column: %w", err)
	}
	if count == 0 {
		if _, err := db.Exec(`ALTER TABLE dispatches ADD COLUMN failure_category TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add failure_category column: %w", err)
		}
	}

	err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('dispatches') WHERE name = 'failure_summary'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check failure_summary column: %w", err)
	}
	if count == 0 {
		if _, err := db.Exec(`ALTER TABLE dispatches ADD COLUMN failure_summary TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add failure_summary column: %w", err)
		}
	}

	// Add log_path column if it doesn't exist
	err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('dispatches') WHERE name = 'log_path'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check log_path column: %w", err)
	}
	if count == 0 {
		if _, err := db.Exec(`ALTER TABLE dispatches ADD COLUMN log_path TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add log_path column: %w", err)
		}
	}

	// Add branch column if it doesn't exist
	err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('dispatches') WHERE name = 'branch'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check branch column: %w", err)
	}
	if count == 0 {
		if _, err := db.Exec(`ALTER TABLE dispatches ADD COLUMN branch TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add branch column: %w", err)
		}
	}

	// Add backend column if it doesn't exist
	err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('dispatches') WHERE name = 'backend'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check backend column: %w", err)
	}
	if count == 0 {
		if _, err := db.Exec(`ALTER TABLE dispatches ADD COLUMN backend TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add backend column: %w", err)
		}
	}

	// Add stage column if it doesn't exist
	err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('dispatches') WHERE name = 'stage'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check stage column: %w", err)
	}
	if count == 0 {
		if _, err := db.Exec(`ALTER TABLE dispatches ADD COLUMN stage TEXT NOT NULL DEFAULT 'dispatched'`); err != nil {
			return fmt.Errorf("add stage column: %w", err)
		}
	}

	// Add labels column if it doesn't exist
	err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('dispatches') WHERE name = 'labels'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check labels column: %w", err)
	}
	if count == 0 {
		if _, err := db.Exec(`ALTER TABLE dispatches ADD COLUMN labels TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add labels column: %w", err)
		}
	}

	// Add token columns to provider_usage if they don't exist
	err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('provider_usage') WHERE name = 'input_tokens'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check provider_usage input_tokens column: %w", err)
	}
	if count == 0 {
		if _, err := db.Exec(`ALTER TABLE provider_usage ADD COLUMN input_tokens INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add provider_usage input_tokens column: %w", err)
		}
	}

	err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('provider_usage') WHERE name = 'output_tokens'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check provider_usage output_tokens column: %w", err)
	}
	if count == 0 {
		if _, err := db.Exec(`ALTER TABLE provider_usage ADD COLUMN output_tokens INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add provider_usage output_tokens column: %w", err)
		}
	}

	// Add PR tracking columns if they don't exist
	err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('dispatches') WHERE name = 'pr_url'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check pr_url column: %w", err)
	}
	if count == 0 {
		if _, err := db.Exec(`ALTER TABLE dispatches ADD COLUMN pr_url TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add pr_url column: %w", err)
		}
	}

	err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('dispatches') WHERE name = 'pr_number'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check pr_number column: %w", err)
	}
	if count == 0 {
		if _, err := db.Exec(`ALTER TABLE dispatches ADD COLUMN pr_number INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add pr_number column: %w", err)
		}
	}

	// Add pending retry scheduling timestamp if it doesn't exist.
	err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('dispatches') WHERE name = 'next_retry_at'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check next_retry_at column: %w", err)
	}
	if count == 0 {
		if _, err := db.Exec(`ALTER TABLE dispatches ADD COLUMN next_retry_at DATETIME`); err != nil {
			return fmt.Errorf("add next_retry_at column: %w", err)
		}
	}

	// Add health event correlation columns if they don't exist
	err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('health_events') WHERE name = 'dispatch_id'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check health_events dispatch_id column: %w", err)
	}
	if count == 0 {
		if _, err := db.Exec(`ALTER TABLE health_events ADD COLUMN dispatch_id INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add health_events dispatch_id column: %w", err)
		}
	}

	err = db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('health_events') WHERE name = 'bead_id'`).Scan(&count)
	if err != nil {
		return fmt.Errorf("check health_events bead_id column: %w", err)
	}
	if count == 0 {
		if _, err := db.Exec(`ALTER TABLE health_events ADD COLUMN bead_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add health_events bead_id column: %w", err)
		}
	}

	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_health_events_dispatch ON health_events(dispatch_id)`); err != nil {
		return fmt.Errorf("create health_events dispatch index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_health_events_bead ON health_events(bead_id)`); err != nil {
		return fmt.Errorf("create health_events bead index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_health_events_created_at ON health_events(created_at)`); err != nil {
		return fmt.Errorf("create health_events created_at index: %w", err)
	}

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS claim_leases (
			bead_id TEXT PRIMARY KEY,
			project TEXT NOT NULL,
			beads_dir TEXT NOT NULL DEFAULT '',
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
			bead_id TEXT NOT NULL,
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
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_overflow_queue_bead_role ON overflow_queue(bead_id, role)`); err != nil {
		return fmt.Errorf("create overflow_queue bead+role index: %w", err)
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
			bead_closed INTEGER NOT NULL DEFAULT 0,
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

	if err := migrateBeadStagesTable(db); err != nil {
		return err
	}

	if err := migrateLessonsTable(db); err != nil {
		return err
	}

	if err := migrateTokenUsageTable(db); err != nil {
		return err
	}

	if err := migrateStingrayTables(db); err != nil {
		return err
	}

	return nil
}

// migrateStingrayTables ensures stingray_runs and stingray_findings tables exist.
func migrateStingrayTables(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS stingray_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project TEXT NOT NULL,
			run_at DATETIME NOT NULL DEFAULT (datetime('now')),
			findings_total INTEGER NOT NULL DEFAULT 0,
			findings_new INTEGER NOT NULL DEFAULT 0,
			findings_resolved INTEGER NOT NULL DEFAULT 0,
			metrics_json TEXT NOT NULL DEFAULT '{}'
		)
	`); err != nil {
		return fmt.Errorf("create stingray_runs table: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_stingray_runs_project ON stingray_runs(project)`); err != nil {
		return fmt.Errorf("create stingray_runs project index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_stingray_runs_run_at ON stingray_runs(run_at)`); err != nil {
		return fmt.Errorf("create stingray_runs run_at index: %w", err)
	}

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS stingray_findings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id INTEGER NOT NULL REFERENCES stingray_runs(id),
			project TEXT NOT NULL,
			category TEXT NOT NULL,
			severity TEXT NOT NULL,
			title TEXT NOT NULL,
			detail TEXT NOT NULL,
			file_path TEXT NOT NULL DEFAULT '',
			evidence TEXT NOT NULL DEFAULT '',
			bead_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'open',
			first_seen DATETIME NOT NULL DEFAULT (datetime('now')),
			last_seen DATETIME NOT NULL DEFAULT (datetime('now'))
		)
	`); err != nil {
		return fmt.Errorf("create stingray_findings table: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_stingray_findings_run ON stingray_findings(run_id)`); err != nil {
		return fmt.Errorf("create stingray_findings run index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_stingray_findings_project ON stingray_findings(project)`); err != nil {
		return fmt.Errorf("create stingray_findings project index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_stingray_findings_status ON stingray_findings(status)`); err != nil {
		return fmt.Errorf("create stingray_findings status index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_stingray_findings_category ON stingray_findings(category)`); err != nil {
		return fmt.Errorf("create stingray_findings category index: %w", err)
	}
	return nil
}

// migrateBeadStagesTable ensures bead_stages uses project+bead keying and indexes.
func migrateBeadStagesTable(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS bead_stages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			bead_id TEXT NOT NULL,
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
		return fmt.Errorf("create bead_stages table: %w", err)
	}

	// Remove legacy bead-only uniqueness to avoid cross-project collisions.
	if _, err := db.Exec(`DROP INDEX IF EXISTS idx_bead_stages_bead`); err != nil {
		return fmt.Errorf("drop legacy bead_stages bead-only index: %w", err)
	}

	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_bead_stages_project_bead ON bead_stages(project, bead_id)`); err != nil {
		return fmt.Errorf("create bead_stages project_bead index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_bead_stages_project_stage ON bead_stages(project, current_stage)`); err != nil {
		return fmt.Errorf("create bead_stages project_stage index: %w", err)
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
