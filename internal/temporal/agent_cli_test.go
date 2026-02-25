package temporal

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antigravity-dev/chum/internal/config"
)

// ---------------------------------------------------------------------------
// cliCommand — builds exec.Cmd for coding mode (prompt piped via stdin)
// ---------------------------------------------------------------------------

func TestCliCommand_Claude(t *testing.T) {
	// claude is a recognized CLI prefix with --dangerously-skip-permissions
	cmd := cliCommand("claude", "/tmp/work")

	require.Contains(t, cmd.Path, "claude")
	require.Equal(t, []string{
		"claude", "--dangerously-skip-permissions",
	}, cmd.Args)
	require.Equal(t, "/tmp/work", cmd.Dir)
}

func TestCliCommand_Codex(t *testing.T) {
	cmd := cliCommand("codex", "/tmp/work")

	require.Contains(t, cmd.Path, "codex")
	require.Equal(t, []string{
		"codex", "exec", "--full-auto", "--json",
	}, cmd.Args)
	require.Equal(t, "/tmp/work", cmd.Dir)
}

func TestCliCommand_Gemini(t *testing.T) {
	cmd := cliCommand("gemini", "/tmp/work")

	require.Contains(t, cmd.Path, "gemini")
	require.Equal(t, []string{
		"gemini", "-p", "", "--yolo", "-o", "json",
	}, cmd.Args)
	require.Equal(t, "/tmp/work", cmd.Dir)
}

func TestCliCommand_UnknownDefaultsToCodex(t *testing.T) {
	cmd := cliCommand("unknown-agent", "/tmp/work")

	// Unknown agents fall through to the default (codex) branch
	require.Equal(t, []string{
		"codex", "exec", "--full-auto", "--json",
	}, cmd.Args)
	require.Equal(t, "/tmp/work", cmd.Dir)
}

func TestCliCommand_CaseInsensitive(t *testing.T) {
	// "CODEX" should match "codex" case
	cmd := cliCommand("CODEX", "/tmp/work")
	require.Equal(t, []string{
		"codex", "exec", "--full-auto", "--json",
	}, cmd.Args)
}

func TestCliCommand_EmptyAgentDefaultsToCodex(t *testing.T) {
	cmd := cliCommand("", "/tmp/work")
	require.Equal(t, []string{
		"codex", "exec", "--full-auto", "--json",
	}, cmd.Args)
}

// ---------------------------------------------------------------------------
// normalizeAgent — provider key → CLI name resolution
// ---------------------------------------------------------------------------

func TestNormalizeAgent_DirectCLINames(t *testing.T) {
	require.Equal(t, "codex", normalizeAgent("codex"))
	require.Equal(t, "gemini", normalizeAgent("gemini"))
	require.Equal(t, "deepseek", normalizeAgent("deepseek"))
}

func TestNormalizeAgent_ProviderKeys(t *testing.T) {
	require.Equal(t, "gemini", normalizeAgent("gemini-pro"))
	require.Equal(t, "gemini", normalizeAgent("gemini-flash"))
	require.Equal(t, "codex", normalizeAgent("codex-spark"))
	require.Equal(t, "deepseek", normalizeAgent("deepseek-v3"))
}

func TestNormalizeAgent_CaseInsensitive(t *testing.T) {
	require.Equal(t, "gemini", normalizeAgent("Gemini-Pro"))
	require.Equal(t, "codex", normalizeAgent("CODEX-SPARK"))
}

func TestNormalizeAgent_Unknown(t *testing.T) {
	require.Equal(t, "claude", normalizeAgent("claude"))
	require.Equal(t, "gpt-4", normalizeAgent("gpt-4"))
}

func TestCliCommand_GeminiProResolvesToGemini(t *testing.T) {
	cmd := cliCommand("gemini-pro", "/tmp/work")
	require.Equal(t, []string{
		"gemini", "-p", "", "--yolo", "-o", "json",
	}, cmd.Args)
}

func TestCliCommand_CodexSparkResolvesToCodex(t *testing.T) {
	cmd := cliCommand("codex-spark", "/tmp/work")
	require.Equal(t, []string{
		"codex", "exec", "--full-auto", "--json",
	}, cmd.Args)
}

func TestCliCommand_WorkDirSet(t *testing.T) {
	paths := []string{"/home/user/project", "/tmp/test-repo", "/var/data"}
	for _, p := range paths {
		cmd := cliCommand("claude", p)
		require.Equal(t, p, cmd.Dir)
	}
}

func TestCliCommand_PromptNotInArgs(t *testing.T) {
	// SECURITY: Prompts must never appear in CLI arguments.
	// They are piped via stdin by runCLI instead.
	cmd := cliCommand("claude", "/tmp/work")
	for _, arg := range cmd.Args {
		require.NotContains(t, arg, "implement", "prompt text must not appear in argv")
	}
	require.Nil(t, cmd.Stdin, "stdin should be nil — runCLI sets it later")
}

// ---------------------------------------------------------------------------
// cliReviewCommand — builds exec.Cmd for review mode (prompt piped via stdin)
// ---------------------------------------------------------------------------

func TestCliReviewCommand_Claude(t *testing.T) {
	// claude is a recognized CLI prefix with --dangerously-skip-permissions
	cmd := cliReviewCommand("claude", "/tmp/work")

	require.Equal(t, []string{
		"claude", "--dangerously-skip-permissions",
	}, cmd.Args)
	require.Equal(t, "/tmp/work", cmd.Dir)
}

func TestCliReviewCommand_Codex(t *testing.T) {
	cmd := cliReviewCommand("codex", "/tmp/work")

	require.Equal(t, []string{
		"codex", "exec", "--full-auto",
	}, cmd.Args)
	require.Equal(t, "/tmp/work", cmd.Dir)
}

func TestCliReviewCommand_CodexNoJSON(t *testing.T) {
	// Review mode for codex omits --json (unlike coding mode)
	cmd := cliReviewCommand("codex", "/tmp/work")
	for _, arg := range cmd.Args {
		require.NotEqual(t, "--json", arg, "codex review should not include --json flag")
	}
}

func TestCliReviewCommand_GeminiResolvesCorrectly(t *testing.T) {
	cmd := cliReviewCommand("gemini", "/tmp/work")
	require.Equal(t, []string{
		"gemini", "-p", "", "--yolo", "-o", "json",
	}, cmd.Args)
}

func TestCliReviewCommand_UnknownDefaultsToCodex(t *testing.T) {
	cmd := cliReviewCommand("unknown", "/tmp/work")
	require.Equal(t, []string{
		"codex", "exec", "--full-auto",
	}, cmd.Args)
}

func TestCliReviewCommand_CaseInsensitive(t *testing.T) {
	cmd := cliReviewCommand("CODEX", "/tmp/work")
	require.Equal(t, []string{
		"codex", "exec", "--full-auto",
	}, cmd.Args)
}

// ---------------------------------------------------------------------------
// Coding vs review mode — structural differences
// ---------------------------------------------------------------------------

func TestCodingVsReviewMode_CodexArgDifferences(t *testing.T) {
	codingCmd := cliCommand("codex", "/tmp")
	reviewCmd := cliReviewCommand("codex", "/tmp")

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

func TestCodingVsReviewMode_Claude(t *testing.T) {
	// claude uses --dangerously-skip-permissions in both modes
	codingCmd := cliCommand("claude", "/tmp")
	reviewCmd := cliReviewCommand("claude", "/tmp")

	// Both use claude binary
	require.Contains(t, codingCmd.Path, "claude")
	require.Contains(t, reviewCmd.Path, "claude")

	// Both use --dangerously-skip-permissions
	require.Equal(t, []string{"claude", "--dangerously-skip-permissions"}, codingCmd.Args)
	require.Equal(t, []string{"claude", "--dangerously-skip-permissions"}, reviewCmd.Args)
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
	require.Equal(t, "gemini", DefaultReviewer("codex"))
}

func TestDefaultReviewer_UnknownFallsBackToCodex(t *testing.T) {
	require.Equal(t, "codex", DefaultReviewer("gemini")) // gemini → codex
	require.Equal(t, "codex", DefaultReviewer(""))
	require.Equal(t, "codex", DefaultReviewer("gpt-4"))
}

func TestDefaultReviewer_CrossModelSymmetry(t *testing.T) {
	// codex's reviewer is gemini, gemini's reviewer is codex — cross-model review
	reviewer1 := DefaultReviewer("codex")
	require.Equal(t, "gemini", reviewer1)
	reviewer2 := DefaultReviewer(reviewer1)
	require.Equal(t, "codex", reviewer2, "cross-model review should be symmetric")
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

// ---------------------------------------------------------------------------
// Config-driven CLI commands (M1b)
// ---------------------------------------------------------------------------

// mockConfigManager implements config.ConfigManager for tests.
type mockConfigManager struct {
	cfg *config.Config
}

func (m *mockConfigManager) Get() *config.Config  { return m.cfg }
func (m *mockConfigManager) Set(*config.Config)    {}
func (m *mockConfigManager) Reload(string) error   { return nil }

func TestCLICommandWithModel_ConfigDriven_CodexHigh(t *testing.T) {
	acts := &Activities{
		CfgMgr: &mockConfigManager{cfg: &config.Config{
			Dispatch: config.Dispatch{
				CLI: map[string]config.CLIConfig{
					"codex-high": {
						Cmd:       "codex",
						Args:      []string{"exec", "--full-auto", "-c", "model_reasoning_effort=high"},
						ModelFlag: "-m",
					},
				},
			},
		}},
	}

	cmd := acts.cliCommandWithModel("codex-high", "/tmp/work", "")
	require.Contains(t, cmd.Path, "codex")
	require.Equal(t, []string{
		"codex", "exec", "--full-auto", "-c", "model_reasoning_effort=high",
	}, cmd.Args)
	require.Equal(t, "/tmp/work", cmd.Dir)
}

func TestCLICommandWithModel_ConfigDriven_CodexLow(t *testing.T) {
	acts := &Activities{
		CfgMgr: &mockConfigManager{cfg: &config.Config{
			Dispatch: config.Dispatch{
				CLI: map[string]config.CLIConfig{
					"codex-low": {
						Cmd:       "codex",
						Args:      []string{"exec", "--full-auto", "-c", "model_reasoning_effort=low"},
						ModelFlag: "-m",
					},
				},
			},
		}},
	}

	cmd := acts.cliCommandWithModel("codex-low", "/tmp/work", "")
	require.Equal(t, []string{
		"codex", "exec", "--full-auto", "-c", "model_reasoning_effort=low",
	}, cmd.Args)
}

func TestCLICommandWithModel_ConfigDriven_DifferentArgs(t *testing.T) {
	// Core acceptance criterion: codex-high and codex-low produce DIFFERENT commands
	acts := &Activities{
		CfgMgr: &mockConfigManager{cfg: &config.Config{
			Dispatch: config.Dispatch{
				CLI: map[string]config.CLIConfig{
					"codex-high": {
						Cmd:       "codex",
						Args:      []string{"exec", "--full-auto", "-c", "model_reasoning_effort=high"},
						ModelFlag: "-m",
					},
					"codex-low": {
						Cmd:       "codex",
						Args:      []string{"exec", "--full-auto", "-c", "model_reasoning_effort=low"},
						ModelFlag: "-m",
					},
				},
			},
		}},
	}

	highCmd := acts.cliCommandWithModel("codex-high", "/tmp", "")
	lowCmd := acts.cliCommandWithModel("codex-low", "/tmp", "")

	// Both use codex binary
	require.Contains(t, highCmd.Path, "codex")
	require.Contains(t, lowCmd.Path, "codex")

	// But args differ
	require.NotEqual(t, highCmd.Args, lowCmd.Args, "codex-high and codex-low must produce different args")
	require.Contains(t, highCmd.Args, "model_reasoning_effort=high")
	require.Contains(t, lowCmd.Args, "model_reasoning_effort=low")
}

func TestCLICommandWithModel_ConfigDriven_WithModelOverride(t *testing.T) {
	acts := &Activities{
		CfgMgr: &mockConfigManager{cfg: &config.Config{
			Dispatch: config.Dispatch{
				CLI: map[string]config.CLIConfig{
					"codex-high": {
						Cmd:       "codex",
						Args:      []string{"exec", "--full-auto"},
						ModelFlag: "-m",
					},
				},
			},
		}},
	}

	cmd := acts.cliCommandWithModel("codex-high", "/tmp", "gpt-5.3-codex")
	require.Equal(t, []string{
		"codex", "exec", "--full-auto", "-m", "gpt-5.3-codex",
	}, cmd.Args)
}

func TestCLICommandWithModel_ConfigDriven_FallbackOnMissingKey(t *testing.T) {
	// Agent key not in config → falls back to hardcoded switch
	acts := &Activities{
		CfgMgr: &mockConfigManager{cfg: &config.Config{
			Dispatch: config.Dispatch{
				CLI: map[string]config.CLIConfig{
					"codex-high": {Cmd: "codex", Args: []string{"exec"}},
				},
			},
		}},
	}

	// "gemini" not in config → should use hardcoded gemini args
	cmd := acts.cliCommandWithModel("gemini", "/tmp", "")
	require.Contains(t, cmd.Path, "gemini")
	require.Contains(t, cmd.Args, "--yolo")
}

func TestCLICommandWithModel_NilCfgMgr_UsesHardcoded(t *testing.T) {
	// CfgMgr is nil (test/zero-value Activities) → hardcoded path
	acts := &Activities{}
	cmd := acts.cliCommandWithModel("codex", "/tmp/work", "")
	require.Equal(t, []string{
		"codex", "exec", "--full-auto", "--json",
	}, cmd.Args)
}

func TestCLICommandWithModel_ConfigDriven_DoesNotMutateConfigArgs(t *testing.T) {
	// Verify that adding model flag doesn't mutate the config's Args slice
	cliCfg := config.CLIConfig{
		Cmd:       "codex",
		Args:      []string{"exec", "--full-auto"},
		ModelFlag: "-m",
	}
	acts := &Activities{
		CfgMgr: &mockConfigManager{cfg: &config.Config{
			Dispatch: config.Dispatch{
				CLI: map[string]config.CLIConfig{"codex-high": cliCfg},
			},
		}},
	}

	// Call with model to trigger append
	acts.cliCommandWithModel("codex-high", "/tmp", "gpt-5")
	// Original config args should be untouched
	require.Equal(t, []string{"exec", "--full-auto"}, cliCfg.Args)
}

