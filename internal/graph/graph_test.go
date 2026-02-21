package graph

import "testing"

func TestBuildDepGraph_BuildsNodesAndEdges(t *testing.T) {
	tasks := []Task{
		{ID: "a", Status: "open"},
		{ID: "b", Status: "open", DependsOn: []string{"a", "a", "missing"}},
		{ID: "c", Status: "open", DependsOn: []string{"a"}},
		{ID: "d", Status: "open", DependsOn: []string{"b", "c", "b"}},
	}

	graph := BuildDepGraph(tasks)

	nodes := graph.Nodes()
	if len(nodes) != 4 {
		t.Fatalf("expected 4 nodes, got %d", len(nodes))
	}

	deps := graph.DependsOnIDs("b")
	if !equalStringSlice(deps, []string{"a", "missing"}) {
		t.Fatalf("unexpected dependencies for b: %v", deps)
	}

	deps = graph.DependsOnIDs("d")
	if !equalStringSlice(deps, []string{"b", "c"}) {
		t.Fatalf("unexpected dependencies for d: %v", deps)
	}

	blocks := graph.BlocksIDs("a")
	if !equalStringSlice(blocks, []string{"b", "c"}) {
		t.Fatalf("unexpected blockers for a: %v", blocks)
	}

	if !equalStringSlice(graph.BlocksIDs("missing"), []string{"b"}) {
		t.Fatalf("expected blockers for missing dependency to include b: %v", graph.BlocksIDs("missing"))
	}

	if got := graph.DependsOnIDs("unknown"); len(got) != 0 {
		t.Fatalf("expected unknown ID to return no dependencies, got %v", got)
	}

	if got := graph.BlocksIDs("unknown"); len(got) != 0 {
		t.Fatalf("expected unknown ID to return no blockers, got %v", got)
	}
}

func TestBuildDepGraph_EmptyAndNilDependencies(t *testing.T) {
	tasks := []Task{
		{ID: "a", Status: "open", DependsOn: nil},
		{ID: "b", Status: "open", DependsOn: []string{}},
		{ID: "c", Status: "open", DependsOn: []string{"a"}},
	}

	graph := BuildDepGraph(tasks)

	if deps := graph.DependsOnIDs("a"); len(deps) != 0 {
		t.Fatalf("expected a to have no dependencies, got %v", deps)
	}
	if deps := graph.DependsOnIDs("b"); len(deps) != 0 {
		t.Fatalf("expected b to have no dependencies, got %v", deps)
	}
	if len(graph.BlocksIDs("a")) != 1 || graph.BlocksIDs("a")[0] != "c" {
		t.Fatalf("expected c to block a, got %v", graph.BlocksIDs("a"))
	}
}

func TestBuildDepGraph_IsolatedFromInputMutation(t *testing.T) {
	tasks := []Task{
		{ID: "root", Status: "open", DependsOn: []string{"dep"}, Labels: []string{"stage:todo"}},
		{ID: "dep", Status: "closed"},
	}

	graph := BuildDepGraph(tasks)

	tasks[0].Status = "closed"
	tasks[0].DependsOn[0] = "ghost"
	tasks[0].Labels[0] = "stage:changed"

	node := graph.Nodes()["root"]
	if node == nil || node.Status != "open" {
		t.Fatalf("graph node status was not isolated from input mutation: %v", node)
	}
	if !equalStringSlice(graph.DependsOnIDs("root"), []string{"dep"}) {
		t.Fatalf("graph dependency slice was not isolated from input mutation: %v", graph.DependsOnIDs("root"))
	}
	if len(node.Labels) != 1 || node.Labels[0] != "stage:todo" {
		t.Fatalf("graph labels were not isolated from input mutation: %v", node.Labels)
	}
}

func TestBuildDepGraph_InitializesEmptyAdjacencyEntries(t *testing.T) {
	tasks := []Task{
		{ID: "leaf", Status: "open"},
		{ID: "root", Status: "open", DependsOn: []string{"leaf"}},
	}

	graph := BuildDepGraph(tasks)

	if deps := graph.DependsOnIDs("leaf"); len(deps) != 0 {
		t.Fatalf("expected leaf to have no dependencies, got %v", deps)
	}
	if blockers := graph.BlocksIDs("leaf"); len(blockers) != 1 || blockers[0] != "root" {
		t.Fatalf("expected leaf to be blocked by root, got %v", blockers)
	}
	if deps := graph.DependsOnIDs("root"); !equalStringSlice(deps, []string{"leaf"}) {
		t.Fatalf("expected root to depend on leaf, got %v", deps)
	}
	if blockers := graph.BlocksIDs("root"); len(blockers) != 0 {
		t.Fatalf("expected root to block nothing, got %v", blockers)
	}
}

func TestNodes_ReturnsShallowCopy(t *testing.T) {
	tasks := []Task{{ID: "a", Status: "open"}}
	graph := BuildDepGraph(tasks)

	// Deleting from the returned map must not affect the graph.
	m := graph.Nodes()
	delete(m, "a")
	if _, ok := graph.Nodes()["a"]; !ok {
		t.Fatalf("deleting from Nodes() result should not affect internal map")
	}

	// Task pointers are shared, so field mutations are visible (shallow copy).
	graph.Nodes()["a"].Status = "closed"
	if graph.Nodes()["a"].Status != "closed" {
		t.Fatalf("expected shared Task pointer mutation to be visible")
	}
}

func TestAccessorsReturnCopiesForDependencies(t *testing.T) {
	tasks := []Task{
		{ID: "a", Status: "open"},
		{ID: "b", Status: "open", DependsOn: []string{"a"}},
	}
	graph := BuildDepGraph(tasks)

	deps := graph.DependsOnIDs("b")
	if len(deps) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(deps))
	}
	deps[0] = "MUTATED"
	afterDeps := graph.DependsOnIDs("b")
	if afterDeps[0] != "a" {
		t.Fatalf("internal forward map was mutated: %v", afterDeps)
	}

	blocks := graph.BlocksIDs("a")
	if len(blocks) != 1 {
		t.Fatalf("expected 1 blocker, got %d", len(blocks))
	}
	blocks[0] = "MUTATED"
	afterBlocks := graph.BlocksIDs("a")
	if afterBlocks[0] != "b" {
		t.Fatalf("internal reverse map was mutated: %v", afterBlocks)
	}
}

func TestFilterUnblockedOpen_DeterministicTieBreakers(t *testing.T) {
	tasks := []Task{
		{ID: "b", Status: "ready", Priority: 1, EstimateMinutes: 30},
		{ID: "a", Status: "ready", Priority: 1, EstimateMinutes: 30},
		{ID: "c", Status: "ready", Priority: 1, EstimateMinutes: 20, Labels: []string{"stage:beta"}},
		{ID: "d", Status: "ready", Priority: 1, EstimateMinutes: 20, Labels: []string{"stage:alpha"}},
	}

	graph := BuildDepGraph(tasks)
	result := FilterUnblockedOpen(tasks, graph)

	expected := []string{"c", "d", "a", "b"}
	if !equalStringSlice(taskIDs(result), expected) {
		t.Fatalf("expected %v, got %v", expected, taskIDs(result))
	}
}

func TestFilterUnblockedOpen_ExcludesBlockedClosedAndEpic(t *testing.T) {
	tasks := []Task{
		{ID: "root", Status: "ready", Priority: 2, EstimateMinutes: 5},
		{ID: "blocked", Status: "ready", DependsOn: []string{"root"}, Priority: 1, EstimateMinutes: 3},
		{ID: "unblocked", Status: "ready", DependsOn: []string{"done"}, Priority: 1, EstimateMinutes: 8},
		{ID: "done", Status: "closed"},
		{ID: "epic", Status: "ready", Type: "epic"},
		{ID: "closed", Status: "closed"},
		{ID: "missing", Status: "ready", DependsOn: []string{"ghost"}},
	}

	graph := BuildDepGraph(tasks)
	result := FilterUnblockedOpen(tasks, graph)

	expected := []string{"unblocked", "root"}
	if !equalStringSlice(taskIDs(result), expected) {
		t.Fatalf("expected %v, got %v", expected, taskIDs(result))
	}
}

func TestFilterUnblockedOpen_ExcludesClosedAndEpic(t *testing.T) {
	tasks := []Task{
		{ID: "open-task", Status: "ready", Type: "task"},
		{ID: "closed-task", Status: "closed", Type: "task"},
		{ID: "open-epic", Status: "ready", Type: "epic"},
		{ID: "open-feature", Status: "ready", Type: "feature"},
	}

	graph := BuildDepGraph(tasks)
	result := FilterUnblockedOpen(tasks, graph)

	expected := []string{"open-feature", "open-task"}
	if !equalStringSlice(taskIDs(result), expected) {
		t.Fatalf("expected %v, got %v", expected, taskIDs(result))
	}
}

func TestFilterUnblockedOpen_UsesGraphDependencySnapshot(t *testing.T) {
	tasks := []Task{
		{ID: "base", Status: "ready"},
		{ID: "child", Status: "ready", DependsOn: []string{"base"}},
	}

	graph := BuildDepGraph(tasks)

	// Mutate caller-owned input after graph creation should not alter graph semantics.
	tasks[1].DependsOn = nil

	result := FilterUnblockedOpen(tasks, graph)
	if !equalStringSlice(taskIDs(result), []string{"base"}) {
		t.Fatalf("expected only base (no deps) to be unblocked; child should remain excluded when dependency stays open in graph: %v", taskIDs(result))
	}
}

func TestFilterUnblockedOpen_FallsBackToInputDepsWhenTaskMissingFromGraph(t *testing.T) {
	base := Task{ID: "base", Status: "ready"}
	orphan := Task{ID: "orphan", Status: "ready", DependsOn: []string{"base"}}

	graph := BuildDepGraph([]Task{base})
	result := FilterUnblockedOpen([]Task{base, orphan}, graph)
	if len(result) != 1 || result[0].ID != "base" {
		t.Fatalf("expected only base (in graph, no deps) to be unblocked; orphan should remain blocked by input dependency: %v", taskIDs(result))
	}
}

func TestFilterUnblockedOpen_DependencyResolution(t *testing.T) {
	tasks := []Task{
		{ID: "dep-closed", Status: "closed"},
		{ID: "dep-open", Status: "ready"},
		{ID: "unblocked", Status: "ready", DependsOn: []string{"dep-closed"}},
		{ID: "blocked", Status: "ready", DependsOn: []string{"dep-open"}},
		{ID: "mixed", Status: "ready", DependsOn: []string{"dep-closed", "dep-open"}},
	}

	graph := BuildDepGraph(tasks)
	result := FilterUnblockedOpen(tasks, graph)

	got := taskIDs(result)
	expected := []string{"dep-open", "unblocked"}
	if !equalStringSlice(got, expected) {
		t.Fatalf("expected %v, got %v", expected, got)
	}
}

func TestFilterUnblockedOpen_SortingByPriorityStageEstimateAndID(t *testing.T) {
	tasks := []Task{
		{ID: "plain-a", Status: "ready", Priority: 1, EstimateMinutes: 5},
		{ID: "plain-b", Status: "ready", Priority: 1, EstimateMinutes: 5},
		{ID: "plain-low", Status: "ready", Priority: 0, EstimateMinutes: 100},
		{ID: "stage-c", Status: "ready", Priority: 1, EstimateMinutes: 2, Labels: []string{"stage:gamma"}},
		{ID: "stage-b", Status: "ready", Priority: 0, EstimateMinutes: 30, Labels: []string{"stage:beta"}},
		{ID: "stage-a", Status: "ready", Priority: 0, EstimateMinutes: 10, Labels: []string{"stage:alpha"}},
		{ID: "stage-d", Status: "ready", Priority: 1, EstimateMinutes: 2, Labels: []string{"stage:zz"}},
		{ID: "stage-e", Status: "ready", Priority: 0, EstimateMinutes: 10, Labels: []string{"stage:epsilon"}},
	}

	graph := BuildDepGraph(tasks)
	result := FilterUnblockedOpen(tasks, graph)

	expected := []string{"stage-a", "stage-e", "stage-b", "stage-c", "stage-d", "plain-low", "plain-a", "plain-b"}
	if !equalStringSlice(taskIDs(result), expected) {
		t.Fatalf("expected %v, got %v", expected, taskIDs(result))
	}
}

func TestFilterUnblockedOpen_SortsByStageBeforePriority(t *testing.T) {
	// Explicitly confirm primary key is stage-labeled.
	tasks := []Task{
		{ID: "low-priority-stage", Status: "ready", Priority: 2, EstimateMinutes: 1, Labels: []string{"stage:high"}},
		{ID: "high-priority-plain", Status: "ready", Priority: 1, EstimateMinutes: 10},
		{ID: "same-priority-stage", Status: "ready", Priority: 1, EstimateMinutes: 5, Labels: []string{"stage:mid"}},
	}

	graph := BuildDepGraph(tasks)
	result := FilterUnblockedOpen(tasks, graph)

	expected := []string{"same-priority-stage", "low-priority-stage", "high-priority-plain"}
	if !equalStringSlice(taskIDs(result), expected) {
		t.Fatalf("expected %v, got %v", expected, taskIDs(result))
	}
}

func TestFilterUnblockedOpen_NilGraph(t *testing.T) {
	tasks := []Task{
		{ID: "open", Status: "ready"},
		{ID: "open-with-missing", Status: "ready", DependsOn: []string{"ghost"}},
	}

	result := FilterUnblockedOpen(tasks, nil)
	expected := []string{"open"}
	if !equalStringSlice(taskIDs(result), expected) {
		t.Fatalf("expected %v, got %v", expected, taskIDs(result))
	}
}

func TestFilterUnblockedOpen_NilDependenciesAndMissingDependencyRefs(t *testing.T) {
	tasks := []Task{
		{ID: "base", Status: "closed", Priority: 1},
		{ID: "child", Status: "ready", DependsOn: []string{"base"}, Priority: 0},
		{ID: "missing", Status: "ready", DependsOn: []string{"ghost"}, Priority: 2},
		{ID: "finished", Status: "closed"},
	}

	graph := BuildDepGraph(tasks)

	if got := graph.DependsOnIDs("base"); len(got) != 0 {
		t.Fatalf("expected base to have no dependencies, got %v", got)
	}

	result := FilterUnblockedOpen(tasks, graph)
	if !equalStringSlice(taskIDs(result), []string{"child"}) {
		t.Fatalf("expected [child] to be the only unblocked open task, got %v", taskIDs(result))
	}
}

func TestFilterUnblockedOpen_EmptyResult(t *testing.T) {
	tasks := []Task{
		{ID: "closed", Status: "closed"},
		{ID: "epic", Status: "ready", Type: "epic"},
		{ID: "blocked", Status: "ready", DependsOn: []string{"epic"}},
	}

	result := FilterUnblockedOpen(tasks, BuildDepGraph(tasks))
	if len(result) != 0 {
		t.Fatalf("expected no unblocked open tasks, got %v", taskIDs(result))
	}
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func taskIDs(tasks []Task) []string {
	out := make([]string, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, task.ID)
	}
	return out
}
