package temporal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.temporal.io/sdk/activity"
)

// JanitorPrefix is the log prefix for janitor activities.
const JanitorPrefix = "\033[33m🧹 JANITOR\033[0m"

// JanitorSweepActivity prunes stale git worktrees and branches across all
// configured projects. A worktree is "stale" if its /tmp/chum-wt-* directory
// still exists but no active Temporal workflow references it.
//
// Pipeline:
//  1. List all /tmp/chum-wt-* directories
//  2. For each project workspace, run `git worktree prune` to clear dead refs
//  3. Delete orphaned chum/* branches that have no matching worktree
func (a *Activities) JanitorSweepActivity(ctx context.Context, workspaces []string) (*JanitorResult, error) {
	logger := activity.GetLogger(ctx)
	result := &JanitorResult{}

	for _, baseDir := range workspaces {
		// Step 1: Prune dead worktree refs from git's internal state.
		pruneCmd := exec.CommandContext(ctx, "git", "worktree", "prune")
		pruneCmd.Dir = baseDir
		if out, err := pruneCmd.CombinedOutput(); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("prune %s: %v (%s)", baseDir, err, string(out)))
			continue
		}

		// Step 2: List active worktrees to know which branches are still in use.
		listCmd := exec.CommandContext(ctx, "git", "worktree", "list", "--porcelain")
		listCmd.Dir = baseDir
		listOut, err := listCmd.Output()
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("list worktrees %s: %v", baseDir, err))
			continue
		}

		activeWorktrees := make(map[string]bool)
		activeBranches := make(map[string]bool)
		for _, line := range strings.Split(string(listOut), "\n") {
			if strings.HasPrefix(line, "worktree ") {
				dir := strings.TrimPrefix(line, "worktree ")
				activeWorktrees[dir] = true
			}
			if strings.HasPrefix(line, "branch refs/heads/") {
				branch := strings.TrimPrefix(line, "branch refs/heads/")
				activeBranches[branch] = true
			}
		}

		// Step 3: Find orphaned /tmp/chum-wt-* directories not in git's worktree list.
		pattern := filepath.Join(os.TempDir(), "chum-wt-*")
		matches, _ := filepath.Glob(pattern)
		for _, dir := range matches {
			if activeWorktrees[dir] {
				continue // Still active, skip
			}
			// Orphaned directory — remove it
			logger.Info(JanitorPrefix+" Removing orphaned worktree dir", "dir", dir)
			if err := os.RemoveAll(dir); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("rmdir %s: %v", dir, err))
			} else {
				result.DirsCleaned++
			}
		}

		// Step 4: Delete stale chum/* branches with no active worktree.
		branchCmd := exec.CommandContext(ctx, "git", "branch", "--list", "chum/*")
		branchCmd.Dir = baseDir
		branchOut, err := branchCmd.Output()
		if err != nil {
			continue
		}

		for _, line := range strings.Split(string(branchOut), "\n") {
			branch := strings.TrimSpace(line)
			branch = strings.TrimPrefix(branch, "* ") // current branch marker
			if branch == "" || !strings.HasPrefix(branch, "chum/") {
				continue
			}
			if activeBranches[branch] {
				continue // Branch has an active worktree, keep it
			}

			// Stale branch — delete locally
			logger.Info(JanitorPrefix+" Pruning stale branch", "branch", branch)
			delCmd := exec.CommandContext(ctx, "git", "branch", "-D", branch)
			delCmd.Dir = baseDir
			if out, err := delCmd.CombinedOutput(); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("delete branch %s: %v (%s)", branch, err, string(out)))
			} else {
				result.BranchesPruned++
			}

			// Also delete the remote branch (best-effort)
			delRemote := exec.CommandContext(ctx, "git", "push", "origin", "--delete", branch)
			delRemote.Dir = baseDir
			_ = delRemote.Run() // best-effort
		}
	}

	// Step 5: Prune worktrees one more time after cleanup.
	for _, baseDir := range workspaces {
		pruneCmd := exec.CommandContext(ctx, "git", "worktree", "prune")
		pruneCmd.Dir = baseDir
		_ = pruneCmd.Run()
	}

	logger.Info(JanitorPrefix+" Sweep complete",
		"dirs_cleaned", result.DirsCleaned,
		"branches_pruned", result.BranchesPruned,
		"errors", len(result.Errors))

	return result, nil
}

// expandHome expands ~ to the user's home directory.
func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}
