package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// GraphTraceEvent represents a single event in the execution graph.
// Events form a tree/graph via parent_event_id, capturing LLM calls,
// tool executions, human feedback, and phase boundaries.
type GraphTraceEvent struct {
	EventID         string  `json:"event_id"`
	ParentEventID   string  `json:"parent_event_id"`
	SessionID       string  `json:"session_id"`
	Timestamp       int64   `json:"timestamp"` // UnixNano for deterministic ordering
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
		event.Timestamp = time.Now().UnixNano()
	}

	// Calculate depth from parent; reject invalid parent references.
	if event.ParentEventID != "" {
		var parentDepth int
		err := s.db.QueryRowContext(ctx, `SELECT depth FROM graph_trace_events WHERE event_id = ?`, event.ParentEventID).Scan(&parentDepth)
		if err != nil {
			return "", fmt.Errorf("parent_event_id %q not found: %w", event.ParentEventID, err)
		}
		if event.Depth == 0 {
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

// GraphTraceEventUpdate holds optional fields for updating a trace event.
// Pointer fields: nil means "don't update", non-nil sets the value (including zero/false).
type GraphTraceEventUpdate struct {
	Reward         *float64
	TerminalReward *float64
	IsTerminal     *bool
	TokensOutput   *int
	ToolSuccess    *bool
	Metadata       *string
}

// UpdateGraphTraceEvent updates fields of an existing trace event.
// Only non-nil fields in the update struct are written.
func (s *Store) UpdateGraphTraceEvent(ctx context.Context, eventID string, updates GraphTraceEventUpdate) error {
	query := `UPDATE graph_trace_events SET `
	args := []interface{}{}
	setClauses := []string{}

	if updates.Reward != nil {
		setClauses = append(setClauses, "reward = ?")
		args = append(args, *updates.Reward)
	}
	if updates.TerminalReward != nil {
		setClauses = append(setClauses, "terminal_reward = ?")
		args = append(args, *updates.TerminalReward)
	}
	if updates.IsTerminal != nil {
		val := 0
		if *updates.IsTerminal {
			val = 1
		}
		setClauses = append(setClauses, "is_terminal = ?")
		args = append(args, val)
	}
	if updates.TokensOutput != nil {
		setClauses = append(setClauses, "tokens_output = ?")
		args = append(args, *updates.TokensOutput)
	}
	if updates.ToolSuccess != nil {
		val := 0
		if *updates.ToolSuccess {
			val = 1
		}
		setClauses = append(setClauses, "tool_success = ?")
		args = append(args, val)
	}
	if updates.Metadata != nil {
		setClauses = append(setClauses, "metadata = ?")
		args = append(args, *updates.Metadata)
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

// BackpropagateReward walks from terminalEventID up the parent chain, setting
// terminal_reward on each ancestor. Only the path from terminal to root is
// updated, so sibling branches in the same session are not poisoned.
func (s *Store) BackpropagateReward(ctx context.Context, terminalEventID string, terminalReward float64) error {
	currentID := terminalEventID
	for currentID != "" {
		if err := s.backpropStep(ctx, currentID, terminalReward); err != nil {
			return err
		}
		currentID = s.parentOf(currentID)
	}
	return nil
}

func (s *Store) backpropStep(ctx context.Context, eventID string, reward float64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE graph_trace_events SET terminal_reward = ? WHERE event_id = ?
	`, reward, eventID)
	if err != nil {
		return fmt.Errorf("backpropagate event %s: %w", eventID, err)
	}
	return nil
}

// parentOf returns the parent_event_id for the given event, or "" if none.
func (s *Store) parentOf(eventID string) string {
	var parentID *string
	if err := s.db.QueryRow(`SELECT parent_event_id FROM graph_trace_events WHERE event_id = ?`, eventID).Scan(&parentID); err != nil || parentID == nil {
		return ""
	}
	return *parentID
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
