package temporal

import (
	"database/sql"
	"os"
	"testing"

	"github.com/antigravity-dev/chum/internal/graph"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	_ "modernc.org/sqlite"
)

func TestMarkMorselDoneActivity(t *testing.T) {
	a := &Activities{}
	s := testsuite.WorkflowTestSuite{}
	actEnv := s.NewTestActivityEnvironment()
	actEnv.RegisterActivity(a.MarkMorselDoneActivity)

	t.Run("updates ready to done", func(t *testing.T) {
		dir := t.TempDir()
		morselsDir := dir + "/.morsels"
		require.NoError(t, os.MkdirAll(morselsDir, 0o755))

		content := "---\ntitle: \"Test task\"\nstatus: ready\npriority: 1\n---\n\nSome description.\n"
		require.NoError(t, os.WriteFile(morselsDir+"/test-task.md", []byte(content), 0o644))

		_, err := actEnv.ExecuteActivity(a.MarkMorselDoneActivity, dir, "test-task")
		require.NoError(t, err)

		got, err := os.ReadFile(morselsDir + "/test-task.md")
		require.NoError(t, err)
		require.Contains(t, string(got), "status: done")
		require.NotContains(t, string(got), "status: ready")
	})

	t.Run("idempotent on already done", func(t *testing.T) {
		dir := t.TempDir()
		morselsDir := dir + "/.morsels"
		require.NoError(t, os.MkdirAll(morselsDir, 0o755))

		content := "---\ntitle: \"Test task\"\nstatus: done\npriority: 1\n---\n\nDone task.\n"
		require.NoError(t, os.WriteFile(morselsDir+"/done-task.md", []byte(content), 0o644))

		_, err := actEnv.ExecuteActivity(a.MarkMorselDoneActivity, dir, "done-task")
		require.NoError(t, err)

		got, err := os.ReadFile(morselsDir + "/done-task.md")
		require.NoError(t, err)
		require.Contains(t, string(got), "status: done")
	})

	t.Run("missing file returns nil", func(t *testing.T) {
		dir := t.TempDir()
		_, err := actEnv.ExecuteActivity(a.MarkMorselDoneActivity, dir, "nonexistent")
		require.NoError(t, err)
	})

	t.Run("handles status with comment", func(t *testing.T) {
		dir := t.TempDir()
		morselsDir := dir + "/.morsels"
		require.NoError(t, os.MkdirAll(morselsDir, 0o755))

		content := "---\ntitle: \"Commented task\"\nstatus: ready # waiting for deps\npriority: 0\n---\n"
		require.NoError(t, os.WriteFile(morselsDir+"/commented.md", []byte(content), 0o644))

		_, err := actEnv.ExecuteActivity(a.MarkMorselDoneActivity, dir, "commented")
		require.NoError(t, err)

		got, err := os.ReadFile(morselsDir + "/commented.md")
		require.NoError(t, err)
		require.Contains(t, string(got), "status: done")
		require.NotContains(t, string(got), "status: ready")
	})
}

func TestDetectWhalesActivity(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	dag := graph.NewDAG(db)
	require.NoError(t, dag.EnsureSchema(t.Context()))

	// Create tasks with varying types and estimates.
	tasks := []graph.Task{
		{Title: "Small task", Type: "task", Priority: 2, EstimateMinutes: 30, Project: "proj"},
		{Title: "Big task", Type: "task", Priority: 1, EstimateMinutes: 120, Project: "proj"},                                                   // whale by estimate
		{Title: "Typed whale", Type: "whale", Priority: 3, EstimateMinutes: 60, Project: "proj"},                                                // whale by type
		{Title: "Epic container", Type: "epic", Priority: 0, EstimateMinutes: 500, Project: "proj"},                                              // excluded
		{Title: "Borderline task", Type: "task", Priority: 2, EstimateMinutes: 90, Project: "proj"},                                              // exactly 90 = not whale
		{Title: "Already decomposed", Type: "whale", Priority: 1, EstimateMinutes: 200, Labels: []string{"groom:decomposed"}, Project: "proj"}, // excluded by label
	}
	for _, task := range tasks {
		_, createErr := dag.CreateTask(t.Context(), task)
		require.NoError(t, createErr)
	}

	acts := &Activities{DAG: dag}
	s := testsuite.WorkflowTestSuite{}
	actEnv := s.NewTestActivityEnvironment()
	actEnv.RegisterActivity(acts.DetectWhalesActivity)

	var result []graph.Task
	val, execErr := actEnv.ExecuteActivity(acts.DetectWhalesActivity, "proj")
	require.NoError(t, execErr)
	require.NoError(t, val.Get(&result))

	// Should find 2 whales: "Big task" (estimate 120 > 90) and "Typed whale" (type=whale).
	// "Small task" (30 min), "Epic container" (excluded), and "Borderline task" (exactly 90) are excluded.
	require.Len(t, result, 2)

	// Sorted by priority: Big task (P1) first, Typed whale (P3) second.
	require.Equal(t, "Big task", result[0].Title)
	require.Equal(t, "Typed whale", result[1].Title)
}

func TestDetectWhalesActivity_NilDAG(t *testing.T) {
	acts := &Activities{DAG: nil}
	s := testsuite.WorkflowTestSuite{}
	actEnv := s.NewTestActivityEnvironment()
	actEnv.RegisterActivity(acts.DetectWhalesActivity)

	var result []graph.Task
	val, execErr := actEnv.ExecuteActivity(acts.DetectWhalesActivity, "proj")
	require.NoError(t, execErr)
	require.NoError(t, val.Get(&result))
	require.Empty(t, result)
}
