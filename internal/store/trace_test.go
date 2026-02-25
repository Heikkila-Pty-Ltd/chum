package store

import (
	"testing"
)

func TestGraphTraceEvent_RecordAndRetrieve(t *testing.T) {
	s := tempStore(t)

	ctx := t.Context()
	sessionID := "test-session-123"

	// Record root event
	rootEvent := GraphTraceEvent{
		SessionID: sessionID,
		EventType: "phase_boundary",
		Phase:     "plan",
		Reward:    0.0,
	}

	rootID, err := s.RecordGraphTraceEvent(ctx, &rootEvent)
	if err != nil {
		t.Fatalf("record root event: %v", err)
	}

	if rootID == "" {
		t.Fatal("expected event ID to be generated")
	}

	// Record child LLM call
	llmEvent := GraphTraceEvent{
		SessionID:     sessionID,
		ParentEventID: rootID,
		EventType:     "llm_call",
		Phase:         "plan",
		ModelName:     "claude-sonnet-4",
		TokensInput:   1000,
		TokensOutput:  500,
		Reward:        0.5,
	}

	llmID, err := s.RecordGraphTraceEvent(ctx, &llmEvent)
	if err != nil {
		t.Fatalf("record llm event: %v", err)
	}

	// Record child tool call
	toolSuccess := true
	toolEvent := GraphTraceEvent{
		SessionID:     sessionID,
		ParentEventID: llmID,
		EventType:     "tool_call",
		Phase:         "plan",
		ToolName:      "Read",
		ToolSuccess:   &toolSuccess,
		Reward:        0.3,
	}

	_, err = s.RecordGraphTraceEvent(ctx, &toolEvent)
	if err != nil {
		t.Fatalf("record tool event: %v", err)
	}

	// Retrieve all events for session
	events, err := s.GetSessionTraceEvents(ctx, sessionID)
	if err != nil {
		t.Fatalf("get session events: %v", err)
	}

	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}

	// Verify depth calculation
	if events[0].Depth != 0 {
		t.Errorf("expected root depth 0, got %d", events[0].Depth)
	}
	if events[1].Depth != 1 {
		t.Errorf("expected child depth 1, got %d", events[1].Depth)
	}
	if events[2].Depth != 2 {
		t.Errorf("expected grandchild depth 2, got %d", events[2].Depth)
	}

	// Verify tool success
	if events[2].ToolSuccess == nil || !*events[2].ToolSuccess {
		t.Error("expected tool success to be true")
	}
}

func TestGraphTraceEvent_ToolSequence(t *testing.T) {
	s := tempStore(t)

	ctx := t.Context()
	sessionID := "test-session-tools"

	// Record a sequence of tool calls
	tools := []string{"Read", "Grep", "Write", "Edit", "Bash"}
	for _, toolName := range tools {
		success := true
		event := GraphTraceEvent{
			SessionID:   sessionID,
			EventType:   "tool_call",
			Phase:       "execute",
			ToolName:    toolName,
			ToolSuccess: &success,
		}

		if _, err := s.RecordGraphTraceEvent(ctx, &event); err != nil {
			t.Fatalf("record tool %s: %v", toolName, err)
		}
	}

	// Get tool sequence
	sequence, err := s.GetToolSequence(ctx, sessionID)
	if err != nil {
		t.Fatalf("get tool sequence: %v", err)
	}

	if len(sequence) != len(tools) {
		t.Fatalf("expected %d tools, got %d", len(tools), len(sequence))
	}

	for i, tool := range tools {
		if sequence[i] != tool {
			t.Errorf("expected tool %d to be %s, got %s", i, tool, sequence[i])
		}
	}
}

func TestGraphTraceEvent_BackpropagateReward(t *testing.T) {
	s := tempStore(t)

	ctx := t.Context()
	sessionID := "test-session-backprop"

	// Create a chain of events
	root := GraphTraceEvent{SessionID: sessionID, EventType: "phase_boundary", Phase: "plan"}
	rootID, _ := s.RecordGraphTraceEvent(ctx, &root)

	child := GraphTraceEvent{SessionID: sessionID, ParentEventID: rootID, EventType: "llm_call", Phase: "plan"}
	childID, _ := s.RecordGraphTraceEvent(ctx, &child)

	grandchild := GraphTraceEvent{SessionID: sessionID, ParentEventID: childID, EventType: "tool_call", Phase: "plan", IsTerminal: true}
	s.RecordGraphTraceEvent(ctx, &grandchild)

	// Backpropagate terminal reward
	terminalReward := 1.0
	if err := s.BackpropagateReward(ctx, sessionID, terminalReward); err != nil {
		t.Fatalf("backpropagate reward: %v", err)
	}

	// Verify all events have terminal_reward set
	events, err := s.GetSessionTraceEvents(ctx, sessionID)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}

	for _, event := range events {
		if event.TerminalReward == nil {
			t.Errorf("expected terminal_reward to be set for event %s", event.EventID)
		} else if *event.TerminalReward != terminalReward {
			t.Errorf("expected terminal_reward %.2f, got %.2f", terminalReward, *event.TerminalReward)
		}
	}
}

func TestGraphTraceEvent_SuccessfulSessions(t *testing.T) {
	s := tempStore(t)

	ctx := t.Context()

	// Create successful session
	success := GraphTraceEvent{
		SessionID:      "success-session",
		EventType:      "phase_boundary",
		Phase:          "record",
		IsTerminal:     true,
		TerminalReward: floatPtr(0.9),
	}
	s.RecordGraphTraceEvent(ctx, &success)

	// Create failed session
	failure := GraphTraceEvent{
		SessionID:      "failed-session",
		EventType:      "phase_boundary",
		Phase:          "record",
		IsTerminal:     true,
		TerminalReward: floatPtr(0.2),
	}
	s.RecordGraphTraceEvent(ctx, &failure)

	// Get successful sessions (threshold 0.8)
	sessions, err := s.GetSuccessfulSessions(ctx, 0.8)
	if err != nil {
		t.Fatalf("get successful sessions: %v", err)
	}

	if len(sessions) != 1 {
		t.Fatalf("expected 1 successful session, got %d", len(sessions))
	}

	if sessions[0] != "success-session" {
		t.Errorf("expected success-session, got %s", sessions[0])
	}
}

func TestGraphTraceEvent_ExtractSolutionPath(t *testing.T) {
	s := tempStore(t)

	ctx := t.Context()
	sessionID := "test-path"

	// Create chain: root -> middle -> terminal
	root := GraphTraceEvent{SessionID: sessionID, EventType: "phase_boundary", Phase: "plan"}
	rootID, _ := s.RecordGraphTraceEvent(ctx, &root)

	middle := GraphTraceEvent{SessionID: sessionID, ParentEventID: rootID, EventType: "llm_call", Phase: "execute"}
	middleID, _ := s.RecordGraphTraceEvent(ctx, &middle)

	terminal := GraphTraceEvent{SessionID: sessionID, ParentEventID: middleID, EventType: "phase_boundary", Phase: "dod", IsTerminal: true}
	terminalID, _ := s.RecordGraphTraceEvent(ctx, &terminal)

	// Extract solution path from terminal
	path, err := s.ExtractSolutionPath(ctx, terminalID)
	if err != nil {
		t.Fatalf("extract solution path: %v", err)
	}

	if len(path) != 3 {
		t.Fatalf("expected path length 3, got %d", len(path))
	}

	// Verify order (root -> middle -> terminal)
	if path[0].EventID != rootID {
		t.Error("expected first event to be root")
	}
	if path[1].EventID != middleID {
		t.Error("expected second event to be middle")
	}
	if path[2].EventID != terminalID {
		t.Error("expected third event to be terminal")
	}
}

func TestGraphTraceEvent_Metadata(t *testing.T) {
	s := tempStore(t)

	ctx := t.Context()

	event := GraphTraceEvent{
		SessionID: "test-metadata",
		EventType: "llm_call",
		Phase:     "plan",
	}

	eventID, err := s.RecordGraphTraceEvent(ctx, &event)
	if err != nil {
		t.Fatalf("record event: %v", err)
	}

	// Add metadata
	metadata := map[string]interface{}{
		"thinking_level": "high",
		"cost_usd":       0.05,
		"latency_ms":     1200,
	}

	err = s.RecordTraceMetadata(ctx, eventID, metadata)
	if err != nil {
		t.Fatalf("record metadata: %v", err)
	}

	// Retrieve and verify metadata exists
	retrieved, err := s.GetGraphTraceEvent(ctx, eventID)
	if err != nil {
		t.Fatalf("get event: %v", err)
	}

	if retrieved.Metadata == "" {
		t.Error("expected metadata to be set")
	}
}

// Helper function
func floatPtr(f float64) *float64 {
	return &f
}
