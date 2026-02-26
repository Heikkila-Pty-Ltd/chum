package store

// schemaScheduling contains tables for dispatch scheduling, concurrency control,
// overflow queuing, safety gates, sprints, and quality scoring.
const schemaScheduling = `
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
CREATE UNIQUE INDEX IF NOT EXISTS idx_overflow_queue_morsel_role ON overflow_queue(morsel_id, role);
CREATE INDEX IF NOT EXISTS idx_overflow_queue_priority_enqueued_at ON overflow_queue(priority, enqueued_at);
CREATE INDEX IF NOT EXISTS idx_claim_leases_project ON claim_leases(project);
CREATE INDEX IF NOT EXISTS idx_claim_leases_heartbeat ON claim_leases(heartbeat_at);
CREATE INDEX IF NOT EXISTS idx_safety_blocks_scope_type ON safety_blocks(scope, block_type);
CREATE INDEX IF NOT EXISTS idx_safety_blocks_blocked_until ON safety_blocks(blocked_until);
CREATE INDEX IF NOT EXISTS idx_sprint_boundaries_start ON sprint_boundaries(sprint_start);
CREATE INDEX IF NOT EXISTS idx_sprint_boundaries_end ON sprint_boundaries(sprint_end);
CREATE INDEX IF NOT EXISTS idx_execution_plan_gate_active ON execution_plan_gate(active_plan_id);
CREATE INDEX IF NOT EXISTS idx_quality_scores_provider_role ON quality_scores(provider, role, recorded_at);
CREATE INDEX IF NOT EXISTS idx_quality_scores_role ON quality_scores(role, recorded_at);
CREATE INDEX IF NOT EXISTS idx_quality_scores_provider ON quality_scores(provider, recorded_at);
`
