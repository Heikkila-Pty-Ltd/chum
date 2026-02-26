package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Dispatch represents a dispatched agent task.
type Dispatch struct {
	ID                int64
	MorselID          string
	Project           string
	AgentID           string
	Provider          string
	Tier              string
	PID               int
	SessionName       string
	Prompt            string
	DispatchedAt      time.Time
	CompletedAt       sql.NullTime
	NextRetryAt       sql.NullTime
	Status            string // running, completed, failed
	Stage             string // dispatched, running, completed, failed, failed_needs_check, canceled, pending_retry
	Labels            string
	PRURL             string
	PRNumber          int
	ExitCode          int
	DurationS         float64
	Retries           int
	EscalatedFromTier string
	FailureCategory   string
	FailureSummary    string
	LogPath           string
	Branch            string
	Backend           string
	InputTokens       int
	OutputTokens      int
	CostUSD           float64
}

// OverflowQueueItem represents a persisted concurrency overflow queue item.
type OverflowQueueItem struct {
	ID         int64
	MorselID   string
	Project    string
	Role       string
	AgentID    string
	Priority   int
	EnqueuedAt time.Time
	Attempts   int
	Reason     string
}

// RecordDispatch inserts a new dispatch record and returns its ID.
func (s *Store) RecordDispatch(morselID, project, agent, provider, tier string, handle int, sessionName, prompt, logPath, branch, backend string) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO dispatches (morsel_id, project, agent_id, provider, tier, pid, session_name, stage, prompt, log_path, branch, backend) VALUES (?, ?, ?, ?, ?, ?, ?, 'dispatched', ?, ?, ?, ?)`,
		morselID, project, agent, provider, tier, handle, sessionName, prompt, logPath, branch, backend,
	)
	if err != nil {
		return 0, fmt.Errorf("store: record dispatch: %w", err)
	}
	return res.LastInsertId()
}

const (
	dispatchPersistFailpointBeforeInsert     = "before_insert"
	dispatchPersistFailpointAfterInsert      = "after_insert"
	dispatchPersistFailpointBeforeStageWrite = "before_stage_write"
)

// SetDispatchPersistHookForTesting configures a failpoint hook for transactional scheduler dispatch persistence.
func (s *Store) SetDispatchPersistHookForTesting(hook func(point string) error) {
	s.dispatchPersistHook = hook
}

func (s *Store) maybeFailDispatchPersist(point string) error {
	if s.dispatchPersistHook == nil {
		return nil
	}
	if err := s.dispatchPersistHook(point); err != nil {
		return fmt.Errorf("store: dispatch persist failpoint %s: %w", point, err)
	}
	return nil
}

// RecordSchedulerDispatch atomically persists the scheduler's dispatch row plus labels/stage updates.
func (s *Store) RecordSchedulerDispatch(morselID, project, agent, provider, tier string, handle int, sessionName, prompt, logPath, branch, backend string, labels []string) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("store: begin scheduler dispatch transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := s.maybeFailDispatchPersist(dispatchPersistFailpointBeforeInsert); err != nil {
		return 0, err
	}

	res, err := tx.Exec(
		`INSERT INTO dispatches (morsel_id, project, agent_id, provider, tier, pid, session_name, stage, prompt, log_path, branch, backend) VALUES (?, ?, ?, ?, ?, ?, ?, 'dispatched', ?, ?, ?, ?)`,
		morselID, project, agent, provider, tier, handle, sessionName, prompt, logPath, branch, backend,
	)
	if err != nil {
		return 0, fmt.Errorf("store: record scheduler dispatch: %w", err)
	}
	dispatchID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: read scheduler dispatch id: %w", err)
	}

	if err := s.maybeFailDispatchPersist(dispatchPersistFailpointAfterInsert); err != nil {
		return 0, err
	}

	normalizedLabels := encodeDispatchLabels(labels)
	if _, err := tx.Exec(`UPDATE dispatches SET labels = ? WHERE id = ?`, normalizedLabels, dispatchID); err != nil {
		return 0, fmt.Errorf("store: record scheduler dispatch labels: %w", err)
	}

	if err := s.maybeFailDispatchPersist(dispatchPersistFailpointBeforeStageWrite); err != nil {
		return 0, err
	}

	if _, err := tx.Exec(`UPDATE dispatches SET stage = ? WHERE id = ?`, "running", dispatchID); err != nil {
		return 0, fmt.Errorf("store: record scheduler dispatch stage: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: commit scheduler dispatch transaction: %w", err)
	}

	return dispatchID, nil
}

// UpdateDispatchStatus updates a dispatch's status, exit code, and duration.
func (s *Store) UpdateDispatchStatus(id int64, status string, exitCode int, durationS float64) error {
	_, err := s.db.Exec(
		`UPDATE dispatches SET status = ?, exit_code = ?, duration_s = ?, completed_at = datetime('now') WHERE id = ?`,
		status, exitCode, durationS, id,
	)
	if err != nil {
		return fmt.Errorf("store: update dispatch status: %w", err)
	}
	return nil
}

// MarkDispatchPendingRetry marks a failed dispatch for retry, increments retries,
// and updates the tier for the next retry attempt.
func (s *Store) MarkDispatchPendingRetry(id int64, nextTier string, nextRetryAt time.Time) error {
	query := `UPDATE dispatches
		 SET status = 'pending_retry',
		     stage = 'pending_retry',
		     completed_at = COALESCE(completed_at, datetime('now')),
		     retries = retries + 1,
		     tier = ?,
		     escalated_from_tier = CASE
		       WHEN escalated_from_tier = '' THEN tier
		       ELSE escalated_from_tier
		     END,
		     next_retry_at = ?
		 WHERE id = ?`

	var nextRetry interface{}
	if nextRetryAt.IsZero() {
		nextRetry = nil
	} else {
		nextRetry = nextRetryAt.UTC().Format(time.DateTime)
	}

	_, err := s.db.Exec(
		query,
		nextTier, nextRetry, id,
	)
	if err != nil {
		return fmt.Errorf("store: mark dispatch pending retry: %w", err)
	}
	return nil
}

// CountRecentDispatchesByFailureCategory counts dispatches diagnosed with category within a window.
func (s *Store) CountRecentDispatchesByFailureCategory(category string, window time.Duration) (int, error) {
	category = strings.TrimSpace(category)
	if category == "" {
		return 0, nil
	}
	if window <= 0 {
		window = time.Minute
	}
	cutoff := time.Now().Add(-window).UTC().Format(time.DateTime)
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM dispatches WHERE failure_category = ? AND completed_at IS NOT NULL AND completed_at >= ?`,
		category, cutoff,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("store: count recent failures by category: %w", err)
	}
	return count, nil
}

const dispatchCols = `id, morsel_id, project, agent_id, provider, tier, pid, session_name, prompt, dispatched_at, completed_at, next_retry_at, status, stage, labels, pr_url, pr_number, exit_code, duration_s, retries, escalated_from_tier, failure_category, failure_summary, log_path, branch, backend, input_tokens, output_tokens, cost_usd`

// GetRunningDispatches returns all dispatches with status 'running'.
func (s *Store) GetRunningDispatches() ([]Dispatch, error) {
	return s.queryDispatches(`SELECT ` + dispatchCols + ` FROM dispatches WHERE status = 'running'`)
}

// ProjectDispatchStatusCounts summarizes dispatch counts per project within a time window.
type ProjectDispatchStatusCounts struct {
	Project   string
	Running   int
	Completed int
	Failed    int
}

// GetProjectDispatchStatusCounts returns counts grouped by project for running/completed/failed dispatches.
func (s *Store) GetProjectDispatchStatusCounts(since time.Time) (map[string]ProjectDispatchStatusCounts, error) {
	query := `
		SELECT
			project,
			SUM(CASE WHEN status = 'running' THEN 1 ELSE 0 END) AS running_count,
			SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END) AS completed_count,
			SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END) AS failed_count
		FROM dispatches`

	args := make([]any, 0, 1)
	if !since.IsZero() {
		query += ` WHERE dispatched_at >= ?`
		args = append(args, since.UTC().Format(time.DateTime))
	}
	query += ` GROUP BY project`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: query project dispatch status counts: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]ProjectDispatchStatusCounts)
	for rows.Next() {
		var project string
		var running, completed, failed int
		if err := rows.Scan(&project, &running, &completed, &failed); err != nil {
			return nil, fmt.Errorf("store: scan project dispatch status counts: %w", err)
		}
		counts[project] = ProjectDispatchStatusCounts{
			Project:   project,
			Running:   running,
			Completed: completed,
			Failed:    failed,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate project dispatch status counts: %w", err)
	}
	return counts, nil
}

// GetStuckDispatches returns running dispatches older than the given timeout.
func (s *Store) GetStuckDispatches(timeout time.Duration) ([]Dispatch, error) {
	cutoff := time.Now().Add(-timeout).UTC().Format(time.DateTime)
	return s.queryDispatches(`SELECT `+dispatchCols+` FROM dispatches WHERE status = 'running' AND dispatched_at < ?`, cutoff)
}

// GetDispatchesByMorsel returns all dispatches for a given morsel ID, ordered by dispatched_at DESC.
func (s *Store) GetDispatchesByMorsel(morselID string) ([]Dispatch, error) {
	return s.queryDispatches(`SELECT `+dispatchCols+` FROM dispatches WHERE morsel_id = ? ORDER BY dispatched_at DESC`, morselID)
}

// GetCompletedDispatchesSince returns all completed dispatches for a project since the given time
func (s *Store) GetCompletedDispatchesSince(projectName, since string) ([]Dispatch, error) {
	return s.queryDispatches(`SELECT `+dispatchCols+` FROM dispatches WHERE project = ? AND status = 'completed' AND dispatched_at >= ? ORDER BY dispatched_at DESC`, projectName, since)
}

// WasMorselDispatchedRecently checks if a morsel has been dispatched within the cooldown period.
// Returns true if the morsel should be skipped due to recent dispatch activity.
func (s *Store) WasMorselDispatchedRecently(morselID string, cooldownPeriod time.Duration) (bool, error) {
	return s.WasMorselAgentDispatchedRecently(morselID, "", cooldownPeriod)
}

// WasMorselAgentDispatchedRecently checks if a morsel has been dispatched within the cooldown period.
// If agentID is empty, checks across all agents.
func (s *Store) WasMorselAgentDispatchedRecently(morselID, agentID string, cooldownPeriod time.Duration) (bool, error) {
	if cooldownPeriod <= 0 {
		return false, nil
	}

	cutoff := time.Now().Add(-cooldownPeriod).UTC()

	var count int
	var err error
	if agentID == "" {
		err = s.db.QueryRow(`
		SELECT COUNT(*) 
		FROM dispatches 
		WHERE morsel_id = ?
		  AND dispatched_at > ?
		  AND status IN ('running', 'completed', 'failed', 'cancelled', 'interrupted', 'pending_retry', 'retried')`,
			morselID, cutoff.Format(time.DateTime),
		).Scan(&count)
	} else {
		err = s.db.QueryRow(`
		SELECT COUNT(*)
		FROM dispatches
		WHERE morsel_id = ?
		  AND agent_id = ?
		  AND dispatched_at > ?
		  AND status IN ('running', 'completed', 'failed', 'cancelled', 'interrupted', 'pending_retry', 'retried')`,
			morselID, agentID, cutoff.Format(time.DateTime),
		).Scan(&count)
	}

	if err != nil {
		return false, fmt.Errorf("check recent dispatch: %w", err)
	}

	return count > 0, nil
}

// HasRecentConsecutiveFailures reports whether the most recent dispatches for a morsel
// are all failed, up to threshold, within the given window.
func (s *Store) HasRecentConsecutiveFailures(morselID string, threshold int, window time.Duration) (bool, error) {
	if threshold <= 0 {
		return false, nil
	}

	cutoff := time.Now().Add(-window).UTC().Format(time.DateTime)
	rows, err := s.db.Query(`
		SELECT status
		FROM dispatches
		WHERE morsel_id = ?
		  AND dispatched_at > ?
		  AND status IN ('failed', 'completed', 'cancelled', 'interrupted', 'retried', 'pending_retry', 'running')
		ORDER BY dispatched_at DESC
		LIMIT ?`,
		morselID, cutoff, threshold,
	)
	if err != nil {
		return false, fmt.Errorf("check recent consecutive failures: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			return false, fmt.Errorf("scan recent consecutive failures: %w", err)
		}
		if !isFailureStatusForQuarantine(status) {
			return false, nil
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate recent consecutive failures: %w", err)
	}
	return count >= threshold, nil
}

func isFailureStatusForQuarantine(status string) bool {
	switch status {
	case "failed", "canceled", "interrupted", "pending_retry", "retried":
		return true
	default:
		return false
	}
}

// GetDispatchByID returns a dispatch by its ID.
func (s *Store) GetDispatchByID(id int64) (*Dispatch, error) {
	dispatches, err := s.queryDispatches(`SELECT `+dispatchCols+` FROM dispatches WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(dispatches) == 0 {
		return nil, fmt.Errorf("dispatch not found: %d", id)
	}
	return &dispatches[0], nil
}

// GetLatestDispatchBySession returns the most recent dispatch for a session name.
func (s *Store) GetLatestDispatchBySession(sessionName string) (*Dispatch, error) {
	sessionName = strings.TrimSpace(sessionName)
	if sessionName == "" {
		return nil, nil
	}

	dispatches, err := s.queryDispatches(`SELECT `+dispatchCols+` FROM dispatches WHERE session_name = ? ORDER BY id DESC LIMIT 1`, sessionName)
	if err != nil {
		return nil, err
	}
	if len(dispatches) == 0 {
		return nil, nil
	}
	return &dispatches[0], nil
}

// GetLatestDispatchByPID returns the most recent dispatch for a PID.
func (s *Store) GetLatestDispatchByPID(pid int) (*Dispatch, error) {
	if pid <= 0 {
		return nil, nil
	}

	dispatches, err := s.queryDispatches(`SELECT `+dispatchCols+` FROM dispatches WHERE pid = ? ORDER BY id DESC LIMIT 1`, pid)
	if err != nil {
		return nil, err
	}
	if len(dispatches) == 0 {
		return nil, nil
	}
	return &dispatches[0], nil
}

// GetPendingRetryDispatches returns all dispatches with status "pending_retry", ordered by dispatched_at ASC.
func (s *Store) GetPendingRetryDispatches() ([]Dispatch, error) {
	return s.queryDispatches(`SELECT ` + dispatchCols + ` FROM dispatches WHERE status = 'pending_retry' AND (next_retry_at IS NULL OR next_retry_at <= datetime('now')) ORDER BY dispatched_at ASC`)
}

// EnqueueOverflowItem stores a workload in the overflow queue for concurrency throttling.
// Returns the row id, deduplicating morsel/role combinations so each pair is tracked once.
func (s *Store) EnqueueOverflowItem(morselID, project, role, agentID string, priority int, reason string) (int64, error) {
	morselID = strings.TrimSpace(morselID)
	project = strings.TrimSpace(project)
	role = strings.TrimSpace(role)
	agentID = strings.TrimSpace(agentID)
	reason = strings.TrimSpace(reason)

	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO overflow_queue (morsel_id, project, role, agent_id, priority, reason, enqueued_at, attempts)
		 VALUES (?, ?, ?, ?, ?, ?, datetime('now'), 0)`,
		morselID, project, role, agentID, priority, reason,
	)
	if err != nil {
		return 0, fmt.Errorf("store: enqueue overflow item: %w", err)
	}

	var id int64
	err = s.db.QueryRow(
		`SELECT id FROM overflow_queue WHERE morsel_id = ? AND role = ?`,
		morselID, role,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: get overflow queue id: %w", err)
	}
	return id, nil
}

// RemoveOverflowItem deletes all persisted queue items for a morsel.
func (s *Store) RemoveOverflowItem(morselID string) (int64, error) {
	morselID = strings.TrimSpace(morselID)
	result, err := s.db.Exec(`DELETE FROM overflow_queue WHERE morsel_id = ?`, morselID)
	if err != nil {
		return 0, fmt.Errorf("store: remove overflow item: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: overflow rows affected: %w", err)
	}
	return affected, nil
}

// ListOverflowQueue returns all pending items in the overflow queue ordered by priority.
func (s *Store) ListOverflowQueue() ([]OverflowQueueItem, error) {
	rows, err := s.db.Query(
		`SELECT id, morsel_id, project, role, agent_id, priority, enqueued_at, attempts, reason FROM overflow_queue ORDER BY priority ASC, enqueued_at ASC, morsel_id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list overflow queue: %w", err)
	}
	defer rows.Close()

	var items []OverflowQueueItem
	for rows.Next() {
		var item OverflowQueueItem
		if err := rows.Scan(&item.ID, &item.MorselID, &item.Project, &item.Role, &item.AgentID, &item.Priority, &item.EnqueuedAt, &item.Attempts, &item.Reason); err != nil {
			return nil, fmt.Errorf("store: scan overflow queue row: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate overflow queue rows: %w", err)
	}
	return items, nil
}

// CountOverflowQueue returns the number of persisted overflow queue items.
func (s *Store) CountOverflowQueue() (int, error) {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM overflow_queue`).Scan(&count); err != nil {
		return 0, fmt.Errorf("store: count overflow queue: %w", err)
	}
	return count, nil
}

// GetRunningDispatchStageCounts returns counts of running dispatches grouped by stage.
func (s *Store) GetRunningDispatchStageCounts() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT stage, COUNT(*) FROM dispatches WHERE status='running' GROUP BY stage`)
	if err != nil {
		return nil, fmt.Errorf("store: query running dispatch stage counts: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var stage string
		var count int
		if err := rows.Scan(&stage, &count); err != nil {
			return nil, fmt.Errorf("store: scan running dispatch stage count: %w", err)
		}
		if stage == "" {
			stage = "unknown"
		}
		counts[stage] = count
	}
	return counts, rows.Err()
}
func (s *Store) queryDispatches(query string, args ...any) ([]Dispatch, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: query dispatches: %w", err)
	}
	defer rows.Close()

	var dispatches []Dispatch
	for rows.Next() {
		var d Dispatch
		if err := rows.Scan(
			&d.ID, &d.MorselID, &d.Project, &d.AgentID, &d.Provider, &d.Tier, &d.PID, &d.SessionName,
			&d.Prompt, &d.DispatchedAt, &d.CompletedAt, &d.NextRetryAt, &d.Status, &d.Stage, &d.Labels, &d.PRURL, &d.PRNumber, &d.ExitCode, &d.DurationS,
			&d.Retries, &d.EscalatedFromTier, &d.FailureCategory, &d.FailureSummary, &d.LogPath, &d.Branch, &d.Backend,
			&d.InputTokens, &d.OutputTokens, &d.CostUSD,
		); err != nil {
			return nil, fmt.Errorf("store: scan dispatch: %w", err)
		}
		dispatches = append(dispatches, d)
	}
	return dispatches, rows.Err()
}

// UpdateDispatchLabels stores morsel labels on a dispatch for downstream profiling.
func (s *Store) UpdateDispatchLabels(id int64, labels []string) error {
	return s.UpdateDispatchLabelsCSV(id, encodeDispatchLabels(labels))
}

// UpdateDispatchLabelsCSV stores an already-serialized labels value, normalized.
func (s *Store) UpdateDispatchLabelsCSV(id int64, labelsCSV string) error {
	normalized := encodeDispatchLabels(decodeDispatchLabels(labelsCSV))
	_, err := s.db.Exec(`UPDATE dispatches SET labels = ? WHERE id = ?`, normalized, id)
	if err != nil {
		return fmt.Errorf("store: update dispatch labels: %w", err)
	}
	return nil
}

// UpdateFailureDiagnosis stores failure category and summary for a dispatch.
func (s *Store) UpdateFailureDiagnosis(id int64, category, summary string) error {
	_, err := s.db.Exec(
		`UPDATE dispatches SET failure_category = ?, failure_summary = ? WHERE id = ?`,
		category, summary, id,
	)
	if err != nil {
		return fmt.Errorf("store: update failure diagnosis: %w", err)
	}
	return nil
}

// RecordProviderUsage records an authed provider dispatch for rate limiting.
func (s *Store) RecordProviderUsage(provider, agentID, morselID string) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO provider_usage (provider, agent_id, morsel_id) VALUES (?, ?, ?)`,
		provider, agentID, morselID,
	)
	if err != nil {
		return 0, fmt.Errorf("store: record provider usage: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: record provider usage id: %w", err)
	}
	return id, nil
}

// DeleteProviderUsage removes a previously recorded usage row by provider_usage row id.
func (s *Store) DeleteProviderUsage(id int64) error {
	if id == 0 {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM provider_usage WHERE rowid = ?`, id)
	if err != nil {
		return fmt.Errorf("store: delete provider usage: %w", err)
	}
	return nil
}

// CountAuthedUsage5h counts provider usage records in the last 5 hours.
func (s *Store) CountAuthedUsage5h() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM provider_usage WHERE dispatched_at >= datetime('now', '-5 hours')`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("store: count 5h usage: %w", err)
	}
	return count, nil
}

// CountAuthedUsageWeekly counts provider usage records in the last 7 days.
func (s *Store) CountAuthedUsageWeekly() (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM provider_usage WHERE dispatched_at >= datetime('now', '-7 days')`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("store: count weekly usage: %w", err)
	}
	return count, nil
}

// IsMorselDispatched checks if a morsel currently has a running dispatch.
func (s *Store) IsMorselDispatched(morselID string) (bool, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM dispatches WHERE morsel_id = ? AND status = 'running'`, morselID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("store: check morsel dispatched: %w", err)
	}
	return count > 0, nil
}

// IsAgentBusy checks if an agent has a running dispatch for the given project.
func (s *Store) IsAgentBusy(project, agent string) (bool, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM dispatches WHERE project = ? AND agent_id = ? AND status = 'running'`,
		project, agent,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("store: check agent busy: %w", err)
	}
	return count > 0, nil
}

// RecordDispatchCost updates token counts and cost for a completed dispatch.
func (s *Store) RecordDispatchCost(dispatchID int64, inputTokens, outputTokens int, costUSD float64) error {
	_, err := s.db.Exec(
		`UPDATE dispatches SET input_tokens = ?, output_tokens = ?, cost_usd = ? WHERE id = ?`,
		inputTokens, outputTokens, costUSD, dispatchID,
	)
	if err != nil {
		return fmt.Errorf("store: record dispatch cost: %w", err)
	}
	return nil
}

// RecordDoDResult records the results of a Definition of Done check.
func (s *Store) RecordDoDResult(dispatchID int64, morselID, project string, passed bool, failures string, checkResults string) error {
	_, err := s.db.Exec(
		`INSERT INTO dod_results (dispatch_id, morsel_id, project, passed, failures, check_results) VALUES (?, ?, ?, ?, ?, ?)`,
		dispatchID, morselID, project, passed, failures, checkResults,
	)
	if err != nil {
		return fmt.Errorf("store: record DoD result: %w", err)
	}
	return nil
}

// GetDispatchCost returns token usage and cost for a dispatch.
func (s *Store) GetDispatchCost(dispatchID int64) (inputTokens, outputTokens int, costUSD float64, err error) {
	err = s.db.QueryRow(
		`SELECT input_tokens, output_tokens, cost_usd FROM dispatches WHERE id = ?`,
		dispatchID,
	).Scan(&inputTokens, &outputTokens, &costUSD)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("store: get dispatch cost: %w", err)
	}
	return inputTokens, outputTokens, costUSD, nil
}

// GetTotalCost returns total cost in USD for a given project (or all projects if empty).
func (s *Store) GetTotalCost(project string) (float64, error) {
	var query string
	var args []any

	if project == "" {
		query = `SELECT COALESCE(SUM(cost_usd), 0) FROM dispatches`
	} else {
		query = `SELECT COALESCE(SUM(cost_usd), 0) FROM dispatches WHERE project = ?`
		args = []any{project}
	}

	var totalCost float64
	err := s.db.QueryRow(query, args...).Scan(&totalCost)
	if err != nil {
		return 0, fmt.Errorf("store: get total cost: %w", err)
	}
	return totalCost, nil
}

// GetTotalCostSince returns total completed dispatch cost since the provided timestamp.
// When project is non-empty, totals are scoped to that project.
func (s *Store) GetTotalCostSince(project string, since time.Time) (float64, error) {
	query := `SELECT COALESCE(SUM(cost_usd), 0) FROM dispatches WHERE status = 'completed' AND completed_at >= ?`
	args := []any{since.UTC().Format(time.DateTime)}

	if strings.TrimSpace(project) != "" {
		query += ` AND project = ?`
		args = append(args, project)
	}

	var totalCost float64
	err := s.db.QueryRow(query, args...).Scan(&totalCost)
	if err != nil {
		return 0, fmt.Errorf("store: get total cost since: %w", err)
	}
	return totalCost, nil
}

// CountDispatchesSince counts dispatches since the provided timestamp.
// If statuses is non-empty, counts only rows whose status matches one of the provided values.
func (s *Store) CountDispatchesSince(since time.Time, statuses []string) (int, error) {
	query := `SELECT COUNT(*) FROM dispatches WHERE dispatched_at >= ?`
	args := []any{since.UTC().Format(time.DateTime)}

	var cleaned []string
	for _, status := range statuses {
		if trimmed := strings.TrimSpace(status); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	if len(cleaned) > 0 {
		placeholders := strings.Repeat("?,", len(cleaned))
		query += ` AND status IN (` + strings.TrimSuffix(placeholders, ",") + `)`
		for _, status := range cleaned {
			args = append(args, status)
		}
	}

	var count int
	err := s.db.QueryRow(query, args...).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("store: count dispatches since: %w", err)
	}
	return count, nil
}

// InterruptRunningDispatches marks all running dispatches as interrupted.
// Returns the count of affected rows.
func (s *Store) InterruptRunningDispatches() (int, error) {
	res, err := s.db.Exec(
		`UPDATE dispatches SET status = 'interrupted', completed_at = datetime('now') WHERE status = 'running'`,
	)
	if err != nil {
		return 0, fmt.Errorf("store: interrupt running dispatches: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: get rows affected: %w", err)
	}
	return int(affected), nil
}

// SetDispatchTime updates the dispatched_at time for a dispatch (used in testing).
func (s *Store) SetDispatchTime(id int64, dispatchedAt time.Time) error {
	_, err := s.db.Exec(
		`UPDATE dispatches SET dispatched_at = ? WHERE id = ?`,
		dispatchedAt.UTC(), id,
	)
	if err != nil {
		return fmt.Errorf("store: set dispatch time: %w", err)
	}
	return nil
}
func decodeDispatchLabels(labelsCSV string) []string {
	parts := strings.Split(labelsCSV, ",")
	return normalizeDispatchLabels(parts)
}

func encodeDispatchLabels(labels []string) string {
	return strings.Join(normalizeDispatchLabels(labels), ",")
}

func normalizeDispatchLabels(labels []string) []string {
	seen := make(map[string]struct{}, len(labels))
	out := make([]string, 0, len(labels))
	for _, raw := range labels {
		label := strings.TrimSpace(raw)
		if label == "" {
			continue
		}
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		out = append(out, label)
	}
	return out
}
