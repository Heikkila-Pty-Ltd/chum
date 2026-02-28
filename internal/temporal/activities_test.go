package temporal

import (
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/graph"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	_ "modernc.org/sqlite"
)

func TestResolveTierAgent(t *testing.T) {
	ResetTierRoundRobin()
	tiers := config.Tiers{
		Fast:     []string{"codex", "gemini"},
		Balanced: []string{"gemini", "claude"},
		Premium:  []string{"claude"},
	}

	tests := []struct {
		name string
		tier string
		want string
	}{
		{name: "fast tier returns first agent", tier: "fast", want: "codex"},
		{name: "fast tier round-robins to second", tier: "fast", want: "gemini"},
		{name: "premium tier returns first agent", tier: "premium", want: "claude"},
		{name: "balanced tier returns first agent", tier: "balanced", want: "gemini"},
		{name: "fast tier wraps around", tier: "fast", want: "codex"},
		{name: "empty tier defaults to fast", tier: "", want: "codex"},
		{name: "unknown tier falls back to codex", tier: "turbo", want: "codex"},
		{name: "case insensitive premium", tier: "Premium", want: "claude"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveTierAgent(tiers, tt.tier)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestResolveTierAgent_EmptyAgentList(t *testing.T) {
	ResetTierRoundRobin()
	tiers := config.Tiers{
		Fast:    []string{},
		Premium: nil,
	}

	tests := []struct {
		name string
		tier string
		want string
	}{
		{name: "empty fast list falls back to codex", tier: "fast", want: "codex"},
		{name: "nil premium list falls back to codex", tier: "premium", want: "codex"},
		{name: "empty tier with empty fast falls back to codex", tier: "", want: "codex"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveTierAgent(tiers, tt.tier)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestParseJSONOutput_ValidClaudeJSON(t *testing.T) {
	input := claudeJSONOutput{
		Result:  "Here is the implementation...",
		CostUSD: 0.042,
	}
	input.Usage.InputTokens = 1500
	input.Usage.OutputTokens = 800
	input.Usage.CacheReadTokens = 200
	input.Usage.CacheCreationTokens = 50

	raw, err := json.Marshal(input)
	require.NoError(t, err)

	result := parseJSONOutput(string(raw))
	require.Equal(t, "Here is the implementation...", result.Output)
	require.Equal(t, 1500, result.Tokens.InputTokens)
	require.Equal(t, 800, result.Tokens.OutputTokens)
	require.Equal(t, 200, result.Tokens.CacheReadTokens)
	require.Equal(t, 50, result.Tokens.CacheCreationTokens)
	require.InDelta(t, 0.042, result.Tokens.CostUSD, 0.0001)
}

func TestParseJSONOutput_PlainText(t *testing.T) {
	// codex or non-JSON output — should return raw text with zero tokens
	raw := "Here is the implementation of the feature..."
	result := parseJSONOutput(raw)
	require.Equal(t, raw, result.Output)
	require.Equal(t, 0, result.Tokens.InputTokens)
	require.Equal(t, 0, result.Tokens.OutputTokens)
	require.Equal(t, 0.0, result.Tokens.CostUSD)
}

func TestParseJSONOutput_MalformedJSON(t *testing.T) {
	raw := `{"result": "partial JSON`
	result := parseJSONOutput(raw)
	require.Equal(t, raw, result.Output)
	require.Equal(t, 0, result.Tokens.InputTokens)
}

func TestParseJSONOutput_JSONWithoutUsage(t *testing.T) {
	// JSON that parses but has no usage or result — treated as non-claude output
	raw := `{"some_other": "field"}`
	result := parseJSONOutput(raw)
	require.Equal(t, raw, result.Output)
	require.Equal(t, 0, result.Tokens.InputTokens)
}

func TestParseJSONOutput_ResultOnlyNoUsage(t *testing.T) {
	// Has result but no usage tokens — still extracts result
	raw := `{"result": "some output text", "usage": {}}`
	result := parseJSONOutput(raw)
	require.Equal(t, "some output text", result.Output)
	require.Equal(t, 0, result.Tokens.InputTokens)
}

func TestParseAgentOutput_RoutesClaude(t *testing.T) {
	input := claudeJSONOutput{
		Result: "claude output",
	}
	input.Usage.InputTokens = 100
	raw, _ := json.Marshal(input)

	result := parseAgentOutput("claude", string(raw))
	require.Equal(t, "claude output", result.Output)
	require.Equal(t, 100, result.Tokens.InputTokens)
}

func TestParseAgentOutput_RoutesCodex(t *testing.T) {
	raw := "codex plain text output"
	result := parseAgentOutput("codex", raw)
	require.Equal(t, raw, result.Output)
	require.Equal(t, 0, result.Tokens.InputTokens)
}

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
		{Title: "Big task", Type: "task", Priority: 1, EstimateMinutes: 120, Project: "proj"},     // whale by estimate
		{Title: "Typed whale", Type: "whale", Priority: 3, EstimateMinutes: 60, Project: "proj"},   // whale by type
		{Title: "Epic container", Type: "epic", Priority: 0, EstimateMinutes: 500, Project: "proj"}, // excluded
		{Title: "Borderline task", Type: "task", Priority: 2, EstimateMinutes: 90, Project: "proj"}, // exactly 90 = not whale
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

func TestTokenUsageAdd(t *testing.T) {
	a := TokenUsage{InputTokens: 100, OutputTokens: 50, CacheReadTokens: 10, CacheCreationTokens: 5, CostUSD: 0.01}
	b := TokenUsage{InputTokens: 200, OutputTokens: 100, CacheReadTokens: 20, CacheCreationTokens: 10, CostUSD: 0.02}
	a.Add(b)
	require.Equal(t, 300, a.InputTokens)
	require.Equal(t, 150, a.OutputTokens)
	require.Equal(t, 30, a.CacheReadTokens)
	require.Equal(t, 15, a.CacheCreationTokens)
	require.InDelta(t, 0.03, a.CostUSD, 0.0001)
}
