package temporal

import (
	"strings"
)

// ScoreTaskComplexity assigns a 0-100 complexity score to a task based on heuristics.
// Higher score = more complex.
// - 0-30: Simple (direct assign)
// - 31-70: Moderate (Cambrian Explosion if Gen 0)
// - 71-100: Complex (Turtle Ceremony)
func ScoreTaskComplexity(title, prompt, acceptance string, estimateMinutes int) int {
	score := 0

	// 1. Estimate-based scoring (up to 40 points)
	// 2h (120m) = 20 points, 4h (240m) = 40 points.
	score += estimateMinutes / 6
	if score > 40 {
		score = 40
	}

	// 2. Keyword-based scoring (up to 40 points)
	combined := strings.ToLower(title + " " + prompt + " " + acceptance)

	highComplexityKeywords := []string{
		"architect", "design", "refactor", "rewrite", "migration",
		"security", "concurrency", "race condition", "protocol",
		"consensus", "authentication", "authorization", "distributed",
	}

	keywordScore := 0
	for _, kw := range highComplexityKeywords {
		if strings.Contains(combined, kw) {
			keywordScore += 10
		}
	}
	if keywordScore > 40 {
		keywordScore = 40
	}
	score += keywordScore

	// 3. Length-based scoring (up to 20 points)
	// Very long prompts usually imply complex requirements.
	promptLen := len(prompt)
	switch {
	case promptLen > 2000:
		score += 20
	case promptLen > 1000:
		score += 10
	case promptLen > 500:
		score += 5
	}

	// Cap at 100
	if score > 100 {
		score = 100
	}

	return score
}
