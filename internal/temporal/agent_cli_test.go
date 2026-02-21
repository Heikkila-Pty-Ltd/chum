package temporal

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antigravity-dev/chum/internal/config"
)

// ---------------------------------------------------------------------------
// cliCommand — builds exec.Cmd for coding mode
// ---------------------------------------------------------------------------

func TestCliCommand_Claude(t *testing.T) {
	cmd := cliCommand("claude", "implement the feature", "/tmp/work")

	require.Equal(t, "claude", cmd.Path[len(cmd.Path)-len("claude"):])
	require.Equal(t, []string{
		"claude", "--print", "--output-format", "json", "--dangerously-skip-permissions",
		"implement the feature",
	}, cmd.Args)
	require.Equal(t, "/tmp/work", cmd.Dir)
}

func TestCliCommand_Codex(t *testing.T) {
	cmd := cliCommand("codex", "fix the bug", "/tmp/work")

	require.Contains(t, cmd.Path, "codex")
	require.Equal(t, []string{
		"codex", "exec", "--full-auto", "--json", "fix the bug",
	}, cmd.Args)
	require.Equal(t, "/tmp/work", cmd.Dir)
}

func TestCliCommand_Gemini(t *testing.T) {
	cmd := cliCommand("gemini", "refactor the module", "/tmp/work")

	require.Contains(t, cmd.Path, "gemini")
	require.Equal(t, []string{
		"gemini", "-p", "refactor the module", "--yolo", "-o", "json",
	}, cmd.Args)
	require.Equal(t, "/tmp/work", cmd.Dir)
}

func TestCliCommand_UnknownDefaultsToClaude(t *testing.T) {
	cmd := cliCommand("unknown-agent", "do stuff", "/tmp/work")

	// Unknown agents fall through to the default (claude) branch
	require.Equal(t, []string{
		"claude", "--print", "--output-format", "json", "--dangerously-skip-permissions",
		"do stuff",
	}, cmd.Args)
	require.Equal(t, "/tmp/work", cmd.Dir)
}

func TestCliCommand_CaseInsensitive(t *testing.T) {
	// "CODEX" should match "codex" case
	cmd := cliCommand("CODEX", "task", "/tmp/work")
	require.Equal(t, []string{
		"codex", "exec", "--full-auto", "--json", "task",
	}, cmd.Args)
}

func TestCliCommand_EmptyAgentDefaultsToClaude(t *testing.T) {
	cmd := cliCommand("", "prompt", "/tmp/work")
	require.Equal(t, []string{
		"claude", "--print", "--output-format", "json", "--dangerously-skip-permissions",
		"prompt",
	}, cmd.Args)
}

func TestCliCommand_WorkDirSet(t *testing.T) {
	paths := []string{"/home/user/project", "/tmp/test-repo", "/var/data"}
	for _, p := range paths {
		cmd := cliCommand("claude", "test", p)
		require.Equal(t, p, cmd.Dir)
	}
}

func TestCliCommand_PromptWithSpecialChars(t *testing.T) {
	prompt := `implement "quoted" feature with $pecial chars & pipes | stuff`
	cmd := cliCommand("claude", prompt, "/tmp/work")
	// The prompt should be passed as a single argument, preserving special characters
	require.Equal(t, prompt, cmd.Args[len(cmd.Args)-1])
}

func TestCliCommand_MultilinePrompt(t *testing.T) {
	prompt := "line one\nline two\nline three"
	cmd := cliCommand("codex", prompt, "/tmp/work")
	require.Equal(t, prompt, cmd.Args[len(cmd.Args)-1])
}

// ---------------------------------------------------------------------------
// cliReviewCommand — builds exec.Cmd for review mode
// ---------------------------------------------------------------------------

func TestCliReviewCommand_Claude(t *testing.T) {
	cmd := cliReviewCommand("claude", "review this code", "/tmp/work")

	require.Equal(t, []string{
		"claude", "--print", "--output-format", "json", "--dangerously-skip-permissions",
		"review this code",
	}, cmd.Args)
	require.Equal(t, "/tmp/work", cmd.Dir)
}

func TestCliReviewCommand_Codex(t *testing.T) {
	cmd := cliReviewCommand("codex", "review the diff", "/tmp/work")

	require.Equal(t, []string{
		"codex", "exec", "--full-auto", "review the diff",
	}, cmd.Args)
	require.Equal(t, "/tmp/work", cmd.Dir)
}

func TestCliReviewCommand_CodexNoJSON(t *testing.T) {
	// Review mode for codex omits --json (unlike coding mode)
	cmd := cliReviewCommand("codex", "review", "/tmp/work")
	for _, arg := range cmd.Args {
		require.NotEqual(t, "--json", arg, "codex review should not include --json flag")
	}
}

func TestCliReviewCommand_UnknownDefaultsToClaude(t *testing.T) {
	cmd := cliReviewCommand("gemini", "review", "/tmp/work")
	// gemini is not in the codex case, falls to default (claude)
	require.Equal(t, []string{
		"claude", "--print", "--output-format", "json", "--dangerously-skip-permissions",
		"review",
	}, cmd.Args)
}

func TestCliReviewCommand_CaseInsensitive(t *testing.T) {
	cmd := cliReviewCommand("CODEX", "review", "/tmp/work")
	require.Equal(t, []string{
		"codex", "exec", "--full-auto", "review",
	}, cmd.Args)
}

// ---------------------------------------------------------------------------
// Coding vs review mode — structural differences
// ---------------------------------------------------------------------------

func TestCodingVsReviewMode_CodexArgDifferences(t *testing.T) {
	codingCmd := cliCommand("codex", "prompt", "/tmp")
	reviewCmd := cliReviewCommand("codex", "prompt", "/tmp")

	// Coding mode has --json, review mode does not
	codingHasJSON := false
	for _, arg := range codingCmd.Args {
		if arg == "--json" {
			codingHasJSON = true
		}
	}
	require.True(t, codingHasJSON, "codex coding mode should include --json")

	reviewHasJSON := false
	for _, arg := range reviewCmd.Args {
		if arg == "--json" {
			reviewHasJSON = true
		}
	}
	require.False(t, reviewHasJSON, "codex review mode should not include --json")
}

func TestCodingVsReviewMode_ClaudeSameArgs(t *testing.T) {
	// Claude uses the same flags for both modes — differentiation is in the prompt
	codingCmd := cliCommand("claude", "same prompt", "/tmp")
	reviewCmd := cliReviewCommand("claude", "same prompt", "/tmp")

	require.Equal(t, codingCmd.Args, reviewCmd.Args)
}

// ---------------------------------------------------------------------------
// ResolveTierAgent — additional edge cases beyond activities_test.go
// ---------------------------------------------------------------------------

func TestResolveTierAgent_SingleAgentTiers(t *testing.T) {
	tiers := config.Tiers{
		Fast:     []string{"gemini"},
		Balanced: []string{"claude"},
		Premium:  []string{"codex"},
	}

	require.Equal(t, "gemini", ResolveTierAgent(tiers, "fast"))
	require.Equal(t, "claude", ResolveTierAgent(tiers, "balanced"))
	require.Equal(t, "codex", ResolveTierAgent(tiers, "premium"))
}

func TestResolveTierAgent_OnlyFirstAgentReturned(t *testing.T) {
	tiers := config.Tiers{
		Fast: []string{"first", "second", "third"},
	}

	// Only the first agent in the list should be returned
	require.Equal(t, "first", ResolveTierAgent(tiers, "fast"))
}

func TestResolveTierAgent_AllTiersEmpty(t *testing.T) {
	tiers := config.Tiers{}
	require.Equal(t, "codex", ResolveTierAgent(tiers, "fast"))
	require.Equal(t, "codex", ResolveTierAgent(tiers, "balanced"))
	require.Equal(t, "codex", ResolveTierAgent(tiers, "premium"))
	require.Equal(t, "codex", ResolveTierAgent(tiers, ""))
}

func TestResolveTierAgent_MixedCaseAndWhitespace(t *testing.T) {
	tiers := config.Tiers{
		Fast:    []string{"gemini"},
		Premium: []string{"claude"},
	}

	tests := []struct {
		tier string
		want string
	}{
		{tier: " FAST ", want: "gemini"},
		{tier: " Premium\t", want: "claude"},
		{tier: "  BALANCED  ", want: "codex"}, // balanced is empty, falls back to codex
	}
	for _, tt := range tests {
		got := ResolveTierAgent(tiers, tt.tier)
		require.Equal(t, tt.want, got, "tier=%q", tt.tier)
	}
}

// ---------------------------------------------------------------------------
// DefaultReviewer
// ---------------------------------------------------------------------------

func TestDefaultReviewer_Claude(t *testing.T) {
	require.Equal(t, "codex", DefaultReviewer("claude"))
}

func TestDefaultReviewer_Codex(t *testing.T) {
	require.Equal(t, "claude", DefaultReviewer("codex"))
}

func TestDefaultReviewer_UnknownFallsBackToCodex(t *testing.T) {
	require.Equal(t, "codex", DefaultReviewer("gemini"))
	require.Equal(t, "codex", DefaultReviewer(""))
	require.Equal(t, "codex", DefaultReviewer("gpt-4"))
}

func TestDefaultReviewer_CrossModelSymmetry(t *testing.T) {
	// claude's reviewer is codex, codex's reviewer is claude — cross-model review
	reviewer1 := DefaultReviewer("claude")
	reviewer2 := DefaultReviewer(reviewer1)
	require.Equal(t, "claude", reviewer2, "cross-model review should be symmetric")
}

// ---------------------------------------------------------------------------
// StructuredPlan.Validate
// ---------------------------------------------------------------------------

func TestStructuredPlan_Validate_ValidPlan(t *testing.T) {
	plan := &StructuredPlan{
		Summary:            "Add user authentication",
		Steps:              []PlanStep{{Description: "Create auth middleware", File: "auth.go", Rationale: "Security"}},
		FilesToModify:      []string{"auth.go"},
		AcceptanceCriteria: []string{"POST /login returns JWT"},
	}

	issues := plan.Validate()
	require.Empty(t, issues)
}

func TestStructuredPlan_Validate_AllEmpty(t *testing.T) {
	plan := &StructuredPlan{}

	issues := plan.Validate()
	require.Len(t, issues, 4)
	require.Contains(t, issues[0], "summary")
	require.Contains(t, issues[1], "steps")
	require.Contains(t, issues[2], "acceptance criteria")
	require.Contains(t, issues[3], "files")
}

func TestStructuredPlan_Validate_MissingSummary(t *testing.T) {
	plan := &StructuredPlan{
		Steps:              []PlanStep{{Description: "step"}},
		FilesToModify:      []string{"file.go"},
		AcceptanceCriteria: []string{"check"},
	}

	issues := plan.Validate()
	require.Len(t, issues, 1)
	require.Contains(t, issues[0], "summary")
}

func TestStructuredPlan_Validate_MissingSteps(t *testing.T) {
	plan := &StructuredPlan{
		Summary:            "Do something",
		FilesToModify:      []string{"file.go"},
		AcceptanceCriteria: []string{"check"},
	}

	issues := plan.Validate()
	require.Len(t, issues, 1)
	require.Contains(t, issues[0], "steps")
}

func TestStructuredPlan_Validate_MissingAcceptanceCriteria(t *testing.T) {
	plan := &StructuredPlan{
		Summary:       "Do something",
		Steps:         []PlanStep{{Description: "step"}},
		FilesToModify: []string{"file.go"},
	}

	issues := plan.Validate()
	require.Len(t, issues, 1)
	require.Contains(t, issues[0], "acceptance criteria")
}

func TestStructuredPlan_Validate_MissingFiles(t *testing.T) {
	plan := &StructuredPlan{
		Summary:            "Do something",
		Steps:              []PlanStep{{Description: "step"}},
		AcceptanceCriteria: []string{"check"},
	}

	issues := plan.Validate()
	require.Len(t, issues, 1)
	require.Contains(t, issues[0], "files")
}

func TestStructuredPlan_Validate_MultipleStepsAndCriteria(t *testing.T) {
	plan := &StructuredPlan{
		Summary: "Complex feature",
		Steps: []PlanStep{
			{Description: "Step 1", File: "a.go", Rationale: "Foundation"},
			{Description: "Step 2", File: "b.go", Rationale: "Extension"},
			{Description: "Step 3", File: "c.go", Rationale: "Polish"},
		},
		FilesToModify:      []string{"a.go", "b.go", "c.go"},
		AcceptanceCriteria: []string{"test 1", "test 2", "test 3"},
	}

	issues := plan.Validate()
	require.Empty(t, issues)
}
