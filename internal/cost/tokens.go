// Package tokens provides utilities for counting LLM tokens.

package cost

import (
	"regexp"
	"strconv"
)

// TokenUsage represents input and output token counts.
type TokenUsage struct {
	Input  int
	Output int
}

var (
	// OpenClaw often reports tokens in this format at the end of output
	tokenRe = regexp.MustCompile(`Tokens: (\d+) input, (\d+) output`)
	// Alternatively, some models report separately
	inputRe  = regexp.MustCompile(`Input tokens: (\d+)`)
	outputRe = regexp.MustCompile(`Output tokens: (\d+)`)
)

// ExtractTokenUsage attempts to parse token counts from agent output.
// Fallback: estimate from prompt and output length if parsing fails.
func ExtractTokenUsage(output, prompt string) TokenUsage {
	usage := TokenUsage{}

	// Try parsing from output
	if m := tokenRe.FindStringSubmatch(output); len(m) == 3 {
		if parsed, err := strconv.Atoi(m[1]); err == nil {
			usage.Input = parsed
		}
		if parsed, err := strconv.Atoi(m[2]); err == nil {
			usage.Output = parsed
		}
	} else {
		if m := inputRe.FindStringSubmatch(output); len(m) == 2 {
			if parsed, err := strconv.Atoi(m[1]); err == nil {
				usage.Input = parsed
			}
		}
		if m := outputRe.FindStringSubmatch(output); len(m) == 2 {
			if parsed, err := strconv.Atoi(m[1]); err == nil {
				usage.Output = parsed
			}
		}
	}

	// Fallback estimation if still 0
	if usage.Input == 0 {
		usage.Input = estimateTokens(prompt)
	}
	if usage.Output == 0 {
		usage.Output = estimateTokens(output)
	}

	return usage
}

// estimateTokens provides a rough estimate of token count (approx 4 chars per token).
func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	// Rough heuristic for English/Code: 1 token per 4 characters
	tokens := len(text) / 4
	if tokens == 0 && text != "" {
		return 1
	}
	return tokens
}

// CalculateCost calculates total cost in USD based on token counts and pricing per million tokens.
func CalculateCost(usage TokenUsage, inputPriceMtok, outputPriceMtok float64) float64 {
	inputCost := (float64(usage.Input) / 1000000.0) * inputPriceMtok
	outputCost := (float64(usage.Output) / 1000000.0) * outputPriceMtok
	return inputCost + outputCost
}
