package temporal

import (
	"encoding/json"
	"strings"
)

// CLIResult wraps the text output of a CLI command together with token usage
// extracted from claude's --output-format json. For non-JSON agents (codex),
// Tokens is zero-valued.
type CLIResult struct {
	Output string
	Tokens TokenUsage
}

// claudeJSONOutput matches the JSON structure from `claude --print --output-format json`.
type claudeJSONOutput struct {
	Result string `json:"result"`
	Usage  struct {
		InputTokens         int `json:"input_tokens"`
		OutputTokens        int `json:"output_tokens"`
		CacheReadTokens     int `json:"cache_read_input_tokens"`
		CacheCreationTokens int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
	CostUSD float64 `json:"cost_usd"`
}

// parseJSONOutput extracts text result and token usage from claude's JSON output.
// If the output is not valid JSON or doesn't have a result field, it falls back
// to returning the raw output with zero tokens (graceful degradation for codex).
func parseJSONOutput(raw string) CLIResult {
	var parsed claudeJSONOutput
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return CLIResult{Output: raw}
	}
	// If the JSON parsed but has no result field, it's probably not claude output
	if parsed.Result == "" && parsed.Usage.InputTokens == 0 {
		return CLIResult{Output: raw}
	}
	output := parsed.Result
	if output == "" {
		output = raw // fallback: keep original if result is empty but we got tokens
	}
	return CLIResult{
		Output: output,
		Tokens: TokenUsage{
			InputTokens:         parsed.Usage.InputTokens,
			OutputTokens:        parsed.Usage.OutputTokens,
			CacheReadTokens:     parsed.Usage.CacheReadTokens,
			CacheCreationTokens: parsed.Usage.CacheCreationTokens,
			CostUSD:             parsed.CostUSD,
		},
	}
}

// parseAgentOutput routes output parsing based on agent type.
// Claude: single JSON object with tokens. Codex: JSONL events. Gemini: JSON with stats.
func parseAgentOutput(agent, raw string) CLIResult {
	switch strings.ToLower(agent) {
	case "claude":
		return parseJSONOutput(raw)
	case "codex":
		return parseCodexOutput(raw)
	case "gemini":
		return parseGeminiOutput(raw)
	case "deepseek":
		return parseDeepSeekOutput(raw)
	default:
		return CLIResult{Output: raw}
	}
}

// parseCodexOutput extracts token usage from codex JSONL output.
// Codex --json emits JSONL events; the last turn.completed has usage data.
func parseCodexOutput(raw string) CLIResult {
	result := CLIResult{Output: raw}

	// Extract text from agent_message items for readable output
	var textParts []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event struct {
			Type string `json:"type"`
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
			Usage struct {
				InputTokens       int64 `json:"input_tokens"`
				CachedInputTokens int64 `json:"cached_input_tokens"`
				OutputTokens      int64 `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if event.Item.Type == "agent_message" && event.Item.Text != "" {
			textParts = append(textParts, event.Item.Text)
		}
		if event.Type == "turn.completed" {
			result.Tokens.InputTokens = int(event.Usage.InputTokens)
			result.Tokens.OutputTokens = int(event.Usage.OutputTokens)
			result.Tokens.CacheReadTokens = int(event.Usage.CachedInputTokens)
		}
	}
	if len(textParts) > 0 {
		result.Output = strings.Join(textParts, "\n")
	}
	return result
}

// parseGeminiOutput extracts token usage from gemini JSON output.
// Gemini -o json emits a single JSON object with stats.models.*.tokens
// and the model's response in the "response" field (as an escaped string).
func parseGeminiOutput(raw string) CLIResult {
	result := CLIResult{Output: raw}

	var geminiOut struct {
		Response string `json:"response"`
		Stats    struct {
			Models map[string]struct {
				Tokens struct {
					Input      int64 `json:"input"`
					Candidates int64 `json:"candidates"`
					Cached     int64 `json:"cached"`
					Total      int64 `json:"total"`
					Thoughts   int64 `json:"thoughts"`
				} `json:"tokens"`
			} `json:"models"`
		} `json:"stats"`
	}

	if err := json.Unmarshal([]byte(raw), &geminiOut); err != nil {
		return result
	}

	// Extract the actual model response — this is where the useful content lives.
	// Without this, downstream extractJSON finds the outer envelope instead of
	// the model's actual JSON output.
	if geminiOut.Response != "" {
		result.Output = geminiOut.Response
	}

	// Sum across all models (gemini may use multiple models per session)
	var totalInput, totalOutput, totalCached int64
	for _, model := range geminiOut.Stats.Models {
		totalInput += model.Tokens.Input
		totalOutput += model.Tokens.Candidates + model.Tokens.Thoughts
		totalCached += model.Tokens.Cached
	}

	result.Tokens.InputTokens = int(totalInput)
	result.Tokens.OutputTokens = int(totalOutput)
	result.Tokens.CacheReadTokens = int(totalCached)
	return result
}

// parseDeepSeekOutput extracts result text and token usage from the
// deepseek-cli wrapper's JSON output (OpenRouter chat completions format).
func parseDeepSeekOutput(raw string) CLIResult {
	result := CLIResult{Output: raw}

	var dsOut struct {
		Result string `json:"result"`
		Usage  struct {
			PromptTokens     int     `json:"prompt_tokens"`
			CompletionTokens int     `json:"completion_tokens"`
			Cost             float64 `json:"cost"`
		} `json:"usage"`
		Error string `json:"error"`
	}

	if err := json.Unmarshal([]byte(raw), &dsOut); err != nil {
		return result
	}
	if dsOut.Error != "" {
		result.Output = "deepseek error: " + dsOut.Error
		return result
	}
	if dsOut.Result != "" {
		result.Output = dsOut.Result
	}
	result.Tokens.InputTokens = dsOut.Usage.PromptTokens
	result.Tokens.OutputTokens = dsOut.Usage.CompletionTokens
	result.Tokens.CostUSD = dsOut.Usage.Cost
	return result
}
