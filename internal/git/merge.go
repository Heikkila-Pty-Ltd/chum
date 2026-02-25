package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var supportedMergeMethods = map[string]string{
	"squash": "--squash",
	"merge":  "--merge",
	"rebase": "--rebase",
}

// DoDResult contains the overall result of running DoD checks.
type DoDResult struct {
	Passed   bool          // true if all checks passed
	Checks   []CheckResult // per-command results
	Failures []string      // human-readable failure reasons
}

// CheckResult contains the result of running a single check command.
type CheckResult struct {
	Command  string        // the command that was executed
	ExitCode int           // process exit code
	Output   string        // truncated stdout/stderr output
	Passed   bool          // true if the check passed
	Duration time.Duration // how long the command took
}

// MergePR merges an approved PR using gh CLI.
// method must be one of: squash, merge, rebase.
func MergePR(workspace string, prNumber int, method string) error {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return fmt.Errorf("workspace is required")
	}

	method = strings.ToLower(strings.TrimSpace(method))
	if method == "" {
		return fmt.Errorf("merge method is required (supported methods: squash, merge, rebase)")
	}

	mergeFlag, ok := supportedMergeMethods[method]
	if !ok {
		return fmt.Errorf("unsupported merge method %q (supported methods: squash, merge, rebase)", method)
	}

	if prNumber <= 0 {
		return fmt.Errorf("invalid PR number: %d", prNumber)
	}

	cmd := exec.Command("gh", "pr", "merge", fmt.Sprintf("%d", prNumber), mergeFlag)
	cmd.Dir = workspace
	out, err := cmd.CombinedOutput()
	if err != nil {
		output := strings.TrimSpace(string(out))
		if output != "" {
			return fmt.Errorf("failed to merge PR #%d using %q in %s: %w (%s)", prNumber, method, workspace, err, output)
		}
		return fmt.Errorf("failed to merge PR #%d using %q in %s: %w", prNumber, method, workspace, err)
	}
	return nil
}

// RevertMerge reverts the given merge commit and pushes the change.
func RevertMerge(workspace string, commitSHA string) error {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return fmt.Errorf("workspace is required")
	}

	commitSHA = strings.TrimSpace(commitSHA)
	if commitSHA == "" {
		return fmt.Errorf("commit SHA is required")
	}

	revertCmd := exec.Command("git", "revert", commitSHA, "--no-edit")
	revertCmd.Dir = workspace
	out, err := revertCmd.CombinedOutput()
	if err != nil {
		output := strings.TrimSpace(string(out))
		if output != "" {
			return fmt.Errorf("failed to revert commit %s in %s: %w (%s)", commitSHA, workspace, err, output)
		}
		return fmt.Errorf("failed to revert commit %s in %s: %w", commitSHA, workspace, err)
	}

	pushCmd := exec.Command("git", "push")
	pushCmd.Dir = workspace
	out, err = pushCmd.CombinedOutput()
	if err != nil {
		output := strings.TrimSpace(string(out))
		if output != "" {
			return fmt.Errorf("failed to push revert of commit %s from %s: %w (%s)", commitSHA, workspace, err, output)
		}
		return fmt.Errorf("failed to push revert of commit %s from %s: %w", commitSHA, workspace, err)
	}
	return nil
}

// RunPostMergeChecks runs PR merge validation checks after merge.
func RunPostMergeChecks(workspace string, checks []string) (*DoDResult, error) {
	result := &DoDResult{
		Passed:   true,
		Checks:   make([]CheckResult, 0, len(checks)),
		Failures: make([]string, 0),
	}

	for _, check := range checks {
		check = strings.TrimSpace(check)
		if check == "" {
			result.Passed = false
			result.Failures = append(result.Failures, "empty post-merge check command")
			result.Checks = append(result.Checks, CheckResult{
				Command:  "",
				ExitCode: -1,
				Output:   "empty post-merge check command",
				Passed:   false,
				Duration: 0,
			})
			continue
		}

		checkResult := runSinglePostMergeCheck(workspace, check)
		result.Checks = append(result.Checks, *checkResult)
		if !checkResult.Passed {
			result.Passed = false
			result.Failures = append(result.Failures,
				fmt.Sprintf("Command failed: %s (exit %d)", check, checkResult.ExitCode))
		}
	}

	return result, nil
}

func runSinglePostMergeCheck(workspace, command string) *CheckResult {
	start := time.Now()
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return &CheckResult{
			Command:  command,
			ExitCode: -1,
			Output:   "empty post-merge check command",
			Passed:   false,
			Duration: 0,
		}
	}
	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = workspace
	// Worktrees lack full VCS context — disable Go build's VCS stamping
	// to prevent 'error obtaining VCS status: exit status 128'.
	// Augment PATH with common Go tool directories so golangci-lint et al.
	// are findable even in restricted dispatch shells (systemd, cron).
	env := os.Environ()
	env = append(env, "GOFLAGS=-buildvcs=false")
	env = augmentPATH(env)
	cmd.Env = env

	output, err := cmd.CombinedOutput()
	duration := time.Since(start)

	exitCode := 0
	passed := true
	if err != nil {
		exitCode = 1
		passed = false
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
	}

	out := strings.TrimSpace(string(output))
	if len(out) > 2000 {
		out = out[:2000] + "\n... [truncated]"
	}

	return &CheckResult{
		Command:  command,
		ExitCode: exitCode,
		Output:   out,
		Passed:   passed,
		Duration: duration,
	}
}

// LatestCommitSHA returns HEAD commit SHA for workspace.
func LatestCommitSHA(workspace string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = workspace
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to read HEAD commit: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// augmentPATH appends common Go tool directories to PATH in the env slice.
// This ensures golangci-lint and other Go tools are discoverable even when
// the dispatch shell has a minimal PATH (systemd, cron, restricted shells).
func augmentPATH(env []string) []string {
	extraDirs := []string{
		"/usr/local/bin",
		"/usr/local/go/bin",
	}
	if home, err := os.UserHomeDir(); err == nil {
		extraDirs = append(extraDirs,
			filepath.Join(home, "go", "bin"),
			filepath.Join(home, ".local", "bin"),
		)
	}

	for i, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			current := strings.TrimPrefix(e, "PATH=")
			for _, d := range extraDirs {
				if !strings.Contains(current, d) {
					current += ":" + d
				}
			}
			env[i] = "PATH=" + current
			return env
		}
	}
	// No PATH found; set a reasonable default.
	env = append(env, "PATH="+strings.Join(extraDirs, ":"))
	return env
}
