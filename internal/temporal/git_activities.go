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

// SetupWorktreeActivity creates an isolated git worktree for this shark organism.
// Each organism gets its own workspace so concurrent sharks don't compete for
// build locks, .next/ directories, or other stateful artifacts.
// Returns the absolute path to the worktree directory.
func (a *Activities) SetupWorktreeActivity(ctx context.Context, baseDir, taskID, explosionID string) (string, error) {
	logger := activity.GetLogger(ctx)

	// Worktree path: $TMPDIR/chum-wt-{taskID}[-{explosionID}] (unique per organism)
	wtDir := WorktreeDir(taskID, "")
	branch := fmt.Sprintf("chum/%s", taskID)
	if explosionID != "" {
		wtDir = WorktreeDir(taskID, explosionID)
		branch = fmt.Sprintf("chum/%s-%s", taskID, explosionID)
	}

	logger.Info(SharkPrefix+" Setting up worktree", "base", baseDir, "worktree", wtDir, "branch", branch)

	// Remove stale worktree if exists (from a dead organism)
	rmCmd := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", wtDir)
	rmCmd.Dir = baseDir
	if err := rmCmd.Run(); err != nil {
		logger.Debug(SharkPrefix+" No stale worktree to remove", "worktree", wtDir, "error", err)
	}

	// Delete stale branch if exists
	delBranch := exec.CommandContext(ctx, "git", "branch", "-D", branch)
	delBranch.Dir = baseDir
	if err := delBranch.Run(); err != nil {
		logger.Debug(SharkPrefix+" No stale branch to delete", "branch", branch, "error", err)
	}

	// Create fresh worktree with a new branch from HEAD
	addCmd := exec.CommandContext(ctx, "git", "worktree", "add", "-b", branch, wtDir)
	addCmd.Dir = baseDir
	if out, err := addCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git worktree add failed: %w\n%s", err, string(out))
	}

	// If the base project has node_modules but the worktree doesn't, symlink them.
	// This avoids expensive `npm install` per organism.
	nmSrc := baseDir + "/node_modules"
	nmDst := wtDir + "/node_modules"
	if _, err := exec.LookPath("node"); err == nil {
		// Check if package.json exists and node_modules doesn't in worktree
		pkgCmd := exec.CommandContext(ctx, "test", "-f", wtDir+"/package.json")
		nmCheck := exec.CommandContext(ctx, "test", "-d", nmDst)
		if pkgCmd.Run() == nil && nmCheck.Run() != nil {
			// Copy node_modules instead of symlinking to avoid Next.js Turbopack symlink-out-of-root errors
			cpCmd := exec.CommandContext(ctx, "cp", "-a", nmSrc, nmDst)
			if cpCmd.Run() != nil {
				// Symlink failed, install fresh
				installCmd := exec.CommandContext(ctx, "npm", "install", "--prefer-offline")
				installCmd.Dir = wtDir
				if err := installCmd.Run(); err != nil {
					logger.Warn(SharkPrefix+" npm install failed", "worktree", wtDir, "error", err)
				}
			}
		}
	}

	// Copy .env* files from base repo — git worktrees don't include .gitignore'd files.
	// Without .env.local, Next.js/Supabase builds fail because NEXT_PUBLIC_* vars are missing at build time.
	envGlob, _ := filepath.Glob(filepath.Join(baseDir, ".env*"))
	for _, envFile := range envGlob {
		baseName := filepath.Base(envFile)
		dst := filepath.Join(wtDir, baseName)
		// Only copy if not already present (don't overwrite if the branch has its own)
		if _, err := os.Stat(dst); err != nil {
			src, readErr := os.ReadFile(envFile)
			if readErr == nil {
				if writeErr := os.WriteFile(dst, src, 0600); writeErr == nil {
					logger.Info(SharkPrefix+" Copied env file to worktree", "file", baseName)
				}
			}
		}
	}

	logger.Info(SharkPrefix+" Worktree ready", "path", wtDir)
	return wtDir, nil
}

// PushWorktreeActivity pushes the organism's code branch to the remote origin.
// Returns nil (skip) if no origin remote is configured — this is expected in
// local-only worktrees and must not trigger Temporal retries.
func (a *Activities) PushWorktreeActivity(ctx context.Context, wtDir string) error {
	logger := activity.GetLogger(ctx)
	logger.Info(SharkPrefix+" Pushing worktree branch to remote", "worktree", wtDir)

	// Check if origin remote exists before attempting push.
	// Missing remote is expected in local-only setups — skip push
	// without triggering Temporal retry.
	if !hasGitRemote(ctx, wtDir, "origin") {
		logger.Warn(SharkPrefix+" No origin remote configured, skipping push", "worktree", wtDir)
		return nil
	}

	pushCmd := exec.CommandContext(ctx, "git", "push", "origin", "HEAD")
	pushCmd.Dir = wtDir
	if out, err := pushCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git push origin HEAD failed: %w\n%s", err, string(out))
	}

	logger.Info(SharkPrefix + " Worktree branch pushed successfully")
	return nil
}

// hasGitRemote checks whether a named remote exists in the given git directory.
func hasGitRemote(ctx context.Context, dir, remote string) bool {
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", remote)
	cmd.Dir = dir
	return cmd.Run() == nil
}

// MergeToMainActivity creates a PR from the feature branch instead of merging
// directly. Code reaches master only through reviewed PRs.
// Uses `gh pr create` when available; falls back to logging the branch name.
// Returns the PR number (0 if unknown or failed) and an error.
func (a *Activities) MergeToMainActivity(ctx context.Context, baseDir, featureBranch, taskSummary string) (int, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(SharkPrefix+" Creating PR for feature branch",
		"baseDir", baseDir, "featureBranch", featureBranch)

	// Check if gh CLI is available (best-effort: skip PR creation if missing).
	ghAvailable := true
	if _, e := exec.LookPath("gh"); e != nil {
		ghAvailable = false
	}
	if !ghAvailable {
		logger.Warn(SharkPrefix+" gh CLI not found — branch pushed but no PR created. Merge manually.",
			"featureBranch", featureBranch)
		return 0, nil
	}

	// Create PR using gh CLI. The branch was already pushed by PushWorktreeActivity.
	title := fmt.Sprintf("[CHUM] %s", truncate(taskSummary, 120))
	body := fmt.Sprintf("Automated PR created by CHUM shark.\n\n**Branch:** `%s`\n**Summary:** %s",
		featureBranch, taskSummary)

	prCmd := exec.CommandContext(ctx, "gh", "pr", "create",
		"--base", "master",
		"--head", featureBranch,
		"--title", title,
		"--body", body,
		"--label", "chum",
	)
	prCmd.Dir = baseDir
	out, err := prCmd.CombinedOutput()
	if err != nil {
		outStr := string(out)
		// If a PR already exists, that's fine — not an error.
		if strings.Contains(outStr, "already exists") {
			logger.Info(SharkPrefix+" PR already exists for branch", "featureBranch", featureBranch)
			return 0, nil
		}
		// If the label doesn't exist, retry without it.
		if strings.Contains(outStr, "label") {
			prCmd2 := exec.CommandContext(ctx, "gh", "pr", "create",
				"--base", "master",
				"--head", featureBranch,
				"--title", title,
				"--body", body,
			)
			prCmd2.Dir = baseDir
			out2, err2 := prCmd2.CombinedOutput()
			if err2 != nil {
				logger.Warn(SharkPrefix+" gh pr create failed (branch pushed, merge manually)",
					"error", err2, "output", string(out2), "featureBranch", featureBranch)
				return 0, nil // non-fatal: branch is pushed, human can merge
			}
			logger.Info(SharkPrefix+" PR created (without label)", "output", strings.TrimSpace(string(out2)))
			return parsePRNumberFromURL(strings.TrimSpace(string(out2))), nil
		}
		logger.Warn(SharkPrefix+" gh pr create failed (branch pushed, merge manually)",
			"error", err, "output", outStr, "featureBranch", featureBranch)
		return 0, nil // non-fatal: branch is pushed, human can merge
	}

	prURL := strings.TrimSpace(string(out))
	prNumber := parsePRNumberFromURL(prURL)
	logger.Info(SharkPrefix+" PR created successfully",
		"featureBranch", featureBranch,
		"prNumber", prNumber,
		"output", prURL)
	return prNumber, nil
}

// parsePRNumberFromURL extracts the PR number from a GitHub PR URL
// like "https://github.com/org/repo/pull/123".
func parsePRNumberFromURL(prURL string) int {
	parts := strings.Split(prURL, "/")
	if len(parts) > 0 {
		var num int
		if _, err := fmt.Sscanf(parts[len(parts)-1], "%d", &num); err == nil {
			return num
		}
	}
	return 0
}

// GetWorktreeDiffActivity returns the git diff of a worktree against its base branch.
// Used by the explosion workflow to get diffs for senior reviewer comparison.
func (a *Activities) GetWorktreeDiffActivity(ctx context.Context, wtDir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "main", "--stat", "--patch")
	cmd.Dir = wtDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git diff in %s failed: %w\n%s", wtDir, err, string(out))
	}
	diff := string(out)
	if len(diff) > 8000 {
		diff = diff[:8000] + "\n... [truncated]"
	}
	return diff, nil
}

// CleanupWorktreeActivity removes the git worktree after the organism completes.
// Called at both success and failure paths — organisms are mortal.
func (a *Activities) CleanupWorktreeActivity(ctx context.Context, baseDir, wtDir string) error {
	logger := activity.GetLogger(ctx)
	logger.Info(SharkPrefix+" Cleaning up worktree", "worktree", wtDir)

	// Detect the branch name before removal so we can cleanup remote.
	branchCmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	branchCmd.Dir = wtDir
	branchOut, _ := branchCmd.Output()
	branchName := strings.TrimSpace(string(branchOut))

	// Remove the worktree
	rmCmd := exec.CommandContext(ctx, "git", "worktree", "remove", "--force", wtDir)
	rmCmd.Dir = baseDir
	if out, err := rmCmd.CombinedOutput(); err != nil {
		logger.Warn(SharkPrefix+" Worktree removal failed (best-effort)", "error", err, "output", string(out))
	}

	// Prune stale worktree entries
	pruneCmd := exec.CommandContext(ctx, "git", "worktree", "prune")
	pruneCmd.Dir = baseDir
	if err := pruneCmd.Run(); err != nil {
		logger.Warn(SharkPrefix+" git worktree prune failed", "error", err)
	}

	// Delete the remote branch to prevent stale chum/* branches accumulating.
	if branchName != "" && branchName != "HEAD" && strings.HasPrefix(branchName, "chum/") {
		delRemote := exec.CommandContext(ctx, "git", "push", "origin", "--delete", branchName)
		delRemote.Dir = baseDir
		if err := delRemote.Run(); err != nil {
			logger.Debug(SharkPrefix+" Failed to delete remote branch (best-effort)", "branch", branchName, "error", err)
		}

		delLocal := exec.CommandContext(ctx, "git", "branch", "-D", branchName)
		delLocal.Dir = baseDir
		if err := delLocal.Run(); err != nil {
			logger.Debug(SharkPrefix+" Failed to delete local branch (best-effort)", "branch", branchName, "error", err)
		}
	}

	return nil
}

// ResetWorkspaceActivity hard resets the codebase and cleans untracked files
// to give a backup agent a fresh slate when taking over a failed execution.
func (a *Activities) ResetWorkspaceActivity(ctx context.Context, workDir string) error {
	logger := activity.GetLogger(ctx)
	logger.Info(SharkPrefix+" Resetting workspace for fresh handoff", "WorkDir", workDir)

	cmd := exec.CommandContext(ctx, "git", "reset", "--hard", "HEAD")
	cmd.Dir = workDir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git reset: %w", err)
	}

	cleanCmd := exec.CommandContext(ctx, "git", "clean", "-fd")
	cleanCmd.Dir = workDir
	if err := cleanCmd.Run(); err != nil {
		return fmt.Errorf("git clean: %w", err)
	}

	return nil
}
