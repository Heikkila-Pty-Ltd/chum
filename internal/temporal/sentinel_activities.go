package temporal

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"go.temporal.io/sdk/activity"
)

// SentinelScanRequest carries the parameters for post-execute scope drift detection.
type SentinelScanRequest struct {
	WorktreePath  string   `json:"worktree_path"`
	ExpectedFiles []string `json:"expected_files"` // plan.FilesToModify
	MorselID      string   `json:"morsel_id"`
	Project       string   `json:"project"`
	Attempt       int      `json:"attempt"`
}

// SentinelResult reports which files were changed outside the expected scope.
type SentinelResult struct {
	OutOfScopeFiles []string `json:"out_of_scope_files,omitempty"` // files changed but not in the plan
	RevertedFiles   []string `json:"reverted_files,omitempty"`     // files reverted to main
	BuildBroken     bool     `json:"build_broken"`                 // true if out-of-scope changes broke go build
	Passed          bool     `json:"passed"`                       // true if no critical drift
}

// SentinelScanActivity detects execution drift by comparing the worktree diff
// against the plan's expected file list. Out-of-scope changes that break the
// build are auto-reverted, and findings are fed back to the shark via PreviousErrors.
//
// The sentinel is deliberately conservative:
// - Files in the same package as expected files are allowed (reasonable scope expansion)
// - Only files completely outside expected packages are flagged
// - Reverts only happen when go build is actually broken
func (a *Activities) SentinelScanActivity(ctx context.Context, req SentinelScanRequest) (*SentinelResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(SharkPrefix+" Sentinel scan starting",
		"MorselID", req.MorselID, "WorktreePath", req.WorktreePath,
		"ExpectedFiles", len(req.ExpectedFiles))

	result := &SentinelResult{Passed: true}
	workDir := req.WorktreePath
	if workDir == "" {
		return result, nil
	}

	// 1. Get list of changed files vs main
	changedFiles, err := gitDiffNameOnly(ctx, workDir)
	if err != nil {
		logger.Warn(SharkPrefix+" Sentinel: git diff failed (non-fatal)", "error", err)
		return result, nil
	}
	if len(changedFiles) == 0 {
		return result, nil
	}

	// 2. Build set of expected packages (directories) for generous scope matching
	expectedPkgs := make(map[string]bool)
	for _, f := range req.ExpectedFiles {
		dir := filepath.Dir(f)
		expectedPkgs[dir] = true
		// Also allow parent directory (e.g. internal/temporal/ allows internal/temporal/foo.go)
		expectedPkgs[filepath.Dir(dir)] = true
	}

	// Also allow top-level files (README, go.mod, etc)
	expectedPkgs["."] = true

	// 3. Classify each changed file
	for _, f := range changedFiles {
		if isInExpectedScope(f, req.ExpectedFiles, expectedPkgs) {
			continue
		}
		result.OutOfScopeFiles = append(result.OutOfScopeFiles, f)
	}

	if len(result.OutOfScopeFiles) == 0 {
		logger.Info(SharkPrefix+" Sentinel: all changes in scope", "ChangedFiles", len(changedFiles))
		return result, nil
	}

	logger.Warn(SharkPrefix+" Sentinel: out-of-scope files detected",
		"OutOfScope", len(result.OutOfScopeFiles),
		"InScope", len(changedFiles)-len(result.OutOfScopeFiles),
		"Files", strings.Join(result.OutOfScopeFiles, ", "))

	// 4. Check if build is broken
	buildBroken := isBuildBroken(ctx, workDir)
	result.BuildBroken = buildBroken

	if !buildBroken {
		// Out-of-scope but build still passes — warn but allow
		logger.Info(SharkPrefix+" Sentinel: out-of-scope files found but build OK, allowing")
		result.Passed = true
		return result, nil
	}

	// 5. Build is broken — revert out-of-scope files and re-check
	logger.Warn(SharkPrefix+" Sentinel: build broken with out-of-scope changes, reverting")
	for _, f := range result.OutOfScopeFiles {
		revertCmd := exec.CommandContext(ctx, "git", "checkout", "main", "--", f)
		revertCmd.Dir = workDir
		if err := revertCmd.Run(); err != nil {
			logger.Warn(SharkPrefix+" Sentinel: failed to revert file", "file", f, "error", err)
			continue
		}
		result.RevertedFiles = append(result.RevertedFiles, f)
	}

	// 6. Re-check build after revert
	if len(result.RevertedFiles) > 0 {
		if isBuildBroken(ctx, workDir) {
			// Build still broken even after revert — the in-scope changes are broken too
			logger.Warn(SharkPrefix + " Sentinel: build still broken after revert — in-scope code is broken")
			result.Passed = false
		} else {
			// Build fixed by revert — the shark was the problem
			logger.Info(SharkPrefix+" Sentinel: build fixed by reverting out-of-scope changes",
				"RevertedFiles", len(result.RevertedFiles))
			result.Passed = true // allow pipeline to continue with clean worktree
		}
	} else {
		result.Passed = false
	}

	return result, nil
}

// gitDiffNameOnly returns the list of files changed vs main in the worktree.
func gitDiffNameOnly(ctx context.Context, workDir string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--name-only", "main")
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff --name-only main: %w", err)
	}

	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// isInExpectedScope checks whether a changed file is within the expected scope.
// A file is in scope if:
// - It's explicitly listed in expectedFiles
// - It's in the same package (directory) as an expected file
// - It's a test file for an expected file
func isInExpectedScope(file string, expectedFiles []string, expectedPkgs map[string]bool) bool {
	// Direct match
	for _, expected := range expectedFiles {
		if file == expected {
			return true
		}
	}

	// Package match — same directory as an expected file
	dir := filepath.Dir(file)
	if expectedPkgs[dir] {
		return true
	}

	// Test file for an expected file (e.g. foo_test.go for foo.go)
	if strings.HasSuffix(file, "_test.go") {
		base := strings.TrimSuffix(file, "_test.go") + ".go"
		for _, expected := range expectedFiles {
			if base == expected {
				return true
			}
		}
	}

	return false
}

// isBuildBroken runs `go build ./...` and returns true if it fails.
func isBuildBroken(ctx context.Context, workDir string) bool {
	cmd := exec.CommandContext(ctx, "go", "build", "./...")
	cmd.Dir = workDir
	return cmd.Run() != nil
}
