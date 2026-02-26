package temporal

import (
	"testing"
)

func TestScoreTaskComplexity(t *testing.T) {
	tests := []struct {
		name            string
		title           string
		prompt          string
		acceptance      string
		estimateMinutes int
		wantMin         int
		wantMax         int
	}{
		{
			name:            "Simple task",
			title:           "Fix typo",
			prompt:          "Correct spelling in README",
			acceptance:      "README is correct",
			estimateMinutes: 30,
			wantMin:         0,
			wantMax:         20,
		},
		{
			name:            "Moderate task - estimate based",
			title:           "Implement feature",
			prompt:          "Add new button to UI",
			acceptance:      "Button works",
			estimateMinutes: 240, // 40 points
			wantMin:         40,
			wantMax:         50,
		},
		{
			name:            "Complex task - keywords",
			title:           "Architecture redesign",
			prompt:          "Refactor the distributed consensus protocol for security",
			acceptance:      "No race conditions",
			estimateMinutes: 60,
			wantMin:         50, // 10 (est) + 40 (keywords: architecture, refactor, distributed, consensus, security) -> capped at 40 for keywords
			wantMax:         80,
		},
		{
			name:            "Complex task - long prompt",
			title:           "Big requirement",
			prompt:          string(make([]byte, 2500)), // 20 points
			acceptance:      "Done",
			estimateMinutes: 300, // 40 points (capped)
			wantMin:         60,
			wantMax:         70,
		},
		{
			name:            "Maximum complexity",
			title:           "Security rewrite distributed",
			prompt:          "Refactor architecture " + string(make([]byte, 2500)),
			acceptance:      "authorization authentication consensus",
			estimateMinutes: 600,
			wantMin:         100,
			wantMax:         100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ScoreTaskComplexity(tt.title, tt.prompt, tt.acceptance, tt.estimateMinutes)
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("ScoreTaskComplexity() = %v, want between [%v, %v]", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}
