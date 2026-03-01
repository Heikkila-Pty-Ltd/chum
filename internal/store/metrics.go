package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// HealthEvent represents a recorded health event.
type HealthEvent struct {
	ID         int64
	EventType  string
	Details    string
	DispatchID int64
	MorselID   string
	CreatedAt  time.Time
}

// TickMetric represents metrics recorded for a scheduler tick.
type TickMetric struct {
	ID           int64
	TickAt       time.Time
	Project      string
	MorselsOpen  int
	MorselsReady int
	Dispatched   int
	Completed    int
	Failed       int
	Stuck        int
}

// SprintBoundary tracks normalized sprint windows for shared cadence.
type SprintBoundary struct {
	ID           int64
	SprintNumber int
	SprintStart  time.Time
	SprintEnd    time.Time
	CreatedAt    time.Time
}

// DispatchOutput represents captured output from an agent dispatch.
type DispatchOutput struct {
	ID          int64
	DispatchID  int64
	CapturedAt  time.Time
	Output      string
	OutputTail  string
	OutputBytes int64
}

// QualityScore stores computed quality metrics for completed dispatches.
type QualityScore struct {
	DispatchID   int64
	Provider     string
	Role         string
	Overall      float64
	TestsPassed  *bool
	MorselClosed bool
	CommitMade   bool
	FilesChanged int
	LinesChanged int
	Duration     float64
}

// RecordHealthEvent records a health event.
func (s *Store) RecordHealthEvent(eventType, details string) error {
	return s.RecordHealthEventWithDispatch(eventType, details, 0, "")
}

// RecordHealthEventWithDispatch records a health event with optional dispatch/morsel correlation.
func (s *Store) RecordHealthEventWithDispatch(eventType, details string, dispatchID int64, morselID string) error {
	if dispatchID < 0 {
		dispatchID = 0
	}
	_, err := s.db.Exec(
		`INSERT INTO health_events (event_type, details, dispatch_id, morsel_id) VALUES (?, ?, ?, ?)`,
		eventType, details, dispatchID, strings.TrimSpace(morselID),
	)
	if err != nil {
		return fmt.Errorf("store: record health event: %w", err)
	}
	return nil
}

// HasRecentHealthEvent checks if a health event with the given type and details
// substring was recorded within the last `within` duration. Used to suppress
// duplicate Matrix notifications (e.g. stale hibernator alerts).
func (s *Store) HasRecentHealthEvent(eventType, detailsSubstring string, within time.Duration) bool {
	cutoff := time.Now().UTC().Add(-within).Format("2006-01-02 15:04:05")
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM health_events
		 WHERE event_type = ? AND details LIKE '%' || ? || '%' AND created_at >= ?`,
		eventType, detailsSubstring, cutoff,
	).Scan(&count)
	return err == nil && count > 0
}

// CountRecentHealthEvents returns the number of health events of a given type
// recorded within the specified duration. Used for threshold-based escalation.
func (s *Store) CountRecentHealthEvents(eventType string, within time.Duration) (int, error) {
	cutoff := time.Now().UTC().Add(-within).Format("2006-01-02 15:04:05")
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM health_events WHERE event_type = ? AND created_at >= ?`,
		eventType, cutoff,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("store: count health events: %w", err)
	}
	return count, nil
}

// RecordTickMetrics records metrics for a scheduler tick.
func (s *Store) RecordTickMetrics(project string, open, ready, dispatched, completed, failed, stuck int) error {
	_, err := s.db.Exec(
		`INSERT INTO tick_metrics (project, morsels_open, morsels_ready, dispatched, completed, failed, stuck) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		project, open, ready, dispatched, completed, failed, stuck,
	)
	if err != nil {
		return fmt.Errorf("store: record tick metrics: %w", err)
	}
	return nil
}

// RecordSprintBoundary upserts a sprint boundary window keyed by sprint number.
func (s *Store) RecordSprintBoundary(sprintNumber int, sprintStart, sprintEnd time.Time) error {
	if sprintNumber <= 0 {
		return fmt.Errorf("store: record sprint boundary: sprint number must be > 0")
	}
	if !sprintEnd.After(sprintStart) {
		return fmt.Errorf("store: record sprint boundary: sprint_end must be after sprint_start")
	}
	_, err := s.db.Exec(
		`INSERT INTO sprint_boundaries (sprint_number, sprint_start, sprint_end)
		 VALUES (?, ?, ?)
		 ON CONFLICT(sprint_number) DO UPDATE SET sprint_start=excluded.sprint_start, sprint_end=excluded.sprint_end`,
		sprintNumber,
		sprintStart.UTC().Format(time.DateTime),
		sprintEnd.UTC().Format(time.DateTime),
	)
	if err != nil {
		return fmt.Errorf("store: record sprint boundary: %w", err)
	}
	return nil
}

// GetCurrentSprintNumber returns the current sprint number based on now and recorded boundaries.
func (s *Store) GetCurrentSprintNumber() (int, error) {
	var sprintNumber int
	err := s.db.QueryRow(
		`SELECT sprint_number
		 FROM sprint_boundaries
		 WHERE sprint_start <= datetime('now') AND sprint_end > datetime('now')
		 ORDER BY sprint_number DESC
		 LIMIT 1`,
	).Scan(&sprintNumber)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: get current sprint number: %w", err)
	}
	return sprintNumber, nil
}

// GetRecentHealthEvents returns health events from the last N hours.
func (s *Store) GetRecentHealthEvents(hours int) ([]HealthEvent, error) {
	rows, err := s.db.Query(
		`SELECT id, event_type, details, dispatch_id, morsel_id, created_at FROM health_events WHERE created_at >= datetime('now', ? || ' hours') ORDER BY created_at DESC`,
		fmt.Sprintf("-%d", hours),
	)
	if err != nil {
		return nil, fmt.Errorf("store: query health events: %w", err)
	}
	defer rows.Close()

	var events []HealthEvent
	for rows.Next() {
		var e HealthEvent
		if err := rows.Scan(&e.ID, &e.EventType, &e.Details, &e.DispatchID, &e.MorselID, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("store: scan health event: %w", err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// CaptureOutput captures and stores agent output from a completed dispatch.
// Output is truncated to 500KB max. The tail contains the last 100 lines.
func (s *Store) CaptureOutput(dispatchID int64, output string) error {
	const maxOutputBytes = 500 * 1024 // 500KB

	outputBytes := int64(len(output))

	// Truncate output if too large
	if outputBytes > maxOutputBytes {
		// Find a reasonable truncation point (avoid breaking mid-line)
		truncated := output[len(output)-maxOutputBytes:]
		if newlineIdx := strings.Index(truncated, "\n"); newlineIdx != -1 {
			output = truncated[newlineIdx+1:]
		} else {
			output = truncated
		}
		outputBytes = int64(len(output))
	}

	// Extract last 100 lines for tail
	outputTail := extractTail(output, 100)

	_, err := s.db.Exec(
		`INSERT INTO dispatch_output (dispatch_id, output, output_tail, output_bytes) VALUES (?, ?, ?, ?)`,
		dispatchID, output, outputTail, outputBytes,
	)
	if err != nil {
		return fmt.Errorf("store: capture output: %w", err)
	}
	return nil
}

// GetOutput retrieves the full captured output for a dispatch.
func (s *Store) GetOutput(dispatchID int64) (string, error) {
	var output string
	err := s.db.QueryRow(
		`SELECT output FROM dispatch_output WHERE dispatch_id = ? ORDER BY captured_at DESC LIMIT 1`,
		dispatchID,
	).Scan(&output)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("store: no output found for dispatch %d", dispatchID)
		}
		return "", fmt.Errorf("store: get output: %w", err)
	}
	return output, nil
}

// GetOutputTail retrieves the tail (last 100 lines) of captured output for a dispatch.
func (s *Store) GetOutputTail(dispatchID int64) (string, error) {
	var outputTail string
	err := s.db.QueryRow(
		`SELECT output_tail FROM dispatch_output WHERE dispatch_id = ? ORDER BY captured_at DESC LIMIT 1`,
		dispatchID,
	).Scan(&outputTail)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("store: no output found for dispatch %d", dispatchID)
		}
		return "", fmt.Errorf("store: get output tail: %w", err)
	}
	return outputTail, nil
}

// UpsertQualityScore stores or replaces quality scoring for a dispatch.
func (s *Store) UpsertQualityScore(score QualityScore) error {
	if score.DispatchID <= 0 {
		return fmt.Errorf("store: upsert quality score: dispatch_id must be greater than 0")
	}

	var testsPassed any
	if score.TestsPassed != nil {
		if *score.TestsPassed {
			testsPassed = 1
		} else {
			testsPassed = 0
		}
	}

	_, err := s.db.Exec(
		`INSERT INTO quality_scores (
			dispatch_id, provider, role, overall, tests_passed, morsel_closed, commit_made, files_changed, lines_changed, duration
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(dispatch_id) DO UPDATE SET
			provider = excluded.provider,
			role = excluded.role,
			overall = excluded.overall,
			tests_passed = excluded.tests_passed,
			morsel_closed = excluded.morsel_closed,
			commit_made = excluded.commit_made,
			files_changed = excluded.files_changed,
			lines_changed = excluded.lines_changed,
			duration = excluded.duration,
			recorded_at = datetime('now')`,
		score.DispatchID,
		score.Provider,
		score.Role,
		score.Overall,
		testsPassed,
		boolToInt(score.MorselClosed),
		boolToInt(score.CommitMade),
		score.FilesChanged,
		score.LinesChanged,
		score.Duration,
	)
	if err != nil {
		return fmt.Errorf("store: upsert quality score: %w", err)
	}
	return nil
}

// GetProviderRoleQualityAverages returns provider-role average quality in the time window.
func (s *Store) GetProviderRoleQualityAverages(window time.Duration) (map[string]map[string]float64, error) {
	if window <= 0 {
		window = 24 * time.Hour
	}
	cutoff := time.Now().Add(-window).UTC().Format(time.DateTime)

	rows, err := s.db.Query(`
		SELECT provider, role, AVG(overall)
		FROM quality_scores
		WHERE recorded_at > ?
		  AND provider != ''
		  AND role != ''
		GROUP BY provider, role
		ORDER BY provider, role`,
		cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("store: query provider role quality averages: %w", err)
	}
	defer rows.Close()

	averages := make(map[string]map[string]float64)
	for rows.Next() {
		var provider, role string
		var average float64
		if err := rows.Scan(&provider, &role, &average); err != nil {
			return nil, fmt.Errorf("store: scan provider role quality average: %w", err)
		}
		roleAverages, ok := averages[provider]
		if !ok {
			roleAverages = make(map[string]float64)
			averages[provider] = roleAverages
		}
		roleAverages[role] = average
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate provider role quality averages: %w", err)
	}
	return averages, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// extractTail returns the last N lines from text.
func extractTail(text string, lines int) string {
	if text == "" {
		return ""
	}

	// Split into lines
	allLines := strings.Split(text, "\n")

	// Return the last N lines
	if len(allLines) <= lines {
		return text
	}

	tailLines := allLines[len(allLines)-lines:]
	return strings.Join(tailLines, "\n")
}

// ProviderStat holds basic performance statistics for a provider within a time window.
type ProviderStat struct {
	Provider      string
	Total         int
	Successes     int
	TotalDuration float64
}

// ProviderLabelStat holds performance stats for a provider-label pair.
type ProviderLabelStat struct {
	Provider      string
	Label         string
	Total         int
	Successes     int
	TotalDuration float64
}

// GetProviderStats returns provider performance statistics within the time window.
func (s *Store) GetProviderStats(window time.Duration) (map[string]ProviderStat, error) {
	cutoff := time.Now().Add(-window).UTC().Format(time.DateTime)

	rows, err := s.db.Query(`
		SELECT provider, status, duration_s 
		FROM dispatches 
		WHERE dispatched_at > ?
		  AND completed_at IS NOT NULL
		  AND status IN ('completed', 'failed', 'cancelled', 'interrupted', 'retried')
		ORDER BY dispatched_at DESC`,
		cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("store: query provider stats: %w", err)
	}
	defer rows.Close()

	stats := make(map[string]ProviderStat)

	for rows.Next() {
		var provider, status string
		var duration float64
		if err := rows.Scan(&provider, &status, &duration); err != nil {
			return nil, fmt.Errorf("store: scan provider stats: %w", err)
		}

		stat := stats[provider]
		stat.Provider = provider
		stat.Total++
		stat.TotalDuration += duration

		// Count completed dispatches as successes
		if status == "completed" {
			stat.Successes++
		}

		stats[provider] = stat
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: provider stats rows: %w", err)
	}

	return stats, nil
}

// GetProviderLabelStats aggregates provider performance by label within the time window.
func (s *Store) GetProviderLabelStats(window time.Duration) (map[string]map[string]ProviderLabelStat, error) {
	cutoff := time.Now().Add(-window).UTC().Format(time.DateTime)

	rows, err := s.db.Query(`
		SELECT provider, status, duration_s, labels
		FROM dispatches
		WHERE dispatched_at > ?
		  AND completed_at IS NOT NULL
		  AND status IN ('completed', 'failed', 'cancelled', 'interrupted', 'retried')
		ORDER BY dispatched_at DESC`,
		cutoff,
	)
	if err != nil {
		return nil, fmt.Errorf("store: query provider label stats: %w", err)
	}
	defer rows.Close()

	stats := make(map[string]map[string]ProviderLabelStat)
	for rows.Next() {
		var provider, status, labelsCSV string
		var duration float64
		if err := rows.Scan(&provider, &status, &duration, &labelsCSV); err != nil {
			return nil, fmt.Errorf("store: scan provider label stats: %w", err)
		}

		labels := decodeDispatchLabels(labelsCSV)
		if len(labels) == 0 {
			continue
		}
		if _, ok := stats[provider]; !ok {
			stats[provider] = make(map[string]ProviderLabelStat)
		}

		for _, label := range labels {
			stat := stats[provider][label]
			stat.Provider = provider
			stat.Label = label
			stat.Total++
			stat.TotalDuration += duration
			if status == "completed" {
				stat.Successes++
			}
			stats[provider][label] = stat
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: provider label stats rows: %w", err)
	}

	return stats, nil
}

// --- Token Usage ---

// TokenUsage is a compact token usage payload for per-activity persistence.
type TokenUsage struct {
	InputTokens         int
	OutputTokens        int
	CacheReadTokens     int
	CacheCreationTokens int
	CostUSD             float64
}

// TokenUsageRecord represents a per-activity token consumption record.
type TokenUsageRecord struct {
	ID                  int64
	DispatchID          int64
	MorselID            string
	Project             string
	ActivityName        string
	Agent               string
	InputTokens         int
	OutputTokens        int
	CacheReadTokens     int
	CacheCreationTokens int
	CostUSD             float64
	RecordedAt          time.Time
}

// migrateTokenUsageTable creates the per-activity token tracking table.
func migrateTokenUsageTable(db *sql.DB) error {
	if _, err := db.Exec(`
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
		)
	`); err != nil {
		return fmt.Errorf("create token_usage table: %w", err)
	}

	// Backfill older token_usage schemas that may predate cache metrics columns.
	for _, col := range []struct {
		column string
		ddl    string
	}{
		{"cache_read_tokens", "cache_read_tokens INTEGER NOT NULL DEFAULT 0"},
		{"cache_creation_tokens", "cache_creation_tokens INTEGER NOT NULL DEFAULT 0"},
	} {
		if err := addColumnIfNotExists(db, "token_usage", col.column, col.ddl); err != nil {
			return err
		}
	}

	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_token_usage_dispatch ON token_usage(dispatch_id)`); err != nil {
		return fmt.Errorf("create token_usage dispatch index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_token_usage_project ON token_usage(project, recorded_at)`); err != nil {
		return fmt.Errorf("create token_usage project index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_token_usage_agent ON token_usage(agent, recorded_at)`); err != nil {
		return fmt.Errorf("create token_usage agent index: %w", err)
	}
	return nil
}

// StoreTokenUsage inserts a per-activity token consumption record.
func (s *Store) StoreTokenUsage(dispatchID int64, morselID, project, activityName, agent string, usage TokenUsage) error {
	_, err := s.db.Exec(
		`INSERT INTO token_usage (dispatch_id, morsel_id, project, activity_name, agent, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, cost_usd)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		dispatchID, morselID, project, activityName, agent, usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheCreationTokens, usage.CostUSD,
	)
	if err != nil {
		return fmt.Errorf("store: store token usage: %w", err)
	}
	return nil
}

// GetTokenUsageByDispatch returns all per-activity token records for a dispatch.
func (s *Store) GetTokenUsageByDispatch(dispatchID int64) ([]TokenUsageRecord, error) {
	rows, err := s.db.Query(
		`SELECT id, dispatch_id, morsel_id, project, activity_name, agent, input_tokens, output_tokens, cache_read_tokens, cache_creation_tokens, cost_usd, recorded_at
		 FROM token_usage WHERE dispatch_id = ? ORDER BY id`,
		dispatchID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get token usage by dispatch: %w", err)
	}
	defer rows.Close()

	var records []TokenUsageRecord
	for rows.Next() {
		var r TokenUsageRecord
		if err := rows.Scan(&r.ID, &r.DispatchID, &r.MorselID, &r.Project, &r.ActivityName, &r.Agent,
			&r.InputTokens, &r.OutputTokens, &r.CacheReadTokens, &r.CacheCreationTokens, &r.CostUSD, &r.RecordedAt); err != nil {
			return nil, fmt.Errorf("store: scan token usage: %w", err)
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

// TokenUsageSummary holds aggregate token stats for a grouping key.
type TokenUsageSummary struct {
	Key                      string // grouping key (project, agent, or activity_name)
	TotalInputTokens         int
	TotalOutputTokens        int
	TotalCacheReadTokens     int
	TotalCacheCreationTokens int
	TotalCostUSD             float64
	RecordCount              int
}

// GetTokenUsageSummary returns aggregate token usage grouped by the specified column.
// Valid groupBy values: "project", "agent", "activity_name".
func (s *Store) GetTokenUsageSummary(groupBy string, since time.Time) ([]TokenUsageSummary, error) {
	// Whitelist column names to prevent injection
	switch groupBy {
	case "project", "agent", "activity_name":
	default:
		return nil, fmt.Errorf("store: invalid groupBy column: %s", groupBy)
	}

	query := fmt.Sprintf(
		`SELECT %s, SUM(input_tokens), SUM(output_tokens), SUM(cache_read_tokens), SUM(cache_creation_tokens), SUM(cost_usd), COUNT(*)
		 FROM token_usage WHERE recorded_at >= ? GROUP BY %s ORDER BY SUM(cost_usd) DESC`,
		groupBy, groupBy,
	)

	rows, err := s.db.Query(query, since.UTC().Format(time.DateTime))
	if err != nil {
		return nil, fmt.Errorf("store: get token usage summary: %w", err)
	}
	defer rows.Close()

	var summaries []TokenUsageSummary
	for rows.Next() {
		var s TokenUsageSummary
		if err := rows.Scan(
			&s.Key,
			&s.TotalInputTokens,
			&s.TotalOutputTokens,
			&s.TotalCacheReadTokens,
			&s.TotalCacheCreationTokens,
			&s.TotalCostUSD,
			&s.RecordCount,
		); err != nil {
			return nil, fmt.Errorf("store: scan token usage summary: %w", err)
		}
		summaries = append(summaries, s)
	}
	return summaries, rows.Err()
}

// StepMetricRecord represents a persisted pipeline step metric.
type StepMetricRecord struct {
	ID         int64   `json:"id"`
	DispatchID int64   `json:"dispatch_id"`
	MorselID   string  `json:"morsel_id"`
	Project    string  `json:"project"`
	StepName   string  `json:"step_name"`
	DurationS  float64 `json:"duration_s"`
	Status     string  `json:"status"`
	Slow       bool    `json:"slow"`
	RecordedAt string  `json:"recorded_at"`
}

// StoreStepMetric persists a single pipeline step metric.
func (s *Store) StoreStepMetric(dispatchID int64, morselID, project, stepName string, durationS float64, status string, slow bool) error {
	slowInt := 0
	if slow {
		slowInt = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO step_metrics (dispatch_id, morsel_id, project, step_name, duration_s, status, slow)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		dispatchID, morselID, project, stepName, durationS, status, slowInt,
	)
	if err != nil {
		return fmt.Errorf("store: store step metric: %w", err)
	}
	return nil
}

// GetStepMetricsByDispatch returns all step metrics for a dispatch.
func (s *Store) GetStepMetricsByDispatch(dispatchID int64) ([]StepMetricRecord, error) {
	rows, err := s.db.Query(
		`SELECT id, dispatch_id, morsel_id, project, step_name, duration_s, status, slow, recorded_at
		 FROM step_metrics WHERE dispatch_id = ? ORDER BY id`,
		dispatchID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: get step metrics by dispatch: %w", err)
	}
	defer rows.Close()

	var records []StepMetricRecord
	for rows.Next() {
		var r StepMetricRecord
		var slowInt int
		if err := rows.Scan(&r.ID, &r.DispatchID, &r.MorselID, &r.Project, &r.StepName, &r.DurationS, &r.Status, &slowInt, &r.RecordedAt); err != nil {
			return nil, fmt.Errorf("store: scan step metric: %w", err)
		}
		r.Slow = slowInt != 0
		records = append(records, r)
	}
	return records, rows.Err()
}

// TokenBurn holds aggregate token counts for a rolling window query.
type TokenBurn struct {
	InputTokens  int64
	OutputTokens int64
	CacheRead    int64
	CacheCreate  int64
}

// TokenBurnSince returns total token burns for a given agent since the cutoff time.
// If agent is empty, all agents are included.
func (s *Store) TokenBurnSince(agent string, since time.Time) (TokenBurn, error) {
	cutoff := since.UTC().Format(time.DateTime)
	var b TokenBurn
	var query string
	var args []any

	if agent != "" {
		query = `SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		                COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cache_creation_tokens),0)
		         FROM token_usage WHERE agent = ? AND recorded_at >= ?`
		args = []any{agent, cutoff}
	} else {
		query = `SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		                COALESCE(SUM(cache_read_tokens),0), COALESCE(SUM(cache_creation_tokens),0)
		         FROM token_usage WHERE recorded_at >= ?`
		args = []any{cutoff}
	}

	err := s.db.QueryRow(query, args...).Scan(&b.InputTokens, &b.OutputTokens, &b.CacheRead, &b.CacheCreate)
	if err != nil {
		return b, fmt.Errorf("store: token burn since: %w", err)
	}
	return b, nil
}

// --- Provider Escalation Learning ---

// ProviderEscalation records when a task failed at one provider/tier and
// succeeded (or failed) at a higher tier. This feeds the learner loop to
// automatically route tasks based on historical model performance.
type ProviderEscalation struct {
	ID              int64
	MorselID        string
	Project         string
	TaskLabels      string // comma-separated task labels for pattern matching
	FailedProvider  string // provider key that failed (e.g. "codex-spark")
	FailedCLI       string // CLI agent name (e.g. "codex")
	FailedModel     string // model name (e.g. "")
	FailedTier      string // tier level (e.g. "fast")
	FailureReason   string // why it failed: "exit_error", "dod_fail", "timeout", "review_reject"
	EscalatedTo     string // provider key it escalated to
	EscalatedTier   string // tier level it escalated to
	EscalatedResult string // "success" or "failure"
	RecordedAt      string
}

// migrateProviderEscalations creates the escalation learning table.
func migrateProviderEscalations(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS provider_escalations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			morsel_id TEXT NOT NULL DEFAULT '',
			project TEXT NOT NULL DEFAULT '',
			task_labels TEXT NOT NULL DEFAULT '',
			failed_provider TEXT NOT NULL DEFAULT '',
			failed_cli TEXT NOT NULL DEFAULT '',
			failed_model TEXT NOT NULL DEFAULT '',
			failed_tier TEXT NOT NULL DEFAULT '',
			failure_reason TEXT NOT NULL DEFAULT '',
			escalated_to TEXT NOT NULL DEFAULT '',
			escalated_tier TEXT NOT NULL DEFAULT '',
			escalated_result TEXT NOT NULL DEFAULT '',
			recorded_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)
	`)
	if err != nil {
		return fmt.Errorf("create provider_escalations table: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_escalations_provider ON provider_escalations(failed_provider, failure_reason)`); err != nil {
		return fmt.Errorf("create escalations provider index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_escalations_project ON provider_escalations(project, recorded_at)`); err != nil {
		return fmt.Errorf("create escalations project index: %w", err)
	}
	return nil
}

// RecordEscalation stores a provider escalation event for learning.
func (s *Store) RecordEscalation(esc ProviderEscalation) error {
	_, err := s.db.Exec(
		`INSERT INTO provider_escalations (morsel_id, project, task_labels, failed_provider, failed_cli,
		  failed_model, failed_tier, failure_reason, escalated_to, escalated_tier, escalated_result)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		esc.MorselID, esc.Project, esc.TaskLabels, esc.FailedProvider, esc.FailedCLI,
		esc.FailedModel, esc.FailedTier, esc.FailureReason, esc.EscalatedTo,
		esc.EscalatedTier, esc.EscalatedResult,
	)
	if err != nil {
		return fmt.Errorf("store: record escalation: %w", err)
	}
	return nil
}
