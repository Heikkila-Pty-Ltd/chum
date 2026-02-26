package temporal

import "strings"

// classifyFailure categorizes a DoD failure string into a machine-readable
// category and a one-line summary. This feeds the paleontologist's systemic
// failure detection and the genome's evolutionary memory.
//
// Categories:
//   - "test_failure"     — go test / npm test / pytest failures
//   - "compile_error"    — build doesn't compile (syntax, undefined, type mismatch)
//   - "lint_error"       — linter or static analysis failure
//   - "timeout"          — execution exceeded time limit
//   - "merge_conflict"   — git merge/rebase conflict
//   - "scope_drift"      — sentinel detected out-of-scope changes
//   - "dod_check_failed" — generic DoD check failure (catch-all)
func classifyFailure(failures string) (category, summary string) {
	lower := strings.ToLower(failures)

	// Infrastructure failures first — these are NOT the shark's fault
	if isInfrastructureFailure(lower) {
		return "infrastructure_failure", extractInfraReason(lower)
	}

	switch {
	// Temporal activity timeouts (Heartbeat, StartToClose) — must be before generic timeout check
	case strings.Contains(lower, "heartbeat timeout") ||
		strings.Contains(lower, "starttoclose timeout") ||
		strings.Contains(lower, "activity heartbeat timeout") ||
		strings.Contains(lower, "activity starttoclose timeout"):
		return "activity_timeout", extractFirstLine(failures)

	case strings.Contains(lower, "undefined:") ||
		strings.Contains(lower, "cannot use") ||
		strings.Contains(lower, "syntax error") ||
		strings.Contains(lower, "does not compile") ||
		strings.Contains(lower, "build failed") ||
		strings.Contains(lower, "compilation failed"):
		return "compile_error", extractFirstLine(failures)

	// go test/vet/lint triple failure = compile error (code doesn't compile)
	case strings.Contains(lower, "go test") &&
		strings.Contains(lower, "go vet") &&
		strings.Contains(lower, "golangci-lint"):
		return "compile_error", "Triple DoD fail (test+vet+lint) — code does not compile"

	case strings.Contains(lower, "fail") && (strings.Contains(lower, "test") || strings.Contains(lower, "--- fail")):
		return "test_failure", extractFirstLine(failures)

	// golangci-lint exit 3 = config/runtime error (not lint failure which is exit 1)
	case strings.Contains(lower, "golangci-lint") && strings.Contains(lower, "exit 3"):
		return "lint_config_error", "golangci-lint exit 3: config/runtime error (not lint failure)"

	case strings.Contains(lower, "golangci-lint") ||
		strings.Contains(lower, "eslint") ||
		strings.Contains(lower, "lint") && strings.Contains(lower, "error"):
		return "lint_error", extractFirstLine(failures)

	case strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "exceeded") && strings.Contains(lower, "time"):
		return "timeout", extractFirstLine(failures)

	case strings.Contains(lower, "merge conflict") ||
		strings.Contains(lower, "conflict"):
		return "merge_conflict", extractFirstLine(failures)

	case strings.Contains(lower, "scope") ||
		strings.Contains(lower, "out-of-scope") ||
		strings.Contains(lower, "drift"):
		return "scope_drift", extractFirstLine(failures)

	// Execute error (agent CLI crashed or was killed)
	case strings.Contains(lower, "execute error") ||
		strings.Contains(lower, "executeactivity"):
		return "execution_error", extractFirstLine(failures)

	default:
		if failures != "" {
			return "dod_check_failed", extractFirstLine(failures)
		}
		return "", ""
	}
}

// isInfrastructureFailure returns true if the failure is environmental,
// not caused by the shark's code changes. These failures should NOT burn
// a retry attempt or be fed back as agent guidance.
//
// Infrastructure failures include:
//   - golangci-lint parallel lock ("parallel golangci-lint is running")
//   - golangci-lint exit 3 (config error, not code error)
//   - semgrep exit 7 (config/download error)
//   - tool not found in PATH
//   - permission denied
//   - disk full / no space left
//   - git lock contention
func isInfrastructureFailure(lower string) bool {
	infraPatterns := []string{
		"parallel golangci-lint is running",
		"golangci-lint" + " " + "exit 3",  // config error
		"golangci-lint" + " " + "exit -1", // signal kill (OOM)
		"semgrep" + " " + "exit 7",        // config/download error
		"exit -1",                         // any tool killed by signal (OOM, SIGKILL)
		"error obtaining vcs status",      // golangci-lint in worktrees
		"command not found",
		"no such file or directory",
		"permission denied",
		"no space left on device",
		"disk quota exceeded",
		"git lock",
		"index.lock",
		"unable to create",
		"fatal: unable to access",
	}
	for _, p := range infraPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// isTransientInfraFailure returns true if the infra failure is likely temporary
// and worth one retry (e.g., lock contention). Returns false for persistent
// issues (disk full, missing tools) that need human intervention.
func isTransientInfraFailure(lower string) bool {
	transientPatterns := []string{
		"parallel golangci-lint is running",
		"git lock",
		"index.lock",
		"unable to create",
	}
	for _, p := range transientPatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// extractInfraReason returns a human-readable reason for the infrastructure failure.
func extractInfraReason(lower string) string {
	switch {
	case strings.Contains(lower, "parallel golangci-lint"):
		return "golangci-lint parallel lock (another instance running)"
	case strings.Contains(lower, "golangci-lint") && strings.Contains(lower, "exit 3"):
		return "golangci-lint config/runtime error (exit 3)"
	case strings.Contains(lower, "semgrep") && strings.Contains(lower, "exit 7"):
		return "semgrep config/download error (exit 7)"
	case strings.Contains(lower, "command not found"):
		return "required tool not found in PATH"
	case strings.Contains(lower, "no space left") || strings.Contains(lower, "disk quota"):
		return "disk full"
	case strings.Contains(lower, "git lock") || strings.Contains(lower, "index.lock"):
		return "git lock contention"
	default:
		return "infrastructure/environment error"
	}
}

// extractFirstLine returns the first non-empty line of text, truncated to 200 chars.
func extractFirstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			if len(line) > 200 {
				return line[:200] + "..."
			}
			return line
		}
	}
	return ""
}
