package portfolio

import (
	"testing"

	"github.com/antigravity-dev/chum/internal/graph"
)

func TestPortfolioBacklogStructure(t *testing.T) {
	// Test that all required fields exist and have correct types
	portfolio := &PortfolioBacklog{
		ProjectBacklogs:  make(map[string]ProjectBacklog),
		CrossProjectDeps: []CrossProjectDependency{},
		CapacityBudgets:  make(map[string]int),
		Summary:          PortfolioSummary{},
	}

	// Verify structure can be created
	if portfolio.ProjectBacklogs == nil {
		t.Error("ProjectBacklogs should be initialized")
	}

	if portfolio.CrossProjectDeps == nil {
		t.Error("CrossProjectDeps should be initialized")
	}

	if portfolio.CapacityBudgets == nil {
		t.Error("CapacityBudgets should be initialized")
	}
}

func TestProjectBacklogStructure(t *testing.T) {
	// Test ProjectBacklog struct can be created with expected fields
	backlog := ProjectBacklog{
		ProjectName:     "test",
		Workspace:       "/tmp",
		Priority:        1,
		UnrefinedBeads:  []graph.Task{},
		RefinedBeads:    []graph.Task{},
		AllBeads:        []graph.Task{},
		ReadyToWork:     []graph.Task{},
		TotalEstimate:   0,
		CapacityPercent: 50,
	}

	if backlog.ProjectName != "test" {
		t.Errorf("Expected project name 'test', got '%s'", backlog.ProjectName)
	}

	if backlog.Priority != 1 {
		t.Errorf("Expected priority 1, got %d", backlog.Priority)
	}
}

func TestGetProjectCapacityBudget(t *testing.T) {
	portfolio := &PortfolioBacklog{
		CapacityBudgets: map[string]int{
			"project-a": 60,
			"project-b": 40,
		},
	}

	// Test existing project
	budget := GetProjectCapacityBudget(portfolio, "project-a")
	if budget != 60 {
		t.Errorf("Expected budget 60, got %d", budget)
	}

	// Test non-existent project
	budget = GetProjectCapacityBudget(portfolio, "non-existent")
	if budget != 0 {
		t.Errorf("Expected budget 0 for non-existent project, got %d", budget)
	}
}

func TestGetHighPriorityProjects(t *testing.T) {
	portfolio := &PortfolioBacklog{
		Summary: PortfolioSummary{
			ProjectsByPriority: []string{"project-a", "project-b", "project-c"},
		},
	}

	projects := GetHighPriorityProjects(portfolio)
	expected := []string{"project-a", "project-b", "project-c"}

	if len(projects) != len(expected) {
		t.Errorf("Expected %d projects, got %d", len(expected), len(projects))
	}

	for i, project := range projects {
		if project != expected[i] {
			t.Errorf("Expected project[%d] = %s, got %s", i, expected[i], project)
		}
	}
}

func TestGetCrossProjectBlockersForProject(t *testing.T) {
	portfolio := &PortfolioBacklog{
		CrossProjectDeps: []CrossProjectDependency{
			{
				SourceProject: "project-a",
				SourceBeadID:  "a-1",
				TargetProject: "project-b",
				TargetBeadID:  "b-1",
				IsResolved:    false,
			},
			{
				SourceProject: "project-a",
				SourceBeadID:  "a-2",
				TargetProject: "project-b",
				TargetBeadID:  "b-2",
				IsResolved:    true, // Resolved, should not be blocker
			},
			{
				SourceProject: "project-c", // Different source project
				SourceBeadID:  "c-1",
				TargetProject: "project-b",
				TargetBeadID:  "b-3",
				IsResolved:    false,
			},
		},
	}

	blockers := GetCrossProjectBlockersForProject(portfolio, "project-a")

	// Should only return unresolved blockers for project-a
	if len(blockers) != 1 {
		t.Errorf("Expected 1 blocker, got %d", len(blockers))
	}

	if len(blockers) > 0 && blockers[0].SourceBeadID != "a-1" {
		t.Errorf("Expected blocker a-1, got %s", blockers[0].SourceBeadID)
	}
}

func TestFilterTasksFunctions(t *testing.T) {
	// Create test tasks with various states
	testTasks := []graph.Task{
		{
			ID:              "open-refined",
			Status:          "open",
			Acceptance:      "Has acceptance criteria",
			EstimateMinutes: 30,
		},
		{
			ID:              "open-unrefined",
			Status:          "open",
			Acceptance:      "",
			EstimateMinutes: 0,
		},
		{
			ID:     "closed-task",
			Status: "closed",
		},
		{
			ID:              "open-with-design",
			Status:          "open",
			Design:          "Has design notes",
			EstimateMinutes: 0,
		},
	}

	// Test filterOpenTasks
	openTasks := filterOpenTasks(testTasks)
	if len(openTasks) != 3 { // Should exclude the closed one
		t.Errorf("Expected 3 open tasks, got %d", len(openTasks))
	}

	// Test filterRefinedTasks
	refinedTasks := filterRefinedTasks(openTasks)
	if len(refinedTasks) != 2 { // open-refined and open-with-design
		t.Errorf("Expected 2 refined tasks, got %d", len(refinedTasks))
	}

	// Test filterUnrefinedTasks
	unrefinedTasks := filterUnrefinedTasks(openTasks)
	if len(unrefinedTasks) != 1 { // Only open-unrefined
		t.Errorf("Expected 1 unrefined task, got %d", len(unrefinedTasks))
	}
}
