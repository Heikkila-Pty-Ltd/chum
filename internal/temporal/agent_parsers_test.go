package temporal

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// parseCodexOutput
// ---------------------------------------------------------------------------

func TestParseCodexOutput_SingleAgentMessage(t *testing.T) {
	raw := `{"type":"item.created","item":{"type":"agent_message","text":"Hello from codex"}}
{"type":"turn.completed","usage":{"input_tokens":500,"cached_input_tokens":100,"output_tokens":250}}`

	result := parseCodexOutput(raw)
	require.Equal(t, "Hello from codex", result.Output)
	require.Equal(t, 500, result.Tokens.InputTokens)
	require.Equal(t, 250, result.Tokens.OutputTokens)
	require.Equal(t, 100, result.Tokens.CacheReadTokens)
}

func TestParseCodexOutput_MultipleAgentMessages(t *testing.T) {
	raw := `{"type":"item.created","item":{"type":"agent_message","text":"First part"}}
{"type":"item.created","item":{"type":"agent_message","text":"Second part"}}
{"type":"turn.completed","usage":{"input_tokens":1000,"cached_input_tokens":200,"output_tokens":600}}`

	result := parseCodexOutput(raw)
	require.Equal(t, "First part\nSecond part", result.Output)
	require.Equal(t, 1000, result.Tokens.InputTokens)
	require.Equal(t, 600, result.Tokens.OutputTokens)
	require.Equal(t, 200, result.Tokens.CacheReadTokens)
}

func TestParseCodexOutput_NoAgentMessages(t *testing.T) {
	// Only non-agent_message events — output falls back to raw
	raw := `{"type":"item.created","item":{"type":"function_call","text":""}}
{"type":"turn.completed","usage":{"input_tokens":300,"cached_input_tokens":0,"output_tokens":100}}`

	result := parseCodexOutput(raw)
	require.Equal(t, raw, result.Output)
	require.Equal(t, 300, result.Tokens.InputTokens)
	require.Equal(t, 100, result.Tokens.OutputTokens)
}

func TestParseCodexOutput_PlainText(t *testing.T) {
	// Non-JSON output — graceful degradation
	raw := "just some plain text output from codex"
	result := parseCodexOutput(raw)
	require.Equal(t, raw, result.Output)
	require.Equal(t, 0, result.Tokens.InputTokens)
	require.Equal(t, 0, result.Tokens.OutputTokens)
}

func TestParseCodexOutput_EmptyInput(t *testing.T) {
	result := parseCodexOutput("")
	require.Equal(t, "", result.Output)
	require.Equal(t, 0, result.Tokens.InputTokens)
}

func TestParseCodexOutput_BlankLinesIgnored(t *testing.T) {
	raw := `{"type":"item.created","item":{"type":"agent_message","text":"Hello"}}


{"type":"turn.completed","usage":{"input_tokens":50,"cached_input_tokens":0,"output_tokens":25}}`

	result := parseCodexOutput(raw)
	require.Equal(t, "Hello", result.Output)
	require.Equal(t, 50, result.Tokens.InputTokens)
	require.Equal(t, 25, result.Tokens.OutputTokens)
}

func TestParseCodexOutput_MalformedLinesSkipped(t *testing.T) {
	raw := `not json at all
{"type":"item.created","item":{"type":"agent_message","text":"Valid message"}}
{broken json
{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":0,"output_tokens":50}}`

	result := parseCodexOutput(raw)
	require.Equal(t, "Valid message", result.Output)
	require.Equal(t, 100, result.Tokens.InputTokens)
	require.Equal(t, 50, result.Tokens.OutputTokens)
}

func TestParseCodexOutput_NoTurnCompleted(t *testing.T) {
	// No turn.completed event — tokens stay zero
	raw := `{"type":"item.created","item":{"type":"agent_message","text":"Hello"}}`

	result := parseCodexOutput(raw)
	require.Equal(t, "Hello", result.Output)
	require.Equal(t, 0, result.Tokens.InputTokens)
	require.Equal(t, 0, result.Tokens.OutputTokens)
}

func TestParseCodexOutput_EmptyTextAgentMessageIgnored(t *testing.T) {
	// agent_message with empty text should not appear in output
	raw := `{"type":"item.created","item":{"type":"agent_message","text":""}}
{"type":"item.created","item":{"type":"agent_message","text":"Real content"}}
{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":0,"output_tokens":5}}`

	result := parseCodexOutput(raw)
	require.Equal(t, "Real content", result.Output)
}

// ---------------------------------------------------------------------------
// parseGeminiOutput
// ---------------------------------------------------------------------------

func TestParseGeminiOutput_SingleModel(t *testing.T) {
	raw := `{
		"stats": {
			"models": {
				"gemini-2.0-flash": {
					"tokens": {
						"input": 1200,
						"candidates": 800,
						"cached": 300,
						"total": 2300,
						"thoughts": 50
					}
				}
			}
		}
	}`

	result := parseGeminiOutput(raw)
	require.Equal(t, raw, result.Output) // gemini keeps raw as output
	require.Equal(t, 1200, result.Tokens.InputTokens)
	require.Equal(t, 850, result.Tokens.OutputTokens) // candidates + thoughts
	require.Equal(t, 300, result.Tokens.CacheReadTokens)
}

func TestParseGeminiOutput_MultipleModels(t *testing.T) {
	raw := `{
		"stats": {
			"models": {
				"gemini-2.0-flash": {
					"tokens": {
						"input": 500,
						"candidates": 300,
						"cached": 100,
						"total": 900,
						"thoughts": 20
					}
				},
				"gemini-2.5-pro": {
					"tokens": {
						"input": 800,
						"candidates": 600,
						"cached": 200,
						"total": 1600,
						"thoughts": 30
					}
				}
			}
		}
	}`

	result := parseGeminiOutput(raw)
	require.Equal(t, 1300, result.Tokens.InputTokens)      // 500 + 800
	require.Equal(t, 950, result.Tokens.OutputTokens)       // (300+20) + (600+30)
	require.Equal(t, 300, result.Tokens.CacheReadTokens)    // 100 + 200
}

func TestParseGeminiOutput_NoModels(t *testing.T) {
	raw := `{"stats": {"models": {}}}`

	result := parseGeminiOutput(raw)
	require.Equal(t, raw, result.Output)
	require.Equal(t, 0, result.Tokens.InputTokens)
	require.Equal(t, 0, result.Tokens.OutputTokens)
}

func TestParseGeminiOutput_InvalidJSON(t *testing.T) {
	raw := "not json at all"
	result := parseGeminiOutput(raw)
	require.Equal(t, raw, result.Output)
	require.Equal(t, 0, result.Tokens.InputTokens)
}

func TestParseGeminiOutput_MissingStatsField(t *testing.T) {
	raw := `{"some_other": "field"}`
	result := parseGeminiOutput(raw)
	require.Equal(t, raw, result.Output)
	require.Equal(t, 0, result.Tokens.InputTokens)
}

func TestParseGeminiOutput_ZeroTokens(t *testing.T) {
	raw := `{
		"stats": {
			"models": {
				"gemini-2.0-flash": {
					"tokens": {
						"input": 0,
						"candidates": 0,
						"cached": 0,
						"total": 0,
						"thoughts": 0
					}
				}
			}
		}
	}`

	result := parseGeminiOutput(raw)
	require.Equal(t, 0, result.Tokens.InputTokens)
	require.Equal(t, 0, result.Tokens.OutputTokens)
	require.Equal(t, 0, result.Tokens.CacheReadTokens)
}

// ---------------------------------------------------------------------------
// parseAgentOutput — routing coverage
// ---------------------------------------------------------------------------

func TestParseAgentOutput_RoutesGemini(t *testing.T) {
	raw := `{
		"stats": {
			"models": {
				"gemini-2.0-flash": {
					"tokens": {"input": 100, "candidates": 50, "cached": 10, "total": 160, "thoughts": 5}
				}
			}
		}
	}`

	result := parseAgentOutput("gemini", raw)
	require.Equal(t, 100, result.Tokens.InputTokens)
	require.Equal(t, 55, result.Tokens.OutputTokens) // candidates + thoughts
	require.Equal(t, 10, result.Tokens.CacheReadTokens)
}

func TestParseAgentOutput_UnknownAgentReturnRaw(t *testing.T) {
	raw := "output from some unknown agent"
	result := parseAgentOutput("unknown-agent", raw)
	require.Equal(t, raw, result.Output)
	require.Equal(t, 0, result.Tokens.InputTokens)
	require.Equal(t, 0, result.Tokens.OutputTokens)
}

func TestParseAgentOutput_CaseInsensitive(t *testing.T) {
	// Verify case insensitivity: "CLAUDE" routes to parseJSONOutput
	input := claudeJSONOutput{Result: "test output"}
	input.Usage.InputTokens = 42
	raw, err := json.Marshal(input)
	require.NoError(t, err)

	result := parseAgentOutput("CLAUDE", string(raw))
	require.Equal(t, "test output", result.Output)
	require.Equal(t, 42, result.Tokens.InputTokens)
}

func TestParseAgentOutput_CodexWithJSONL(t *testing.T) {
	// Verify codex routing with actual JSONL content
	raw := `{"type":"item.created","item":{"type":"agent_message","text":"codex did the thing"}}
{"type":"turn.completed","usage":{"input_tokens":750,"cached_input_tokens":50,"output_tokens":400}}`

	result := parseAgentOutput("codex", raw)
	require.Equal(t, "codex did the thing", result.Output)
	require.Equal(t, 750, result.Tokens.InputTokens)
	require.Equal(t, 400, result.Tokens.OutputTokens)
	require.Equal(t, 50, result.Tokens.CacheReadTokens)
}

func TestParseAgentOutput_EmptyAgent(t *testing.T) {
	raw := "some output"
	result := parseAgentOutput("", raw)
	require.Equal(t, raw, result.Output)
	require.Equal(t, 0, result.Tokens.InputTokens)
}

// ---------------------------------------------------------------------------
// parseJSONOutput — additional edge cases beyond activities_test.go
// ---------------------------------------------------------------------------

func TestParseJSONOutput_EmptyResultWithTokens(t *testing.T) {
	// Has tokens but empty result — should fall back to raw
	raw := `{"result":"","usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":0,"cache_creation_input_tokens":0},"cost_usd":0.005}`

	result := parseJSONOutput(raw)
	// The function falls back to raw when result is empty but we have tokens
	require.Equal(t, raw, result.Output)
	require.Equal(t, 100, result.Tokens.InputTokens)
	require.Equal(t, 50, result.Tokens.OutputTokens)
}

func TestParseJSONOutput_EmptyString(t *testing.T) {
	result := parseJSONOutput("")
	require.Equal(t, "", result.Output)
	require.Equal(t, 0, result.Tokens.InputTokens)
}

func TestParseJSONOutput_AllFieldsPopulated(t *testing.T) {
	raw := `{
		"result": "Complete implementation with tests",
		"usage": {
			"input_tokens": 5000,
			"output_tokens": 3000,
			"cache_read_input_tokens": 1500,
			"cache_creation_input_tokens": 800
		},
		"cost_usd": 0.125
	}`

	result := parseJSONOutput(raw)
	require.Equal(t, "Complete implementation with tests", result.Output)
	require.Equal(t, 5000, result.Tokens.InputTokens)
	require.Equal(t, 3000, result.Tokens.OutputTokens)
	require.Equal(t, 1500, result.Tokens.CacheReadTokens)
	require.Equal(t, 800, result.Tokens.CacheCreationTokens)
	require.InDelta(t, 0.125, result.Tokens.CostUSD, 0.0001)
}

func TestParseJSONOutput_MultilineResult(t *testing.T) {
	input := claudeJSONOutput{
		Result: "Line one\nLine two\nLine three",
	}
	input.Usage.InputTokens = 10
	raw, err := json.Marshal(input)
	require.NoError(t, err)

	result := parseJSONOutput(string(raw))
	require.Equal(t, "Line one\nLine two\nLine three", result.Output)
	require.Equal(t, 3, len(strings.Split(result.Output, "\n")))
}

func TestParseJSONOutput_UnicodeResult(t *testing.T) {
	input := claudeJSONOutput{
		Result: "Implementation with unicode: 日本語テスト 🦈",
	}
	input.Usage.InputTokens = 50
	raw, err := json.Marshal(input)
	require.NoError(t, err)

	result := parseJSONOutput(string(raw))
	require.Contains(t, result.Output, "日本語テスト")
	require.Equal(t, 50, result.Tokens.InputTokens)
}

// ---------------------------------------------------------------------------
// CLIResult struct
// ---------------------------------------------------------------------------

func TestCLIResult_ZeroValue(t *testing.T) {
	var r CLIResult
	require.Equal(t, "", r.Output)
	require.Equal(t, 0, r.Tokens.InputTokens)
	require.Equal(t, 0, r.Tokens.OutputTokens)
	require.Equal(t, 0, r.Tokens.CacheReadTokens)
	require.Equal(t, 0, r.Tokens.CacheCreationTokens)
	require.Equal(t, 0.0, r.Tokens.CostUSD)
}
