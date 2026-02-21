package store

import (
	"context"
	"testing"
	"time"

	"github.com/antigravity-dev/chum/internal/graph"
)

// TestStoreStepMetrics verifies that step metrics can be stored and retrieved.
func TestStoreStepMetrics(t *testing.T) {
	s := tempStore(t)
	defer s.Close()

	// Create a dispatch to associate step metrics with.
	dispatchID, err := s.RecordDispatch("bead-step", "proj", "claude", "anthropic", "fast", 0, "", "", "", "", "temporal")
	if err != nil {
		t.Fatalf("RecordDispatch failed: %v", err)
	}

	// Store several step metrics.
	steps := []struct {
		name      string
		durationS float64
		status    string
		slow      bool
	}{
		{"plan", 1.2, "ok", false},
		{"gate", 0.01, "skipped", false},
		{"execute[1]", 45.6, "ok", false},
		{"review[1]", 12.3, "ok", false},
		{"semgrep[1]", 0.8, "ok", false},
		{"dod[1]", 130.5, "failed", true},
	}

	for _, step := range steps {
		if err := s.StoreStepMetric(dispatchID, "bead-step", "proj", step.name, step.durationS, step.status, step.slow); err != nil {
			t.Fatalf("StoreStepMetric(%s) failed: %v", step.name, err)
		}
	}

	// Retrieve and verify.
	records, err := s.GetStepMetricsByDispatch(dispatchID)
	if err != nil {
		t.Fatalf("GetStepMetricsByDispatch failed: %v", err)
	}
	if len(records) != len(steps) {
		t.Fatalf("expected %d step metrics, got %d", len(steps), len(records))
	}

	for i, r := range records {
		if r.StepName != steps[i].name {
			t.Errorf("step %d: name = %q, want %q", i, r.StepName, steps[i].name)
		}
		if r.DurationS != steps[i].durationS {
			t.Errorf("step %d: duration = %f, want %f", i, r.DurationS, steps[i].durationS)
		}
		if r.Status != steps[i].status {
			t.Errorf("step %d: status = %q, want %q", i, r.Status, steps[i].status)
		}
		if r.Slow != steps[i].slow {
			t.Errorf("step %d: slow = %v, want %v", i, r.Slow, steps[i].slow)
		}
		if r.DispatchID != dispatchID {
			t.Errorf("step %d: dispatch_id = %d, want %d", i, r.DispatchID, dispatchID)
		}
	}

	// Verify that a different dispatch ID returns empty.
	empty, err := s.GetStepMetricsByDispatch(999999)
	if err != nil {
		t.Fatalf("GetStepMetricsByDispatch(999999) failed: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected 0 step metrics for non-existent dispatch, got %d", len(empty))
	}
}

// TestGetBacklogBeads verifies that backlog bead filtering works correctly.
func TestGetBacklogBeads(t *testing.T) {
	s := tempStore(t)
	defer s.Close()

	dag := graph.NewDAG(s.DB())
	if err := dag.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("EnsureSchema failed: %v", err)
	}

	ctx := context.Background()
	_, err := dag.CreateTask(ctx, graph.Task{
		Title:   "Backlog task",
		Status:  "open",
		Project: "test-project",
	})
	if err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	backlogBeads, err := s.GetBacklogBeads(ctx, dag, "test-project")
	if err != nil {
		t.Fatalf("GetBacklogBeads failed: %v", err)
	}
	if len(backlogBeads) != 1 {
		t.Errorf("Expected 1 backlog bead, got %d", len(backlogBeads))
	}
}

// TestGetSprintContext verifies that sprint context gathering works.
func TestGetSprintContext(t *testing.T) {
	s := tempStore(t)
	defer s.Close()

	dag := graph.NewDAG(s.DB())
	if err := dag.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("EnsureSchema failed: %v", err)
	}

	ctx := context.Background()
	sprintContext, err := s.GetSprintContext(ctx, dag, "test-project", 7)
	if err != nil {
		t.Fatalf("GetSprintContext failed: %v", err)
	}

	if sprintContext == nil {
		t.Fatal("Expected non-nil sprint context")
	}

	t.Logf("GetSprintContext succeeded: %d backlog, %d in-progress, %d recent",
		len(sprintContext.BacklogBeads), len(sprintContext.InProgressBeads), len(sprintContext.RecentCompletions))
}

// TestBuildDependencyGraph tests the dependency graph building.
func TestBuildDependencyGraph(t *testing.T) {
	testTasks := []graph.Task{
		{ID: "test-1", Title: "First task", Status: "open", DependsOn: []string{}},
		{ID: "test-2", Title: "Second task", Status: "open", DependsOn: []string{"test-1"}},
		{ID: "test-3", Title: "Third task", Status: "closed", DependsOn: []string{"test-2"}},
	}

	depGraph := graph.BuildDepGraph(testTasks)
	if depGraph == nil {
		t.Fatal("BuildDepGraph should return a valid dependency graph")
	}

	nodes := depGraph.Nodes()
	if len(nodes) != 3 {
		t.Errorf("Expected 3 nodes in dependency graph, got %d", len(nodes))
	}

	test2Deps := depGraph.DependsOnIDs("test-2")
	if len(test2Deps) != 1 || test2Deps[0] != "test-1" {
		t.Errorf("Expected test-2 to depend on test-1, got dependencies: %v", test2Deps)
	}

	test1Blocks := depGraph.BlocksIDs("test-1")
	if len(test1Blocks) != 1 || test1Blocks[0] != "test-2" {
		t.Errorf("Expected test-1 to block test-2, got blocks: %v", test1Blocks)
	}
}

// TestEnrichBacklogBeadResilient verifies that enrichment doesn't fail on missing data.
func TestEnrichBacklogBeadResilient(t *testing.T) {
	s := tempStore(t)
	defer s.Close()

	testBead := &BacklogBead{
		Task: &graph.Task{ID: "non-existent-bead", Title: "Test bead"},
	}

	s.enrichBacklogBead("test-project", testBead)

	if testBead.DispatchCount < 0 {
		t.Error("DispatchCount should be non-negative")
	}
	if testBead.FailureCount < 0 {
		t.Error("FailureCount should be non-negative")
	}
}

// TestCalculateReadinessStats tests the dependency blocking logic.
func TestCalculateReadinessStats(t *testing.T) {
	s := tempStore(t)
	defer s.Close()

	testTasks := []graph.Task{
		{ID: "ready-1", Title: "Ready task (no deps)", Status: "open", DependsOn: []string{}},
		{ID: "blocked-1", Title: "Blocked task (open dep)", Status: "open", DependsOn: []string{"ready-1"}},
		{ID: "ready-2", Title: "Ready task (closed dep)", Status: "open", DependsOn: []string{"closed-dep"}},
		{ID: "closed-dep", Title: "Closed dependency", Status: "closed", DependsOn: []string{}},
	}

	depGraph := graph.BuildDepGraph(testTasks)

	backlogBeads := []*BacklogBead{
		{Task: &testTasks[0]},
		{Task: &testTasks[1]},
		{Task: &testTasks[2]},
	}

	readyCount, blockedCount := s.calculateReadinessStats(backlogBeads, depGraph)

	if readyCount != 2 {
		t.Errorf("Expected 2 ready beads, got %d", readyCount)
	}
	if blockedCount != 1 {
		t.Errorf("Expected 1 blocked bead, got %d", blockedCount)
	}

	var blockedBead *BacklogBead
	for _, bead := range backlogBeads {
		if bead.IsBlocked {
			blockedBead = bead
			break
		}
	}

	if blockedBead == nil {
		t.Fatal("Expected to find a blocked bead")
	}

	if blockedBead.ID != "blocked-1" {
		t.Errorf("Expected blocked-1 to be blocked, got %s", blockedBead.ID)
	}

	if len(blockedBead.BlockingReasons) == 0 {
		t.Error("Expected blocking reasons to be set")
	}
}

// TestGetCurrentSprintBoundary tests sprint boundary retrieval.
func TestGetCurrentSprintBoundary(t *testing.T) {
	s := tempStore(t)
	defer s.Close()

	boundary, err := s.GetCurrentSprintBoundary()
	if err != nil {
		t.Errorf("GetCurrentSprintBoundary should not error: %v", err)
	}
	if boundary != nil {
		t.Log("Unexpected sprint boundary found")
	}

	now := time.Now()
	sprintStart := now.AddDate(0, 0, -3)
	sprintEnd := now.AddDate(0, 0, 4)

	if err := s.RecordSprintBoundary(1, sprintStart, sprintEnd); err != nil {
		t.Fatalf("RecordSprintBoundary failed: %v", err)
	}

	boundary, err = s.GetCurrentSprintBoundary()
	if err != nil {
		t.Fatalf("GetCurrentSprintBoundary failed after recording: %v", err)
	}
	if boundary == nil {
		t.Fatal("Expected to find current sprint boundary after recording")
	}
	if boundary.SprintNumber != 1 {
		t.Errorf("Expected sprint number 1, got %d", boundary.SprintNumber)
	}
}

func TestSprintPlanningRecords(t *testing.T) {
	s := tempStore(t)
	defer s.Close()

	last, err := s.GetLastSprintPlanning("test-project")
	if err != nil {
		t.Fatalf("GetLastSprintPlanning failed: %v", err)
	}
	if last != nil {
		t.Fatalf("expected nil when no records exist, got %+v", last)
	}

	if err := s.RecordSprintPlanning("test-project", "threshold", 51, 50, "triggered", "triggered by backlog threshold"); err != nil {
		t.Fatalf("RecordSprintPlanning failed: %v", err)
	}

	last, err = s.GetLastSprintPlanning("test-project")
	if err != nil {
		t.Fatalf("GetLastSprintPlanning after insert failed: %v", err)
	}
	if last == nil {
		t.Fatal("expected last sprint planning record")
	}
	if last.Project != "test-project" {
		t.Fatalf("project = %q, want test-project", last.Project)
	}
	if last.Trigger != "threshold" {
		t.Fatalf("trigger = %q, want threshold", last.Trigger)
	}
	if last.Backlog != 51 {
		t.Fatalf("backlog = %d, want 51", last.Backlog)
	}
	if last.Threshold != 50 {
		t.Fatalf("threshold = %d, want 50", last.Threshold)
	}
	if last.Result != "triggered" {
		t.Fatalf("result = %q, want triggered", last.Result)
	}
	if last.TriggeredAt.IsZero() {
		t.Fatal("triggered_at should be set")
	}
}
