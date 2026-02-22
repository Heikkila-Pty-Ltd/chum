package git

import (
	"reflect"
	"testing"
	"time"
)

func TestExtractMorselIDs(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		expected []string
	}{
		{
			name:     "simple morsel ID",
			message:  "fix(chum-abc): implement new feature",
			expected: []string{"chum-abc"},
		},
		{
			name:     "morsel ID with number suffix",
			message:  "feat(chum-abc.1): add tests for feature",
			expected: []string{"chum-abc.1"},
		},
		{
			name:     "multiple morsel IDs",
			message:  "fix chum-abc and chum-def.2 issues",
			expected: []string{"chum-abc", "chum-def.2"},
		},
		{
			name:     "morsel ID in middle of message",
			message:  "Updated implementation for chum-xyz according to requirements",
			expected: []string{"chum-xyz"},
		},
		{
			name:     "no morsel IDs",
			message:  "general refactoring and cleanup",
			expected: []string{},
		},
		{
			name:     "false positives filtered out",
			message:  "built-in function and non-zero values with utf-8 encoding",
			expected: []string{},
		},
		{
			name:     "edge case short IDs filtered",
			message:  "fix a-b issue",
			expected: []string{},
		},
		{
			name:     "project with numbers",
			message:  "implement hg-website-123.5 feature",
			expected: []string{"hg-website-123.5"},
		},
		{
			name:     "conventional commit format",
			message:  "feat(project-abc): closes project-abc with implementation",
			expected: []string{"project-abc"},
		},
		{
			name:     "duplicate morsel IDs deduplicated",
			message:  "fix chum-xyz issue and update chum-xyz tests for chum-xyz",
			expected: []string{"chum-xyz"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractMorselIDs(tt.message)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("ExtractMorselIDs() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestIsLikelyMorselID(t *testing.T) {
	tests := []struct {
		name      string
		candidate string
		expected  bool
	}{
		{
			name:      "valid morsel ID",
			candidate: "chum-abc",
			expected:  true,
		},
		{
			name:      "valid morsel ID with numbers",
			candidate: "project-123",
			expected:  true,
		},
		{
			name:      "valid morsel ID with suffix",
			candidate: "chum-abc.1",
			expected:  true,
		},
		{
			name:      "too short",
			candidate: "a-b",
			expected:  false,
		},
		{
			name:      "false positive - built-in",
			candidate: "built-in",
			expected:  false,
		},
		{
			name:      "false positive - utf-8",
			candidate: "utf-8",
			expected:  false,
		},
		{
			name:      "false positive - non-zero",
			candidate: "non-zero",
			expected:  false,
		},
		{
			name:      "no dash",
			candidate: "chum",
			expected:  false,
		},
		{
			name:      "first part too short",
			candidate: "a-chum",
			expected:  false,
		},
		{
			name:      "second part too short",
			candidate: "chum-a",
			expected:  false,
		},
		{
			name:      "case insensitive false positive",
			candidate: "BUILT-IN",
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isLikelyMorselID(tt.candidate)
			if result != tt.expected {
				t.Errorf("isLikelyMorselID(%q) = %v, expected %v", tt.candidate, result, tt.expected)
			}
		})
	}
}

func TestCommit_MorselIDs(t *testing.T) {
	// Test that Commit struct properly extracts morsel IDs
	commit := Commit{
		Hash:    "abc123",
		Message: "feat(chum-xyz): implement feature for chum-abc.1",
		Author:  "test@example.com",
		Date:    time.Now(),
	}
	
	commit.MorselIDs = ExtractMorselIDs(commit.Message)
	
	expected := []string{"chum-xyz", "chum-abc.1"}
	if !reflect.DeepEqual(commit.MorselIDs, expected) {
		t.Errorf("Commit.MorselIDs = %v, expected %v", commit.MorselIDs, expected)
	}
}

// Mock test for commit parsing - would need git repo setup for real testing
func TestParseCommitLine(t *testing.T) {
	// This would be used to test the commit parsing logic
	// For real testing, we'd need to set up a git repo with test commits
	parts := []string{"abc123def456", "feat(chum-xyz): implement feature", "John Doe", "2024-01-15 10:30:00 -0500"}
	
	if len(parts) != 4 {
		t.Errorf("Expected 4 parts in commit line, got %d", len(parts))
	}
	
	morselIDs := ExtractMorselIDs(parts[1])
	expected := []string{"chum-xyz"}
	if !reflect.DeepEqual(morselIDs, expected) {
		t.Errorf("MorselIDs = %v, expected %v", morselIDs, expected)
	}
}