package temporal

import "testing"

func TestBuildFallbackTurtlePlanIsParseable(t *testing.T) {
	req := TurtlePlanningRequest{
		TaskID:      "task-123",
		Project:     "chum",
		Description: "Need a plan artifact that crabs can decompose safely.",
		Context:     []string{"internal/temporal/workflow_turtle.go", "internal/temporal/workflow_crab.go"},
	}

	plan := buildFallbackTurtlePlan(req)
	parsed, err := ParseMarkdownPlan(plan)
	if err != nil {
		t.Fatalf("fallback plan should be parseable: %v\nplan:\n%s", err, plan)
	}
	if parsed.Title == "" {
		t.Fatalf("expected non-empty title")
	}
	if len(parsed.ScopeItems) < 2 {
		t.Fatalf("expected at least 2 scope items, got %d", len(parsed.ScopeItems))
	}
}

func TestTurtlePlanCandidatesExtractsFencedMarkdown(t *testing.T) {
	raw := "Planner output:\n```markdown\n# Plan: Turtle Artifact\n## Context\nX\n## Scope\n- [ ] A\n## Acceptance Criteria\n- B\n## Out of Scope\n- C\n```\n"

	candidates := turtlePlanCandidates(raw)
	if len(candidates) == 0 {
		t.Fatalf("expected candidates from fenced markdown")
	}

	foundParseable := false
	for _, c := range candidates {
		if _, err := ParseMarkdownPlan(c); err == nil {
			foundParseable = true
			break
		}
	}
	if !foundParseable {
		t.Fatalf("expected at least one parseable candidate, got %v", candidates)
	}
}
