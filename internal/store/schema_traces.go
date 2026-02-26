package store

// schemaTraces contains tables for execution tracing, crystallization,
// graph trace events, and organism logging.
const schemaTraces = `
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

CREATE INDEX IF NOT EXISTS idx_graph_trace_session ON graph_trace_events(session_id);
CREATE INDEX IF NOT EXISTS idx_graph_trace_parent ON graph_trace_events(parent_event_id);
CREATE INDEX IF NOT EXISTS idx_graph_trace_type ON graph_trace_events(event_type);
CREATE INDEX IF NOT EXISTS idx_graph_trace_terminal ON graph_trace_events(terminal_reward DESC) WHERE is_terminal = 1;
CREATE INDEX IF NOT EXISTS idx_organism_logs_type ON organism_logs(organism_type, created_at);
CREATE INDEX IF NOT EXISTS idx_organism_logs_project ON organism_logs(project, created_at);
CREATE INDEX IF NOT EXISTS idx_organism_logs_task ON organism_logs(task_id);
CREATE INDEX IF NOT EXISTS idx_execution_traces_task ON execution_traces(task_id);
CREATE INDEX IF NOT EXISTS idx_execution_traces_species ON execution_traces(species);
CREATE INDEX IF NOT EXISTS idx_execution_traces_status ON execution_traces(status);
CREATE INDEX IF NOT EXISTS idx_trace_events_trace_id ON trace_events(trace_id);
CREATE INDEX IF NOT EXISTS idx_trace_events_stage ON trace_events(stage);
CREATE UNIQUE INDEX IF NOT EXISTS idx_crystal_candidates_species_goal_status ON crystal_candidates(species, goal_signature, status);
CREATE INDEX IF NOT EXISTS idx_crystal_candidates_status ON crystal_candidates(status);
`
