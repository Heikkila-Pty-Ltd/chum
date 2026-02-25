package temporal

import (
	"strings"
	"testing"
)

func TestInterpretPlanningGoal(t *testing.T) {
	summary, full := interpretPlanningGoal(BacklogPresentation{
		Rationale: "Deliver a safe first brain slice before full rollout.",
		Items: []BacklogItem{
			{ID: "brain-01", Title: "Deterministic gates"},
			{ID: "brain-02", Title: "Trace compaction"},
			{ID: "brain-03", Title: "Candidate replay"},
			{ID: "brain-04", Title: "Dashboard"},
		},
	})

	if summary != "Deliver a safe first brain slice before full rollout." {
		t.Fatalf("summary = %q", summary)
	}
	if !strings.Contains(full, "brain-01:Deterministic gates") {
		t.Fatalf("full missing first candidate: %q", full)
	}
	if strings.Contains(full, "brain-04:Dashboard") {
		t.Fatalf("full should only include top 3 candidates: %q", full)
	}
}

func TestBuildPlanningBehaviorContract(t *testing.T) {
	contract := buildPlanningBehaviorContract(
		BacklogItem{
			ID:        "brain-01",
			Title:     "Deterministic planning gates",
			Rationale: "prevents loops",
		},
		PlanSummary{
			What:      "Implement strict planning gate validation and rollback traces.",
			Why:       "Stabilize planning and avoid shiny-object drift.",
			Effort:    "S",
			DoDChecks: []string{"go test ./internal/temporal", "go test ./internal/store"},
		},
		map[string]string{"1": "A", "2": "A"},
	)

	if !strings.Contains(contract.OptimalSlice, "Deterministic planning gates") {
		t.Fatalf("optimal_slice = %q", contract.OptimalSlice)
	}
	if contract.LooksLike == "" {
		t.Fatal("looks_like should not be empty")
	}
	if !strings.Contains(contract.Does, "go test ./internal/temporal") {
		t.Fatalf("does = %q", contract.Does)
	}
	if contract.WhyNow == "" {
		t.Fatal("why_now should not be empty")
	}
	if len(contract.AnswerHints) != 2 {
		t.Fatalf("answer_hints len = %d, want 2", len(contract.AnswerHints))
	}

	asText := behaviorContractToText(contract)
	if !strings.Contains(asText, "optimal_slice=") || !strings.Contains(asText, "does=") {
		t.Fatalf("behavior contract text missing expected fields: %q", asText)
	}
}

func TestShouldIteratePlanningLoop(t *testing.T) {
	tests := []struct {
		name   string
		cycle  int
		item   BacklogItem
		sum    PlanSummary
		wantIt bool
	}{
		{
			name:   "epic scope triggers loop",
			cycle:  1,
			item:   BacklogItem{Title: "Epic planning overhaul", Effort: "M"},
			sum:    PlanSummary{What: "Refactor planning loop", Why: "Needed for mega-epic roadmap", Effort: "M"},
			wantIt: true,
		},
		{
			name:   "large effort triggers loop",
			cycle:  1,
			item:   BacklogItem{Title: "Build planner", Effort: "L"},
			sum:    PlanSummary{What: "Large cross-system rewrite", Why: "Broad scope", Effort: "large"},
			wantIt: true,
		},
		{
			name:   "small slice proceeds",
			cycle:  1,
			item:   BacklogItem{Title: "Add trace event", Effort: "S"},
			sum:    PlanSummary{What: "Add one event and tests", Why: "Targeted improvement", Effort: "S"},
			wantIt: false,
		},
		{
			name:   "max cycle reached proceeds",
			cycle:  maxPlanningCycles,
			item:   BacklogItem{Title: "Epic planning overhaul", Effort: "XL"},
			sum:    PlanSummary{What: "Broad rewrite", Why: "Epic scope", Effort: "XL"},
			wantIt: false,
		},
	}

	for _, tc := range tests {
		got, _ := shouldIteratePlanningLoop(tc.cycle, tc.item, tc.sum)
		if got != tc.wantIt {
			t.Fatalf("%s: shouldIteratePlanningLoop()=%t, want %t", tc.name, got, tc.wantIt)
		}
	}
}
