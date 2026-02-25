package store

import (
	"math"
	"testing"
)

func TestMCTSTablesCreatedOnOpen(t *testing.T) {
	s := tempStore(t)

	tables := []string{"mcts_nodes", "mcts_edges", "mcts_rollouts", "mcts_edge_stats"}
	for _, table := range tables {
		var count int
		if err := s.DB().QueryRow(`SELECT COUNT(*) FROM pragma_table_info('` + table + `')`).Scan(&count); err != nil {
			t.Fatalf("pragma_table_info(%s) failed: %v", table, err)
		}
		if count == 0 {
			t.Fatalf("table %s not created", table)
		}
	}
}

func TestUpsertAndListMCTSEdgeStats(t *testing.T) {
	s := tempStore(t)

	err := s.UpsertMCTSEdgeStat(MCTSEdgeStat{
		ParentNodeKey:  "dispatch_lane_v2",
		Species:        "species-a",
		ActionKey:      "direct",
		Visits:         2,
		Wins:           1,
		TotalReward:    1.5,
		TotalCostUSD:   0.12,
		TotalDurationS: 30,
		LastOutcome:    "started",
	})
	if err != nil {
		t.Fatalf("first upsert failed: %v", err)
	}

	err = s.UpsertMCTSEdgeStat(MCTSEdgeStat{
		ParentNodeKey:  "dispatch_lane_v2",
		Species:        "species-a",
		ActionKey:      "direct",
		Visits:         5,
		Wins:           3,
		TotalReward:    2.25,
		TotalCostUSD:   0.40,
		TotalDurationS: 75,
		LastOutcome:    "success",
	})
	if err != nil {
		t.Fatalf("second upsert failed: %v", err)
	}

	stats, err := s.ListMCTSEdgeStats("dispatch_lane_v2", "species-a", 10)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("stats len = %d, want 1", len(stats))
	}

	got := stats[0]
	if got.ActionKey != "direct" {
		t.Fatalf("action_key = %q, want direct", got.ActionKey)
	}
	if got.Visits != 5 || got.Wins != 3 {
		t.Fatalf("visits/wins = %d/%d, want 5/3", got.Visits, got.Wins)
	}
	if math.Abs(got.TotalReward-2.25) > 0.0001 {
		t.Fatalf("total_reward = %f, want 2.25", got.TotalReward)
	}
	if got.LastOutcome != "success" {
		t.Fatalf("last_outcome = %q, want success", got.LastOutcome)
	}
}

func TestRecordMCTSRolloutUpdatesEdgeStats(t *testing.T) {
	s := tempStore(t)

	id1, err := s.RecordMCTSRollout(MCTSRollout{
		TaskID:        "morsel-1",
		Project:       "proj",
		Species:       "species-rollout",
		ParentNodeKey: "dispatch_lane_v2",
		ActionKey:     "turtle",
		Outcome:       "started",
		Reward:        1.0,
		CostUSD:       0.50,
		DurationS:     45,
	})
	if err != nil {
		t.Fatalf("record rollout 1 failed: %v", err)
	}
	if id1 <= 0 {
		t.Fatalf("rollout id1 = %d, want > 0", id1)
	}

	id2, err := s.RecordMCTSRollout(MCTSRollout{
		TaskID:        "morsel-1",
		Project:       "proj",
		Species:       "species-rollout",
		ParentNodeKey: "dispatch_lane_v2",
		ActionKey:     "turtle",
		Outcome:       "failed",
		Reward:        0,
		CostUSD:       0.10,
		DurationS:     15,
	})
	if err != nil {
		t.Fatalf("record rollout 2 failed: %v", err)
	}
	if id2 <= id1 {
		t.Fatalf("rollout id2 = %d, want > id1 (%d)", id2, id1)
	}

	stats, err := s.ListMCTSEdgeStats("dispatch_lane_v2", "species-rollout", 10)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("stats len = %d, want 1", len(stats))
	}
	got := stats[0]
	if got.Visits != 2 {
		t.Fatalf("visits = %d, want 2", got.Visits)
	}
	if got.Wins != 1 {
		t.Fatalf("wins = %d, want 1", got.Wins)
	}
	if math.Abs(got.TotalReward-1.0) > 0.0001 {
		t.Fatalf("total_reward = %f, want 1.0", got.TotalReward)
	}
	if math.Abs(got.TotalCostUSD-0.60) > 0.0001 {
		t.Fatalf("total_cost_usd = %f, want 0.60", got.TotalCostUSD)
	}
	if got.LastOutcome != "failed" {
		t.Fatalf("last_outcome = %q, want failed", got.LastOutcome)
	}

	var rolloutCount int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM mcts_rollouts WHERE parent_node_key = ? AND action_key = ?`, "dispatch_lane_v2", "turtle").Scan(&rolloutCount); err != nil {
		t.Fatalf("count rollouts failed: %v", err)
	}
	if rolloutCount != 2 {
		t.Fatalf("rollout count = %d, want 2", rolloutCount)
	}
}

func TestListMCTSEdgeStatsFiltersBySpecies(t *testing.T) {
	s := tempStore(t)

	if err := s.UpsertMCTSEdgeStat(MCTSEdgeStat{
		ParentNodeKey: "dispatch_lane_v2",
		Species:       "species-a",
		ActionKey:     "direct",
		Visits:        1,
	}); err != nil {
		t.Fatalf("upsert species-a failed: %v", err)
	}
	if err := s.UpsertMCTSEdgeStat(MCTSEdgeStat{
		ParentNodeKey: "dispatch_lane_v2",
		Species:       "species-b",
		ActionKey:     "direct",
		Visits:        2,
	}); err != nil {
		t.Fatalf("upsert species-b failed: %v", err)
	}

	statsA, err := s.ListMCTSEdgeStats("dispatch_lane_v2", "species-a", 10)
	if err != nil {
		t.Fatalf("list species-a failed: %v", err)
	}
	if len(statsA) != 1 || statsA[0].Species != "species-a" {
		t.Fatalf("unexpected species-a stats: %#v", statsA)
	}

	statsAll, err := s.ListMCTSEdgeStats("dispatch_lane_v2", "", 10)
	if err != nil {
		t.Fatalf("list all species failed: %v", err)
	}
	if len(statsAll) != 2 {
		t.Fatalf("all species len = %d, want 2", len(statsAll))
	}
}
