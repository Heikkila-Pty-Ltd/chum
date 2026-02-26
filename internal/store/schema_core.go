package store

// schemaCore contains tables essential for any CHUM instance:
// dispatch lifecycle, DoD results, output capture, health events, tick metrics,
// provider usage, token usage, and step metrics.
const schemaCore = `
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
	failure_category TEXT NOT NULL DEFAULT '',
	failure_summary TEXT NOT NULL DEFAULT '',
	log_path TEXT NOT NULL DEFAULT '',
	branch TEXT NOT NULL DEFAULT '',
	backend TEXT NOT NULL DEFAULT '',
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

CREATE INDEX IF NOT EXISTS idx_dispatches_status ON dispatches(status);
CREATE INDEX IF NOT EXISTS idx_dispatches_morsel ON dispatches(morsel_id);
CREATE INDEX IF NOT EXISTS idx_usage_provider ON provider_usage(provider, dispatched_at);
CREATE INDEX IF NOT EXISTS idx_dispatch_output_dispatch ON dispatch_output(dispatch_id);
CREATE INDEX IF NOT EXISTS idx_token_usage_dispatch ON token_usage(dispatch_id);
CREATE INDEX IF NOT EXISTS idx_token_usage_project ON token_usage(project, recorded_at);
CREATE INDEX IF NOT EXISTS idx_token_usage_agent ON token_usage(agent, recorded_at);
CREATE INDEX IF NOT EXISTS idx_step_metrics_dispatch ON step_metrics(dispatch_id);
CREATE INDEX IF NOT EXISTS idx_step_metrics_project ON step_metrics(project, recorded_at);
`
