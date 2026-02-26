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

	// --- Phase 1: Collect ALL active worktrees across ALL workspaces ---
	// This prevents cross-project deletion: before, processing workspace A
	// would delete workspace B's worktrees because they weren't in A's
	// git worktree list.
	globalActiveWorktrees := make(map[string]bool)
	globalActiveBranches := make(map[string]bool)

	for _, baseDir := range workspaces {
		// Prune dead worktree refs first
		pruneCmd := exec.CommandContext(ctx, "git", "worktree", "prune")
		pruneCmd.Dir = baseDir
		if out, err := pruneCmd.CombinedOutput(); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("prune %s: %v (%s)", baseDir, err, string(out)))
			continue
		}

		listCmd := exec.CommandContext(ctx, "git", "worktree", "list", "--porcelain")
		listCmd.Dir = baseDir
		listOut, err := listCmd.Output()
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("list worktrees %s: %v", baseDir, err))
			continue
		}

		for _, line := range strings.Split(string(listOut), "\n") {
			if strings.HasPrefix(line, "worktree ") {
				dir := strings.TrimPrefix(line, "worktree ")
				globalActiveWorktrees[dir] = true
			}
			if strings.HasPrefix(line, "branch refs/heads/") {
				branch := strings.TrimPrefix(line, "branch refs/heads/")
				globalActiveBranches[branch] = true
			}
		}
	}

	// --- Phase 2: Remove orphaned /tmp/chum-wt-* directories ---
	// Now safe: globalActiveWorktrees contains entries from ALL projects.
	pattern := filepath.Join(os.TempDir(), "chum-wt-*")
	matches, _ := filepath.Glob(pattern)
	for _, dir := range matches {
		if globalActiveWorktrees[dir] {
			continue // Active in some project, skip
		}
		logger.Info(JanitorPrefix+" Removing orphaned worktree dir", "dir", dir)
		if err := os.RemoveAll(dir); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("rmdir %s: %v", dir, err))
		} else {
			result.DirsCleaned++
		}
	}

	// --- Phase 3: Delete stale chum/* branches per workspace ---
	for _, baseDir := range workspaces {
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
			if globalActiveBranches[branch] {
				continue // Branch has an active worktree somewhere, keep it
			}

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

	// Final prune pass
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
