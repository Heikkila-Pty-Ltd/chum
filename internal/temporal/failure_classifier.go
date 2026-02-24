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

	switch {
	case strings.Contains(lower, "undefined:") ||
		strings.Contains(lower, "cannot use") ||
		strings.Contains(lower, "syntax error") ||
		strings.Contains(lower, "does not compile") ||
		strings.Contains(lower, "build failed") ||
		strings.Contains(lower, "compilation failed"):
		return "compile_error", extractFirstLine(failures)

	case strings.Contains(lower, "fail") && (strings.Contains(lower, "test") || strings.Contains(lower, "--- fail")):
		return "test_failure", extractFirstLine(failures)

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

	default:
		if failures != "" {
			return "dod_check_failed", extractFirstLine(failures)
		}
		return "", ""
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
