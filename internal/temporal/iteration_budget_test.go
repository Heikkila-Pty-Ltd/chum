package temporal

import (
	"strings"
	"testing"
)

func TestIterationBudgetPrompt(t *testing.T) {
	t.Run("default budget", func(t *testing.T) {
		result := iterationBudgetPrompt(0)
		if !strings.Contains(result, "50 tool-call iterations") {
			t.Errorf("expected default 50 iterations, got: %s", result)
		}
		if !strings.Contains(result, "At iteration 48") {
			t.Errorf("expected wrap-up at 48, got: %s", result)
		}
	})

	t.Run("custom budget", func(t *testing.T) {
		result := iterationBudgetPrompt(30)
		if !strings.Contains(result, "30 tool-call iterations") {
			t.Errorf("expected 30 iterations, got: %s", result)
		}
		if !strings.Contains(result, "At iteration 28") {
			t.Errorf("expected wrap-up at 28, got: %s", result)
		}
	})

	t.Run("very small budget", func(t *testing.T) {
		result := iterationBudgetPrompt(2)
		if !strings.Contains(result, "2 tool-call iterations") {
			t.Errorf("expected 2 iterations, got: %s", result)
		}
		// 2 - 2 = 0, but clamped to 1
		if !strings.Contains(result, "At iteration 1") {
			t.Errorf("expected wrap-up at 1, got: %s", result)
		}
	})

	t.Run("contains wrap-up format", func(t *testing.T) {
		result := iterationBudgetPrompt(50)
		if !strings.Contains(result, "--- WRAP-UP ---") {
			t.Error("expected wrap-up section marker")
		}
		if !strings.Contains(result, "COMPLETED:") {
			t.Error("expected COMPLETED section")
		}
		if !strings.Contains(result, "REMAINING:") {
			t.Error("expected REMAINING section")
		}
		if !strings.Contains(result, "FILES MODIFIED:") {
			t.Error("expected FILES MODIFIED section")
		}
	})
}

func TestGetMaxAgentIterations(t *testing.T) {
	t.Run("nil config returns default", func(t *testing.T) {
		a := &Activities{}
		result := getMaxAgentIterations(a)
		if result != DefaultMaxAgentIterations {
			t.Errorf("expected %d, got %d", DefaultMaxAgentIterations, result)
		}
	})
}
