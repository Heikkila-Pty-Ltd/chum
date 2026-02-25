package temporal

import (
	"encoding/json"
	"testing"
)

func TestRankPlanningCandidatesRanksAndShortlists(t *testing.T) {
	items := []BacklogItem{
		{
			ID:          "option-c",
			Title:       "C item",
			Impact:      "low",
			Effort:      "L",
			Recommended: false,
		},
		{
			ID:          "option-a",
			Title:       "A item",
			Impact:      "high",
			Effort:      "S",
			Recommended: true,
		},
		{
			ID:          "option-b",
			Title:       "B item",
			Impact:      "medium",
			Effort:      "M",
			Recommended: false,
		},
	}

	candidates := rankPlanningCandidates(items, 2)
	if len(candidates) != 3 {
		t.Fatalf("len(candidates) = %d, want 3", len(candidates))
	}

	if candidates[0].Item.ID != "option-a" {
		t.Fatalf("rank 1 = %s, want option-a", candidates[0].Item.ID)
	}
	if candidates[1].Item.ID != "option-b" {
		t.Fatalf("rank 2 = %s, want option-b", candidates[1].Item.ID)
	}
	if candidates[2].Item.ID != "option-c" {
		t.Fatalf("rank 3 = %s, want option-c", candidates[2].Item.ID)
	}

	if candidates[0].Rank != 1 || candidates[1].Rank != 2 || candidates[2].Rank != 3 {
		t.Fatalf("unexpected ranks: %#v", candidates)
	}
	if !candidates[0].Shortlisted || !candidates[1].Shortlisted || candidates[2].Shortlisted {
		t.Fatalf("unexpected shortlist flags: %#v", candidates)
	}
	if !(candidates[0].Score > candidates[1].Score && candidates[1].Score > candidates[2].Score) {
		t.Fatalf("scores are not strictly descending: %.2f %.2f %.2f", candidates[0].Score, candidates[1].Score, candidates[2].Score)
	}
}

func TestRankPlanningCandidatesTieBreaksByID(t *testing.T) {
	items := []BacklogItem{
		{ID: "option-z", Title: "Z", Impact: "medium", Effort: "M"},
		{ID: "option-x", Title: "X", Impact: "medium", Effort: "M"},
		{ID: "option-y", Title: "Y", Impact: "medium", Effort: "M"},
	}

	candidates := rankPlanningCandidates(items, 2)
	if len(candidates) != 3 {
		t.Fatalf("len(candidates) = %d, want 3", len(candidates))
	}

	want := []string{"option-x", "option-y", "option-z"}
	for i := range want {
		if candidates[i].Item.ID != want[i] {
			t.Fatalf("rank %d = %s, want %s", i+1, candidates[i].Item.ID, want[i])
		}
	}
}

func TestCandidateStatusMetadataJSONContainsStatusAndRank(t *testing.T) {
	meta := candidateStatusMetadataJSON(planningCandidate{
		Item:        BacklogItem{ID: "option-a", Impact: "high", Effort: "S", Recommended: true},
		Score:       123.4,
		Rank:        1,
		Shortlisted: true,
	}, "deferred")

	var decoded map[string]any
	if err := json.Unmarshal([]byte(meta), &decoded); err != nil {
		t.Fatalf("metadata json unmarshal failed: %v", err)
	}
	if decoded["status"] != "deferred" {
		t.Fatalf("status = %v, want deferred", decoded["status"])
	}
	if decoded["rank"] != float64(1) {
		t.Fatalf("rank = %v, want 1", decoded["rank"])
	}
}

func TestNormalizePlanningCandidateTopK(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{name: "default on zero", in: 0, want: 5},
		{name: "default on negative", in: -2, want: 5},
		{name: "pass through valid", in: 8, want: 8},
		{name: "clamp max", in: 99, want: 20},
	}

	for _, tc := range tests {
		got := normalizePlanningCandidateTopK(tc.in)
		if got != tc.want {
			t.Fatalf("%s: normalizePlanningCandidateTopK(%d)=%d, want %d", tc.name, tc.in, got, tc.want)
		}
	}
}

func TestRankPlanningCandidatesAppliesAdjustments(t *testing.T) {
	items := []BacklogItem{
		{ID: "a", Title: "A", Impact: "medium", Effort: "M", Recommended: false},
		{ID: "b", Title: "B", Impact: "medium", Effort: "M", Recommended: false},
	}

	adjustments := map[string]float64{"b": 20}
	candidates := rankPlanningCandidates(items, 2, adjustments)
	if len(candidates) != 2 {
		t.Fatalf("len(candidates) = %d, want 2", len(candidates))
	}
	if candidates[0].Item.ID != "b" {
		t.Fatalf("rank 1 = %s, want b", candidates[0].Item.ID)
	}
}

func TestProposeAlternativeCandidatesPromotesDeferredAndPenalizesSelected(t *testing.T) {
	candidates := []planningCandidate{
		{Item: BacklogItem{ID: "sel", Title: "Selected"}, Rank: 1, Shortlisted: true},
		{Item: BacklogItem{ID: "alt-1", Title: "Alt 1"}, Rank: 2, Shortlisted: true},
		{Item: BacklogItem{ID: "alt-2", Title: "Alt 2"}, Rank: 3, Shortlisted: true},
		{Item: BacklogItem{ID: "pruned", Title: "Pruned"}, Rank: 4, Shortlisted: false},
	}
	adjustments := map[string]float64{}

	proposals := proposeAlternativeCandidates("sel", candidates, adjustments)
	if len(proposals) == 0 {
		t.Fatal("expected at least one proposal")
	}
	if proposals[0].Candidate.Item.ID != "alt-1" {
		t.Fatalf("first proposal = %s, want alt-1", proposals[0].Candidate.Item.ID)
	}
	if adjustments["sel"] >= 0 {
		t.Fatalf("selected adjustment = %.1f, want negative penalty", adjustments["sel"])
	}
	if adjustments["alt-1"] <= 0 {
		t.Fatalf("alt-1 adjustment = %.1f, want positive boost", adjustments["alt-1"])
	}
}
