package store

import "testing"

func TestPlanningControlTablesCreatedOnOpen(t *testing.T) {
	s := tempStore(t)

	for _, table := range []string{"planning_state_snapshots", "planning_action_blacklist", "planning_candidate_scores"} {
		var count int
		if err := s.DB().QueryRow(`SELECT COUNT(*) FROM pragma_table_info('` + table + `')`).Scan(&count); err != nil {
			t.Fatalf("pragma_table_info(%s) failed: %v", table, err)
		}
		if count == 0 {
			t.Fatalf("table %s not created", table)
		}
	}
}

func TestPlanningSnapshotLifecycle(t *testing.T) {
	s := tempStore(t)

	if err := s.RecordPlanningStateSnapshot(PlanningStateSnapshot{
		SessionID: "planning-sess-1",
		RunID:     "run-1",
		Project:   "chum",
		TaskID:    "morsel-a",
		Cycle:     1,
		Stage:     "selection",
		StateHash: "hash-a",
		StateJSON: `{"stage":"selection"}`,
		Stable:    false,
		Reason:    "pre-gate",
	}); err != nil {
		t.Fatalf("record unstable snapshot failed: %v", err)
	}

	if err := s.RecordPlanningStateSnapshot(PlanningStateSnapshot{
		SessionID: "planning-sess-1",
		RunID:     "run-1",
		Project:   "chum",
		TaskID:    "morsel-a",
		Cycle:     1,
		Stage:     "summary",
		StateHash: "hash-b",
		StateJSON: `{"stage":"summary"}`,
		Stable:    true,
		Reason:    "gate_passed",
	}); err != nil {
		t.Fatalf("record stable snapshot failed: %v", err)
	}

	latest, err := s.GetLatestStablePlanningSnapshot("planning-sess-1")
	if err != nil {
		t.Fatalf("GetLatestStablePlanningSnapshot failed: %v", err)
	}
	if latest == nil {
		t.Fatal("expected latest stable snapshot")
	}
	if latest.StateHash != "hash-b" {
		t.Fatalf("state_hash = %q, want hash-b", latest.StateHash)
	}
	if latest.Stage != "summary" {
		t.Fatalf("stage = %q, want summary", latest.Stage)
	}
	if !latest.Stable {
		t.Fatal("expected stable=true")
	}
}

func TestPlanningBlacklistLifecycle(t *testing.T) {
	s := tempStore(t)

	if err := s.AddPlanningBlacklistEntry(PlanningBlacklistEntry{
		SessionID:  "planning-sess-2",
		Project:    "chum",
		TaskID:     "morsel-b",
		Cycle:      2,
		Stage:      "selection",
		StateHash:  "state-1",
		ActionHash: "action-1",
		Reason:     "gate_failed",
		Metadata:   `{"code":"selection_not_in_backlog"}`,
	}); err != nil {
		t.Fatalf("AddPlanningBlacklistEntry failed: %v", err)
	}

	blocked, err := s.IsPlanningActionBlacklisted("planning-sess-2", "state-1", "action-1")
	if err != nil {
		t.Fatalf("IsPlanningActionBlacklisted failed: %v", err)
	}
	if !blocked {
		t.Fatal("expected blocked=true")
	}

	notBlocked, err := s.IsPlanningActionBlacklisted("planning-sess-2", "state-1", "action-2")
	if err != nil {
		t.Fatalf("IsPlanningActionBlacklisted (different action) failed: %v", err)
	}
	if notBlocked {
		t.Fatal("expected blocked=false for different action hash")
	}
}

func TestPlanningCandidateScoreLifecycle(t *testing.T) {
	s := tempStore(t)

	initial, err := s.ListPlanningCandidateScores("chum", []string{"opt-a", "opt-b"})
	if err != nil {
		t.Fatalf("ListPlanningCandidateScores initial failed: %v", err)
	}
	if len(initial) != 0 {
		t.Fatalf("expected empty initial candidate score set, got %d", len(initial))
	}

	if err := s.AdjustPlanningCandidateScore("chum", "opt-a", 12, "alternative", "boost after poor outcome"); err != nil {
		t.Fatalf("AdjustPlanningCandidateScore boost failed: %v", err)
	}
	if err := s.AdjustPlanningCandidateScore("chum", "opt-a", -4, "failure", "downrank after rejection"); err != nil {
		t.Fatalf("AdjustPlanningCandidateScore penalty failed: %v", err)
	}
	if err := s.AdjustPlanningCandidateScore("chum", "opt-b", 10, "success", "plan agreed"); err != nil {
		t.Fatalf("AdjustPlanningCandidateScore success failed: %v", err)
	}

	scores, err := s.ListPlanningCandidateScores("chum", []string{"opt-a", "opt-b"})
	if err != nil {
		t.Fatalf("ListPlanningCandidateScores failed: %v", err)
	}
	if len(scores) != 2 {
		t.Fatalf("expected 2 score rows, got %d", len(scores))
	}

	byID := map[string]PlanningCandidateScore{}
	for i := range scores {
		byID[scores[i].OptionID] = scores[i]
	}

	optA, ok := byID["opt-a"]
	if !ok {
		t.Fatal("missing opt-a score row")
	}
	if optA.ScoreAdjustment != 8 {
		t.Fatalf("opt-a score_adjustment = %.1f, want 8.0", optA.ScoreAdjustment)
	}
	if optA.Successes != 0 {
		t.Fatalf("opt-a successes = %d, want 0", optA.Successes)
	}
	if optA.Failures != 1 {
		t.Fatalf("opt-a failures = %d, want 1", optA.Failures)
	}
	if optA.LastReason != "downrank after rejection" {
		t.Fatalf("opt-a last_reason = %q, want %q", optA.LastReason, "downrank after rejection")
	}

	optB, ok := byID["opt-b"]
	if !ok {
		t.Fatal("missing opt-b score row")
	}
	if optB.ScoreAdjustment != 10 {
		t.Fatalf("opt-b score_adjustment = %.1f, want 10.0", optB.ScoreAdjustment)
	}
	if optB.Successes != 1 {
		t.Fatalf("opt-b successes = %d, want 1", optB.Successes)
	}
	if optB.Failures != 0 {
		t.Fatalf("opt-b failures = %d, want 0", optB.Failures)
	}
}

func TestPlanningCandidateScoreClamp(t *testing.T) {
	s := tempStore(t)

	if err := s.AdjustPlanningCandidateScore("chum", "opt-a", 200, "success", "oversized boost"); err != nil {
		t.Fatalf("AdjustPlanningCandidateScore +200 failed: %v", err)
	}
	scores, err := s.ListPlanningCandidateScores("chum", []string{"opt-a"})
	if err != nil {
		t.Fatalf("ListPlanningCandidateScores +200 failed: %v", err)
	}
	if len(scores) != 1 {
		t.Fatalf("expected 1 score row, got %d", len(scores))
	}
	if scores[0].ScoreAdjustment != 50 {
		t.Fatalf("score_adjustment after +200 = %.1f, want 50.0", scores[0].ScoreAdjustment)
	}

	if err := s.AdjustPlanningCandidateScore("chum", "opt-b", -200, "failure", "oversized penalty"); err != nil {
		t.Fatalf("AdjustPlanningCandidateScore -200 failed: %v", err)
	}
	scores, err = s.ListPlanningCandidateScores("chum", []string{"opt-b"})
	if err != nil {
		t.Fatalf("ListPlanningCandidateScores -200 failed: %v", err)
	}
	if len(scores) != 1 {
		t.Fatalf("expected 1 score row, got %d", len(scores))
	}
	if scores[0].ScoreAdjustment != -50 {
		t.Fatalf("score_adjustment after -200 = %.1f, want -50.0", scores[0].ScoreAdjustment)
	}
}
