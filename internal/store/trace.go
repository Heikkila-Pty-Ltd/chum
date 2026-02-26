package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// GraphTraceEvent represents a single event in the execution graph.
// Events form a tree/graph via parent_event_id, capturing LLM calls,
// tool executions, human feedback, and phase boundaries.
type GraphTraceEvent struct {
	EventID         string  `json:"event_id"`
	ParentEventID   string  `json:"parent_event_id"`
	SessionID       string  `json:"session_id"`
	Timestamp       int64   `json:"timestamp"`
	Depth           int     `json:"depth"`
	EventType       string  `json:"event_type"` // 'llm_call' | 'tool_call' | 'human_feedback' | 'phase_boundary'
	Phase           string  `json:"phase"`      // 'plan' | 'execute' | 'review' | 'dod' | 'record'
	ModelName       string  `json:"model_name,omitempty"`
	TokensInput     int     `json:"tokens_input,omitempty"`
	TokensOutput    int     `json:"tokens_output,omitempty"`
	ToolName        string  `json:"tool_name,omitempty"`
	ToolSuccess     *bool   `json:"tool_success,omitempty"`
	HumanMessage    string  `json:"human_message,omitempty"`
	Reward          float64 `json:"reward"`
	TerminalReward  *float64 `json:"terminal_reward,omitempty"`
	IsTerminal      bool    `json:"is_terminal"`
	Metadata        string  `json:"metadata,omitempty"` // JSON for extensibility
}

// RecordGraphTraceEvent inserts a new trace event into the graph.
// Returns the event ID (either provided or generated).
func (s *Store) RecordGraphTraceEvent(ctx context.Context, event *GraphTraceEvent) (string, error) {
	if event.EventID == "" {
		event.EventID = generateEventID()
	}
	if event.Timestamp == 0 {
		event.Timestamp = time.Now().Unix()
	}

	// Calculate depth from parent
	if event.ParentEventID != "" && event.Depth == 0 {
		var parentDepth int
		err := s.db.QueryRowContext(ctx, `SELECT depth FROM graph_trace_events WHERE event_id = ?`, event.ParentEventID).Scan(&parentDepth)
		if err == nil {
			event.Depth = parentDepth + 1
		}
	}

	var toolSuccessInt *int
	if event.ToolSuccess != nil {
		val := 0
		if *event.ToolSuccess {
			val = 1
		}
		toolSuccessInt = &val
	}

	var terminalRewardVal *float64
	if event.TerminalReward != nil {
		terminalRewardVal = event.TerminalReward
	}

	isTerminalInt := 0
	if event.IsTerminal {
		isTerminalInt = 1
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO graph_trace_events (
			event_id, parent_event_id, session_id, timestamp, depth,
			event_type, phase, model_name, tokens_input, tokens_output,
			tool_name, tool_success, human_message, reward, terminal_reward, is_terminal, metadata
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		event.EventID, event.ParentEventID, event.SessionID, event.Timestamp, event.Depth,
		event.EventType, event.Phase, event.ModelName, event.TokensInput, event.TokensOutput,
		event.ToolName, toolSuccessInt, event.HumanMessage, event.Reward, terminalRewardVal, isTerminalInt, event.Metadata,
	)

	if err != nil {
		return "", err
	}
	return event.EventID, nil
}

// UpdateGraphTraceEvent updates fields of an existing trace event.
func (s *Store) UpdateGraphTraceEvent(ctx context.Context, eventID string, updates GraphTraceEvent) error {
	// Build dynamic update based on which fields are set
	query := `UPDATE graph_trace_events SET `
	args := []interface{}{}
	setClauses := []string{}

	if updates.Reward != 0 {
		setClauses = append(setClauses, "reward = ?")
		args = append(args, updates.Reward)
	}
	if updates.TerminalReward != nil {
		setClauses = append(setClauses, "terminal_reward = ?")
		args = append(args, *updates.TerminalReward)
	}
	if updates.IsTerminal {
		setClauses = append(setClauses, "is_terminal = ?")
		args = append(args, 1)
	}
	if updates.TokensOutput > 0 {
		setClauses = append(setClauses, "tokens_output = ?")
		args = append(args, updates.TokensOutput)
	}
	if updates.ToolSuccess != nil {
		val := 0
		if *updates.ToolSuccess {
			val = 1
		}
		setClauses = append(setClauses, "tool_success = ?")
		args = append(args, val)
	}
	if updates.Metadata != "" {
		setClauses = append(setClauses, "metadata = ?")
		args = append(args, updates.Metadata)
	}

	if len(setClauses) == 0 {
		return nil // nothing to update
	}

	query += setClauses[0]
	for _, clause := range setClauses[1:] {
		query += ", " + clause
	}
	query += " WHERE event_id = ?"
	args = append(args, eventID)

	_, err := s.db.ExecContext(ctx, query, args...)
	return err
}

// BackpropagateReward walks up the tree from a terminal node and sets terminal_reward for all ancestors.
func (s *Store) BackpropagateReward(ctx context.Context, sessionID string, terminalReward float64) error {
	// Update all events in this session with the terminal reward
	_, err := s.db.ExecContext(ctx, `
		UPDATE graph_trace_events
		SET terminal_reward = ?
		WHERE session_id = ?
	`, terminalReward, sessionID)

	return err
}

// GetGraphTraceEvent retrieves a single event by ID.
func (s *Store) GetGraphTraceEvent(ctx context.Context, eventID string) (*GraphTraceEvent, error) {
	var event GraphTraceEvent
	var toolSuccessInt *int
	var terminalReward *float64
	var isTerminalInt int

	err := s.db.QueryRowContext(ctx, `
		SELECT event_id, parent_event_id, session_id, timestamp, depth,
		       event_type, phase, model_name, tokens_input, tokens_output,
		       tool_name, tool_success, human_message, reward, terminal_reward, is_terminal, metadata
		FROM graph_trace_events
		WHERE event_id = ?
	`, eventID).Scan(
		&event.EventID, &event.ParentEventID, &event.SessionID, &event.Timestamp, &event.Depth,
		&event.EventType, &event.Phase, &event.ModelName, &event.TokensInput, &event.TokensOutput,
		&event.ToolName, &toolSuccessInt, &event.HumanMessage, &event.Reward, &terminalReward, &isTerminalInt, &event.Metadata,
	)

	if err != nil {
		return nil, err
	}

	if toolSuccessInt != nil {
		val := *toolSuccessInt == 1
		event.ToolSuccess = &val
	}
	event.TerminalReward = terminalReward
	event.IsTerminal = isTerminalInt == 1

	return &event, nil
}

// GetSessionTraceEvents retrieves all events for a session, ordered by timestamp.
func (s *Store) GetSessionTraceEvents(ctx context.Context, sessionID string) ([]*GraphTraceEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT event_id, parent_event_id, session_id, timestamp, depth,
		       event_type, phase, model_name, tokens_input, tokens_output,
		       tool_name, tool_success, human_message, reward, terminal_reward, is_terminal, metadata
		FROM graph_trace_events
		WHERE session_id = ?
		ORDER BY timestamp ASC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []*GraphTraceEvent
	for rows.Next() {
		var event GraphTraceEvent
		var toolSuccessInt *int
		var terminalReward *float64
		var isTerminalInt int

		err := rows.Scan(
			&event.EventID, &event.ParentEventID, &event.SessionID, &event.Timestamp, &event.Depth,
			&event.EventType, &event.Phase, &event.ModelName, &event.TokensInput, &event.TokensOutput,
			&event.ToolName, &toolSuccessInt, &event.HumanMessage, &event.Reward, &terminalReward, &isTerminalInt, &event.Metadata,
		)
		if err != nil {
			return nil, err
		}

		if toolSuccessInt != nil {
			val := *toolSuccessInt == 1
			event.ToolSuccess = &val
		}
		event.TerminalReward = terminalReward
		event.IsTerminal = isTerminalInt == 1

		events = append(events, &event)
	}

	return events, rows.Err()
}

// GetToolSequence extracts the sequence of tool calls for a session, in order.
func (s *Store) GetToolSequence(ctx context.Context, sessionID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT tool_name
		FROM graph_trace_events
		WHERE session_id = ? AND event_type = 'tool_call' AND tool_name != ''
		ORDER BY timestamp ASC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tools []string
	for rows.Next() {
		var tool string
		if err := rows.Scan(&tool); err != nil {
			return nil, err
		}
		tools = append(tools, tool)
	}

	return tools, rows.Err()
}

// GetSuccessfulSessions returns session IDs with terminal_reward above threshold.
func (s *Store) GetSuccessfulSessions(ctx context.Context, minReward float64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT session_id
		FROM graph_trace_events
		WHERE is_terminal = 1 AND terminal_reward >= ?
	`, minReward)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []string
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			return nil, err
		}
		sessions = append(sessions, sessionID)
	}

	return sessions, rows.Err()
}

// ExtractSolutionPath walks from terminal event back to root, returning the path.
func (s *Store) ExtractSolutionPath(ctx context.Context, terminalEventID string) ([]*GraphTraceEvent, error) {
	path := []*GraphTraceEvent{}

	currentEventID := terminalEventID
	for currentEventID != "" {
		event, err := s.GetGraphTraceEvent(ctx, currentEventID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			return nil, err
		}

		path = append([]*GraphTraceEvent{event}, path...) // prepend
		currentEventID = event.ParentEventID
	}

	return path, nil
}

// RecordTraceMetadata stores arbitrary metadata in a trace event's metadata JSON field.
func (s *Store) RecordTraceMetadata(ctx context.Context, eventID string, metadata map[string]interface{}) error {
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		UPDATE graph_trace_events SET metadata = ? WHERE event_id = ?
	`, string(metadataJSON), eventID)

	return err
}

// generateEventID creates a random event ID.
func generateEventID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Should never happen with crypto/rand
		panic(fmt.Sprintf("crypto/rand.Read failed: %v", err))
	}
	return hex.EncodeToString(b)
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
func (s *Store) CompleteExecutionTrace(traceID int64, status, outcome string, supportCount, successCount int) error {
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
