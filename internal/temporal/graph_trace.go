package temporal

import (
	"context"
	"encoding/json"
	"fmt"

	"go.temporal.io/sdk/activity"

	"github.com/antigravity-dev/chum/internal/store"
)

// GraphTraceRequest is the serializable payload for recording a graph trace event.
// Mirrors store.GraphTraceEvent but lives in the temporal package for Temporal serialization.
type GraphTraceRequest struct {
	EventID       string  `json:"event_id,omitempty"`
	ParentEventID string  `json:"parent_event_id,omitempty"`
	SessionID     string  `json:"session_id"`
	EventType     string  `json:"event_type"` // phase_boundary, llm_call, tool_call
	Phase         string  `json:"phase"`      // plan, execute, review, ubs, dod, record, escalate
	ModelName     string  `json:"model_name,omitempty"`
	TokensInput   int     `json:"tokens_input,omitempty"`
	TokensOutput  int     `json:"tokens_output,omitempty"`
	ToolName      string  `json:"tool_name,omitempty"`
	Reward        float64 `json:"reward"`
	IsTerminal    bool    `json:"is_terminal,omitempty"`
	Metadata      string  `json:"metadata,omitempty"`
}

// BackpropagateRewardRequest is the payload for backpropagating a terminal reward.
type BackpropagateRewardRequest struct {
	SessionID string  `json:"session_id"`
	Reward    float64 `json:"reward"`
}

// RecordGraphTraceEventActivity persists one graph trace event to the store.
// Returns the generated event ID. Best-effort — callers should ignore errors.
func (a *Activities) RecordGraphTraceEventActivity(ctx context.Context, req GraphTraceRequest) (string, error) {
	if a == nil || a.Store == nil {
		return "", nil
	}

	event := &store.GraphTraceEvent{
		EventID:       req.EventID,
		ParentEventID: req.ParentEventID,
		SessionID:     req.SessionID,
		EventType:     req.EventType,
		Phase:         req.Phase,
		ModelName:     req.ModelName,
		TokensInput:   req.TokensInput,
		TokensOutput:  req.TokensOutput,
		ToolName:      req.ToolName,
		Reward:        req.Reward,
		IsTerminal:    req.IsTerminal,
		Metadata:      req.Metadata,
	}

	eventID, err := a.Store.RecordGraphTraceEvent(ctx, event)
	if err != nil {
		activity.GetLogger(ctx).Warn("graph trace event recording failed (non-fatal)", "error", err, "phase", req.Phase)
		return "", err
	}
	return eventID, nil
}

// BackpropagateRewardActivity sets terminal_reward on all events in a session.
// Called once at workflow completion (success or failure).
func (a *Activities) BackpropagateRewardActivity(ctx context.Context, req BackpropagateRewardRequest) error {
	if a == nil || a.Store == nil {
		return nil
	}

	if err := a.Store.BackpropagateReward(ctx, req.SessionID, req.Reward); err != nil {
		activity.GetLogger(ctx).Warn("graph trace backpropagation failed (non-fatal)", "error", err, "session", req.SessionID)
		return err
	}
	return nil
}

// traceMetadataJSON builds a JSON string from key-value pairs for trace event metadata.
// Pairs should alternate key (string) and value (any). Odd-length slices drop the last key.
func traceMetadataJSON(pairs ...any) string {
	if len(pairs) < 2 {
		return ""
	}
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			key = fmt.Sprintf("%v", pairs[i])
		}
		m[key] = pairs[i+1]
	}
	data, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(data)
}
