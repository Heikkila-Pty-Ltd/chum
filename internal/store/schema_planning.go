package store

// schemaPlanning contains tables for MCTS planning ceremonies,
// planning trace events, state snapshots, and action blacklists.
const schemaPlanning = `
CREATE TABLE IF NOT EXISTS mcts_nodes (
	node_key TEXT PRIMARY KEY,
	node_type TEXT NOT NULL DEFAULT '',
	metadata_json TEXT NOT NULL DEFAULT '{}',
	created_at DATETIME NOT NULL DEFAULT (datetime('now')),
	updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS mcts_edges (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	parent_node_key TEXT NOT NULL,
	child_node_key TEXT NOT NULL,
	action_key TEXT NOT NULL,
	metadata_json TEXT NOT NULL DEFAULT '{}',
	created_at DATETIME NOT NULL DEFAULT (datetime('now')),
	UNIQUE(parent_node_key, action_key)
);

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

CREATE INDEX IF NOT EXISTS idx_mcts_nodes_type ON mcts_nodes(node_type, updated_at);
CREATE INDEX IF NOT EXISTS idx_mcts_edges_parent ON mcts_edges(parent_node_key);
CREATE INDEX IF NOT EXISTS idx_mcts_edges_child ON mcts_edges(child_node_key);
CREATE INDEX IF NOT EXISTS idx_mcts_rollouts_parent_species_created ON mcts_rollouts(parent_node_key, species, created_at);
CREATE INDEX IF NOT EXISTS idx_mcts_rollouts_project_created ON mcts_rollouts(project, created_at);
CREATE INDEX IF NOT EXISTS idx_mcts_rollouts_task ON mcts_rollouts(task_id, created_at);
CREATE INDEX IF NOT EXISTS idx_mcts_edge_stats_parent_species_updated ON mcts_edge_stats(parent_node_key, species, updated_at);
CREATE INDEX IF NOT EXISTS idx_planning_trace_session_created ON planning_trace_events(session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_planning_trace_project_reward_created ON planning_trace_events(project, reward, created_at);
CREATE INDEX IF NOT EXISTS idx_planning_trace_event_created ON planning_trace_events(event_type, created_at);
CREATE INDEX IF NOT EXISTS idx_planning_trace_actor_created ON planning_trace_events(actor, created_at);
CREATE INDEX IF NOT EXISTS idx_planning_trace_branch_created ON planning_trace_events(branch_id, created_at);
CREATE INDEX IF NOT EXISTS idx_planning_trace_node ON planning_trace_events(node_id);
CREATE INDEX IF NOT EXISTS idx_planning_snapshots_session_created ON planning_state_snapshots(session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_planning_snapshots_state_hash ON planning_state_snapshots(state_hash);
CREATE INDEX IF NOT EXISTS idx_planning_blacklist_session_created ON planning_action_blacklist(session_id, created_at);
CREATE INDEX IF NOT EXISTS idx_planning_blacklist_state_action ON planning_action_blacklist(state_hash, action_hash);
`
