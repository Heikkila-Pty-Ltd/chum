package temporal

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// execCalcifiedScript runs a calcified script, passing input via stdin.
// Returns stdout, exit code, and any error.
func execCalcifiedScript(ctx context.Context, scriptPath, stdin string) (string, int, error) {
	// Enforce a hard timeout to prevent runaway scripts.
	execCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(execCtx, scriptPath)
	cmd.Stdin = strings.NewReader(stdin)

	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return string(out), -1, fmt.Errorf("exec calcified script %s: %w", scriptPath, err)
		}
	}
	return string(out), exitCode, nil
}

// verifyScriptIntegrity checks that the SHA-256 hash of a script file matches
// the expected hash stored in the database. Prevents execution of tampered scripts.
func verifyScriptIntegrity(scriptPath, expectedHash string) error {
	f, err := os.Open(scriptPath)
	if err != nil {
		return fmt.Errorf("open script for integrity check: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash script: %w", err)
	}

	actual := fmt.Sprintf("%x", h.Sum(nil))
	if actual != expectedHash {
		return fmt.Errorf("integrity check failed for %s: expected %s, got %s", scriptPath, expectedHash, actual)
	}
	return nil
}

// hashFile computes the SHA-256 hex digest of a file.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// compareOutputs performs a normalized comparison of LLM output vs script output.
// Trims whitespace and normalizes line endings for tolerance of cosmetic differences.
func compareOutputs(llmOutput, scriptOutput string) bool {
	normalize := func(s string) string {
		s = strings.TrimSpace(s)
		s = strings.ReplaceAll(s, "\r\n", "\n")
		s = strings.ReplaceAll(s, "\r", "\n")
		return s
	}
	return normalize(llmOutput) == normalize(scriptOutput)
}
