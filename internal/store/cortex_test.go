package store

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestRecordCortexMemory(t *testing.T) {
	s := tempStore(t)
	ctx := t.Context()

	mem := &CortexMemory{
		MemoryType:     "solution_path",
		Species:        "go-feature",
		Signature:      "abc123hash",
		Description:    "plan then execute then review",
		PatternJSON:    `{"phases":["plan","execute","review"]}`,
		SourceSessions: `["session-1"]`,
	}

	id, err := s.RecordCortexMemory(ctx, mem)
	if err != nil {
		t.Fatalf("RecordCortexMemory: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty memory ID")
	}

	got, err := s.GetCortexMemory(ctx, id)
	if err != nil {
		t.Fatalf("GetCortexMemory: %v", err)
	}
	if got.MemoryType != "solution_path" {
		t.Errorf("expected memory_type 'solution_path', got %q", got.MemoryType)
	}
	if got.Species != "go-feature" {
		t.Errorf("expected species 'go-feature', got %q", got.Species)
	}
	if got.Signature != "abc123hash" {
		t.Errorf("expected signature 'abc123hash', got %q", got.Signature)
	}
	if got.Description != "plan then execute then review" {
		t.Errorf("unexpected description: %q", got.Description)
	}
	if got.PatternJSON != `{"phases":["plan","execute","review"]}` {
		t.Errorf("unexpected pattern_json: %q", got.PatternJSON)
	}
	if got.VisitCount != 0 {
		t.Errorf("expected visit_count 0, got %d", got.VisitCount)
	}
}

func TestRecordCortexMemory_UpsertBySignature(t *testing.T) {
	s := tempStore(t)
	ctx := t.Context()

	// First insert
	mem1 := &CortexMemory{
		MemoryType:  "solution_path",
		Species:     "go-feature",
		Signature:   "same-sig",
		Description: "original description",
		PatternJSON: `{"v":1}`,
		VisitCount:  5,
		WinCount:    3,
		TotalReward: 2.5,
	}
	id1, err := s.RecordCortexMemory(ctx, mem1)
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}

	// Second insert with same signature — should upsert
	mem2 := &CortexMemory{
		MemoryType:  "solution_path",
		Species:     "go-feature",
		Signature:   "same-sig",
		Description: "updated description",
		PatternJSON: `{"v":2}`,
		VisitCount:  99,
		WinCount:    99,
	}
	_, err = s.RecordCortexMemory(ctx, mem2)
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Verify: description updated, stats preserved from first insert
	got, err := s.GetCortexMemory(ctx, id1)
	if err != nil {
		t.Fatalf("get after upsert: %v", err)
	}
	if got.Description != "updated description" {
		t.Errorf("expected updated description, got %q", got.Description)
	}
	if got.PatternJSON != `{"v":2}` {
		t.Errorf("expected updated pattern_json, got %q", got.PatternJSON)
	}
	// Stats should be from the original insert (not overwritten)
	if got.VisitCount != 5 {
		t.Errorf("expected preserved visit_count 5, got %d", got.VisitCount)
	}
	if got.WinCount != 3 {
		t.Errorf("expected preserved win_count 3, got %d", got.WinCount)
	}
}

func TestRecordCortexMemory_RequiresSignature(t *testing.T) {
	s := tempStore(t)
	_, err := s.RecordCortexMemory(t.Context(), &CortexMemory{
		MemoryType: "solution_path",
	})
	if err == nil {
		t.Fatal("expected error for empty signature")
	}
}

func TestRecordCortexMemory_GeneratesID(t *testing.T) {
	s := tempStore(t)
	mem := &CortexMemory{
		MemoryType: "solution_path",
		Signature:  "auto-id-test",
	}
	id, err := s.RecordCortexMemory(t.Context(), mem)
	if err != nil {
		t.Fatalf("RecordCortexMemory: %v", err)
	}
	if id == "" {
		t.Fatal("expected auto-generated ID")
	}
	if len(id) != 32 { // 16 bytes = 32 hex chars
		t.Errorf("expected 32-char hex ID, got %d chars: %q", len(id), id)
	}
}

func TestReinforceCortexMemory(t *testing.T) {
	s := tempStore(t)
	ctx := t.Context()

	id, err := s.RecordCortexMemory(ctx, &CortexMemory{
		MemoryType: "solution_path",
		Species:    "go-feature",
		Signature:  "reinforce-test",
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	// Reinforce with positive reward
	err = s.ReinforceCortexMemory(ctx, id, 1.0, "session-a")
	if err != nil {
		t.Fatalf("reinforce 1: %v", err)
	}

	// Reinforce with zero reward (not a win)
	err = s.ReinforceCortexMemory(ctx, id, 0.0, "session-b")
	if err != nil {
		t.Fatalf("reinforce 2: %v", err)
	}

	got, err := s.GetCortexMemory(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.VisitCount != 2 {
		t.Errorf("expected visit_count 2, got %d", got.VisitCount)
	}
	if got.WinCount != 1 {
		t.Errorf("expected win_count 1, got %d", got.WinCount)
	}
	if got.TotalReward != 1.0 {
		t.Errorf("expected total_reward 1.0, got %f", got.TotalReward)
	}
	if got.AvgReward != 0.5 {
		t.Errorf("expected avg_reward 0.5, got %f", got.AvgReward)
	}
	if got.UCB1Score <= 0 {
		t.Errorf("expected positive UCB1 score, got %f", got.UCB1Score)
	}
	if got.LastReinforcedAt == nil {
		t.Error("expected non-nil last_reinforced_at")
	}
}

func TestReinforceCortexMemory_AppendsSessions(t *testing.T) {
	s := tempStore(t)
	ctx := t.Context()

	id, err := s.RecordCortexMemory(ctx, &CortexMemory{
		MemoryType: "tool_chain",
		Signature:  "session-append-test",
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	err = s.ReinforceCortexMemory(ctx, id, 1.0, "s1")
	if err != nil {
		t.Fatalf("reinforce 1: %v", err)
	}
	err = s.ReinforceCortexMemory(ctx, id, 0.5, "s2")
	if err != nil {
		t.Fatalf("reinforce 2: %v", err)
	}

	got, err := s.GetCortexMemory(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	var sessions []string
	if err := json.Unmarshal([]byte(got.SourceSessions), &sessions); err != nil {
		t.Fatalf("unmarshal source_sessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d: %v", len(sessions), sessions)
	}
	if sessions[0] != "s1" || sessions[1] != "s2" {
		t.Errorf("expected [s1, s2], got %v", sessions)
	}
}

func TestQueryCortexMemories(t *testing.T) {
	s := tempStore(t)
	ctx := t.Context()

	// Insert memories with different species/types/scores
	memories := []CortexMemory{
		{MemoryType: "solution_path", Species: "go-feature", Signature: "q1", UCB1Score: 5.0},
		{MemoryType: "solution_path", Species: "go-feature", Signature: "q2", UCB1Score: 10.0},
		{MemoryType: "tool_chain", Species: "go-feature", Signature: "q3", UCB1Score: 3.0},
		{MemoryType: "solution_path", Species: "js-component", Signature: "q4", UCB1Score: 8.0},
		{MemoryType: "context_hint", Species: "", Signature: "q5", UCB1Score: 7.0}, // universal
	}
	for i := range memories {
		if _, err := s.RecordCortexMemory(ctx, &memories[i]); err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
	}

	// Query for go-feature solution_path — should get q2, q1 (ordered by UCB1 desc)
	results, err := s.QueryCortexMemories(ctx, "go-feature", "solution_path", 10)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Signature != "q2" {
		t.Errorf("expected first result q2 (highest UCB1), got %q", results[0].Signature)
	}
	if results[1].Signature != "q1" {
		t.Errorf("expected second result q1, got %q", results[1].Signature)
	}

	// Query for go-feature (all types) — should include universal memory q5
	results, err = s.QueryCortexMemories(ctx, "go-feature", "", 10)
	if err != nil {
		t.Fatalf("query all types: %v", err)
	}
	if len(results) != 4 { // q1, q2, q3 (go-feature) + q5 (universal)
		t.Errorf("expected 4 results (3 species + 1 universal), got %d", len(results))
	}

	// Query empty species — returns all
	results, err = s.QueryCortexMemories(ctx, "", "", 10)
	if err != nil {
		t.Fatalf("query all: %v", err)
	}
	if len(results) != 5 {
		t.Errorf("expected 5 results, got %d", len(results))
	}
}

func TestGetTopCortexMemories(t *testing.T) {
	s := tempStore(t)
	ctx := t.Context()

	for i, sig := range []string{"top1", "top2", "top3"} {
		_, err := s.RecordCortexMemory(ctx, &CortexMemory{
			MemoryType: "solution_path",
			Species:    "go-feature",
			Signature:  sig,
			UCB1Score:  float64(i + 1),
		})
		if err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	results, err := s.GetTopCortexMemories(ctx, "go-feature", 2)
	if err != nil {
		t.Fatalf("GetTopCortexMemories: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	// top3 has highest UCB1 score (3.0)
	if results[0].Signature != "top3" {
		t.Errorf("expected first result 'top3', got %q", results[0].Signature)
	}
}

func TestTouchCortexMemory(t *testing.T) {
	s := tempStore(t)
	ctx := t.Context()

	id, err := s.RecordCortexMemory(ctx, &CortexMemory{
		MemoryType: "solution_path",
		Signature:  "touch-test",
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	// Before touch: last_accessed_at should be nil
	got, err := s.GetCortexMemory(ctx, id)
	if err != nil {
		t.Fatalf("get before touch: %v", err)
	}
	if got.LastAccessedAt != nil {
		t.Error("expected nil last_accessed_at before touch")
	}

	// Touch
	err = s.TouchCortexMemory(ctx, id)
	if err != nil {
		t.Fatalf("touch: %v", err)
	}

	// After touch: last_accessed_at should be set
	got, err = s.GetCortexMemory(ctx, id)
	if err != nil {
		t.Fatalf("get after touch: %v", err)
	}
	if got.LastAccessedAt == nil {
		t.Error("expected non-nil last_accessed_at after touch")
	}
}

func TestDecayCortexMemories(t *testing.T) {
	s := tempStore(t)
	ctx := t.Context()

	// Record a memory and reinforce it (so it has visit_count > 0)
	id, err := s.RecordCortexMemory(ctx, &CortexMemory{
		MemoryType: "solution_path",
		Signature:  "decay-test",
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	err = s.ReinforceCortexMemory(ctx, id, 1.0, "s1")
	if err != nil {
		t.Fatalf("reinforce: %v", err)
	}

	before, err := s.GetCortexMemory(ctx, id)
	if err != nil {
		t.Fatalf("get before decay: %v", err)
	}

	// Decay with olderThan in the future (should affect all)
	affected, err := s.DecayCortexMemories(ctx, time.Now().Add(time.Hour), 0.5)
	if err != nil {
		t.Fatalf("decay: %v", err)
	}
	if affected != 1 {
		t.Errorf("expected 1 affected, got %d", affected)
	}

	after, err := s.GetCortexMemory(ctx, id)
	if err != nil {
		t.Fatalf("get after decay: %v", err)
	}

	expectedScore := before.UCB1Score * 0.5
	if math.Abs(after.UCB1Score-expectedScore) > 0.001 {
		t.Errorf("expected ucb1_score ~%f, got %f", expectedScore, after.UCB1Score)
	}
}

func TestDecayCortexMemories_InvalidFactor(t *testing.T) {
	s := tempStore(t)
	_, err := s.DecayCortexMemories(t.Context(), time.Now(), 0.0)
	if err == nil {
		t.Error("expected error for factor 0.0")
	}
	_, err = s.DecayCortexMemories(t.Context(), time.Now(), 1.0)
	if err == nil {
		t.Error("expected error for factor 1.0")
	}
	_, err = s.DecayCortexMemories(t.Context(), time.Now(), 1.5)
	if err == nil {
		t.Error("expected error for factor 1.5")
	}
}

func TestComputeUCB1(t *testing.T) {
	// Unvisited: always explore
	score := computeUCB1(0, 0, 100)
	if score != math.MaxFloat64 {
		t.Errorf("expected MaxFloat64 for unvisited, got %f", score)
	}

	// Known values: avgReward=0.5, visits=10, parentVisits=100
	score = computeUCB1(0.5, 10, 100)
	// exploitation = 0.5
	// exploration = 1.414 * sqrt(ln(100) / 10) = 1.414 * sqrt(4.605/10) = 1.414 * 0.6788 = 0.9598
	expected := 0.5 + 1.414*math.Sqrt(math.Log(100)/10)
	if math.Abs(score-expected) > 0.001 {
		t.Errorf("expected UCB1 ~%f, got %f", expected, score)
	}

	// Zero parent visits: treated as 1
	score = computeUCB1(0.5, 1, 0)
	expected = 0.5 + 1.414*math.Sqrt(math.Log(1)/1)
	if math.Abs(score-expected) > 0.001 {
		t.Errorf("expected UCB1 ~%f with zero parents, got %f", expected, score)
	}
}
