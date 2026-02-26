package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const (
	defaultMCTSEdgeStatsLimit = 32
	maxMCTSEdgeStatsLimit     = 500
	mctsDefaultSpecies        = "*"
	mctsOutcomeUnknown        = "unknown"
)

// MCTSEdgeStat stores aggregated planner outcomes for one edge.
type MCTSEdgeStat struct {
	ParentNodeKey  string
	Species        string
	ActionKey      string
	Visits         int
	Wins           int
	TotalReward    float64
	TotalCostUSD   float64
	TotalDurationS float64
	LastOutcome    string
	UpdatedAt      time.Time
}

// MCTSRollout is one planner decision outcome event.
type MCTSRollout struct {
	TaskID        string
	Project       string
	Species       string
	ParentNodeKey string
	ActionKey     string
	Outcome       string
	Reward        float64
	CostUSD       float64
	DurationS     float64
	MetadataJSON  string
}

func migrateMCTSTables(db *sql.DB) error {
	stmts := []struct {
		name string
		sql  string
	}{
		{
			name: "mcts_nodes table",
			sql: `
				CREATE TABLE IF NOT EXISTS mcts_nodes (
					node_key TEXT PRIMARY KEY,
					node_type TEXT NOT NULL DEFAULT '',
					metadata_json TEXT NOT NULL DEFAULT '{}',
					created_at DATETIME NOT NULL DEFAULT (datetime('now')),
					updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
				)`,
		},
		{
			name: "mcts_edges table",
			sql: `
				CREATE TABLE IF NOT EXISTS mcts_edges (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					parent_node_key TEXT NOT NULL,
					child_node_key TEXT NOT NULL,
					action_key TEXT NOT NULL,
					metadata_json TEXT NOT NULL DEFAULT '{}',
					created_at DATETIME NOT NULL DEFAULT (datetime('now')),
					UNIQUE(parent_node_key, action_key)
				)`,
		},
		{
			name: "mcts_rollouts table",
			sql: `
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
				)`,
		},
		{
			name: "mcts_edge_stats table",
			sql: `
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
				)`,
		},
		{
			name: "idx_mcts_nodes_type",
			sql:  `CREATE INDEX IF NOT EXISTS idx_mcts_nodes_type ON mcts_nodes(node_type, updated_at)`,
		},
		{
			name: "idx_mcts_edges_parent",
			sql:  `CREATE INDEX IF NOT EXISTS idx_mcts_edges_parent ON mcts_edges(parent_node_key)`,
		},
		{
			name: "idx_mcts_edges_child",
			sql:  `CREATE INDEX IF NOT EXISTS idx_mcts_edges_child ON mcts_edges(child_node_key)`,
		},
		{
			name: "idx_mcts_rollouts_parent_species_created",
			sql:  `CREATE INDEX IF NOT EXISTS idx_mcts_rollouts_parent_species_created ON mcts_rollouts(parent_node_key, species, created_at)`,
		},
		{
			name: "idx_mcts_rollouts_project_created",
			sql:  `CREATE INDEX IF NOT EXISTS idx_mcts_rollouts_project_created ON mcts_rollouts(project, created_at)`,
		},
		{
			name: "idx_mcts_rollouts_task",
			sql:  `CREATE INDEX IF NOT EXISTS idx_mcts_rollouts_task ON mcts_rollouts(task_id, created_at)`,
		},
		{
			name: "idx_mcts_edge_stats_parent_species_updated",
			sql:  `CREATE INDEX IF NOT EXISTS idx_mcts_edge_stats_parent_species_updated ON mcts_edge_stats(parent_node_key, species, updated_at)`,
		},
	}

	for _, stmt := range stmts {
		if _, err := db.Exec(stmt.sql); err != nil {
			return fmt.Errorf("create %s: %w", stmt.name, err)
		}
	}
	return nil
}

// UpsertMCTSEdgeStat stores aggregate values for one parent/species/action edge.
func (s *Store) UpsertMCTSEdgeStat(stat MCTSEdgeStat) error {
	parent := strings.TrimSpace(stat.ParentNodeKey)
	action := strings.TrimSpace(stat.ActionKey)
	if parent == "" {
		return fmt.Errorf("store: upsert mcts edge stat: parent_node_key is required")
	}
	if action == "" {
		return fmt.Errorf("store: upsert mcts edge stat: action_key is required")
	}
	species := normalizeMCTSSpecies(stat.Species)
	lastOutcome := strings.TrimSpace(stat.LastOutcome)

	visits := stat.Visits
	if visits < 0 {
		visits = 0
	}
	wins := stat.Wins
	if wins < 0 {
		wins = 0
	}
	if wins > visits {
		wins = visits
	}

	totalCost := stat.TotalCostUSD
	if totalCost < 0 {
		totalCost = 0
	}
	totalDuration := stat.TotalDurationS
	if totalDuration < 0 {
		totalDuration = 0
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: upsert mcts edge stat: begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := ensureMCTSGraph(tx, parent, action); err != nil {
		return fmt.Errorf("store: upsert mcts edge stat: ensure graph: %w", err)
	}

	_, err = tx.Exec(`
		INSERT INTO mcts_edge_stats (
			parent_node_key, species, action_key, visits, wins,
			total_reward, total_cost_usd, total_duration_s, last_outcome, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(parent_node_key, species, action_key) DO UPDATE SET
			visits = excluded.visits,
			wins = excluded.wins,
			total_reward = excluded.total_reward,
			total_cost_usd = excluded.total_cost_usd,
			total_duration_s = excluded.total_duration_s,
			last_outcome = excluded.last_outcome,
			updated_at = datetime('now')
	`, parent, species, action, visits, wins, stat.TotalReward, totalCost, totalDuration, lastOutcome)
	if err != nil {
		return fmt.Errorf("store: upsert mcts edge stat: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: upsert mcts edge stat: commit: %w", err)
	}
	return nil
}

// ListMCTSEdgeStats returns persisted edge stats sorted by visits and reward.
func (s *Store) ListMCTSEdgeStats(parentNodeKey, species string, limit int) ([]MCTSEdgeStat, error) {
	parent := strings.TrimSpace(parentNodeKey)
	if parent == "" {
		return []MCTSEdgeStat{}, nil
	}

	if limit <= 0 {
		limit = defaultMCTSEdgeStatsLimit
	}
	if limit > maxMCTSEdgeStatsLimit {
		limit = maxMCTSEdgeStatsLimit
	}

	species = strings.TrimSpace(species)
	query := `
		SELECT parent_node_key, species, action_key, visits, wins,
		       total_reward, total_cost_usd, total_duration_s, last_outcome, updated_at
		FROM mcts_edge_stats
		WHERE parent_node_key = ?
	`
	args := []any{parent}
	if species != "" {
		query += " AND species = ?"
		args = append(args, normalizeMCTSSpecies(species))
	}
	query += " ORDER BY visits DESC, total_reward DESC, action_key ASC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list mcts edge stats: %w", err)
	}
	defer rows.Close()

	stats := make([]MCTSEdgeStat, 0, limit)
	for rows.Next() {
		var stat MCTSEdgeStat
		if err := rows.Scan(
			&stat.ParentNodeKey,
			&stat.Species,
			&stat.ActionKey,
			&stat.Visits,
			&stat.Wins,
			&stat.TotalReward,
			&stat.TotalCostUSD,
			&stat.TotalDurationS,
			&stat.LastOutcome,
			&stat.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("store: list mcts edge stats: scan: %w", err)
		}
		stats = append(stats, stat)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list mcts edge stats: rows: %w", err)
	}
	return stats, nil
}

// RecordMCTSRollout records one planner rollout and atomically updates edge stats.
func (s *Store) RecordMCTSRollout(rollout MCTSRollout) (int64, error) {
	parent := strings.TrimSpace(rollout.ParentNodeKey)
	action := strings.TrimSpace(rollout.ActionKey)
	if parent == "" {
		return 0, fmt.Errorf("store: record mcts rollout: parent_node_key is required")
	}
	if action == "" {
		return 0, fmt.Errorf("store: record mcts rollout: action_key is required")
	}

	species := normalizeMCTSSpecies(rollout.Species)
	taskID := strings.TrimSpace(rollout.TaskID)
	project := strings.TrimSpace(rollout.Project)
	outcome := strings.TrimSpace(rollout.Outcome)
	if outcome == "" {
		outcome = mctsOutcomeUnknown
	}
	metadataJSON := strings.TrimSpace(rollout.MetadataJSON)
	if metadataJSON == "" {
		metadataJSON = "{}"
	}

	costUSD := rollout.CostUSD
	if costUSD < 0 {
		costUSD = 0
	}
	durationS := rollout.DurationS
	if durationS < 0 {
		durationS = 0
	}

	win := 0
	if rollout.Reward > 0 || strings.EqualFold(outcome, "success") || strings.EqualFold(outcome, "started") {
		win = 1
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store: record mcts rollout: begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := ensureMCTSGraph(tx, parent, action); err != nil {
		return 0, fmt.Errorf("store: record mcts rollout: ensure graph: %w", err)
	}

	res, err := tx.Exec(`
		INSERT INTO mcts_rollouts (
			task_id, project, species, parent_node_key, action_key, outcome,
			reward, cost_usd, duration_s, metadata_json
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, taskID, project, species, parent, action, outcome, rollout.Reward, costUSD, durationS, metadataJSON)
	if err != nil {
		return 0, fmt.Errorf("store: record mcts rollout: insert: %w", err)
	}

	rolloutID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: record mcts rollout: get insert id: %w", err)
	}

	_, err = tx.Exec(`
		INSERT INTO mcts_edge_stats (
			parent_node_key, species, action_key, visits, wins,
			total_reward, total_cost_usd, total_duration_s, last_outcome, updated_at
		)
		VALUES (?, ?, ?, 1, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(parent_node_key, species, action_key) DO UPDATE SET
			visits = mcts_edge_stats.visits + 1,
			wins = mcts_edge_stats.wins + excluded.wins,
			total_reward = mcts_edge_stats.total_reward + excluded.total_reward,
			total_cost_usd = mcts_edge_stats.total_cost_usd + excluded.total_cost_usd,
			total_duration_s = mcts_edge_stats.total_duration_s + excluded.total_duration_s,
			last_outcome = excluded.last_outcome,
			updated_at = datetime('now')
	`, parent, species, action, win, rollout.Reward, costUSD, durationS, outcome)
	if err != nil {
		return 0, fmt.Errorf("store: record mcts rollout: upsert edge stats: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: record mcts rollout: commit: %w", err)
	}

	return rolloutID, nil
}

func normalizeMCTSSpecies(species string) string {
	species = strings.TrimSpace(species)
	if species == "" {
		return mctsDefaultSpecies
	}
	return species
}

func mctsChildNodeKey(parentNodeKey, actionKey string) string {
	return parentNodeKey + "::" + actionKey
}

func ensureMCTSGraph(tx *sql.Tx, parentNodeKey, actionKey string) error {
	childNodeKey := mctsChildNodeKey(parentNodeKey, actionKey)

	if _, err := tx.Exec(`
		INSERT INTO mcts_nodes (node_key, node_type)
		VALUES (?, 'root')
		ON CONFLICT(node_key) DO UPDATE SET updated_at = datetime('now')
	`, parentNodeKey); err != nil {
		return fmt.Errorf("upsert parent node: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO mcts_nodes (node_key, node_type)
		VALUES (?, 'action')
		ON CONFLICT(node_key) DO UPDATE SET updated_at = datetime('now')
	`, childNodeKey); err != nil {
		return fmt.Errorf("upsert child node: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO mcts_edges (parent_node_key, child_node_key, action_key)
		VALUES (?, ?, ?)
		ON CONFLICT(parent_node_key, action_key) DO UPDATE SET
			child_node_key = excluded.child_node_key
	`, parentNodeKey, childNodeKey, actionKey); err != nil {
		return fmt.Errorf("upsert edge: %w", err)
	}
	return nil
}
