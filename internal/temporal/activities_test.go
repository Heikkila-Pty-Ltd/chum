package temporal

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antigravity-dev/chum/internal/config"
)

func TestResolveTierAgent(t *testing.T) {
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
		{name: "premium tier returns first agent", tier: "premium", want: "claude"},
		{name: "balanced tier returns first agent", tier: "balanced", want: "gemini"},
		{name: "empty tier defaults to fast", tier: "", want: "codex"},
		{name: "unknown tier falls back to codex", tier: "turbo", want: "codex"},
		{name: "case insensitive", tier: "FAST", want: "codex"},
		{name: "case insensitive premium", tier: "Premium", want: "claude"},
		{name: "whitespace trimmed", tier: " fast ", want: "codex"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveTierAgent(tiers, tt.tier)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestResolveTierAgent_EmptyAgentList(t *testing.T) {
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
		Result: "Here is the implementation...",
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
