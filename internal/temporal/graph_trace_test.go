package temporal

import (
	"encoding/json"
	"testing"

	"github.com/antigravity-dev/chum/internal/store"
)

func TestRecordGraphTraceEventActivity(t *testing.T) {
	st := newTestStore(t)
	acts := &Activities{Store: st}
	ctx := t.Context()

	req := GraphTraceRequest{
		SessionID: "test-session-trace",
		EventType: "phase_boundary",
		Phase:     "plan",
		ModelName: "gpt-5.3-codex",
		Reward:    0.1,
		Metadata:  `{"agent":"codex"}`,
	}

	eventID, err := acts.RecordGraphTraceEventActivity(ctx, req)
	if err != nil {
		t.Fatalf("RecordGraphTraceEventActivity: %v", err)
	}
	if eventID == "" {
		t.Fatal("expected non-empty event ID")
	}

	// Verify event was persisted
	event, err := st.GetGraphTraceEvent(ctx, eventID)
	if err != nil {
		t.Fatalf("GetGraphTraceEvent: %v", err)
	}
	if event.SessionID != "test-session-trace" {
		t.Errorf("expected session_id 'test-session-trace', got %q", event.SessionID)
	}
	if event.Phase != "plan" {
		t.Errorf("expected phase 'plan', got %q", event.Phase)
	}
	if event.Reward != 0.1 {
		t.Errorf("expected reward 0.1, got %f", event.Reward)
	}
}

func TestRecordGraphTraceEventActivity_NilStore(t *testing.T) {
	acts := &Activities{Store: nil}

	eventID, err := acts.RecordGraphTraceEventActivity(t.Context(), GraphTraceRequest{
		SessionID: "x", EventType: "phase_boundary", Phase: "plan",
	})
	if err != nil {
		t.Fatalf("expected nil error for nil store, got %v", err)
	}
	if eventID != "" {
		t.Errorf("expected empty event ID for nil store, got %q", eventID)
	}
}

func TestRecordGraphTraceEventActivity_ParentChaining(t *testing.T) {
	st := newTestStore(t)
	acts := &Activities{Store: st}
	ctx := t.Context()

	// Record parent
	parentID, err := acts.RecordGraphTraceEventActivity(ctx, GraphTraceRequest{
		SessionID: "chain-session",
		EventType: "phase_boundary",
		Phase:     "plan",
		Reward:    0.1,
	})
	if err != nil {
		t.Fatalf("record parent: %v", err)
	}

	// Record child with parent
	childID, err := acts.RecordGraphTraceEventActivity(ctx, GraphTraceRequest{
		ParentEventID: parentID,
		SessionID:     "chain-session",
		EventType:     "phase_boundary",
		Phase:         "execute",
		Reward:        0.3,
	})
	if err != nil {
		t.Fatalf("record child: %v", err)
	}

	// Verify depth calculated
	child, err := st.GetGraphTraceEvent(ctx, childID)
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if child.Depth != 1 {
		t.Errorf("expected child depth 1, got %d", child.Depth)
	}
	if child.ParentEventID != parentID {
		t.Errorf("expected parent_event_id %q, got %q", parentID, child.ParentEventID)
	}
}

func TestBackpropagateRewardActivity(t *testing.T) {
	st := newTestStore(t)
	acts := &Activities{Store: st}
	ctx := t.Context()

	sessionID := "backprop-session"

	// Create a few events
	for _, phase := range []string{"plan", "execute", "dod"} {
		_, err := st.RecordGraphTraceEvent(ctx, &store.GraphTraceEvent{
			SessionID: sessionID,
			EventType: "phase_boundary",
			Phase:     phase,
		})
		if err != nil {
			t.Fatalf("record %s: %v", phase, err)
		}
	}

	// Backpropagate
	err := acts.BackpropagateRewardActivity(ctx, BackpropagateRewardRequest{
		SessionID: sessionID,
		Reward:    1.0,
	})
	if err != nil {
		t.Fatalf("BackpropagateRewardActivity: %v", err)
	}

	// Verify all events got terminal_reward
	events, err := st.GetSessionTraceEvents(ctx, sessionID)
	if err != nil {
		t.Fatalf("get events: %v", err)
	}
	for _, e := range events {
		if e.TerminalReward == nil {
			t.Errorf("event %s: expected terminal_reward, got nil", e.EventID)
		} else if *e.TerminalReward != 1.0 {
			t.Errorf("event %s: expected terminal_reward 1.0, got %f", e.EventID, *e.TerminalReward)
		}
	}
}

func TestBackpropagateRewardActivity_NilStore(t *testing.T) {
	acts := &Activities{Store: nil}
	err := acts.BackpropagateRewardActivity(t.Context(), BackpropagateRewardRequest{
		SessionID: "x", Reward: 1.0,
	})
	if err != nil {
		t.Fatalf("expected nil error for nil store, got %v", err)
	}
}

func TestTraceMetadataJSON(t *testing.T) {
	// Normal case
	result := traceMetadataJSON("agent", "codex", "tokens", 1000, "passed", true)
	if result == "" {
		t.Fatal("expected non-empty JSON")
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(result), &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m["agent"] != "codex" {
		t.Errorf("expected agent=codex, got %v", m["agent"])
	}
	if m["tokens"] != float64(1000) {
		t.Errorf("expected tokens=1000, got %v", m["tokens"])
	}
	if m["passed"] != true {
		t.Errorf("expected passed=true, got %v", m["passed"])
	}

	// Empty
	if result := traceMetadataJSON(); result != "" {
		t.Errorf("expected empty for no args, got %q", result)
	}

	// Single arg (odd length — drops last)
	if result := traceMetadataJSON("key"); result != "" {
		t.Errorf("expected empty for single arg, got %q", result)
	}
}

// newTestStore creates a temporary in-memory store for testing.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("create test store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}
