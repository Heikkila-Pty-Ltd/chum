package graph

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// newTestDAGFile creates a DAG backed by a file-based SQLite database with a
// busy timeout. This is needed for concurrent tests because :memory: DBs are
// per-connection, and concurrent writers need a busy timeout to retry on lock.
func newTestDAGFile(t *testing.T) *DAG {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open file db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	dag := NewDAG(db)
	if err := dag.EnsureSchema(t.Context()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return dag
}

// ---------------------------------------------------------------------------
// ParseCrossDep edge cases
// ---------------------------------------------------------------------------

func TestParseCrossDep_LocalDependency(t *testing.T) {
	project, taskID, isCross := ParseCrossDep("task-abc123")
	if isCross {
		t.Fatal("expected local dependency")
	}
	if project != "" {
		t.Fatalf("expected empty project, got %q", project)
	}
	if taskID != "task-abc123" {
		t.Fatalf("expected taskID=task-abc123, got %q", taskID)
	}
}

func TestParseCrossDep_CrossDependency(t *testing.T) {
	project, taskID, isCross := ParseCrossDep("frontend:task-abc123")
	if !isCross {
		t.Fatal("expected cross dependency")
	}
	if project != "frontend" {
		t.Fatalf("expected project=frontend, got %q", project)
	}
	if taskID != "task-abc123" {
		t.Fatalf("expected taskID=task-abc123, got %q", taskID)
	}
}

func TestParseCrossDep_Empty(t *testing.T) {
	project, taskID, isCross := ParseCrossDep("")
	if isCross {
		t.Fatal("expected local dependency for empty string")
	}
	if project != "" {
		t.Fatalf("expected empty project, got %q", project)
	}
	if taskID != "" {
		t.Fatalf("expected empty taskID, got %q", taskID)
	}
}

func TestParseCrossDep_NoColon(t *testing.T) {
	project, taskID, isCross := ParseCrossDep("just-a-task-id")
	if isCross {
		t.Fatal("expected local dependency when no colon present")
	}
	if project != "" {
		t.Fatalf("expected empty project, got %q", project)
	}
	if taskID != "just-a-task-id" {
		t.Fatalf("expected taskID=just-a-task-id, got %q", taskID)
	}
}

func TestParseCrossDep_MultipleColons(t *testing.T) {
	// Only the first colon splits project from task ID.
	project, taskID, isCross := ParseCrossDep("proj:task:extra:colons")
	if !isCross {
		t.Fatal("expected cross dependency")
	}
	if project != "proj" {
		t.Fatalf("expected project=proj, got %q", project)
	}
	if taskID != "task:extra:colons" {
		t.Fatalf("expected taskID=task:extra:colons, got %q", taskID)
	}
}

func TestParseCrossDep_ColonAtStart(t *testing.T) {
	// Leading colon means empty project name.
	project, taskID, isCross := ParseCrossDep(":task-abc")
	if !isCross {
		t.Fatal("expected cross dependency (colon present)")
	}
	if project != "" {
		t.Fatalf("expected empty project, got %q", project)
	}
	if taskID != "task-abc" {
		t.Fatalf("expected taskID=task-abc, got %q", taskID)
	}
}

func TestParseCrossDep_ColonAtEnd(t *testing.T) {
	// Trailing colon means empty task ID.
	project, taskID, isCross := ParseCrossDep("proj:")
	if !isCross {
		t.Fatal("expected cross dependency (colon present)")
	}
	if project != "proj" {
		t.Fatalf("expected project=proj, got %q", project)
	}
	if taskID != "" {
		t.Fatalf("expected empty taskID, got %q", taskID)
	}
}

func TestParseCrossDep_JustColon(t *testing.T) {
	project, taskID, isCross := ParseCrossDep(":")
	if !isCross {
		t.Fatal("expected cross dependency (colon present)")
	}
	if project != "" {
		t.Fatalf("expected empty project, got %q", project)
	}
	if taskID != "" {
		t.Fatalf("expected empty taskID, got %q", taskID)
	}
}

// ---------------------------------------------------------------------------
// GetCrossProjectBlockers
// ---------------------------------------------------------------------------

func TestGetCrossProjectBlockers_MixedDeps(t *testing.T) {
	task := Task{
		ID:        "t1",
		DependsOn: []string{"local-dep", "frontend:task-001", "backend:task-002"},
	}

	blockers := GetCrossProjectBlockers(task)
	if len(blockers) != 2 {
		t.Fatalf("expected 2 cross-project blockers, got %d", len(blockers))
	}
	if blockers[0].Project != "frontend" || blockers[0].TaskID != "task-001" {
		t.Fatalf("unexpected first blocker: %+v", blockers[0])
	}
	if blockers[1].Project != "backend" || blockers[1].TaskID != "task-002" {
		t.Fatalf("unexpected second blocker: %+v", blockers[1])
	}
}

func TestGetCrossProjectBlockers_NoCrossDeps(t *testing.T) {
	task := Task{
		ID:        "t1",
		DependsOn: []string{"local-a", "local-b"},
	}

	blockers := GetCrossProjectBlockers(task)
	if len(blockers) != 0 {
		t.Fatalf("expected 0 cross-project blockers, got %d", len(blockers))
	}
}

func TestGetCrossProjectBlockers_NoDeps(t *testing.T) {
	task := Task{ID: "t1"}
	blockers := GetCrossProjectBlockers(task)
	if len(blockers) != 0 {
		t.Fatalf("expected 0 blockers, got %d", len(blockers))
	}
}

// ---------------------------------------------------------------------------
// IsCrossDepResolved
// ---------------------------------------------------------------------------

func TestIsCrossDepResolved_Closed(t *testing.T) {
	cpg := &CrossProjectGraph{
		Projects: map[string]map[string]*Task{
			"frontend": {
				"task-001": {ID: "task-001", Status: "closed"},
			},
		},
	}
	if !cpg.IsCrossDepResolved("frontend", "task-001") {
		t.Fatal("expected closed task to be resolved")
	}
}

func TestIsCrossDepResolved_Open(t *testing.T) {
	cpg := &CrossProjectGraph{
		Projects: map[string]map[string]*Task{
			"frontend": {
				"task-001": {ID: "task-001", Status: "open"},
			},
		},
	}
	if cpg.IsCrossDepResolved("frontend", "task-001") {
		t.Fatal("expected open task to be unresolved")
	}
}

func TestIsCrossDepResolved_MissingProject(t *testing.T) {
	cpg := &CrossProjectGraph{
		Projects: map[string]map[string]*Task{},
	}
	if cpg.IsCrossDepResolved("nonexistent", "task-001") {
		t.Fatal("expected missing project to be unresolved")
	}
}

func TestIsCrossDepResolved_MissingTask(t *testing.T) {
	cpg := &CrossProjectGraph{
		Projects: map[string]map[string]*Task{
			"frontend": {},
		},
	}
	if cpg.IsCrossDepResolved("frontend", "task-999") {
		t.Fatal("expected missing task to be unresolved")
	}
}

func TestIsCrossDepResolved_NilGraph(t *testing.T) {
	var cpg *CrossProjectGraph
	if cpg.IsCrossDepResolved("any", "task-001") {
		t.Fatal("expected nil graph to return false")
	}
}

func TestIsCrossDepResolved_NilProjects(t *testing.T) {
	cpg := &CrossProjectGraph{Projects: nil}
	if cpg.IsCrossDepResolved("any", "task-001") {
		t.Fatal("expected nil Projects to return false")
	}
}

// ---------------------------------------------------------------------------
// BuildCrossProjectGraph — multi-project load
// ---------------------------------------------------------------------------

func TestBuildCrossProjectGraph_MultiProject(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	// Create tasks in two projects.
	idA, _ := dag.CreateTask(ctx, Task{Title: "frontend-task", Project: "frontend"})
	idB, _ := dag.CreateTask(ctx, Task{Title: "backend-task", Project: "backend", Status: "closed"})

	cpg, err := BuildCrossProjectGraph(ctx, dag, map[string]string{
		"frontend": "/path/to/frontend",
		"backend":  "/path/to/backend",
	})
	if err != nil {
		t.Fatalf("BuildCrossProjectGraph: %v", err)
	}

	// Verify both projects loaded.
	if len(cpg.Projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(cpg.Projects))
	}

	// Check frontend project.
	frontendTasks := cpg.Projects["frontend"]
	if len(frontendTasks) != 1 {
		t.Fatalf("expected 1 frontend task, got %d", len(frontendTasks))
	}
	if frontendTasks[idA] == nil {
		t.Fatalf("expected frontend task %s to be present", idA)
	}
	if frontendTasks[idA].Title != "frontend-task" {
		t.Fatalf("expected title=frontend-task, got %q", frontendTasks[idA].Title)
	}

	// Check backend project.
	backendTasks := cpg.Projects["backend"]
	if len(backendTasks) != 1 {
		t.Fatalf("expected 1 backend task, got %d", len(backendTasks))
	}
	if backendTasks[idB] == nil {
		t.Fatalf("expected backend task %s to be present", idB)
	}
	if backendTasks[idB].Status != "closed" {
		t.Fatalf("expected status=closed, got %q", backendTasks[idB].Status)
	}
}

func TestBuildCrossProjectGraph_EmptyProject(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	cpg, err := BuildCrossProjectGraph(ctx, dag, map[string]string{
		"empty": "/path/to/empty",
	})
	if err != nil {
		t.Fatalf("BuildCrossProjectGraph: %v", err)
	}

	if len(cpg.Projects["empty"]) != 0 {
		t.Fatalf("expected 0 tasks for empty project, got %d", len(cpg.Projects["empty"]))
	}
}

func TestBuildCrossProjectGraph_NoProjects(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	cpg, err := BuildCrossProjectGraph(ctx, dag, map[string]string{})
	if err != nil {
		t.Fatalf("BuildCrossProjectGraph: %v", err)
	}

	if len(cpg.Projects) != 0 {
		t.Fatalf("expected 0 projects, got %d", len(cpg.Projects))
	}
}

func TestBuildCrossProjectGraph_TasksAreCloned(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	id, _ := dag.CreateTask(ctx, Task{Title: "original", Project: "proj", Labels: []string{"a"}})

	cpg, err := BuildCrossProjectGraph(ctx, dag, map[string]string{"proj": "/p"})
	if err != nil {
		t.Fatalf("BuildCrossProjectGraph: %v", err)
	}

	// Mutate the loaded task and verify original data not affected via a second load.
	cpg.Projects["proj"][id].Labels[0] = "mutated"

	cpg2, err := BuildCrossProjectGraph(ctx, dag, map[string]string{"proj": "/p"})
	if err != nil {
		t.Fatalf("second BuildCrossProjectGraph: %v", err)
	}
	if cpg2.Projects["proj"][id].Labels[0] != "a" {
		t.Fatalf("expected label 'a' on fresh load, got %q", cpg2.Projects["proj"][id].Labels[0])
	}
}

// ---------------------------------------------------------------------------
// FilterUnblockedCrossProject — cross-project blocking
// ---------------------------------------------------------------------------

func TestFilterUnblockedCrossProject_AllLocalDepsResolved(t *testing.T) {
	// Task with only local deps, all closed.
	tasks := []Task{
		{ID: "dep", Status: "closed"},
		{ID: "worker", Status: "ready", DependsOn: []string{"dep"}, Priority: 1},
	}
	localGraph := BuildDepGraph(tasks)
	cpg := &CrossProjectGraph{Projects: map[string]map[string]*Task{}}

	result := FilterUnblockedCrossProject(tasks, localGraph, cpg)
	if len(result) != 1 || result[0].ID != "worker" {
		t.Fatalf("expected [worker], got %v", taskIDs(result))
	}
}

func TestFilterUnblockedCrossProject_CrossDepBlocks(t *testing.T) {
	// Task depends on a cross-project task that is still open.
	tasks := []Task{
		{ID: "worker", Status: "ready", DependsOn: []string{"frontend:task-001"}, Priority: 1},
	}
	localGraph := BuildDepGraph(tasks)
	cpg := &CrossProjectGraph{
		Projects: map[string]map[string]*Task{
			"frontend": {
				"task-001": {ID: "task-001", Status: "open"},
			},
		},
	}

	result := FilterUnblockedCrossProject(tasks, localGraph, cpg)
	if len(result) != 0 {
		t.Fatalf("expected no unblocked tasks (cross dep open), got %v", taskIDs(result))
	}
}

func TestFilterUnblockedCrossProject_CrossDepResolved(t *testing.T) {
	// Task depends on a cross-project task that is closed.
	tasks := []Task{
		{ID: "worker", Status: "ready", DependsOn: []string{"frontend:task-001"}, Priority: 1},
	}
	localGraph := BuildDepGraph(tasks)
	cpg := &CrossProjectGraph{
		Projects: map[string]map[string]*Task{
			"frontend": {
				"task-001": {ID: "task-001", Status: "closed"},
			},
		},
	}

	result := FilterUnblockedCrossProject(tasks, localGraph, cpg)
	if len(result) != 1 || result[0].ID != "worker" {
		t.Fatalf("expected [worker], got %v", taskIDs(result))
	}
}

func TestFilterUnblockedCrossProject_MixedLocalAndCross(t *testing.T) {
	// Task with both local and cross deps. All must be resolved.
	tasks := []Task{
		{ID: "local-dep", Status: "closed"},
		{ID: "worker", Status: "ready", DependsOn: []string{"local-dep", "backend:api-task"}, Priority: 1},
	}
	localGraph := BuildDepGraph(tasks)

	// Cross dep is closed — worker should be unblocked.
	cpg := &CrossProjectGraph{
		Projects: map[string]map[string]*Task{
			"backend": {
				"api-task": {ID: "api-task", Status: "closed"},
			},
		},
	}

	result := FilterUnblockedCrossProject(tasks, localGraph, cpg)
	if len(result) != 1 || result[0].ID != "worker" {
		t.Fatalf("expected [worker], got %v", taskIDs(result))
	}
}

func TestFilterUnblockedCrossProject_MixedLocalAndCross_LocalBlocks(t *testing.T) {
	// Local dep is ready — even though cross dep is resolved, worker is blocked.
	tasks := []Task{
		{ID: "local-dep", Status: "ready"},
		{ID: "worker", Status: "ready", DependsOn: []string{"local-dep", "backend:api-task"}, Priority: 1},
	}
	localGraph := BuildDepGraph(tasks)
	cpg := &CrossProjectGraph{
		Projects: map[string]map[string]*Task{
			"backend": {
				"api-task": {ID: "api-task", Status: "closed"},
			},
		},
	}

	result := FilterUnblockedCrossProject(tasks, localGraph, cpg)
	// Only local-dep (open, no deps) is unblocked.
	if len(result) != 1 || result[0].ID != "local-dep" {
		t.Fatalf("expected [local-dep], got %v", taskIDs(result))
	}
}

func TestFilterUnblockedCrossProject_MissingCrossProject(t *testing.T) {
	// Cross dep references a project not in the graph — treated as unresolved.
	tasks := []Task{
		{ID: "worker", Status: "ready", DependsOn: []string{"unknown:task-001"}, Priority: 1},
	}
	localGraph := BuildDepGraph(tasks)
	cpg := &CrossProjectGraph{Projects: map[string]map[string]*Task{}}

	result := FilterUnblockedCrossProject(tasks, localGraph, cpg)
	if len(result) != 0 {
		t.Fatalf("expected no unblocked tasks (missing project), got %v", taskIDs(result))
	}
}

func TestFilterUnblockedCrossProject_NilCrossGraph(t *testing.T) {
	tasks := []Task{
		{ID: "worker", Status: "ready", DependsOn: []string{"proj:task-001"}, Priority: 1},
	}
	localGraph := BuildDepGraph(tasks)

	result := FilterUnblockedCrossProject(tasks, localGraph, nil)
	if len(result) != 0 {
		t.Fatalf("expected no unblocked tasks (nil cross graph), got %v", taskIDs(result))
	}
}

func TestFilterUnblockedCrossProject_ExcludesEpicsAndClosed(t *testing.T) {
	tasks := []Task{
		{ID: "open-task", Status: "ready", Priority: 1},
		{ID: "closed-task", Status: "closed"},
		{ID: "epic-task", Status: "ready", Type: "epic"},
	}
	localGraph := BuildDepGraph(tasks)
	cpg := &CrossProjectGraph{Projects: map[string]map[string]*Task{}}

	result := FilterUnblockedCrossProject(tasks, localGraph, cpg)
	if len(result) != 1 || result[0].ID != "open-task" {
		t.Fatalf("expected [open-task], got %v", taskIDs(result))
	}
}

func TestFilterUnblockedCrossProject_SortOrder(t *testing.T) {
	// Verify same sort order as FilterUnblockedOpen: stage > priority > estimate > ID.
	tasks := []Task{
		{ID: "plain-b", Status: "ready", Priority: 1, EstimateMinutes: 10},
		{ID: "plain-a", Status: "ready", Priority: 1, EstimateMinutes: 10},
		{ID: "stage-z", Status: "ready", Priority: 2, EstimateMinutes: 60, Labels: []string{"stage:late"}},
		{ID: "stage-a", Status: "ready", Priority: 0, EstimateMinutes: 5, Labels: []string{"stage:early"}},
	}
	localGraph := BuildDepGraph(tasks)
	cpg := &CrossProjectGraph{Projects: map[string]map[string]*Task{}}

	result := FilterUnblockedCrossProject(tasks, localGraph, cpg)
	expected := []string{"stage-a", "stage-z", "plain-a", "plain-b"}
	if !equalStringSlice(taskIDs(result), expected) {
		t.Fatalf("expected %v, got %v", expected, taskIDs(result))
	}
}

func TestFilterUnblockedCrossProject_NilLocalGraph(t *testing.T) {
	// With nil local graph and no cross deps, tasks without deps should pass.
	tasks := []Task{
		{ID: "free", Status: "ready"},
		{ID: "blocked", Status: "ready", DependsOn: []string{"free"}},
	}
	cpg := &CrossProjectGraph{Projects: map[string]map[string]*Task{}}

	result := FilterUnblockedCrossProject(tasks, nil, cpg)
	if len(result) != 1 || result[0].ID != "free" {
		t.Fatalf("expected [free], got %v", taskIDs(result))
	}
}

// ---------------------------------------------------------------------------
// Concurrent DAG writes — verify WAL mode handles parallel CreateTask
// ---------------------------------------------------------------------------

func TestConcurrentCreateTask(t *testing.T) {
	dag := newTestDAGFile(t)
	ctx := t.Context()

	const workers = 10
	const tasksPerWorker = 10

	var wg sync.WaitGroup
	errs := make(chan error, workers*tasksPerWorker)
	allIDs := make(chan string, workers*tasksPerWorker)

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := 0; i < tasksPerWorker; i++ {
				id, err := dag.CreateTask(ctx, Task{
					Title:   fmt.Sprintf("worker-%d-task-%d", workerID, i),
					Project: "concurrent",
				})
				if err != nil {
					errs <- fmt.Errorf("worker %d task %d: %w", workerID, i, err)
					return
				}
				allIDs <- id
			}
		}(w)
	}

	wg.Wait()
	close(errs)
	close(allIDs)

	for err := range errs {
		t.Fatalf("concurrent CreateTask error: %v", err)
	}

	// Verify all tasks created with unique IDs.
	seen := make(map[string]struct{})
	for id := range allIDs {
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate task ID: %s", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != workers*tasksPerWorker {
		t.Fatalf("expected %d unique tasks, got %d", workers*tasksPerWorker, len(seen))
	}

	// Verify all tasks exist in the database.
	tasks, err := dag.ListTasks(ctx, "concurrent")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != workers*tasksPerWorker {
		t.Fatalf("expected %d tasks in DB, got %d", workers*tasksPerWorker, len(tasks))
	}
}

func TestConcurrentCreateAndClose(t *testing.T) {
	dag := newTestDAGFile(t)
	ctx := t.Context()

	// Pre-create tasks.
	const n = 20
	taskIDs := make([]string, n)
	for i := 0; i < n; i++ {
		id, err := dag.CreateTask(ctx, Task{
			Title:   fmt.Sprintf("task-%d", i),
			Project: "concurrent",
		})
		if err != nil {
			t.Fatalf("setup CreateTask: %v", err)
		}
		taskIDs[i] = id
	}

	// Concurrently close all tasks.
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for _, id := range taskIDs {
		wg.Add(1)
		go func(taskID string) {
			defer wg.Done()
			if err := dag.CloseTask(ctx, taskID); err != nil {
				errs <- fmt.Errorf("close %s: %w", taskID, err)
			}
		}(id)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent CloseTask error: %v", err)
	}

	// Verify all tasks are closed.
	tasks, err := dag.ListTasks(ctx, "concurrent", "closed")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != n {
		t.Fatalf("expected %d closed tasks, got %d", n, len(tasks))
	}
}

func TestConcurrentAddEdge(t *testing.T) {
	dag := newTestDAGFile(t)
	ctx := t.Context()

	// Create a hub and spokes: all spokes depend on hub.
	hubID, _ := dag.CreateTask(ctx, Task{Title: "hub", Project: "concurrent", Status: "ready"})

	const spokes = 20
	spokeIDs := make([]string, spokes)
	for i := 0; i < spokes; i++ {
		id, err := dag.CreateTask(ctx, Task{
			Title:   fmt.Sprintf("spoke-%d", i),
			Project: "concurrent",
			Status:  "ready",
		})
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
		spokeIDs[i] = id
	}

	// Concurrently add all edges.
	var wg sync.WaitGroup
	errs := make(chan error, spokes)
	for _, sid := range spokeIDs {
		wg.Add(1)
		go func(spokeID string) {
			defer wg.Done()
			if err := dag.AddEdge(ctx, spokeID, hubID); err != nil {
				errs <- fmt.Errorf("edge %s->%s: %w", spokeID, hubID, err)
			}
		}(sid)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent AddEdge error: %v", err)
	}

	// Only hub should be ready (all spokes depend on it).
	ready, err := dag.GetReadyNodes(ctx, "concurrent")
	if err != nil {
		t.Fatalf("GetReadyNodes: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != hubID {
		t.Fatalf("expected only hub to be ready, got %v", ids(ready))
	}
}

// ---------------------------------------------------------------------------
// Large graph — 100+ nodes GetReadyNodes
// ---------------------------------------------------------------------------

func TestGetReadyNodes_LargeGraph(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	const totalNodes = 150
	const chainLength = 50 // linear chain: 0 → 1 → 2 → ... → 49

	allIDs := make([]string, totalNodes)

	// Create all nodes.
	for i := 0; i < totalNodes; i++ {
		id, err := dag.CreateTask(ctx, Task{
			Title:    fmt.Sprintf("node-%d", i),
			Project:  "large",
			Priority: i % 5,
			Status:   "ready",
		})
		if err != nil {
			t.Fatalf("CreateTask node-%d: %v", i, err)
		}
		allIDs[i] = id
	}

	// Build a linear chain: node[0] ← node[1] ← ... ← node[chainLength-1]
	// (each depends on the previous)
	for i := 1; i < chainLength; i++ {
		if err := dag.AddEdge(ctx, allIDs[i], allIDs[i-1]); err != nil {
			t.Fatalf("AddEdge chain %d->%d: %v", i, i-1, err)
		}
	}

	// Nodes chainLength..totalNodes-1 are independent (no dependencies).
	// Node 0 in the chain has no deps.
	// Nodes 1..chainLength-1 are blocked.

	ready, err := dag.GetReadyNodes(ctx, "large")
	if err != nil {
		t.Fatalf("GetReadyNodes: %v", err)
	}

	// Expected ready: node[0] + all independent nodes (chainLength..totalNodes-1)
	expectedReady := 1 + (totalNodes - chainLength)
	if len(ready) != expectedReady {
		t.Fatalf("expected %d ready nodes, got %d", expectedReady, len(ready))
	}

	// Verify node[0] is in the ready set.
	readySet := make(map[string]struct{}, len(ready))
	for _, r := range ready {
		readySet[r.ID] = struct{}{}
	}
	if _, ok := readySet[allIDs[0]]; !ok {
		t.Fatal("expected chain head (node-0) to be ready")
	}

	// Verify blocked chain nodes are NOT ready.
	for i := 1; i < chainLength; i++ {
		if _, ok := readySet[allIDs[i]]; ok {
			t.Fatalf("node-%d should be blocked, but found in ready set", i)
		}
	}

	// Verify all independent nodes are ready.
	for i := chainLength; i < totalNodes; i++ {
		if _, ok := readySet[allIDs[i]]; !ok {
			t.Fatalf("independent node-%d should be ready but not found", i)
		}
	}
}

func TestGetReadyNodes_LargeGraph_ClosingDepsUnblocks(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	// Create a fan-out: 1 root, 100 children that depend on root.
	rootID, _ := dag.CreateTask(ctx, Task{Title: "root", Project: "large", Status: "ready"})

	const children = 100
	childIDs := make([]string, children)
	for i := 0; i < children; i++ {
		id, err := dag.CreateTask(ctx, Task{
			Title:   fmt.Sprintf("child-%d", i),
			Project: "large",
			Status:  "ready",
		})
		if err != nil {
			t.Fatalf("CreateTask child-%d: %v", i, err)
		}
		childIDs[i] = id
		if err := dag.AddEdge(ctx, id, rootID); err != nil {
			t.Fatalf("AddEdge child-%d->root: %v", i, err)
		}
	}

	// Only root should be ready.
	ready, err := dag.GetReadyNodes(ctx, "large")
	if err != nil {
		t.Fatalf("GetReadyNodes: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != rootID {
		t.Fatalf("expected only root to be ready, got %d tasks", len(ready))
	}

	// Close root → all children become ready.
	if closeErr := dag.CloseTask(ctx, rootID); closeErr != nil {
		t.Fatalf("CloseTask root: %v", closeErr)
	}

	ready, err = dag.GetReadyNodes(ctx, "large")
	if err != nil {
		t.Fatalf("GetReadyNodes after close: %v", err)
	}
	if len(ready) != children {
		t.Fatalf("expected %d ready children after root closed, got %d", children, len(ready))
	}
}

func TestGetReadyNodes_LargeGraph_OrderPreserved(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	// Create 100 independent nodes with varying priorities.
	const n = 100
	for i := 0; i < n; i++ {
		_, err := dag.CreateTask(ctx, Task{
			Title:           fmt.Sprintf("node-%d", i),
			Project:         "large",
			Priority:        i % 5,
			EstimateMinutes: (n - i),
			Status:          "ready",
		})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
	}

	ready, err := dag.GetReadyNodes(ctx, "large")
	if err != nil {
		t.Fatalf("GetReadyNodes: %v", err)
	}
	if len(ready) != n {
		t.Fatalf("expected %d ready, got %d", n, len(ready))
	}

	// Verify sort is by priority ascending, then estimate ascending.
	for i := 1; i < len(ready); i++ {
		prev := ready[i-1]
		curr := ready[i]
		if prev.Priority > curr.Priority {
			t.Fatalf("sort violation at index %d: priority %d > %d", i, prev.Priority, curr.Priority)
		}
		if prev.Priority == curr.Priority && prev.EstimateMinutes > curr.EstimateMinutes {
			t.Fatalf("sort violation at index %d: same priority but estimate %d > %d", i, prev.EstimateMinutes, curr.EstimateMinutes)
		}
	}
}

// ---------------------------------------------------------------------------
// UpdateTask_AllFields (was missing from dag_test.go, added for coverage)
// ---------------------------------------------------------------------------

func TestUpdateTask_AllFields(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	id, _ := dag.CreateTask(ctx, Task{Title: "original", Project: "p"})

	err := dag.UpdateTask(ctx, id, map[string]any{
		"title":            "new title",
		"description":      "new desc",
		"status":           "in_progress",
		"priority":         3,
		"type":             "feature",
		"assignee":         "bob",
		"labels":           []string{"urgent"},
		"estimate_minutes": 90,
		"parent_id":        "parent-123",
		"acceptance":       "tests pass",
		"design":           "new design",
		"notes":            "new notes",
	})
	if err != nil {
		t.Fatalf("UpdateTask all fields: %v", err)
	}

	task, _ := dag.GetTask(ctx, id)
	if task.Title != "new title" {
		t.Errorf("Title=%q", task.Title)
	}
	if task.Description != "new desc" {
		t.Errorf("Description=%q", task.Description)
	}
	if task.Status != "in_progress" {
		t.Errorf("Status=%q", task.Status)
	}
	if task.Priority != 3 {
		t.Errorf("Priority=%d", task.Priority)
	}
	if task.Type != "feature" {
		t.Errorf("Type=%q", task.Type)
	}
	if task.Assignee != "bob" {
		t.Errorf("Assignee=%q", task.Assignee)
	}
	if len(task.Labels) != 1 || task.Labels[0] != "urgent" {
		t.Errorf("Labels=%v", task.Labels)
	}
	if task.EstimateMinutes != 90 {
		t.Errorf("EstimateMinutes=%d", task.EstimateMinutes)
	}
	if task.ParentID != "parent-123" {
		t.Errorf("ParentID=%q", task.ParentID)
	}
	if task.Acceptance != "tests pass" {
		t.Errorf("Acceptance=%q", task.Acceptance)
	}
	if task.Design != "new design" {
		t.Errorf("Design=%q", task.Design)
	}
	if task.Notes != "new notes" {
		t.Errorf("Notes=%q", task.Notes)
	}
}
