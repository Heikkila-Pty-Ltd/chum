package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	defaultPlanningTraceLimit = 200
	maxPlanningTraceLimit     = 5000
)

// PlanningTraceEvent is a single persisted planning trace event.
// Stores both summarized and full-fidelity text for post-hoc analysis.
type PlanningTraceEvent struct {
	ID               int64
	SessionID        string
	RunID            string
	Project          string
	TaskID           string
	Cycle            int
	Stage            string
	NodeID           string
	ParentNodeID     string
	BranchID         string
	OptionID         string
	EventType        string
	Actor            string
	ToolName         string
	ToolInput        string
	ToolOutput       string
	PromptText       string
	ResponseText     string
	SummaryText      string
	FullText         string
	SelectedOption   string
	Reward           float64
	MetadataJSON     string
	InteractionClass string
	InteractionType  string
	HumanInteractive bool
	CreatedAt        time.Time
}

func migratePlanningTraceTables(db *sql.DB) error {
	stmts := []struct {
		name string
		sql  string
	}{
		{
			name: "planning_trace_events table",
			sql: `
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
				)`,
		},
		{
			name: "idx_planning_trace_session_created",
			sql:  `CREATE INDEX IF NOT EXISTS idx_planning_trace_session_created ON planning_trace_events(session_id, created_at)`,
		},
		{
			name: "idx_planning_trace_project_reward_created",
			sql:  `CREATE INDEX IF NOT EXISTS idx_planning_trace_project_reward_created ON planning_trace_events(project, reward, created_at)`,
		},
		{
			name: "idx_planning_trace_event_created",
			sql:  `CREATE INDEX IF NOT EXISTS idx_planning_trace_event_created ON planning_trace_events(event_type, created_at)`,
		},
		{
			name: "idx_planning_trace_actor_created",
			sql:  `CREATE INDEX IF NOT EXISTS idx_planning_trace_actor_created ON planning_trace_events(actor, created_at)`,
		},
		{
			name: "idx_planning_trace_branch_created",
			sql:  `CREATE INDEX IF NOT EXISTS idx_planning_trace_branch_created ON planning_trace_events(branch_id, created_at)`,
		},
		{
			name: "idx_planning_trace_node",
			sql:  `CREATE INDEX IF NOT EXISTS idx_planning_trace_node ON planning_trace_events(node_id)`,
		},
	}

	for _, stmt := range stmts {
		if _, err := db.Exec(stmt.sql); err != nil {
			return fmt.Errorf("create %s: %w", stmt.name, err)
		}
	}

	// Backfill new topology columns for databases created before branch-aware tracing.
	if err := addColumnIfNotExists(db, "planning_trace_events", "node_id", "node_id TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfNotExists(db, "planning_trace_events", "parent_node_id", "parent_node_id TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfNotExists(db, "planning_trace_events", "branch_id", "branch_id TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfNotExists(db, "planning_trace_events", "option_id", "option_id TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfNotExists(db, "planning_trace_events", "interaction_class", "interaction_class TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfNotExists(db, "planning_trace_events", "interaction_type", "interaction_type TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := addColumnIfNotExists(db, "planning_trace_events", "human_interactive", "human_interactive INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_planning_trace_human_type_created ON planning_trace_events(human_interactive, interaction_type, created_at)`); err != nil {
		return fmt.Errorf("create idx_planning_trace_human_type_created: %w", err)
	}
	return nil
}

// RecordPlanningTraceEvent persists one planning trace event.
func (s *Store) RecordPlanningTraceEvent(event PlanningTraceEvent) error {
	sessionID := strings.TrimSpace(event.SessionID)
	if sessionID == "" {
		return fmt.Errorf("store: record planning trace: session_id is required")
	}
	eventType := strings.TrimSpace(event.EventType)
	if eventType == "" {
		return fmt.Errorf("store: record planning trace: event_type is required")
	}

	cycle := event.Cycle
	if cycle < 0 {
		cycle = 0
	}
	metadataJSON := strings.TrimSpace(event.MetadataJSON)
	if metadataJSON == "" {
		metadataJSON = "{}"
	}

	interactionClass := strings.TrimSpace(event.InteractionClass)
	interactionType := strings.TrimSpace(event.InteractionType)
	humanInteractive := event.HumanInteractive
	if interactionClass == "" && interactionType == "" && !humanInteractive {
		interactionClass, interactionType, humanInteractive = classifyPlanningInteraction(event)
	}
	humanInteractiveInt := 0
	if humanInteractive {
		humanInteractiveInt = 1
	}

	_, err := s.db.Exec(`
		INSERT INTO planning_trace_events (
			session_id, run_id, project, task_id, cycle, stage, node_id, parent_node_id, branch_id, option_id, event_type, actor,
			tool_name, tool_input, tool_output, prompt_text, response_text,
			summary_text, full_text, selected_option, reward, metadata_json,
			interaction_class, interaction_type, human_interactive
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		sessionID,
		strings.TrimSpace(event.RunID),
		strings.TrimSpace(event.Project),
		strings.TrimSpace(event.TaskID),
		cycle,
		strings.TrimSpace(event.Stage),
		strings.TrimSpace(event.NodeID),
		strings.TrimSpace(event.ParentNodeID),
		strings.TrimSpace(event.BranchID),
		strings.TrimSpace(event.OptionID),
		eventType,
		strings.TrimSpace(event.Actor),
		strings.TrimSpace(event.ToolName),
		event.ToolInput,
		event.ToolOutput,
		event.PromptText,
		event.ResponseText,
		event.SummaryText,
		event.FullText,
		strings.TrimSpace(event.SelectedOption),
		event.Reward,
		metadataJSON,
		interactionClass,
		interactionType,
		humanInteractiveInt,
	)
	if err != nil {
		return fmt.Errorf("store: record planning trace: %w", err)
	}
	return nil
}

// ListPlanningTraceEvents returns planning trace events ordered by insertion.
func (s *Store) ListPlanningTraceEvents(sessionID string, limit int) ([]PlanningTraceEvent, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return []PlanningTraceEvent{}, nil
	}

	if limit <= 0 {
		limit = defaultPlanningTraceLimit
	}
	if limit > maxPlanningTraceLimit {
		limit = maxPlanningTraceLimit
	}

	rows, err := s.db.Query(`
		SELECT
			id, session_id, run_id, project, task_id, cycle, stage, node_id, parent_node_id, branch_id, option_id, event_type, actor,
			tool_name, tool_input, tool_output, prompt_text, response_text,
			summary_text, full_text, selected_option, reward, metadata_json,
			interaction_class, interaction_type, human_interactive, created_at
		FROM planning_trace_events
		WHERE session_id = ?
		ORDER BY id ASC
		LIMIT ?
	`, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list planning trace events: %w", err)
	}
	defer rows.Close()

	events := make([]PlanningTraceEvent, 0, limit)
	for rows.Next() {
		var event PlanningTraceEvent
		var humanInteractiveInt int
		if err := rows.Scan(
			&event.ID,
			&event.SessionID,
			&event.RunID,
			&event.Project,
			&event.TaskID,
			&event.Cycle,
			&event.Stage,
			&event.NodeID,
			&event.ParentNodeID,
			&event.BranchID,
			&event.OptionID,
			&event.EventType,
			&event.Actor,
			&event.ToolName,
			&event.ToolInput,
			&event.ToolOutput,
			&event.PromptText,
			&event.ResponseText,
			&event.SummaryText,
			&event.FullText,
			&event.SelectedOption,
			&event.Reward,
			&event.MetadataJSON,
			&event.InteractionClass,
			&event.InteractionType,
			&humanInteractiveInt,
			&event.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("store: list planning trace events: scan: %w", err)
		}
		event.HumanInteractive = humanInteractiveInt == 1
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list planning trace events: rows: %w", err)
	}
	return events, nil
}

func classifyPlanningInteraction(event PlanningTraceEvent) (string, string, bool) {
	eventType := strings.ToLower(strings.TrimSpace(event.EventType))
	summary := strings.ToUpper(strings.TrimSpace(event.SummaryText))

	switch eventType {
	case "control_session_started":
		return "human_session", "start_session", true
	case "control_session_stopped":
		return "human_session", "stop_session", true
	case "control_prompt_presented":
		return "human_control", "request_prompt", true
	case "control_status_requested":
		return "human_control", "status_check", true
	case "control_signal_submitted":
		switch planningSignalFromMetadata(event.MetadataJSON) {
		case "item-selected":
			return "human_decision", "select_item", true
		case "answer":
			return "human_clarification", "answer_question", true
		case "greenlight":
			return "human_decision", "greenlight_decision", true
		default:
			return "human_control", "submit_signal", true
		}
	case "item_selected":
		return "human_decision", "select_item", true
	case "answer_recorded":
		return "human_clarification", "answer_question", true
	case "greenlight_decision":
		switch summary {
		case "GO":
			return "human_decision", "greenlight_go", true
		case "REALIGN", "NO":
			return "human_decision", "greenlight_realign", true
		default:
			return "human_decision", "greenlight_decision", true
		}
	}

	actor := strings.ToLower(strings.TrimSpace(event.Actor))
	switch actor {
	case "matrix-control", "human", "user":
		return "human_control", "control_action", true
	}

	return "", "", false
}

func planningSignalFromMetadata(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ""
	}
	signal, _ := payload["signal"].(string)
	return strings.TrimSpace(strings.ToLower(signal))
}
