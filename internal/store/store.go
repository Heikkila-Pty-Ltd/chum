package store

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

// Store provides SQLite-backed persistence for CHUM state.
type Store struct {
	db                  *sql.DB
	dispatchPersistHook func(point string) error
}

var crystalCandidatesEnabled = parseStoreBoolEnv("CHUM_ENABLE_CRYSTAL_CANDIDATES")

func parseStoreBoolEnv(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
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

CREATE TABLE IF NOT EXISTS ubs_findings (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	dispatch_id   INTEGER NOT NULL DEFAULT 0,
	morsel_id     TEXT    NOT NULL DEFAULT '',
	project       TEXT    NOT NULL DEFAULT '',
	provider      TEXT    NOT NULL DEFAULT '',
	species       TEXT    NOT NULL DEFAULT '',
	rule_id       TEXT    NOT NULL DEFAULT '',
	severity      TEXT    NOT NULL DEFAULT '',
	file_path     TEXT    NOT NULL DEFAULT '',
	line_number   INTEGER NOT NULL DEFAULT 0,
	message       TEXT    NOT NULL DEFAULT '',
	language      TEXT    NOT NULL DEFAULT '',
	attempt       INTEGER NOT NULL DEFAULT 0,
	fixed         BOOLEAN NOT NULL DEFAULT 0,
	created_at    DATETIME NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_ubs_provider ON ubs_findings(provider);
CREATE INDEX IF NOT EXISTS idx_ubs_species  ON ubs_findings(species, rule_id);
CREATE INDEX IF NOT EXISTS idx_ubs_project  ON ubs_findings(project, created_at);

CREATE TABLE IF NOT EXISTS proteins (
	id          TEXT PRIMARY KEY,
	category    TEXT NOT NULL DEFAULT '',
	name        TEXT NOT NULL DEFAULT '',
	molecules   TEXT NOT NULL DEFAULT '[]',
	generation  INTEGER NOT NULL DEFAULT 0,
	successes   INTEGER NOT NULL DEFAULT 0,
	failures    INTEGER NOT NULL DEFAULT 0,
	avg_tokens  REAL NOT NULL DEFAULT 0,
	fitness     REAL NOT NULL DEFAULT 0,
	parent_id   TEXT NOT NULL DEFAULT '',
	created_at  DATETIME NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_proteins_category ON proteins(category);

CREATE TABLE IF NOT EXISTS protein_folds (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	protein_id   TEXT NOT NULL DEFAULT '',
	project      TEXT NOT NULL DEFAULT '',
	morsel_id    TEXT NOT NULL DEFAULT '',
	provider     TEXT NOT NULL DEFAULT '',
	total_tokens INTEGER NOT NULL DEFAULT 0,
	duration_s   REAL NOT NULL DEFAULT 0,
	success      BOOLEAN NOT NULL DEFAULT 0,
	retro        TEXT NOT NULL DEFAULT '{}',
	created_at   DATETIME NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_folds_protein ON protein_folds(protein_id);
CREATE INDEX IF NOT EXISTS idx_folds_project ON protein_folds(project);

CREATE TABLE IF NOT EXISTS mcts_nodes (
	node_key TEXT PRIMARY KEY,
	node_type TEXT NOT NULL DEFAULT '',
	metadata_json TEXT NOT NULL DEFAULT '{}',
	created_at DATETIME NOT NULL DEFAULT (datetime('now')),
	updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_mcts_nodes_type ON mcts_nodes(node_type, updated_at);

CREATE TABLE IF NOT EXISTS mcts_edges (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	parent_node_key TEXT NOT NULL,
	child_node_key TEXT NOT NULL,
	action_key TEXT NOT NULL,
	metadata_json TEXT NOT NULL DEFAULT '{}',
	created_at DATETIME NOT NULL DEFAULT (datetime('now')),
	UNIQUE(parent_node_key, action_key)
);
CREATE INDEX IF NOT EXISTS idx_mcts_edges_parent ON mcts_edges(parent_node_key);
CREATE INDEX IF NOT EXISTS idx_mcts_edges_child ON mcts_edges(child_node_key);

CREATE TABLE IF NOT EXISTS mcts_rollouts (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	task_id TEXT NOT NULL DEFAULT '',
	project TEXT NOT NULL DEFAULT '',
	species TEXT NOT NULL DEFAULT '*',
	parent_node_key TEXT NOT NULL,
	action_key TEXT NOT NULL,
	outcome TEXT NOT NULL DEFAULT '',
	reward REAL NOT NULL DEFAULT 0,
	cost_usd REAL NOT NULL DEFAULT 0,
	duration_s REAL NOT NULL DEFAULT 0,
	metadata_json TEXT NOT NULL DEFAULT '{}',
	created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_mcts_rollouts_parent_species_created ON mcts_rollouts(parent_node_key, species, created_at);
CREATE INDEX IF NOT EXISTS idx_mcts_rollouts_project_created ON mcts_rollouts(project, created_at);
CREATE INDEX IF NOT EXISTS idx_mcts_rollouts_task ON mcts_rollouts(task_id, created_at);

CREATE TABLE IF NOT EXISTS mcts_edge_stats (
	parent_node_key TEXT NOT NULL,
	species TEXT NOT NULL DEFAULT '*',
	action_key TEXT NOT NULL,
	visits INTEGER NOT NULL DEFAULT 0,
	wins INTEGER NOT NULL DEFAULT 0,
	total_reward REAL NOT NULL DEFAULT 0,
	total_cost_usd REAL NOT NULL DEFAULT 0,
	total_duration_s REAL NOT NULL DEFAULT 0,
	last_outcome TEXT NOT NULL DEFAULT '',
	updated_at DATETIME NOT NULL DEFAULT (datetime('now')),
	PRIMARY KEY(parent_node_key, species, action_key)
);
CREATE INDEX IF NOT EXISTS idx_mcts_edge_stats_parent_species_updated ON mcts_edge_stats(parent_node_key, species, updated_at);

CREATE TABLE IF NOT EXISTS planning_trace_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT NOT NULL,
	run_id TEXT NOT NULL DEFAULT '',
	project TEXT NOT NULL DEFAULT '',
	task_id TEXT NOT NULL DEFAULT '',
	cycle INTEGER NOT NULL DEFAULT 0,
	stage TEXT NOT NULL DEFAULT '',
	node_id TEXT NOT NULL DEFAULT '',
	parent_node_id TEXT NOT NULL DEFAULT '',
	branch_id TEXT NOT NULL DEFAULT '',
	option_id TEXT NOT NULL DEFAULT '',
	event_type TEXT NOT NULL DEFAULT '',
	actor TEXT NOT NULL DEFAULT '',
	tool_name TEXT NOT NULL DEFAULT '',
	tool_input TEXT NOT NULL DEFAULT '',
	tool_output TEXT NOT NULL DEFAULT '',
	prompt_text TEXT NOT NULL DEFAULT '',
	response_text TEXT NOT NULL DEFAULT '',
	summary_text TEXT NOT NULL DEFAULT '',
	full_text TEXT NOT NULL DEFAULT '',
	selected_option TEXT NOT NULL DEFAULT '',
	reward REAL NOT NULL DEFAULT 0,
	metadata_json TEXT NOT NULL DEFAULT '{}',
	interaction_class TEXT NOT NULL DEFAULT '',
	interaction_type TEXT NOT NULL DEFAULT '',
	human_interactive INTEGER NOT NULL DEFAULT 0,
	created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_planning_trace_session_created ON planning_trace_events(session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_planning_trace_project_reward_created ON planning_trace_events(project, reward, created_at);
CREATE INDEX IF NOT EXISTS idx_planning_trace_event_created ON planning_trace_events(event_type, created_at);
CREATE INDEX IF NOT EXISTS idx_planning_trace_actor_created ON planning_trace_events(actor, created_at);
CREATE INDEX IF NOT EXISTS idx_planning_trace_branch_created ON planning_trace_events(branch_id, created_at);
CREATE INDEX IF NOT EXISTS idx_planning_trace_node ON planning_trace_events(node_id);

CREATE TABLE IF NOT EXISTS planning_state_snapshots (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT NOT NULL,
	run_id TEXT NOT NULL DEFAULT '',
	project TEXT NOT NULL DEFAULT '',
	task_id TEXT NOT NULL DEFAULT '',
	cycle INTEGER NOT NULL DEFAULT 0,
	stage TEXT NOT NULL DEFAULT '',
	state_hash TEXT NOT NULL,
	state_json TEXT NOT NULL DEFAULT '{}',
	stable INTEGER NOT NULL DEFAULT 1,
	reason TEXT NOT NULL DEFAULT '',
	created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_planning_snapshots_session_created ON planning_state_snapshots(session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_planning_snapshots_state_hash ON planning_state_snapshots(state_hash);

CREATE TABLE IF NOT EXISTS planning_action_blacklist (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id TEXT NOT NULL,
	project TEXT NOT NULL DEFAULT '',
	task_id TEXT NOT NULL DEFAULT '',
	cycle INTEGER NOT NULL DEFAULT 0,
	stage TEXT NOT NULL DEFAULT '',
	state_hash TEXT NOT NULL,
	action_hash TEXT NOT NULL,
	reason TEXT NOT NULL DEFAULT '',
	metadata_json TEXT NOT NULL DEFAULT '{}',
	created_at DATETIME NOT NULL DEFAULT (datetime('now')),
	UNIQUE(session_id, state_hash, action_hash)
);
CREATE INDEX IF NOT EXISTS idx_planning_blacklist_session_created ON planning_action_blacklist(session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_planning_blacklist_state_action ON planning_action_blacklist(state_hash, action_hash);

CREATE TABLE IF NOT EXISTS paleontology_runs (
	id                     INTEGER PRIMARY KEY AUTOINCREMENT,
	run_at                 DATETIME NOT NULL DEFAULT (datetime('now')),
	antibodies_discovered  INTEGER NOT NULL DEFAULT 0,
	genes_mutated          INTEGER NOT NULL DEFAULT 0,
	proteins_nominated     INTEGER NOT NULL DEFAULT 0,
	species_audited        INTEGER NOT NULL DEFAULT 0,
	cost_alerts            INTEGER NOT NULL DEFAULT 0,
	summary                TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS graph_trace_events (
	event_id TEXT PRIMARY KEY,
	parent_event_id TEXT,
	session_id TEXT NOT NULL,
	timestamp INTEGER NOT NULL,
	depth INTEGER DEFAULT 0,
	event_type TEXT NOT NULL,
	phase TEXT,
	model_name TEXT,
	tokens_input INTEGER DEFAULT 0,
	tokens_output INTEGER DEFAULT 0,
	tool_name TEXT,
	tool_success INTEGER,
	human_message TEXT,
	reward REAL DEFAULT 0.0,
	terminal_reward REAL,
	is_terminal INTEGER DEFAULT 0,
	metadata TEXT,
	FOREIGN KEY(parent_event_id) REFERENCES graph_trace_events(event_id)
);

CREATE INDEX IF NOT EXISTS idx_graph_trace_session ON graph_trace_events(session_id);
CREATE INDEX IF NOT EXISTS idx_graph_trace_parent ON graph_trace_events(parent_event_id);
CREATE INDEX IF NOT EXISTS idx_graph_trace_type ON graph_trace_events(event_type);
CREATE INDEX IF NOT EXISTS idx_graph_trace_terminal ON graph_trace_events(terminal_reward DESC) WHERE is_terminal = 1;

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
);
CREATE INDEX IF NOT EXISTS idx_organism_logs_type ON organism_logs(organism_type, created_at);
CREATE INDEX IF NOT EXISTS idx_organism_logs_project ON organism_logs(project, created_at);
CREATE INDEX IF NOT EXISTS idx_organism_logs_task ON organism_logs(task_id);

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
);

CREATE TABLE IF NOT EXISTS trace_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	trace_id INTEGER NOT NULL REFERENCES execution_traces(id) ON DELETE CASCADE,
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
);

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
);

CREATE INDEX IF NOT EXISTS idx_execution_traces_task ON execution_traces(task_id);
CREATE INDEX IF NOT EXISTS idx_execution_traces_species ON execution_traces(species);
CREATE INDEX IF NOT EXISTS idx_execution_traces_status ON execution_traces(status);
CREATE INDEX IF NOT EXISTS idx_trace_events_trace_id ON trace_events(trace_id);
CREATE INDEX IF NOT EXISTS idx_trace_events_stage ON trace_events(stage);
CREATE UNIQUE INDEX IF NOT EXISTS idx_crystal_candidates_species_goal_status ON crystal_candidates(species, goal_signature, status);
CREATE INDEX IF NOT EXISTS idx_crystal_candidates_status ON crystal_candidates(status);

CREATE TABLE IF NOT EXISTS graph_trace_events (
	event_id TEXT PRIMARY KEY,
	parent_event_id TEXT,
	session_id TEXT NOT NULL,
	timestamp INTEGER NOT NULL,
	depth INTEGER DEFAULT 0,
	event_type TEXT NOT NULL,
	phase TEXT,
	model_name TEXT,
	tokens_input INTEGER DEFAULT 0,
	tokens_output INTEGER DEFAULT 0,
	tool_name TEXT,
	tool_success INTEGER,
	human_message TEXT,
	reward REAL DEFAULT 0.0,
	terminal_reward REAL,
	is_terminal INTEGER DEFAULT 0,
	metadata TEXT,
	FOREIGN KEY(parent_event_id) REFERENCES graph_trace_events(event_id)
);

CREATE INDEX IF NOT EXISTS idx_graph_trace_session ON graph_trace_events(session_id);
CREATE INDEX IF NOT EXISTS idx_graph_trace_parent ON graph_trace_events(parent_event_id);
CREATE INDEX IF NOT EXISTS idx_graph_trace_type ON graph_trace_events(event_type);
CREATE INDEX IF NOT EXISTS idx_graph_trace_terminal ON graph_trace_events(terminal_reward DESC) WHERE is_terminal = 1;
`

// Open creates or opens a SQLite database at the given path and ensures the schema exists.
// Uses WAL mode for concurrent reads, busy_timeout of 30s to survive Cambrian Explosion
// multi-writer contention, and MaxOpenConns=1 to serialize writes at the Go level.
func Open(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(30000)")
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", dbPath, err)
	}

	// Single-writer enforcement: serialize all writes through one connection.
	// SQLite WAL supports concurrent reads but only one writer at a time.
	// Without this, Cambrian Explosion child workflows race for the write lock
	// and get SQLITE_BUSY errors even with busy_timeout.
	db.SetMaxOpenConns(1)

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

	s := &Store{db: db}

	// Ensure evolutionary genome table exists BEFORE migrations
	// (migrate() adds columns like 'hibernating' to genomes)
	if err := s.ensureGenomesTable(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: genomes table: %w", err)
	}

	// Run migrations for existing databases
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}

	// Pre-flight check: verify critical columns exist after all migrations.
	// Catches schema drift when multiple binaries hit the same DB.
	if err := s.verifySchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: schema verification failed: %w", err)
	}

	// Seed initial proteins (deterministic workflow sequences)
	if err := s.SeedProteins(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: seed proteins: %w", err)
	}

	return s, nil
}

// addColumnIfNotExists checks whether a column exists on a table and adds it
// using the supplied DDL fragment when it is missing.
// verifySchema runs post-migration sanity checks on the DB schema.
// Catches drift caused by multiple binaries hitting the same DB file.
func (s *Store) verifySchema() error {
	criticalTables := []string{"morsel_stages", "dispatches", "lessons", "genomes", "execution_traces", "trace_events", "crystal_candidates"}
	for _, table := range criticalTables {
		var count int
		err := s.db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('` + table + `')`,
		).Scan(&count)
		if err != nil {
			return fmt.Errorf("verify table %s: %w", table, err)
		}
		if count == 0 {
			return fmt.Errorf("critical table %q is missing — schema migration may have failed", table)
		}
	}

	// Verify critical columns exist (catches schema drift from older binaries)
	colChecks := []struct {
		table  string
		column string
	}{
		{"dispatches", "morsel_id"},
		{"dispatches", "failure_category"},
		{"dispatches", "backend"},
		{"dispatches", "labels"},
		{"genomes", "provider_genes"},
		{"genomes", "hibernating"},
		{"execution_traces", "success_rate"},
		{"execution_traces", "goal_signature"},
		{"execution_traces", "attempt_count"},
		{"trace_events", "trace_id"},
		{"crystal_candidates", "success_rate"},
	}
	for _, c := range colChecks {
		var hasCol int
		if err := s.db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('`+c.table+`') WHERE name = ?`, c.column,
		).Scan(&hasCol); err != nil {
			return fmt.Errorf("verify %s.%s: %w", c.table, c.column, err)
		}
		if hasCol == 0 {
			return fmt.Errorf("schema drift: %s.%s column missing — run migrations or update binary", c.table, c.column)
		}
	}

	return nil
}

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

	// Add recurring_failures column to paleontology_runs (for systemic DoD failure detection)
	if err := addColumnIfNotExists(db, "paleontology_runs", "recurring_failures", "recurring_failures INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}

	return nil
}

// StartExecutionTrace creates a new trace row for a workflow task.
func (s *Store) StartExecutionTrace(taskID, species, goalSignature string) (int64, error) {
	taskID = strings.TrimSpace(taskID)
	species = strings.TrimSpace(species)
	goalSignature = strings.TrimSpace(goalSignature)

	result, err := s.db.Exec(`
		INSERT INTO execution_traces (task_id, species, goal_signature)
		VALUES (?, ?, ?)`,
		taskID, species, goalSignature,
	)
	if err != nil {
		return 0, fmt.Errorf("store: start execution trace: %w", err)
	}

	traceID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: execution trace last insert id: %w", err)
	}

	return traceID, nil
}

// AppendTraceEvent appends a normalized event to an execution trace.
func (s *Store) AppendTraceEvent(traceID int64, event TraceEvent) error {
	_, err := s.db.Exec(`
		INSERT INTO trace_events (
			trace_id, stage, step, tool, command, input_summary, output_summary, duration_ms, success, error_context
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		traceID,
		event.Stage,
		event.Step,
		event.Tool,
		event.Command,
		event.InputSummary,
		event.OutputSummary,
		event.DurationMs,
		boolToInt(event.Success),
		event.ErrorContext,
	)
	if err != nil {
		return fmt.Errorf("store: append trace event: %w", err)
	}
	return nil
}

// CompleteExecutionTrace updates trace completion metadata.
func (s *Store) CompleteExecutionTrace(traceID int64, status, outcome string, supportCount int, successCount int) error {
	successRate := 0.0
	if supportCount > 0 {
		successRate = float64(successCount) / float64(supportCount)
	}

	_, err := s.db.Exec(`
		UPDATE execution_traces
		SET status = ?,
			outcome = ?,
			support_count = ?,
			success_count = ?,
			attempt_count = ?,
			success_rate = ?,
			completed_at = datetime('now'),
			updated_at = datetime('now')
		WHERE id = ?`,
		status,
		outcome,
		supportCount,
		successCount,
		supportCount,
		successRate,
		traceID,
	)
	if err != nil {
		return fmt.Errorf("store: complete execution trace: %w", err)
	}
	return nil
}

// ListExecutionTraces returns all traces for a task.
func (s *Store) ListExecutionTraces(taskID string) ([]ExecutionTrace, error) {
	rows, err := s.db.Query(`
		SELECT id, task_id, species, goal_signature, status, started_at, completed_at, outcome,
		       attempt_count, support_count, success_rate, created_at, updated_at
		FROM execution_traces
		WHERE task_id = ?
		ORDER BY created_at ASC`,
		taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list execution traces: %w", err)
	}
	defer rows.Close()

	var traces []ExecutionTrace
	for rows.Next() {
		var trace ExecutionTrace
		var completed sql.NullTime
		if err := rows.Scan(
			&trace.ID,
			&trace.TaskID,
			&trace.Species,
			&trace.GoalSignature,
			&trace.Status,
			&trace.StartedAt,
			&completed,
			&trace.Outcome,
			&trace.AttemptCount,
			&trace.SupportCount,
			&trace.SuccessRate,
			&trace.CreatedAt,
			&trace.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan execution trace: %w", err)
		}
		if completed.Valid {
			trace.CompletedAt = completed.Time
		}
		traces = append(traces, trace)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list execution traces rows: %w", err)
	}
	return traces, nil
}

// GetTraceEvents returns canonical events for a trace id.
func (s *Store) GetTraceEvents(traceID int64) ([]TraceEvent, error) {
	rows, err := s.db.Query(`
		SELECT id, trace_id, stage, step, tool, command, input_summary, output_summary, duration_ms, success, error_context, created_at
		FROM trace_events
		WHERE trace_id = ?
		ORDER BY created_at ASC`,
		traceID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list trace events: %w", err)
	}
	defer rows.Close()

	var events []TraceEvent
	for rows.Next() {
		var event TraceEvent
		var success int
		if err := rows.Scan(
			&event.ID,
			&event.TraceID,
			&event.Stage,
			&event.Step,
			&event.Tool,
			&event.Command,
			&event.InputSummary,
			&event.OutputSummary,
			&event.DurationMs,
			&success,
			&event.ErrorContext,
			&event.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan trace event: %w", err)
		}
		event.Success = success == 1
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list trace events rows: %w", err)
	}
	return events, nil
}

// UpsertCrystalCandidate stores or updates a deterministic candidate flow.
func (s *Store) UpsertCrystalCandidate(candidate CrystalCandidate) error {
	if !crystalCandidatesEnabled {
		return nil
	}

	if candidate.Status == "" {
		candidate.Status = CrystalCandidateStatusPending
	}

	_, err := s.db.Exec(`
		INSERT INTO crystal_candidates (
			species, goal_signature, status, template_json, support_count, attempt_count,
			success_count, success_rate, preconditions, ordered_steps, verification_checks,
			required_inputs, last_seen_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(species, goal_signature, status) DO UPDATE SET
			template_json = excluded.template_json,
			support_count = crystal_candidates.support_count + excluded.support_count,
			attempt_count = crystal_candidates.attempt_count + excluded.attempt_count,
			success_count = crystal_candidates.success_count + excluded.success_count,
			success_rate = CASE
				WHEN (crystal_candidates.attempt_count + excluded.attempt_count) = 0 THEN 0
				ELSE CAST(crystal_candidates.success_count + excluded.success_count AS REAL) /
				     CAST(crystal_candidates.attempt_count + excluded.attempt_count AS REAL)
			END,
			preconditions = excluded.preconditions,
			ordered_steps = excluded.ordered_steps,
			verification_checks = excluded.verification_checks,
			required_inputs = excluded.required_inputs,
			updated_at = datetime('now'),
			last_seen_at = datetime('now')
	`,
		candidate.Species,
		candidate.GoalSignature,
		candidate.Status,
		candidate.TemplateJSON,
		candidate.SupportCount,
		candidate.AttemptCount,
		candidate.SuccessCount,
		candidate.SuccessRate,
		candidate.Preconditions,
		candidate.OrderedSteps,
		candidate.VerificationChecks,
		candidate.RequiredInputs,
	)
	if err != nil {
		return fmt.Errorf("store: upsert crystal candidate: %w", err)
	}
	return nil
}

// GetCrystalCandidatesBySpeciesAndGoal returns candidates for a species/signature pair.
func (s *Store) GetCrystalCandidatesBySpeciesAndGoal(species, goalSignature string) ([]CrystalCandidate, error) {
	if !crystalCandidatesEnabled {
		return nil, nil
	}

	rows, err := s.db.Query(`
		SELECT id, species, goal_signature, status, template_json, support_count, attempt_count,
		       success_count, success_rate, preconditions, ordered_steps, verification_checks,
		       required_inputs, last_seen_at, created_at, updated_at
		FROM crystal_candidates
		WHERE species = ? AND goal_signature = ?
		ORDER BY support_count DESC, updated_at DESC`,
		species, goalSignature,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get crystal candidates by species/signature: %w", err)
	}
	defer rows.Close()

	var candidates []CrystalCandidate
	for rows.Next() {
		var c CrystalCandidate
		var status string
		if err := rows.Scan(
			&c.ID,
			&c.Species,
			&c.GoalSignature,
			&status,
			&c.TemplateJSON,
			&c.SupportCount,
			&c.AttemptCount,
			&c.SuccessCount,
			&c.SuccessRate,
			&c.Preconditions,
			&c.OrderedSteps,
			&c.VerificationChecks,
			&c.RequiredInputs,
			&c.LastSeenAt,
			&c.CreatedAt,
			&c.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan crystal candidate: %w", err)
		}
		c.Status = CrystalCandidateStatus(status)
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: get crystal candidates by species/signature rows: %w", err)
	}
	return candidates, nil
}

// GetCrystalCandidatesByStatus returns all candidates in a lifecycle state.
func (s *Store) GetCrystalCandidatesByStatus(status CrystalCandidateStatus) ([]CrystalCandidate, error) {
	if !crystalCandidatesEnabled {
		return nil, nil
	}

	rows, err := s.db.Query(`
		SELECT id, species, goal_signature, status, template_json, support_count, attempt_count,
		       success_count, success_rate, preconditions, ordered_steps, verification_checks,
		       required_inputs, last_seen_at, created_at, updated_at
		FROM crystal_candidates
		WHERE status = ?
		ORDER BY success_rate DESC, support_count DESC`,
		status,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get crystal candidates by status: %w", err)
	}
	defer rows.Close()

	var candidates []CrystalCandidate
	for rows.Next() {
		var c CrystalCandidate
		var statusText string
		if err := rows.Scan(
			&c.ID,
			&c.Species,
			&c.GoalSignature,
			&statusText,
			&c.TemplateJSON,
			&c.SupportCount,
			&c.AttemptCount,
			&c.SuccessCount,
			&c.SuccessRate,
			&c.Preconditions,
			&c.OrderedSteps,
			&c.VerificationChecks,
			&c.RequiredInputs,
			&c.LastSeenAt,
			&c.CreatedAt,
			&c.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan crystal candidate: %w", err)
		}
		c.Status = CrystalCandidateStatus(statusText)
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: get crystal candidates by status rows: %w", err)
	}
	return candidates, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB returns the underlying sql.DB for advanced queries.
func (s *Store) DB() *sql.DB {
	return s.db
}
