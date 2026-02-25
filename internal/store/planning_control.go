package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// PlanningStateSnapshot stores one serialized planning state checkpoint.
type PlanningStateSnapshot struct {
	ID        int64
	SessionID string
	RunID     string
	Project   string
	TaskID    string
	Cycle     int
	Stage     string
	StateHash string
	StateJSON string
	Stable    bool
	Reason    string
	CreatedAt time.Time
}

// PlanningBlacklistEntry blocks repeating a failed action from the same state.
type PlanningBlacklistEntry struct {
	ID         int64
	SessionID  string
	Project    string
	TaskID     string
	Cycle      int
	Stage      string
	StateHash  string
	ActionHash string
	Reason     string
	Metadata   string
	CreatedAt  time.Time
}

// PlanningCandidateScore captures persistent per-project option weighting.
// This enables cross-session adaptation of planning candidate ranking.
type PlanningCandidateScore struct {
	Project         string
	OptionID        string
	ScoreAdjustment float64
	Successes       int
	Failures        int
	LastReason      string
	UpdatedAt       time.Time
}

func migratePlanningControlTables(db *sql.DB) error {
	stmts := []struct {
		name string
		sql  string
	}{
		{
			name: "planning_state_snapshots table",
			sql: `
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
				)`,
		},
		{
			name: "idx_planning_snapshots_session_created",
			sql:  `CREATE INDEX IF NOT EXISTS idx_planning_snapshots_session_created ON planning_state_snapshots(session_id, created_at)`,
		},
		{
			name: "idx_planning_snapshots_state_hash",
			sql:  `CREATE INDEX IF NOT EXISTS idx_planning_snapshots_state_hash ON planning_state_snapshots(state_hash)`,
		},
		{
			name: "planning_action_blacklist table",
			sql: `
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
				)`,
		},
		{
			name: "idx_planning_blacklist_session_created",
			sql:  `CREATE INDEX IF NOT EXISTS idx_planning_blacklist_session_created ON planning_action_blacklist(session_id, created_at)`,
		},
		{
			name: "idx_planning_blacklist_state_action",
			sql:  `CREATE INDEX IF NOT EXISTS idx_planning_blacklist_state_action ON planning_action_blacklist(state_hash, action_hash)`,
		},
		{
			name: "planning_candidate_scores table",
			sql: `
				CREATE TABLE IF NOT EXISTS planning_candidate_scores (
					project TEXT NOT NULL,
					option_id TEXT NOT NULL,
					score_adjustment REAL NOT NULL DEFAULT 0,
					successes INTEGER NOT NULL DEFAULT 0,
					failures INTEGER NOT NULL DEFAULT 0,
					last_reason TEXT NOT NULL DEFAULT '',
					updated_at DATETIME NOT NULL DEFAULT (datetime('now')),
					PRIMARY KEY(project, option_id)
				)`,
		},
		{
			name: "idx_planning_candidate_scores_project_updated",
			sql:  `CREATE INDEX IF NOT EXISTS idx_planning_candidate_scores_project_updated ON planning_candidate_scores(project, updated_at)`,
		},
	}

	for _, stmt := range stmts {
		if _, err := db.Exec(stmt.sql); err != nil {
			return fmt.Errorf("create %s: %w", stmt.name, err)
		}
	}
	return nil
}

// RecordPlanningStateSnapshot persists a planning state checkpoint.
func (s *Store) RecordPlanningStateSnapshot(snapshot PlanningStateSnapshot) error {
	sessionID := strings.TrimSpace(snapshot.SessionID)
	if sessionID == "" {
		return fmt.Errorf("store: record planning snapshot: session_id is required")
	}
	stateHash := strings.TrimSpace(snapshot.StateHash)
	if stateHash == "" {
		return fmt.Errorf("store: record planning snapshot: state_hash is required")
	}

	cycle := snapshot.Cycle
	if cycle < 0 {
		cycle = 0
	}

	stateJSON := strings.TrimSpace(snapshot.StateJSON)
	if stateJSON == "" {
		stateJSON = "{}"
	}

	stableInt := 0
	if snapshot.Stable {
		stableInt = 1
	}

	_, err := s.db.Exec(`
		INSERT INTO planning_state_snapshots (
			session_id, run_id, project, task_id, cycle, stage,
			state_hash, state_json, stable, reason
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		sessionID,
		strings.TrimSpace(snapshot.RunID),
		strings.TrimSpace(snapshot.Project),
		strings.TrimSpace(snapshot.TaskID),
		cycle,
		strings.TrimSpace(snapshot.Stage),
		stateHash,
		stateJSON,
		stableInt,
		strings.TrimSpace(snapshot.Reason),
	)
	if err != nil {
		return fmt.Errorf("store: record planning snapshot: %w", err)
	}
	return nil
}

// GetLatestStablePlanningSnapshot returns the most recent stable snapshot for a session.
func (s *Store) GetLatestStablePlanningSnapshot(sessionID string) (*PlanningStateSnapshot, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, nil
	}

	var snapshot PlanningStateSnapshot
	var stableInt int
	err := s.db.QueryRow(`
		SELECT id, session_id, run_id, project, task_id, cycle, stage,
		       state_hash, state_json, stable, reason, created_at
		FROM planning_state_snapshots
		WHERE session_id = ? AND stable = 1
		ORDER BY id DESC
		LIMIT 1
	`, sessionID).Scan(
		&snapshot.ID,
		&snapshot.SessionID,
		&snapshot.RunID,
		&snapshot.Project,
		&snapshot.TaskID,
		&snapshot.Cycle,
		&snapshot.Stage,
		&snapshot.StateHash,
		&snapshot.StateJSON,
		&stableInt,
		&snapshot.Reason,
		&snapshot.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("store: get latest stable planning snapshot: %w", err)
	}
	snapshot.Stable = stableInt == 1
	return &snapshot, nil
}

// AddPlanningBlacklistEntry records a blocked state-action pair.
func (s *Store) AddPlanningBlacklistEntry(entry PlanningBlacklistEntry) error {
	sessionID := strings.TrimSpace(entry.SessionID)
	stateHash := strings.TrimSpace(entry.StateHash)
	actionHash := strings.TrimSpace(entry.ActionHash)
	if sessionID == "" || stateHash == "" || actionHash == "" {
		return fmt.Errorf("store: add planning blacklist entry: session_id, state_hash, and action_hash are required")
	}

	metadata := strings.TrimSpace(entry.Metadata)
	if metadata == "" {
		metadata = "{}"
	}

	cycle := entry.Cycle
	if cycle < 0 {
		cycle = 0
	}

	_, err := s.db.Exec(`
		INSERT INTO planning_action_blacklist (
			session_id, project, task_id, cycle, stage, state_hash, action_hash, reason, metadata_json
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id, state_hash, action_hash) DO UPDATE SET
			reason = excluded.reason,
			metadata_json = excluded.metadata_json
	`,
		sessionID,
		strings.TrimSpace(entry.Project),
		strings.TrimSpace(entry.TaskID),
		cycle,
		strings.TrimSpace(entry.Stage),
		stateHash,
		actionHash,
		strings.TrimSpace(entry.Reason),
		metadata,
	)
	if err != nil {
		return fmt.Errorf("store: add planning blacklist entry: %w", err)
	}
	return nil
}

// IsPlanningActionBlacklisted checks if a state-action pair is already blocked.
func (s *Store) IsPlanningActionBlacklisted(sessionID, stateHash, actionHash string) (bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	stateHash = strings.TrimSpace(stateHash)
	actionHash = strings.TrimSpace(actionHash)
	if sessionID == "" || stateHash == "" || actionHash == "" {
		return false, nil
	}

	var count int
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM planning_action_blacklist
		WHERE session_id = ? AND state_hash = ? AND action_hash = ?
	`, sessionID, stateHash, actionHash).Scan(&count); err != nil {
		return false, fmt.Errorf("store: check planning action blacklist: %w", err)
	}
	return count > 0, nil
}

// ListPlanningCandidateScores returns persisted score adjustments for the given project options.
func (s *Store) ListPlanningCandidateScores(project string, optionIDs []string) ([]PlanningCandidateScore, error) {
	project = strings.TrimSpace(project)
	if project == "" || len(optionIDs) == 0 {
		return []PlanningCandidateScore{}, nil
	}

	seen := make(map[string]struct{}, len(optionIDs))
	normalized := make([]string, 0, len(optionIDs))
	for _, id := range optionIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	if len(normalized) == 0 {
		return []PlanningCandidateScore{}, nil
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(normalized)), ",")
	query := fmt.Sprintf(`
		SELECT project, option_id, score_adjustment, successes, failures, last_reason, updated_at
		FROM planning_candidate_scores
		WHERE project = ? AND option_id IN (%s)
		ORDER BY score_adjustment DESC, updated_at DESC
	`, placeholders)

	args := make([]any, 0, len(normalized)+1)
	args = append(args, project)
	for _, id := range normalized {
		args = append(args, id)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list planning candidate scores: %w", err)
	}
	defer rows.Close()

	scores := make([]PlanningCandidateScore, 0, len(normalized))
	for rows.Next() {
		var rec PlanningCandidateScore
		if err := rows.Scan(
			&rec.Project,
			&rec.OptionID,
			&rec.ScoreAdjustment,
			&rec.Successes,
			&rec.Failures,
			&rec.LastReason,
			&rec.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("store: list planning candidate scores: scan: %w", err)
		}
		scores = append(scores, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list planning candidate scores: rows: %w", err)
	}
	return scores, nil
}

// AdjustPlanningCandidateScore increments a project's option adjustment and outcome counters.
func (s *Store) AdjustPlanningCandidateScore(project, optionID string, delta float64, outcome, reason string) error {
	project = strings.TrimSpace(project)
	optionID = strings.TrimSpace(optionID)
	if project == "" || optionID == "" {
		return fmt.Errorf("store: adjust planning candidate score: project and option_id are required")
	}

	successInc := 0
	failureInc := 0
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "success", "agreed", "selected":
		successInc = 1
	case "failure", "blocked", "rejected", "penalized", "fallback":
		failureInc = 1
	}

	if delta > 50 {
		delta = 50
	}
	if delta < -50 {
		delta = -50
	}

	_, err := s.db.Exec(`
		INSERT INTO planning_candidate_scores (
			project, option_id, score_adjustment, successes, failures, last_reason
		)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(project, option_id) DO UPDATE SET
			score_adjustment = MIN(50.0, MAX(-50.0, planning_candidate_scores.score_adjustment + excluded.score_adjustment)),
			successes = planning_candidate_scores.successes + excluded.successes,
			failures = planning_candidate_scores.failures + excluded.failures,
			last_reason = excluded.last_reason,
			updated_at = datetime('now')
	`,
		project,
		optionID,
		delta,
		successInc,
		failureInc,
		strings.TrimSpace(reason),
	)
	if err != nil {
		return fmt.Errorf("store: adjust planning candidate score: %w", err)
	}
	return nil
}
