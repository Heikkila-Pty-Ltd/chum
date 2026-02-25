package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// CortexMemory is an observational pattern with UCB1-based scoring,
// recording what approaches were observed to work under what conditions.
// Part of the Graph-Brain declarative memory system.
type CortexMemory struct {
	MemoryID         string     `json:"memory_id"`
	MemoryType       string     `json:"memory_type"`    // solution_path, phase_sequence, tool_chain, context_hint
	Species          string     `json:"species"`         // task species (empty = universal)
	Signature        string     `json:"signature"`       // content hash for dedup
	Description      string     `json:"description"`
	PatternJSON      string     `json:"pattern_json"`
	VisitCount       int        `json:"visit_count"`     // N for UCB1
	WinCount         int        `json:"win_count"`       // successful visits
	TotalReward      float64    `json:"total_reward"`
	AvgReward        float64    `json:"avg_reward"`      // total_reward / visit_count
	UCB1Score        float64    `json:"ucb1_score"`
	SourceSessions   string     `json:"source_sessions"` // JSON array of session IDs
	LastReinforcedAt *time.Time `json:"last_reinforced_at,omitempty"`
	LastAccessedAt   *time.Time `json:"last_accessed_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

const ucb1ExplorationConstant = 1.414 // sqrt(2)

// computeUCB1 calculates the UCB1 score for exploration vs. exploitation.
// parentVisits is the total visit count across all memories in the same group.
// Unvisited memories return MaxFloat64 to ensure exploration.
func computeUCB1(avgReward float64, visitCount, parentVisits int) float64 {
	if visitCount == 0 {
		return math.MaxFloat64
	}
	if parentVisits <= 0 {
		parentVisits = 1
	}
	exploitation := avgReward
	exploration := ucb1ExplorationConstant * math.Sqrt(math.Log(float64(parentVisits))/float64(visitCount))
	return exploitation + exploration
}

// migrateCortexMemories ensures the cortex_memories table and indexes exist.
func migrateCortexMemories(db *sql.DB) error {
	stmts := []struct {
		name string
		sql  string
	}{
		{
			name: "cortex_memories table",
			sql: `CREATE TABLE IF NOT EXISTS cortex_memories (
				memory_id TEXT PRIMARY KEY,
				memory_type TEXT NOT NULL,
				species TEXT NOT NULL DEFAULT '',
				signature TEXT NOT NULL,
				description TEXT NOT NULL DEFAULT '',
				pattern_json TEXT NOT NULL DEFAULT '{}',
				visit_count INTEGER NOT NULL DEFAULT 0,
				win_count INTEGER NOT NULL DEFAULT 0,
				total_reward REAL NOT NULL DEFAULT 0,
				avg_reward REAL NOT NULL DEFAULT 0,
				ucb1_score REAL NOT NULL DEFAULT 0,
				source_sessions TEXT NOT NULL DEFAULT '[]',
				last_reinforced_at DATETIME,
				last_accessed_at DATETIME,
				created_at DATETIME NOT NULL DEFAULT (datetime('now')),
				updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
			)`,
		},
		{name: "idx_cortex_memories_signature", sql: `CREATE UNIQUE INDEX IF NOT EXISTS idx_cortex_memories_signature ON cortex_memories(signature)`},
		{name: "idx_cortex_memories_species_type", sql: `CREATE INDEX IF NOT EXISTS idx_cortex_memories_species_type ON cortex_memories(species, memory_type)`},
		{name: "idx_cortex_memories_ucb1", sql: `CREATE INDEX IF NOT EXISTS idx_cortex_memories_ucb1 ON cortex_memories(ucb1_score DESC)`},
		{name: "idx_cortex_memories_species_ucb1", sql: `CREATE INDEX IF NOT EXISTS idx_cortex_memories_species_ucb1 ON cortex_memories(species, ucb1_score DESC)`},
		{name: "idx_cortex_memories_type", sql: `CREATE INDEX IF NOT EXISTS idx_cortex_memories_type ON cortex_memories(memory_type)`},
	}

	for _, stmt := range stmts {
		if _, err := db.Exec(stmt.sql); err != nil {
			return fmt.Errorf("create %s: %w", stmt.name, err)
		}
	}
	return nil
}

// RecordCortexMemory inserts or updates a cortex memory, deduplicating by signature.
// On conflict (same signature), updates description, pattern_json, and source_sessions
// but preserves stats. Returns the memory_id (generated if not provided).
func (s *Store) RecordCortexMemory(ctx context.Context, mem *CortexMemory) (string, error) {
	if mem.Signature == "" {
		return "", fmt.Errorf("store: record cortex memory: signature is required")
	}
	if mem.MemoryType == "" {
		return "", fmt.Errorf("store: record cortex memory: memory_type is required")
	}
	if mem.MemoryID == "" {
		mem.MemoryID = generateEventID()
	}
	if mem.PatternJSON == "" {
		mem.PatternJSON = "{}"
	}
	if mem.SourceSessions == "" {
		mem.SourceSessions = "[]"
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO cortex_memories (
			memory_id, memory_type, species, signature, description,
			pattern_json, visit_count, win_count, total_reward, avg_reward,
			ucb1_score, source_sessions, last_reinforced_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, datetime('now'), datetime('now'))
		ON CONFLICT(signature) DO UPDATE SET
			description = excluded.description,
			pattern_json = excluded.pattern_json,
			source_sessions = excluded.source_sessions,
			updated_at = datetime('now')
	`,
		mem.MemoryID,
		mem.MemoryType,
		mem.Species,
		mem.Signature,
		mem.Description,
		mem.PatternJSON,
		mem.VisitCount,
		mem.WinCount,
		mem.TotalReward,
		mem.AvgReward,
		mem.UCB1Score,
		mem.SourceSessions,
	)
	if err != nil {
		return "", fmt.Errorf("store: record cortex memory: %w", err)
	}
	return mem.MemoryID, nil
}

// ReinforceCortexMemory increments visit/win/reward counters for a memory
// and recomputes the UCB1 score. The sessionID is appended to source_sessions.
func (s *Store) ReinforceCortexMemory(ctx context.Context, memoryID string, reward float64, sessionID string) error {
	if memoryID == "" {
		return fmt.Errorf("store: reinforce cortex memory: memory_id is required")
	}

	win := 0
	if reward > 0 {
		win = 1
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: reinforce cortex memory: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is a no-op

	// Update counters
	_, err = tx.ExecContext(ctx, `
		UPDATE cortex_memories SET
			visit_count = visit_count + 1,
			win_count = win_count + ?,
			total_reward = total_reward + ?,
			avg_reward = (total_reward + ?) / (visit_count + 1),
			last_reinforced_at = datetime('now'),
			updated_at = datetime('now')
		WHERE memory_id = ?
	`, win, reward, reward, memoryID)
	if err != nil {
		return fmt.Errorf("store: reinforce cortex memory: update counters: %w", err)
	}

	// Append session to source_sessions (Go-side JSON manipulation)
	if sessionID != "" {
		var sessionsJSON string
		err = tx.QueryRowContext(ctx, `SELECT source_sessions FROM cortex_memories WHERE memory_id = ?`, memoryID).Scan(&sessionsJSON)
		if err != nil {
			return fmt.Errorf("store: reinforce cortex memory: read sessions: %w", err)
		}

		var sessions []string
		if unmarshalErr := json.Unmarshal([]byte(sessionsJSON), &sessions); unmarshalErr != nil {
			sessions = []string{}
		}
		sessions = append(sessions, sessionID)
		updated, marshalErr := json.Marshal(sessions)
		if marshalErr != nil {
			return fmt.Errorf("store: reinforce cortex memory: marshal sessions: %w", marshalErr)
		}

		_, err = tx.ExecContext(ctx, `UPDATE cortex_memories SET source_sessions = ? WHERE memory_id = ?`, string(updated), memoryID)
		if err != nil {
			return fmt.Errorf("store: reinforce cortex memory: update sessions: %w", err)
		}
	}

	// Recompute UCB1 score (Go-side computation)
	var avgReward float64
	var visitCount int
	var totalParentVisits int
	err = tx.QueryRowContext(ctx, `
		SELECT cm.avg_reward, cm.visit_count,
		       COALESCE((SELECT SUM(visit_count) FROM cortex_memories cm2
		                 WHERE cm2.species = cm.species AND cm2.memory_type = cm.memory_type), 1)
		FROM cortex_memories cm WHERE cm.memory_id = ?
	`, memoryID).Scan(&avgReward, &visitCount, &totalParentVisits)
	if err != nil {
		return fmt.Errorf("store: reinforce cortex memory: read stats: %w", err)
	}

	ucb1 := computeUCB1(avgReward, visitCount, totalParentVisits)

	_, err = tx.ExecContext(ctx, `UPDATE cortex_memories SET ucb1_score = ? WHERE memory_id = ?`, ucb1, memoryID)
	if err != nil {
		return fmt.Errorf("store: reinforce cortex memory: update ucb1: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: reinforce cortex memory: commit: %w", err)
	}
	return nil
}

// GetCortexMemory retrieves a single cortex memory by ID.
func (s *Store) GetCortexMemory(ctx context.Context, memoryID string) (*CortexMemory, error) {
	var mem CortexMemory
	var lastReinforced, lastAccessed sql.NullTime

	err := s.db.QueryRowContext(ctx, `
		SELECT memory_id, memory_type, species, signature, description,
		       pattern_json, visit_count, win_count, total_reward, avg_reward,
		       ucb1_score, source_sessions, last_reinforced_at, last_accessed_at,
		       created_at, updated_at
		FROM cortex_memories
		WHERE memory_id = ?
	`, memoryID).Scan(
		&mem.MemoryID, &mem.MemoryType, &mem.Species, &mem.Signature, &mem.Description,
		&mem.PatternJSON, &mem.VisitCount, &mem.WinCount, &mem.TotalReward, &mem.AvgReward,
		&mem.UCB1Score, &mem.SourceSessions, &lastReinforced, &lastAccessed,
		&mem.CreatedAt, &mem.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get cortex memory: %w", err)
	}

	if lastReinforced.Valid {
		mem.LastReinforcedAt = &lastReinforced.Time
	}
	if lastAccessed.Valid {
		mem.LastAccessedAt = &lastAccessed.Time
	}

	return &mem, nil
}

// QueryCortexMemories returns memories filtered by species and type, ordered by UCB1 score descending.
// Empty species matches all species. Empty memType matches all types.
// Universal memories (species='') are included when querying for a specific species.
func (s *Store) QueryCortexMemories(ctx context.Context, species, memType string, limit int) ([]*CortexMemory, error) {
	if limit <= 0 {
		limit = 10
	}

	query := `
		SELECT memory_id, memory_type, species, signature, description,
		       pattern_json, visit_count, win_count, total_reward, avg_reward,
		       ucb1_score, source_sessions, last_reinforced_at, last_accessed_at,
		       created_at, updated_at
		FROM cortex_memories
		WHERE 1=1
	`
	args := []any{}

	if species != "" {
		query += " AND (species = ? OR species = '')"
		args = append(args, species)
	}
	if memType != "" {
		query += " AND memory_type = ?"
		args = append(args, memType)
	}

	query += " ORDER BY ucb1_score DESC LIMIT ?"
	args = append(args, limit)

	return s.scanCortexMemories(ctx, query, args...)
}

// GetTopCortexMemories returns the top memories for a species across all types.
func (s *Store) GetTopCortexMemories(ctx context.Context, species string, limit int) ([]*CortexMemory, error) {
	return s.QueryCortexMemories(ctx, species, "", limit)
}

// TouchCortexMemory updates the last_accessed_at timestamp.
func (s *Store) TouchCortexMemory(ctx context.Context, memoryID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE cortex_memories SET last_accessed_at = datetime('now') WHERE memory_id = ?
	`, memoryID)
	if err != nil {
		return fmt.Errorf("store: touch cortex memory: %w", err)
	}
	return nil
}

// DecayCortexMemories applies a multiplicative decay to UCB1 scores for memories
// not reinforced since olderThan. Returns the number of memories decayed.
// Factor must be in (0, 1) — e.g. 0.9 reduces scores by 10%.
func (s *Store) DecayCortexMemories(ctx context.Context, olderThan time.Time, factor float64) (int, error) {
	if factor <= 0 || factor >= 1 {
		return 0, fmt.Errorf("store: decay cortex memories: factor must be in (0, 1), got %f", factor)
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE cortex_memories SET
			ucb1_score = ucb1_score * ?,
			updated_at = datetime('now')
		WHERE (last_reinforced_at IS NULL OR last_reinforced_at < ?)
		  AND visit_count > 0
	`, factor, olderThan.UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		return 0, fmt.Errorf("store: decay cortex memories: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: decay cortex memories: rows affected: %w", err)
	}
	return int(affected), nil
}

// scanCortexMemories is a shared row scanner for cortex memory queries.
func (s *Store) scanCortexMemories(ctx context.Context, query string, args ...any) ([]*CortexMemory, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: query cortex memories: %w", err)
	}
	defer rows.Close()

	var memories []*CortexMemory
	for rows.Next() {
		var mem CortexMemory
		var lastReinforced, lastAccessed sql.NullTime

		if err := rows.Scan(
			&mem.MemoryID, &mem.MemoryType, &mem.Species, &mem.Signature, &mem.Description,
			&mem.PatternJSON, &mem.VisitCount, &mem.WinCount, &mem.TotalReward, &mem.AvgReward,
			&mem.UCB1Score, &mem.SourceSessions, &lastReinforced, &lastAccessed,
			&mem.CreatedAt, &mem.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("store: scan cortex memory: %w", err)
		}

		if lastReinforced.Valid {
			mem.LastReinforcedAt = &lastReinforced.Time
		}
		if lastAccessed.Valid {
			mem.LastAccessedAt = &lastAccessed.Time
		}

		memories = append(memories, &mem)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: query cortex memories rows: %w", err)
	}
	return memories, nil
}
