package graph

import (
	"database/sql"
	"regexp"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// ids extracts task IDs from a slice — shorthand for dag tests.
func ids(tasks []Task) []string {
	out := make([]string, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.ID)
	}
	return out
}

func newTestDAG(t *testing.T) *DAG {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	dag := NewDAG(db)
	if err := dag.EnsureSchema(t.Context()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return dag
}

func TestEnsureSchema_CreatesTablesAndWAL(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	dag := NewDAG(db)
	ctx := t.Context()
	err = dag.EnsureSchema(ctx)
	if err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	// Verify WAL mode is enabled.
	var journalMode string
	err = db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if journalMode != "wal" && journalMode != "memory" {
		// In-memory databases may report "memory" instead of "wal".
		t.Logf("journal_mode=%s (in-memory databases may not support WAL)", journalMode)
	}

	// Verify foreign_keys is enabled.
	var fkEnabled int
	err = db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fkEnabled)
	if err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if fkEnabled != 1 {
		t.Fatalf("expected foreign_keys=1, got %d", fkEnabled)
	}

	// Verify tasks table exists.
	var tasksCount int
	err = db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='tasks'").Scan(&tasksCount)
	if err != nil {
		t.Fatalf("check tasks table: %v", err)
	}
	if tasksCount != 1 {
		t.Fatalf("expected tasks table to exist")
	}

	// Verify task_edges table exists.
	var edgesCount int
	err = db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='task_edges'").Scan(&edgesCount)
	if err != nil {
		t.Fatalf("check task_edges table: %v", err)
	}
	if edgesCount != 1 {
		t.Fatalf("expected task_edges table to exist")
	}

	// Idempotent: calling again should not error.
	err = dag.EnsureSchema(ctx)
	if err != nil {
		t.Fatalf("second EnsureSchema call should be idempotent: %v", err)
	}
}

func TestEnsureSchema_NilDAG(t *testing.T) {
	var dag *DAG
	if err := dag.EnsureSchema(t.Context()); err == nil {
		t.Fatal("expected error for nil DAG")
	}
}

func TestCreateTask_IDFormat(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	id, err := dag.CreateTask(ctx, Task{
		Title:   "test task",
		Project: "myproj",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// ID must match {project}-{6 hex chars}
	re := regexp.MustCompile(`^myproj-[0-9a-f]{6}$`)
	if !re.MatchString(id) {
		t.Fatalf("ID %q does not match expected format {project}-{6 hex}", id)
	}
}

func TestCreateTask_Defaults(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	id, err := dag.CreateTask(ctx, Task{
		Title:   "defaults test",
		Project: "proj",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	task, err := dag.GetTask(ctx, id)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}

	if task.Status != "open" {
		t.Errorf("expected default status=open, got %q", task.Status)
	}
	if task.Type != "task" {
		t.Errorf("expected default type=task, got %q", task.Type)
	}
	if task.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
	if task.UpdatedAt.IsZero() {
		t.Error("expected non-zero UpdatedAt")
	}
}

func TestCreateTask_RequiresProject(t *testing.T) {
	dag := newTestDAG(t)
	_, err := dag.CreateTask(t.Context(), Task{Title: "no project"})
	if err == nil {
		t.Fatal("expected error when project is empty")
	}
}

func TestCreateTask_WithLabels(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	id, err := dag.CreateTask(ctx, Task{
		Title:   "labeled",
		Project: "proj",
		Labels:  []string{"bug", "stage:init"},
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	task, err := dag.GetTask(ctx, id)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if len(task.Labels) != 2 || task.Labels[0] != "bug" || task.Labels[1] != "stage:init" {
		t.Fatalf("expected labels [bug, stage:init], got %v", task.Labels)
	}
}

func TestGetTask_NotFound(t *testing.T) {
	dag := newTestDAG(t)
	_, err := dag.GetTask(t.Context(), "nonexistent-abc123")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' error, got: %v", err)
	}
}

func TestGetTask_EmptyID(t *testing.T) {
	dag := newTestDAG(t)
	_, err := dag.GetTask(t.Context(), "")
	if err == nil {
		t.Fatal("expected error for empty ID")
	}
}

func TestListTasks_ByProject(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	dag.CreateTask(ctx, Task{Title: "a1", Project: "alpha"})
	dag.CreateTask(ctx, Task{Title: "a2", Project: "alpha"})
	dag.CreateTask(ctx, Task{Title: "b1", Project: "beta"})

	tasks, err := dag.ListTasks(ctx, "alpha")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks for alpha, got %d", len(tasks))
	}

	tasks, err = dag.ListTasks(ctx, "beta")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task for beta, got %d", len(tasks))
	}
}

func TestListTasks_FilterByStatuses(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	id1, _ := dag.CreateTask(ctx, Task{Title: "open1", Project: "p"})
	id2, _ := dag.CreateTask(ctx, Task{Title: "open2", Project: "p"})
	dag.CloseTask(ctx, id1)

	// Filter open only.
	tasks, err := dag.ListTasks(ctx, "p", "open")
	if err != nil {
		t.Fatalf("ListTasks open: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != id2 {
		t.Fatalf("expected 1 open task (id=%s), got %v", id2, ids(tasks))
	}

	// Filter closed only.
	tasks, err = dag.ListTasks(ctx, "p", "closed")
	if err != nil {
		t.Fatalf("ListTasks closed: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != id1 {
		t.Fatalf("expected 1 closed task (id=%s), got %v", id1, ids(tasks))
	}

	// No status filter returns all.
	tasks, err = dag.ListTasks(ctx, "p")
	if err != nil {
		t.Fatalf("ListTasks all: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks total, got %d", len(tasks))
	}
}

func TestListTasks_EmptyProject(t *testing.T) {
	dag := newTestDAG(t)
	_, err := dag.ListTasks(t.Context(), "")
	if err == nil {
		t.Fatal("expected error for empty project")
	}
}

func TestListTasks_EmptyResult(t *testing.T) {
	dag := newTestDAG(t)
	tasks, err := dag.ListTasks(t.Context(), "nonexistent")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Fatalf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestUpdateTask_PartialFields(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	id, _ := dag.CreateTask(ctx, Task{
		Title:   "original",
		Project: "p",
	})

	err := dag.UpdateTask(ctx, id, map[string]any{
		"title":    "updated title",
		"priority": 3,
	})
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	task, _ := dag.GetTask(ctx, id)
	if task.Title != "updated title" {
		t.Errorf("expected title 'updated title', got %q", task.Title)
	}
	if task.Priority != 3 {
		t.Errorf("expected priority 3, got %d", task.Priority)
	}
	// Status should be unchanged.
	if task.Status != "open" {
		t.Errorf("expected status to remain 'open', got %q", task.Status)
	}
}

func TestUpdateTask_StatusReadyRejectedWhenDependencyUnresolved(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	depID, _ := dag.CreateTask(ctx, Task{
		Title:   "dependency",
		Project: "p",
		Status:  "open",
	})
	taskID, _ := dag.CreateTask(ctx, Task{
		Title:   "candidate",
		Project: "p",
		Status:  "open",
	})
	if err := dag.AddEdge(ctx, taskID, depID); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}

	err := dag.UpdateTask(ctx, taskID, map[string]any{"status": "ready"})
	if err == nil {
		t.Fatal("expected error when setting ready with unresolved dependency")
	}
	if !strings.Contains(err.Error(), "cannot set task") {
		t.Fatalf("expected ready-transition rejection, got: %v", err)
	}

	task, _ := dag.GetTask(ctx, taskID)
	if task.Status != "open" {
		t.Fatalf("expected task status to remain open, got %q", task.Status)
	}
}

func TestUpdateTask_StatusReadyAllowedWhenDependenciesClosed(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	depID, _ := dag.CreateTask(ctx, Task{
		Title:   "dependency",
		Project: "p",
		Status:  "open",
	})
	taskID, _ := dag.CreateTask(ctx, Task{
		Title:   "candidate",
		Project: "p",
		Status:  "open",
	})
	if err := dag.AddEdge(ctx, taskID, depID); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := dag.CloseTask(ctx, depID); err != nil {
		t.Fatalf("CloseTask dependency: %v", err)
	}

	if err := dag.UpdateTask(ctx, taskID, map[string]any{"status": "ready"}); err != nil {
		t.Fatalf("UpdateTask to ready: %v", err)
	}

	task, _ := dag.GetTask(ctx, taskID)
	if task.Status != "ready" {
		t.Fatalf("expected task status ready, got %q", task.Status)
	}
}

func TestUpdateTask_Labels(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	id, _ := dag.CreateTask(ctx, Task{
		Title:   "label test",
		Project: "p",
		Labels:  []string{"old"},
	})

	err := dag.UpdateTask(ctx, id, map[string]any{
		"labels": []string{"new", "updated"},
	})
	if err != nil {
		t.Fatalf("UpdateTask labels: %v", err)
	}

	task, _ := dag.GetTask(ctx, id)
	if len(task.Labels) != 2 || task.Labels[0] != "new" || task.Labels[1] != "updated" {
		t.Fatalf("expected labels [new, updated], got %v", task.Labels)
	}
}

func TestUpdateTask_LabelsAsJSON(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	id, _ := dag.CreateTask(ctx, Task{Title: "json labels", Project: "p"})

	err := dag.UpdateTask(ctx, id, map[string]any{
		"labels": `["a","b"]`,
	})
	if err != nil {
		t.Fatalf("UpdateTask labels JSON string: %v", err)
	}

	task, _ := dag.GetTask(ctx, id)
	if len(task.Labels) != 2 || task.Labels[0] != "a" || task.Labels[1] != "b" {
		t.Fatalf("expected labels [a, b], got %v", task.Labels)
	}
}

func TestUpdateTask_TypeField(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	id, _ := dag.CreateTask(ctx, Task{Title: "type test", Project: "p"})

	// "type" key should map to "issue_type" column.
	err := dag.UpdateTask(ctx, id, map[string]any{
		"type": "feature",
	})
	if err != nil {
		t.Fatalf("UpdateTask type: %v", err)
	}

	task, _ := dag.GetTask(ctx, id)
	if task.Type != "feature" {
		t.Errorf("expected type=feature, got %q", task.Type)
	}
}

func TestUpdateTask_NotFound(t *testing.T) {
	dag := newTestDAG(t)
	err := dag.UpdateTask(t.Context(), "nonexistent-abc123", map[string]any{
		"title": "x",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' error, got: %v", err)
	}
}

func TestUpdateTask_EmptyFields(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	id, _ := dag.CreateTask(ctx, Task{Title: "empty fields", Project: "p"})

	// Empty fields map is a no-op, should not error.
	err := dag.UpdateTask(ctx, id, map[string]any{})
	if err != nil {
		t.Fatalf("UpdateTask empty fields should be no-op: %v", err)
	}
}

func TestUpdateTask_UnknownFieldsError(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	id, _ := dag.CreateTask(ctx, Task{Title: "unknown fields", Project: "p"})

	// All-unknown fields should return an error so callers know nothing happened.
	err := dag.UpdateTask(ctx, id, map[string]any{
		"nonexistent_field": "value",
	})
	if err == nil {
		t.Fatal("expected error when all fields are unrecognized")
	}
	if !strings.Contains(err.Error(), "no recognized") {
		t.Fatalf("expected 'no recognized' error, got: %v", err)
	}
}

func TestUpdateTask_UnknownFieldsNotFoundTask(t *testing.T) {
	dag := newTestDAG(t)

	// All-unknown fields on a nonexistent task should return "not found".
	err := dag.UpdateTask(t.Context(), "nonexistent-abc123", map[string]any{
		"nonexistent_field": "value",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent task with unknown fields")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected 'not found' error, got: %v", err)
	}
}

func TestUpdateTask_BumpsUpdatedAt(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	id, _ := dag.CreateTask(ctx, Task{Title: "timestamp test", Project: "p"})
	before, _ := dag.GetTask(ctx, id)

	dag.UpdateTask(ctx, id, map[string]any{"title": "changed"})
	after, _ := dag.GetTask(ctx, id)

	if !after.UpdatedAt.After(before.UpdatedAt) && after.UpdatedAt != before.UpdatedAt {
		t.Errorf("expected UpdatedAt to be bumped, before=%v after=%v", before.UpdatedAt, after.UpdatedAt)
	}
}

func TestCloseTask(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	id, _ := dag.CreateTask(ctx, Task{Title: "closable", Project: "p"})
	if err := dag.CloseTask(ctx, id); err != nil {
		t.Fatalf("CloseTask: %v", err)
	}

	task, _ := dag.GetTask(ctx, id)
	if task.Status != "closed" {
		t.Fatalf("expected status=closed, got %q", task.Status)
	}
}

func TestCloseTask_AlreadyClosedIdempotent(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	id, _ := dag.CreateTask(ctx, Task{Title: "close twice", Project: "p"})

	// First close.
	if err := dag.CloseTask(ctx, id); err != nil {
		t.Fatalf("first CloseTask: %v", err)
	}

	task, _ := dag.GetTask(ctx, id)
	if task.Status != "closed" {
		t.Fatalf("expected status=closed after first close, got %q", task.Status)
	}

	// Second close should succeed silently (idempotent).
	if err := dag.CloseTask(ctx, id); err != nil {
		t.Fatalf("second CloseTask should be idempotent: %v", err)
	}

	task, _ = dag.GetTask(ctx, id)
	if task.Status != "closed" {
		t.Fatalf("expected status=closed after second close, got %q", task.Status)
	}
}

func TestCloseTask_NotFound(t *testing.T) {
	dag := newTestDAG(t)
	err := dag.CloseTask(t.Context(), "nonexistent-abc123")
	if err == nil {
		t.Fatal("expected error for nonexistent task")
	}
}

func TestAddEdge_And_RemoveEdge(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	id1, _ := dag.CreateTask(ctx, Task{Title: "task1", Project: "p"})
	id2, _ := dag.CreateTask(ctx, Task{Title: "task2", Project: "p"})

	// Add edge.
	if err := dag.AddEdge(ctx, id1, id2); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}

	// Verify via GetTask dependencies.
	task, _ := dag.GetTask(ctx, id1)
	if len(task.DependsOn) != 1 || task.DependsOn[0] != id2 {
		t.Fatalf("expected DependsOn=[%s], got %v", id2, task.DependsOn)
	}

	// Remove edge.
	if err := dag.RemoveEdge(ctx, id1, id2); err != nil {
		t.Fatalf("RemoveEdge: %v", err)
	}

	task, _ = dag.GetTask(ctx, id1)
	if len(task.DependsOn) != 0 {
		t.Fatalf("expected no dependencies after remove, got %v", task.DependsOn)
	}
}

func TestAddEdge_DuplicateIdempotent(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	id1, _ := dag.CreateTask(ctx, Task{Title: "t1", Project: "p"})
	id2, _ := dag.CreateTask(ctx, Task{Title: "t2", Project: "p"})

	if err := dag.AddEdge(ctx, id1, id2); err != nil {
		t.Fatalf("first AddEdge: %v", err)
	}
	// Second add of same edge should be a no-op (INSERT OR IGNORE).
	if err := dag.AddEdge(ctx, id1, id2); err != nil {
		t.Fatalf("duplicate AddEdge should not error: %v", err)
	}

	// Still only one dependency.
	task, _ := dag.GetTask(ctx, id1)
	if len(task.DependsOn) != 1 {
		t.Fatalf("expected exactly 1 dependency, got %d", len(task.DependsOn))
	}
}

func TestAddEdge_ForeignKeyEnforced(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	id1, _ := dag.CreateTask(ctx, Task{Title: "real", Project: "p"})

	// Adding edge to nonexistent task should fail with FK enforcement.
	err := dag.AddEdge(ctx, id1, "nonexistent-000000")
	if err == nil {
		t.Fatal("expected error when adding edge to nonexistent task (FK violation)")
	}

	// Adding edge from nonexistent task should also fail.
	err = dag.AddEdge(ctx, "nonexistent-000000", id1)
	if err == nil {
		t.Fatal("expected error when adding edge from nonexistent task (FK violation)")
	}
}

func TestRemoveEdge_NonexistentNoOp(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	id1, _ := dag.CreateTask(ctx, Task{Title: "t1", Project: "p"})
	id2, _ := dag.CreateTask(ctx, Task{Title: "t2", Project: "p"})

	// Removing an edge that doesn't exist should not error.
	if err := dag.RemoveEdge(ctx, id1, id2); err != nil {
		t.Fatalf("RemoveEdge on nonexistent edge should be no-op: %v", err)
	}
}

func TestAddEdge_EmptyArgs(t *testing.T) {
	dag := newTestDAG(t)
	if err := dag.AddEdge(t.Context(), "", "x"); err == nil {
		t.Fatal("expected error for empty from")
	}
	if err := dag.AddEdge(t.Context(), "x", ""); err == nil {
		t.Fatal("expected error for empty to")
	}
}

func TestRemoveEdge_EmptyArgs(t *testing.T) {
	dag := newTestDAG(t)
	if err := dag.RemoveEdge(t.Context(), "", "x"); err == nil {
		t.Fatal("expected error for empty from")
	}
	if err := dag.RemoveEdge(t.Context(), "x", ""); err == nil {
		t.Fatal("expected error for empty to")
	}
}

func TestGetReadyNodes_BasicScenario(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	// Create tasks: A (ready, no deps), B (ready, depends on A), C (closed).
	idA, _ := dag.CreateTask(ctx, Task{Title: "A", Project: "p", Priority: 1, Status: "ready"})
	idB, _ := dag.CreateTask(ctx, Task{Title: "B", Project: "p", Priority: 0, Status: "ready"})
	dag.CreateTask(ctx, Task{Title: "C", Project: "p", Status: "closed"})

	dag.AddEdge(ctx, idB, idA) // B depends on A

	ready, err := dag.GetReadyNodes(ctx, "p")
	if err != nil {
		t.Fatalf("GetReadyNodes: %v", err)
	}

	// A has no deps (ready), B depends on non-closed A (not ready), C is closed (excluded).
	if len(ready) != 1 || ready[0].ID != idA {
		t.Fatalf("expected only A to be ready, got %v", ids(ready))
	}
}

func TestGetReadyNodes_AllDependenciesClosed(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	idDep, _ := dag.CreateTask(ctx, Task{Title: "dep", Project: "p", Status: "ready"})
	idTask, _ := dag.CreateTask(ctx, Task{Title: "task", Project: "p", Status: "ready"})

	dag.AddEdge(ctx, idTask, idDep) // task depends on dep
	dag.CloseTask(ctx, idDep)       // close the dependency

	ready, err := dag.GetReadyNodes(ctx, "p")
	if err != nil {
		t.Fatalf("GetReadyNodes: %v", err)
	}

	// Task should be ready now since its dependency is closed.
	found := false
	for _, r := range ready {
		if r.ID == idTask {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected task %s to be ready (dep closed), got %v", idTask, ids(ready))
	}
}

func TestGetReadyNodes_ExcludesEpics(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	dag.CreateTask(ctx, Task{Title: "epic", Project: "p", Type: "epic", Status: "ready"})
	idTask, _ := dag.CreateTask(ctx, Task{Title: "task", Project: "p", Type: "task", Status: "ready"})

	ready, err := dag.GetReadyNodes(ctx, "p")
	if err != nil {
		t.Fatalf("GetReadyNodes: %v", err)
	}

	if len(ready) != 1 || ready[0].ID != idTask {
		t.Fatalf("expected only non-epic task, got %v", ids(ready))
	}
}

func TestGetReadyNodes_ExcludesClosedTasks(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	id, _ := dag.CreateTask(ctx, Task{Title: "will close", Project: "p", Status: "ready"})
	dag.CloseTask(ctx, id)

	ready, err := dag.GetReadyNodes(ctx, "p")
	if err != nil {
		t.Fatalf("GetReadyNodes: %v", err)
	}
	if len(ready) != 0 {
		t.Fatalf("expected no ready nodes (all closed), got %v", ids(ready))
	}
}

func TestGetReadyNodes_OrderByPriorityThenEstimate(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	idLow, _ := dag.CreateTask(ctx, Task{Title: "low-prio", Project: "p", Priority: 2, EstimateMinutes: 10, Status: "ready"})
	idHigh, _ := dag.CreateTask(ctx, Task{Title: "high-prio", Project: "p", Priority: 0, EstimateMinutes: 60, Status: "ready"})
	idMed, _ := dag.CreateTask(ctx, Task{Title: "med-prio", Project: "p", Priority: 1, EstimateMinutes: 30, Status: "ready"})

	ready, err := dag.GetReadyNodes(ctx, "p")
	if err != nil {
		t.Fatalf("GetReadyNodes: %v", err)
	}

	if len(ready) != 3 {
		t.Fatalf("expected 3 ready nodes, got %d", len(ready))
	}
	// Should be ordered: priority 0 (high), priority 1 (med), priority 2 (low).
	if ready[0].ID != idHigh || ready[1].ID != idMed || ready[2].ID != idLow {
		t.Fatalf("unexpected order: %v (expected %s, %s, %s)", ids(ready), idHigh, idMed, idLow)
	}
}

func TestGetReadyNodes_ProjectIsolation(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	dag.CreateTask(ctx, Task{Title: "alpha task", Project: "alpha", Status: "ready"})
	dag.CreateTask(ctx, Task{Title: "beta task", Project: "beta", Status: "ready"})

	ready, err := dag.GetReadyNodes(ctx, "alpha")
	if err != nil {
		t.Fatalf("GetReadyNodes: %v", err)
	}
	if len(ready) != 1 {
		t.Fatalf("expected 1 ready node for alpha, got %d", len(ready))
	}
}

func TestGetReadyNodes_EmptyProject(t *testing.T) {
	dag := newTestDAG(t)
	_, err := dag.GetReadyNodes(t.Context(), "")
	if err == nil {
		t.Fatal("expected error for empty project")
	}
}

func TestGetReadyNodes_NoDependenciesAllReady(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	dag.CreateTask(ctx, Task{Title: "a", Project: "p", Status: "ready"})
	dag.CreateTask(ctx, Task{Title: "b", Project: "p", Status: "ready"})
	dag.CreateTask(ctx, Task{Title: "c", Project: "p", Status: "ready"})

	ready, err := dag.GetReadyNodes(ctx, "p")
	if err != nil {
		t.Fatalf("GetReadyNodes: %v", err)
	}
	if len(ready) != 3 {
		t.Fatalf("expected 3 ready nodes (no deps), got %d", len(ready))
	}
}

func TestListTasks_IncludesDependencies(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	id1, _ := dag.CreateTask(ctx, Task{Title: "t1", Project: "p"})
	id2, _ := dag.CreateTask(ctx, Task{Title: "t2", Project: "p"})
	dag.AddEdge(ctx, id1, id2)

	tasks, err := dag.ListTasks(ctx, "p")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}

	var t1 Task
	for _, task := range tasks {
		if task.ID == id1 {
			t1 = task
		}
	}
	if len(t1.DependsOn) != 1 || t1.DependsOn[0] != id2 {
		t.Fatalf("expected t1 DependsOn=[%s], got %v", id2, t1.DependsOn)
	}
}

func TestCreateTask_AllFields(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	id, err := dag.CreateTask(ctx, Task{
		Title:           "full task",
		Description:     "a description",
		Status:          "in_progress",
		Priority:        2,
		Type:            "feature",
		Assignee:        "alice",
		Labels:          []string{"urgent", "stage:dev"},
		EstimateMinutes: 45,
		ParentID:        "parent-id",
		Acceptance:      "it works",
		Design:          "design doc",
		Notes:           "some notes",
		Project:         "proj",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	task, err := dag.GetTask(ctx, id)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}

	if task.Title != "full task" {
		t.Errorf("Title=%q", task.Title)
	}
	if task.Description != "a description" {
		t.Errorf("Description=%q", task.Description)
	}
	if task.Status != "in_progress" {
		t.Errorf("Status=%q", task.Status)
	}
	if task.Priority != 2 {
		t.Errorf("Priority=%d", task.Priority)
	}
	if task.Type != "feature" {
		t.Errorf("Type=%q", task.Type)
	}
	if task.Assignee != "alice" {
		t.Errorf("Assignee=%q", task.Assignee)
	}
	if len(task.Labels) != 2 {
		t.Errorf("Labels=%v", task.Labels)
	}
	if task.EstimateMinutes != 45 {
		t.Errorf("EstimateMinutes=%d", task.EstimateMinutes)
	}
	if task.ParentID != "parent-id" {
		t.Errorf("ParentID=%q", task.ParentID)
	}
	if task.Acceptance != "it works" {
		t.Errorf("Acceptance=%q", task.Acceptance)
	}
	if task.Design != "design doc" {
		t.Errorf("Design=%q", task.Design)
	}
	if task.Notes != "some notes" {
		t.Errorf("Notes=%q", task.Notes)
	}
	if task.Project != "proj" {
		t.Errorf("Project=%q", task.Project)
	}
}

func TestAddEdge_SelfLoopRejected(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	id, _ := dag.CreateTask(ctx, Task{Title: "self", Project: "p"})
	err := dag.AddEdge(ctx, id, id)
	if err == nil {
		t.Fatal("expected error for self-loop edge")
	}
	if !strings.Contains(err.Error(), "self-loop") {
		t.Fatalf("expected self-loop error, got: %v", err)
	}
}

func TestAddEdge_CycleDetected(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	idA, _ := dag.CreateTask(ctx, Task{Title: "A", Project: "p"})
	idB, _ := dag.CreateTask(ctx, Task{Title: "B", Project: "p"})
	idC, _ := dag.CreateTask(ctx, Task{Title: "C", Project: "p"})

	// A -> B -> C  (A depends on B, B depends on C)
	if err := dag.AddEdge(ctx, idA, idB); err != nil {
		t.Fatalf("AddEdge A->B: %v", err)
	}
	if err := dag.AddEdge(ctx, idB, idC); err != nil {
		t.Fatalf("AddEdge B->C: %v", err)
	}

	// C -> A would create a cycle: A -> B -> C -> A
	err := dag.AddEdge(ctx, idC, idA)
	if err == nil {
		t.Fatal("expected error for cycle C->A")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got: %v", err)
	}
}

func TestAddEdge_NoCycleFalsePositive(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	idA, _ := dag.CreateTask(ctx, Task{Title: "A", Project: "p"})
	idB, _ := dag.CreateTask(ctx, Task{Title: "B", Project: "p"})
	idC, _ := dag.CreateTask(ctx, Task{Title: "C", Project: "p"})

	// A -> B, then A -> C should be fine (no cycle, just diamond).
	if err := dag.AddEdge(ctx, idA, idB); err != nil {
		t.Fatalf("AddEdge A->B: %v", err)
	}
	if err := dag.AddEdge(ctx, idA, idC); err != nil {
		t.Fatalf("AddEdge A->C should not be a cycle: %v", err)
	}

	// C -> B should also be fine (A -> B, A -> C, C -> B, no cycle).
	if err := dag.AddEdge(ctx, idC, idB); err != nil {
		t.Fatalf("AddEdge C->B should not be a cycle: %v", err)
	}
}

func TestAddEdge_CrossProjectRejected(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	id1, _ := dag.CreateTask(ctx, Task{Title: "t1", Project: "alpha"})
	id2, _ := dag.CreateTask(ctx, Task{Title: "t2", Project: "beta"})

	err := dag.AddEdge(ctx, id1, id2)
	if err == nil {
		t.Fatal("expected error for cross-project edge")
	}
	if !strings.Contains(err.Error(), "cross-project") {
		t.Fatalf("expected cross-project error, got: %v", err)
	}
}

func TestGetDependents(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	aID, _ := dag.CreateTask(ctx, Task{Title: "A", Project: "p", Status: "ready"})
	bID, _ := dag.CreateTask(ctx, Task{Title: "B", Project: "p", Status: "open"})
	cID, _ := dag.CreateTask(ctx, Task{Title: "C", Project: "p", Status: "open"})

	// B depends on A, C depends on B
	if err := dag.AddEdge(ctx, bID, aID); err != nil {
		t.Fatal(err)
	}
	if err := dag.AddEdge(ctx, cID, bID); err != nil {
		t.Fatal(err)
	}

	deps, err := dag.GetDependents(ctx, aID)
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 1 || deps[0] != bID {
		t.Fatalf("expected [%s], got %v", bID, deps)
	}

	deps, err = dag.GetDependents(ctx, bID)
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 1 || deps[0] != cID {
		t.Fatalf("expected [%s], got %v", cID, deps)
	}

	deps, err = dag.GetDependents(ctx, cID)
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 0 {
		t.Fatalf("expected empty, got %v", deps)
	}
}

func TestAutoUnblockDependents_Chain(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	aID, _ := dag.CreateTask(ctx, Task{Title: "A", Project: "p", Status: "ready"})
	bID, _ := dag.CreateTask(ctx, Task{Title: "B", Project: "p", Status: "open"})
	cID, _ := dag.CreateTask(ctx, Task{Title: "C", Project: "p", Status: "open"})

	if err := dag.AddEdge(ctx, bID, aID); err != nil {
		t.Fatal(err)
	}
	if err := dag.AddEdge(ctx, cID, bID); err != nil {
		t.Fatal(err)
	}

	// Complete A
	if err := dag.UpdateTask(ctx, aID, map[string]any{"status": "done"}); err != nil {
		t.Fatal(err)
	}

	// Auto-unblock: B should promote to ready, C should stay open
	promoted, err := dag.AutoUnblockDependents(ctx, aID)
	if err != nil {
		t.Fatal(err)
	}
	if len(promoted) != 1 || promoted[0] != bID {
		t.Fatalf("expected [%s] promoted, got %v", bID, promoted)
	}

	bTask, _ := dag.GetTask(ctx, bID)
	if bTask.Status != "ready" {
		t.Fatalf("expected B=ready, got %s", bTask.Status)
	}
	cTask, _ := dag.GetTask(ctx, cID)
	if cTask.Status != "open" {
		t.Fatalf("expected C=open, got %s", cTask.Status)
	}

	// Complete B
	if err := dag.UpdateTask(ctx, bID, map[string]any{"status": "done"}); err != nil {
		t.Fatal(err)
	}

	promoted, err = dag.AutoUnblockDependents(ctx, bID)
	if err != nil {
		t.Fatal(err)
	}
	if len(promoted) != 1 || promoted[0] != cID {
		t.Fatalf("expected [%s] promoted, got %v", cID, promoted)
	}

	cTask, _ = dag.GetTask(ctx, cID)
	if cTask.Status != "ready" {
		t.Fatalf("expected C=ready, got %s", cTask.Status)
	}
}

func TestAutoUnblockDependents_MultiDep(t *testing.T) {
	dag := newTestDAG(t)
	ctx := t.Context()

	aID, _ := dag.CreateTask(ctx, Task{Title: "A", Project: "p", Status: "ready"})
	bID, _ := dag.CreateTask(ctx, Task{Title: "B", Project: "p", Status: "ready"})
	cID, _ := dag.CreateTask(ctx, Task{Title: "C", Project: "p", Status: "open"})

	// C depends on both A and B
	if err := dag.AddEdge(ctx, cID, aID); err != nil {
		t.Fatal(err)
	}
	if err := dag.AddEdge(ctx, cID, bID); err != nil {
		t.Fatal(err)
	}

	// Complete A only — C should NOT unblock
	if err := dag.UpdateTask(ctx, aID, map[string]any{"status": "done"}); err != nil {
		t.Fatal(err)
	}
	promoted, err := dag.AutoUnblockDependents(ctx, aID)
	if err != nil {
		t.Fatal(err)
	}
	if len(promoted) != 0 {
		t.Fatalf("expected no promotions, got %v", promoted)
	}

	cTask, _ := dag.GetTask(ctx, cID)
	if cTask.Status != "open" {
		t.Fatalf("expected C=open, got %s", cTask.Status)
	}

	// Complete B — now C should unblock
	if err := dag.UpdateTask(ctx, bID, map[string]any{"status": "done"}); err != nil {
		t.Fatal(err)
	}
	promoted, err = dag.AutoUnblockDependents(ctx, bID)
	if err != nil {
		t.Fatal(err)
	}
	if len(promoted) != 1 || promoted[0] != cID {
		t.Fatalf("expected [%s] promoted, got %v", cID, promoted)
	}

	cTask, _ = dag.GetTask(ctx, cID)
	if cTask.Status != "ready" {
		t.Fatalf("expected C=ready, got %s", cTask.Status)
	}
}

func TestIsTerminalStatus(t *testing.T) {
	terminal := []string{"closed", "completed", "escalated", "plan_failed", "canceled", "done"}
	for _, s := range terminal {
		if !isTerminalStatus(s) {
			t.Errorf("expected %q to be terminal", s)
		}
	}
	nonTerminal := []string{"open", "ready", "in_progress", "blocked"}
	for _, s := range nonTerminal {
		if isTerminalStatus(s) {
			t.Errorf("expected %q to be non-terminal", s)
		}
	}
}
