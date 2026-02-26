package git

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type PRStatus struct {
	Number         int    `json:"number"`
	URL            string `json:"url"`
	State          string `json:"state"`
	ReviewDecision string `json:"reviewDecision"`
}

// CreatePR creates a pull request for a feature branch using gh CLI
func CreatePR(workspace, branch, baseBranch, title, body string) (prURL string, prNumber int, err error) {
	cmd := exec.Command("gh", "pr", "create",
		"--head", branch,
		"--base", baseBranch,
		"--title", title,
		"--body", body,
	)
	cmd.Dir = workspace
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", 0, fmt.Errorf("failed to create PR: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	prURL = strings.TrimSpace(string(out))

	// Extract number from URL (https://github.com/org/repo/pull/123)
	parts := strings.Split(prURL, "/")
	if len(parts) > 0 {
		if num, convErr := strconv.Atoi(parts[len(parts)-1]); convErr == nil {
			return prURL, num, nil
		}
	}

	return prURL, 0, nil
}

// GetPRStatus checks if a PR exists and its status using gh CLI
func GetPRStatus(workspace, branch string) (*PRStatus, error) {
	cmd := exec.Command("gh", "pr", "view", branch, "--json", "number,url,state,reviewDecision")
	cmd.Dir = workspace
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "no pull requests found") {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get PR status: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	var status PRStatus
	if err := json.Unmarshal(out, &status); err != nil {
		return nil, fmt.Errorf("failed to unmarshal PR status: %w", err)
	}

	return &status, nil
}
