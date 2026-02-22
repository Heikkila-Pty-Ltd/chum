package cost

import (
	"testing"
)

func TestExtractTokenUsage(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		prompt      string
		wantInput   int
		wantOutput  int
		description string
	}{
		{
			name:        "OpenClaw format",
			output:      "Some output\nTokens: 1500 input, 2500 output\nDone.",
			prompt:      "Test prompt",
			wantInput:   1500,
			wantOutput:  2500,
			description: "Standard OpenClaw token reporting format",
		},
		{
			name:        "Separate lines format",
			output:      "Input tokens: 1200\nOutput tokens: 800\nComplete.",
			prompt:      "Test prompt",
			wantInput:   1200,
			wantOutput:  800,
			description: "Separate line token reporting",
		},
		{
			name:        "No token info - fallback estimation",
			output:      "This is some output text without token information.",
			prompt:      "This is a test prompt for estimation",
			wantInput:   9,  // ~35 chars / 4 = 8.75 -> 8 tokens, but actual is 9
			wantOutput:  12, // ~49 chars / 4 = 12.25 -> 12 tokens
			description: "Should fallback to length estimation",
		},
		{
			name:        "Empty strings",
			output:      "",
			prompt:      "",
			wantInput:   0,
			wantOutput:  0,
			description: "Empty input should return 0 tokens",
		},
		{
			name:        "Partial token info",
			output:      "Input tokens: 1000\nNo output token info",
			prompt:      "Test",
			wantInput:   1000,
			wantOutput:  9, // Estimated from output length (37 chars / 4 = 9.25 -> 9)
			description: "Should extract input and estimate output",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage := ExtractTokenUsage(tt.output, tt.prompt)

			if usage.Input != tt.wantInput {
				t.Errorf("Input tokens = %d, want %d", usage.Input, tt.wantInput)
			}
			if usage.Output != tt.wantOutput {
				t.Errorf("Output tokens = %d, want %d", usage.Output, tt.wantOutput)
			}
		})
	}
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected int
	}{
		{"Empty string", "", 0},
		{"Single character", "x", 1},
		{"Short text", "hi", 1},
		{"Moderate text", "This is a test", 3},                            // 14 chars / 4 = 3.5 -> 3
		{"Longer text", "This is a longer text with more characters", 10}, // 42 chars / 4 = 10.5 -> 10
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := estimateTokens(tt.text)
			if result != tt.expected {
				t.Errorf("estimateTokens(%q) = %d, want %d", tt.text, result, tt.expected)
			}
		})
	}
}

func TestCalculateCost(t *testing.T) {
	usage := TokenUsage{
		Input:  1500, // 1.5K tokens
		Output: 2500, // 2.5K tokens
	}

	inputPrice := 15.0  // $15 per 1M tokens
	outputPrice := 75.0 // $75 per 1M tokens

	expectedCost := (1500.0/1000000.0)*15.0 + (2500.0/1000000.0)*75.0
	// = 0.0015 * 15 + 0.0025 * 75
	// = 0.0225 + 0.1875
	// = 0.21

	result := CalculateCost(usage, inputPrice, outputPrice)

	if result != expectedCost {
		t.Errorf("CalculateCost() = %.4f, want %.4f", result, expectedCost)
	}

	// Test zero pricing
	zeroCost := CalculateCost(usage, 0, 0)
	if zeroCost != 0 {
		t.Errorf("CalculateCost with zero prices = %.4f, want 0", zeroCost)
	}

	// Test zero usage
	zeroUsage := TokenUsage{Input: 0, Output: 0}
	zeroResult := CalculateCost(zeroUsage, inputPrice, outputPrice)
	if zeroResult != 0 {
		t.Errorf("CalculateCost with zero usage = %.4f, want 0", zeroResult)
	}
}

func TestTokenRegexPatterns(t *testing.T) {
	// Test the regex patterns directly
	tests := []struct {
		text    string
		pattern string
		matches bool
		groups  []string
	}{
		{
			text:    "Tokens: 1500 input, 2500 output",
			pattern: "tokenRe",
			matches: true,
			groups:  []string{"1500", "2500"},
		},
		{
			text:    "Input tokens: 1200",
			pattern: "inputRe",
			matches: true,
			groups:  []string{"1200"},
		},
		{
			text:    "Output tokens: 800",
			pattern: "outputRe",
			matches: true,
			groups:  []string{"800"},
		},
		{
			text:    "No token information here",
			pattern: "tokenRe",
			matches: false,
			groups:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.text[:10], func(t *testing.T) {
			var matches []string
			switch tt.pattern {
			case "tokenRe":
				matches = tokenRe.FindStringSubmatch(tt.text)
			case "inputRe":
				matches = inputRe.FindStringSubmatch(tt.text)
			case "outputRe":
				matches = outputRe.FindStringSubmatch(tt.text)
			}

			if tt.matches {
				if len(matches) == 0 {
					t.Errorf("Expected pattern %s to match %q, but it didn't", tt.pattern, tt.text)
				} else if len(matches) > 1 {
					// Check captured groups (excluding full match at index 0)
					for i, expected := range tt.groups {
						if i+1 >= len(matches) || matches[i+1] != expected {
							t.Errorf("Expected group %d to be %q, got %q", i, expected, matches[i+1])
						}
					}
				}
			} else {
				if len(matches) > 0 {
					t.Errorf("Expected pattern %s NOT to match %q, but it did", tt.pattern, tt.text)
				}
			}
		})
	}
}
