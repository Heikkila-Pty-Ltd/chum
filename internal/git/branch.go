// Package git provides branch and commit operations for DoD verification.

package git

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

var ErrMergeConflict = errors.New("git merge conflict")

// CreateFeatureBranch creates and checks out a branch for a morsel
// Branch name: feat/{morsel-id} (e.g. feat/chum-abc)
func CreateFeatureBranch(workspace, morselID, baseBranch string) error {
	branchName := fmt.Sprintf("feat/%s", morselID)

	// Create and checkout the new branch from the base branch
	cmd := exec.Command("git", "checkout", "-b", branchName, baseBranch)
	cmd.Dir = workspace
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to create branch %s from %s: %w (%s)", branchName, baseBranch, err, strings.TrimSpace(string(out)))
	}

	return nil
}

// GetCurrentBranch returns the current branch name
func GetCurrentBranch(workspace string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = workspace
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get current branch: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	return strings.TrimSpace(string(out)), nil
}

// BranchExists checks if a branch already exists
func BranchExists(workspace, branch string) (bool, error) {
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", fmt.Sprintf("refs/heads/%s", branch))
	cmd.Dir = workspace
	err := cmd.Run()
	if err != nil {
		// Exit code 1 means branch doesn't exist, other errors are real failures
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("failed to check if branch %s exists: %w", branch, err)
	}

	return true, nil
}

// EnsureFeatureBranch creates branch if not exists, checks out if exists
func EnsureFeatureBranch(workspace, morselID string) error {
	branchName := fmt.Sprintf("feat/%s", morselID)

	// Check if the branch already exists
	exists, err := BranchExists(workspace, branchName)
	if err != nil {
		return fmt.Errorf("failed to check if branch exists: %w", err)
	}

	if exists {
		// Branch exists, just check it out
		cmd := exec.Command("git", "checkout", branchName)
		cmd.Dir = workspace
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to checkout existing branch %s: %w (%s)", branchName, err, strings.TrimSpace(string(out)))
		}
	} else {
		// Branch doesn't exist, create it from main
		// First, make sure we're up to date with the base branch
		cmd := exec.Command("git", "fetch", "origin")
		cmd.Dir = workspace
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to fetch from origin: %w (%s)", err, strings.TrimSpace(string(out)))
		}

		// Create the new branch from origin/main (assuming main is the base)
		if err := CreateFeatureBranch(workspace, morselID, "origin/main"); err != nil {
			return err
		}
	}

	return nil
}

// EnsureFeatureBranchWithBase creates branch if not exists, checks out if exists, with custom base branch
func EnsureFeatureBranchWithBase(workspace, morselID, baseBranch, branchPrefix string) error {
	branchName := fmt.Sprintf("%s%s", branchPrefix, morselID)

	// Check if the branch already exists
	exists, err := BranchExists(workspace, branchName)
	if err != nil {
		return fmt.Errorf("failed to check if branch exists: %w", err)
	}

	if exists {
		// Branch exists, just check it out
		cmd := exec.Command("git", "checkout", branchName)
		cmd.Dir = workspace
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to checkout existing branch %s: %w (%s)", branchName, err, strings.TrimSpace(string(out)))
		}
	} else {
		// Branch doesn't exist, create it from the specified base branch
		// Try to fetch from origin (optional - ignore if no remote)
		cmd := exec.Command("git", "fetch", "origin")
		cmd.Dir = workspace
		cmd.CombinedOutput() // Ignore errors - remote may not exist

		// Try to create from remote branch first, fall back to local
		remoteBranch := fmt.Sprintf("origin/%s", baseBranch)
		cmd = exec.Command("git", "checkout", "-b", branchName, remoteBranch)
		cmd.Dir = workspace
		if err := cmd.Run(); err != nil {
			// If remote branch doesn't exist, try local branch
			cmd = exec.Command("git", "checkout", "-b", branchName, baseBranch)
			cmd.Dir = workspace
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("failed to create branch %s from %s: %w (%s)", branchName, baseBranch, err, strings.TrimSpace(string(out)))
			}
		}
	}

	return nil
}

// MergeBranchIntoBase checks out the base branch and merges the feature branch.
// If merge conflicts occur, ErrMergeConflict is returned.
func MergeBranchIntoBase(workspace, featureBranch, baseBranch, mergeStrategy string) error {
	baseBranch = strings.TrimSpace(baseBranch)
	if baseBranch == "" {
		baseBranch = "main"
	}

	if _, err := runGitCommand(workspace, "checkout", baseBranch); err != nil {
		return fmt.Errorf("failed to checkout base branch %s: %w", baseBranch, err)
	}

	strategy := strings.ToLower(strings.TrimSpace(mergeStrategy))
	if strategy == "" {
		strategy = "merge"
	}

	mergeOrRebaseConflict := func(op string, err error) error {
		text := strings.TrimSpace(err.Error())
		if isMergeConflictText(text) {
			if strategy != "rebase" {
				abortMergeInProgress(workspace)
				_, _ = runGitCommand(workspace, "checkout", baseBranch)
			}
			return fmt.Errorf("%w: %s", ErrMergeConflict, text)
		}
		return fmt.Errorf("failed to %s branch %s into %s: %w", op, featureBranch, baseBranch, err)
	}

	var (
		output string
		err    error
	)
	switch strategy {
	case "merge":
		output, err = runGitCommand(workspace, "merge", "--no-ff", "--no-edit", featureBranch)
	case "squash":
		output, err = runGitCommand(workspace, "merge", "--squash", featureBranch)
	case "rebase":
		if _, checkoutErr := runGitCommand(workspace, "checkout", featureBranch); checkoutErr != nil {
			return fmt.Errorf("failed to checkout feature branch %s for rebase: %w", featureBranch, checkoutErr)
		}
		if _, rebaseErr := runGitCommand(workspace, "rebase", baseBranch); rebaseErr != nil {
			if isMergeConflictText(rebaseErr.Error()) {
				_, _ = runGitCommand(workspace, "rebase", "--abort")
				_, _ = runGitCommand(workspace, "checkout", baseBranch)
				return fmt.Errorf("%w: %s", ErrMergeConflict, strings.TrimSpace(rebaseErr.Error()))
			}
			_, _ = runGitCommand(workspace, "checkout", baseBranch)
			return fmt.Errorf("failed to rebase branch %s onto %s: %w", featureBranch, baseBranch, rebaseErr)
		}
		if _, checkoutErr := runGitCommand(workspace, "checkout", baseBranch); checkoutErr != nil {
			return fmt.Errorf("failed to checkout base branch %s after rebase: %w", baseBranch, checkoutErr)
		}
		output, err = runGitCommand(workspace, "merge", "--ff-only", featureBranch)
	default:
		return fmt.Errorf("unsupported merge strategy %q", mergeStrategy)
	}
	if err != nil {
		if output != "" {
			err = fmt.Errorf("%w (%s)", err, strings.TrimSpace(output))
		}
		op := "merge"
		if strategy == "rebase" {
			op = "fast-forward merge rebased"
		}
		return mergeOrRebaseConflict(op, err)
	}

	if strategy == "squash" {
		commitMsg := fmt.Sprintf("squash merge %s", featureBranch)
		if _, err := runGitCommand(workspace, "commit", "-m", commitMsg); err != nil {
			return fmt.Errorf("failed to commit squash merge for %s: %w", featureBranch, err)
		}
	}

	return nil
}

// DeleteBranch deletes a local branch after successful merge.
func DeleteBranch(workspace, branch string) error {
	if _, err := runGitCommand(workspace, "branch", "-d", branch); err != nil {
		return fmt.Errorf("failed to delete branch %s: %w", branch, err)
	}
	return nil
}

func isMergeConflictText(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	conflictMarkers := []string{
		"conflict",
		"merge conflict",
		"automatic merge failed",
		"could not apply",
		"resolve all conflicts manually",
	}
	for _, marker := range conflictMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func runGitCommand(workspace string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = workspace
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text == "" {
			return "", err
		}
		return text, fmt.Errorf("%w (%s)", err, text)
	}
	return text, nil
}

func abortMergeInProgress(workspace string) {
	if _, err := runGitCommand(workspace, "merge", "--abort"); err != nil {
		_, _ = runGitCommand(workspace, "reset", "--merge")
	}
}
