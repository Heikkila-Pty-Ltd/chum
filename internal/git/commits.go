package git

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Commit represents a git commit with metadata
type Commit struct {
	Hash      string
	Message   string
	Author    string
	Date      time.Time
	BeadIDs   []string // Extracted bead IDs from commit message
}

// GetRecentCommits returns commits from the last N days
func GetRecentCommits(workspace string, days int) ([]Commit, error) {
	since := fmt.Sprintf("--since=%d.days.ago", days)
	cmd := exec.Command("git", "log", since, "--pretty=format:%H|%s|%an|%ai", "--no-merges")
	cmd.Dir = workspace
	
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to get recent commits: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	
	if strings.TrimSpace(string(out)) == "" {
		return []Commit{}, nil
	}
	
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	commits := make([]Commit, 0, len(lines))
	
	for _, line := range lines {
		if line == "" {
			continue
		}
		
		parts := strings.Split(line, "|")
		if len(parts) != 4 {
			continue
		}
		
		// Parse commit date
		date, err := time.Parse("2006-01-02 15:04:05 -0700", parts[3])
		if err != nil {
			// Try alternate format
			date, err = time.Parse("2006-01-02 15:04:05", parts[3])
			if err != nil {
				continue // Skip commits with unparseable dates
			}
		}
		
		commit := Commit{
			Hash:    parts[0],
			Message: parts[1],
			Author:  parts[2],
			Date:    date,
			BeadIDs: ExtractBeadIDs(parts[1]),
		}
		
		commits = append(commits, commit)
	}
	
	return commits, nil
}

// ExtractBeadIDs finds bead ID patterns in commit messages
// Matches patterns like: chum-abc, chum-abc.1, project-def.2, hg-website-123.5, etc.
func ExtractBeadIDs(message string) []string {
	// Pattern matches: word-word-...-word[.digits] (e.g., chum-abc, hg-website-123.5, project-def.2)
	pattern := `\b([a-zA-Z][a-zA-Z0-9]*(?:-[a-zA-Z0-9]+)+(?:\.[0-9]+)?)\b`
	re := regexp.MustCompile(pattern)
	
	matches := re.FindAllStringSubmatch(message, -1)
	beadIDs := make([]string, 0, len(matches))
	seen := make(map[string]bool)
	
	for _, match := range matches {
		if len(match) > 1 {
			beadID := match[1]
			// Filter out obvious false positives
			if !isLikelyBeadID(beadID) {
				continue
			}
			
			if !seen[beadID] {
				beadIDs = append(beadIDs, beadID)
				seen[beadID] = true
			}
		}
	}
	
	return beadIDs
}

// isLikelyBeadID filters out common false positives
func isLikelyBeadID(candidate string) bool {
	candidate = strings.ToLower(candidate)
	
	// Common false positives to exclude
	falsePositives := []string{
		"built-in", "sub-command", "non-zero", "up-to-date",
		"self-contained", "well-known", "user-defined", "real-time",
		"long-term", "short-term", "run-time", "full-time",
		"end-to-end", "one-time", "multi-step", "step-by-step",
		"co-author", "co-authored", "x-ray", "x-axis", "y-axis",
		"utf-8", "base64", "sha-256", "md5",
	}
	
	for _, fp := range falsePositives {
		if candidate == fp {
			return false
		}
	}
	
	// Must be at least 5 characters (e.g., "a-bc")
	if len(candidate) < 5 {
		return false
	}
	
	// Should look like project-identifier pattern (can have multiple dashes)
	parts := strings.Split(candidate, "-")
	if len(parts) < 2 {
		return false
	}
	
	// First part should be at least 2 chars, last part at least 2 chars
	if len(parts[0]) < 2 || len(parts[len(parts)-1]) < 2 {
		return false
	}
	
	// All parts should be non-empty
	for _, part := range parts {
		if len(part) == 0 {
			return false
		}
	}
	
	return true
}

// GetCommitsWithBeadID returns commits that reference a specific bead ID
func GetCommitsWithBeadID(workspace, beadID string, days int) ([]Commit, error) {
	commits, err := GetRecentCommits(workspace, days)
	if err != nil {
		return nil, err
	}
	
	var matchingCommits []Commit
	for _, commit := range commits {
		for _, id := range commit.BeadIDs {
			if id == beadID {
				matchingCommits = append(matchingCommits, commit)
				break
			}
		}
	}
	
	return matchingCommits, nil
}

// GetAllBeadIDsFromCommits extracts all unique bead IDs from recent commits
func GetAllBeadIDsFromCommits(workspace string, days int) ([]string, error) {
	commits, err := GetRecentCommits(workspace, days)
	if err != nil {
		return nil, err
	}
	
	seen := make(map[string]bool)
	var allBeadIDs []string
	
	for _, commit := range commits {
		for _, beadID := range commit.BeadIDs {
			if !seen[beadID] {
				allBeadIDs = append(allBeadIDs, beadID)
				seen[beadID] = true
			}
		}
	}
	
	return allBeadIDs, nil
}