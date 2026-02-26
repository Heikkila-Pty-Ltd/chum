package git

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupTestRepo creates a temporary git repository for testing
func setupTestRepo(t *testing.T) string {
	t.Helper()

	tmpDir := t.TempDir()

	// Initialize git repo
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to init git repo: %v", err)
	}

	// Configure git user (required for commits)
	cmd = exec.Command("git", "config", "user.name", "Test User")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to set git user.name: %v", err)
	}
	cmd = exec.Command("git", "config", "user.email", "test@example.com")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to set git user.email: %v", err)
	}

	// Create an initial commit
	testFile := filepath.Join(tmpDir, "README.md")
	if err := os.WriteFile(testFile, []byte("# Test Repo\n"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	cmd = exec.Command("git", "add", "README.md")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to add file: %v", err)
	}

	cmd = exec.Command("git", "commit", "-m", "Initial commit")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to commit: %v", err)
	}

	return tmpDir
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v (%s)", args, err, string(out))
	}
	return string(out)
}

func TestGetCurrentBranch(t *testing.T) {
	repo := setupTestRepo(t)

	branch, err := GetCurrentBranch(repo)
	if err != nil {
		t.Fatalf("GetCurrentBranch failed: %v", err)
	}

	// Should be on main or master
	if branch != "main" && branch != "master" {
		t.Errorf("expected branch to be main or master, got %s", branch)
	}
}

func TestCreateFeatureBranch(t *testing.T) {
	repo := setupTestRepo(t)

	// Get current branch to use as base
	baseBranch, err := GetCurrentBranch(repo)
	if err != nil {
		t.Fatalf("failed to get current branch: %v", err)
	}

	morselID := "test-123"
	if createErr := CreateFeatureBranch(repo, morselID, baseBranch); createErr != nil {
		t.Fatalf("CreateFeatureBranch failed: %v", createErr)
	}

	// Verify we're on the new branch
	currentBranch, err := GetCurrentBranch(repo)
	if err != nil {
		t.Fatalf("failed to get current branch after creation: %v", err)
	}

	expectedBranch := "feat/test-123"
	if currentBranch != expectedBranch {
		t.Errorf("expected to be on branch %s, got %s", expectedBranch, currentBranch)
	}
}

func TestBranchExists(t *testing.T) {
	repo := setupTestRepo(t)

	// Check existing branch
	currentBranch, _ := GetCurrentBranch(repo)
	exists, err := BranchExists(repo, currentBranch)
	if err != nil {
		t.Fatalf("BranchExists failed: %v", err)
	}
	if !exists {
		t.Errorf("expected current branch %s to exist", currentBranch)
	}

	// Check non-existing branch
	exists, err = BranchExists(repo, "nonexistent-branch")
	if err != nil {
		t.Fatalf("BranchExists failed for nonexistent branch: %v", err)
	}
	if exists {
		t.Errorf("expected nonexistent-branch to not exist")
	}
}

func TestEnsureFeatureBranchWithBase(t *testing.T) {
	repo := setupTestRepo(t)

	baseBranch, _ := GetCurrentBranch(repo)
	morselID := "test-456"

	// Test creating new branch
	if err := EnsureFeatureBranchWithBase(repo, morselID, baseBranch, "feat/"); err != nil {
		t.Fatalf("EnsureFeatureBranchWithBase failed: %v", err)
	}

	expectedBranch := "feat/test-456"
	currentBranch, _ := GetCurrentBranch(repo)
	if currentBranch != expectedBranch {
		t.Errorf("expected to be on branch %s, got %s", expectedBranch, currentBranch)
	}

	// Switch to different branch
	cmd := exec.Command("git", "checkout", baseBranch)
	cmd.Dir = repo
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to switch back to base branch: %v", err)
	}

	// Test switching to existing branch
	if err := EnsureFeatureBranchWithBase(repo, morselID, baseBranch, "feat/"); err != nil {
		t.Fatalf("EnsureFeatureBranchWithBase failed on existing branch: %v", err)
	}

	currentBranch, _ = GetCurrentBranch(repo)
	if currentBranch != expectedBranch {
		t.Errorf("expected to be on existing branch %s, got %s", expectedBranch, currentBranch)
	}
}

func TestMergeBranchIntoBaseSuccess(t *testing.T) {
	repo := setupTestRepo(t)

	baseBranch, _ := GetCurrentBranch(repo)
	morselID := "merge-123"
	if err := EnsureFeatureBranchWithBase(repo, morselID, baseBranch, "ctx/"); err != nil {
		t.Fatalf("EnsureFeatureBranchWithBase failed: %v", err)
	}

	testFile := filepath.Join(repo, "README.md")
	if err := os.WriteFile(testFile, []byte("# Test Repo\nfeature line\n"), 0644); err != nil {
		t.Fatalf("failed to update test file: %v", err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "feature commit")
	runGit(t, repo, "checkout", baseBranch)

	if err := MergeBranchIntoBase(repo, "ctx/"+morselID, baseBranch, "merge"); err != nil {
		t.Fatalf("MergeBranchIntoBase failed: %v", err)
	}

	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("failed to read test file: %v", err)
	}
	if string(data) != "# Test Repo\nfeature line\n" {
		t.Fatalf("expected merged content in README, got %q", string(data))
	}
}

func TestMergeBranchIntoBaseConflict(t *testing.T) {
	repo := setupTestRepo(t)

	baseBranch, _ := GetCurrentBranch(repo)
	morselID := "conflict-456"
	if err := EnsureFeatureBranchWithBase(repo, morselID, baseBranch, "ctx/"); err != nil {
		t.Fatalf("EnsureFeatureBranchWithBase failed: %v", err)
	}

	testFile := filepath.Join(repo, "README.md")
	if err := os.WriteFile(testFile, []byte("# Test Repo\nfeature line\n"), 0644); err != nil {
		t.Fatalf("failed to write feature change: %v", err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "feature change")

	runGit(t, repo, "checkout", baseBranch)
	if err := os.WriteFile(testFile, []byte("# Test Repo\nbase line\n"), 0644); err != nil {
		t.Fatalf("failed to write base change: %v", err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "base change")

	err := MergeBranchIntoBase(repo, "ctx/"+morselID, baseBranch, "merge")
	if err == nil {
		t.Fatal("expected merge conflict error")
	}
	if !errors.Is(err, ErrMergeConflict) {
		t.Fatalf("expected ErrMergeConflict, got %v", err)
	}
}

func TestMergeBranchIntoBaseRebaseWithAdvancedBase(t *testing.T) {
	repo := setupTestRepo(t)

	baseBranch, _ := GetCurrentBranch(repo)
	morselID := "rebase-111"
	featureBranch := "ctx/" + morselID
	if err := EnsureFeatureBranchWithBase(repo, morselID, baseBranch, "ctx/"); err != nil {
		t.Fatalf("EnsureFeatureBranchWithBase failed: %v", err)
	}

	readmePath := filepath.Join(repo, "README.md")
	if err := os.WriteFile(readmePath, []byte("# Test Repo\nfeature line\n"), 0644); err != nil {
		t.Fatalf("failed to write feature change: %v", err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "feature change")

	runGit(t, repo, "checkout", baseBranch)
	baseFile := filepath.Join(repo, "BASE.md")
	if err := os.WriteFile(baseFile, []byte("base advanced\n"), 0644); err != nil {
		t.Fatalf("failed to write base file: %v", err)
	}
	runGit(t, repo, "add", "BASE.md")
	runGit(t, repo, "commit", "-m", "base advanced")

	if err := MergeBranchIntoBase(repo, featureBranch, baseBranch, "rebase"); err != nil {
		t.Fatalf("MergeBranchIntoBase rebase failed: %v", err)
	}

	data, err := os.ReadFile(readmePath)
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	if string(data) != "# Test Repo\nfeature line\n" {
		t.Fatalf("expected rebased feature content in README, got %q", string(data))
	}

	if _, err := os.Stat(baseFile); err != nil {
		t.Fatalf("expected base advanced file to remain after rebase merge: %v", err)
	}
}

func TestMergeBranchIntoBaseRebaseConflict(t *testing.T) {
	repo := setupTestRepo(t)

	baseBranch, _ := GetCurrentBranch(repo)
	morselID := "rebase-conflict-222"
	featureBranch := "ctx/" + morselID
	if err := EnsureFeatureBranchWithBase(repo, morselID, baseBranch, "ctx/"); err != nil {
		t.Fatalf("EnsureFeatureBranchWithBase failed: %v", err)
	}

	readmePath := filepath.Join(repo, "README.md")
	if err := os.WriteFile(readmePath, []byte("# Test Repo\nfeature line\n"), 0644); err != nil {
		t.Fatalf("failed to write feature change: %v", err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "feature change")

	runGit(t, repo, "checkout", baseBranch)
	if err := os.WriteFile(readmePath, []byte("# Test Repo\nbase line\n"), 0644); err != nil {
		t.Fatalf("failed to write base change: %v", err)
	}
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "base change")

	err := MergeBranchIntoBase(repo, featureBranch, baseBranch, "rebase")
	if err == nil {
		t.Fatal("expected rebase conflict error")
	}
	if !errors.Is(err, ErrMergeConflict) {
		t.Fatalf("expected ErrMergeConflict, got %v", err)
	}

	status := runGit(t, repo, "status", "--porcelain")
	if strings.TrimSpace(status) != "" {
		t.Fatalf("expected clean repository after rebase conflict abort, got status %q", status)
	}
}

func TestDeleteBranch(t *testing.T) {
	repo := setupTestRepo(t)

	baseBranch, _ := GetCurrentBranch(repo)
	morselID := "delete-789"
	branchName := "ctx/" + morselID
	if err := EnsureFeatureBranchWithBase(repo, morselID, baseBranch, "ctx/"); err != nil {
		t.Fatalf("EnsureFeatureBranchWithBase failed: %v", err)
	}
	runGit(t, repo, "checkout", baseBranch)

	if err := DeleteBranch(repo, branchName); err != nil {
		t.Fatalf("DeleteBranch failed: %v", err)
	}

	exists, err := BranchExists(repo, branchName)
	if err != nil {
		t.Fatalf("BranchExists failed: %v", err)
	}
	if exists {
		t.Fatalf("expected branch %s to be deleted", branchName)
	}
}
