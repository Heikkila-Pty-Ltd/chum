package dispatch

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDispatch_ArgumentLimits(t *testing.T) {
	d := NewDispatcher()
	ctx := t.Context()

	// Create a prompt larger than the CLI arg-safe threshold.
	limit := maxArgSizeFromSystem(t)
	largePrompt := strings.Repeat("a", limit+100)
	binDir := t.TempDir()
	capturePath, statePath := writeFakeOpenclawBinary(t, binDir)
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
	t.Setenv("OPENCLAW_CAPTURE_PATH", capturePath)
	t.Setenv("OPENCLAW_STATE_PATH", statePath)
	t.Setenv("OPENCLAW_REQUIRE_STDIN", "1")

	pid, err := d.Dispatch(ctx, "agent", largePrompt, "provider", "low", t.TempDir())
	if err != nil {
		t.Fatalf("dispatch with large prompt should succeed: %v", err)
	}
	waitForProcessCompletion(t, d, pid, 5*time.Second)

	state := d.GetProcessState(pid)
	if state.ExitCode != 0 {
		var output string
		if state.OutputPath != "" {
			if out, readErr := os.ReadFile(state.OutputPath); readErr == nil {
				output = string(out)
			}
		}
		t.Fatalf("dispatch with large prompt should exit 0, got %d (output: %q)", state.ExitCode, output)
	}
	d.CleanupProcess(pid)

	capturedPrompt, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("failed to read captured prompt: %v", err)
	}
	if len(capturedPrompt) != len(largePrompt) {
		t.Fatalf("captured prompt length mismatch: got %d want %d", len(capturedPrompt), len(largePrompt))
	}
	if string(capturedPrompt) != largePrompt {
		t.Fatal("captured prompt content mismatch")
	}
	output, err := os.ReadFile(state.OutputPath)
	if err == nil && strings.Contains(string(output), "Syntax error") {
		t.Fatalf("dispatch with large prompt should avoid shell syntax errors: %s", strings.TrimSpace(string(output)))
	}

	// Create an agent config slightly larger than the limit
	largeAgent := strings.Repeat("b", limit+100)
	_, err = d.Dispatch(ctx, largeAgent, "prompt", "provider", "low", ".")
	if err == nil {
		t.Error("Dispatch with large agent string should fail")
	} else if !strings.Contains(err.Error(), "agent configuration too large") {
		t.Errorf("expected 'agent configuration too large' error, got: %v", err)
	}
}

func maxArgSizeFromSystem(t *testing.T) int {
	t.Helper()

	out, err := exec.Command("getconf", "ARG_MAX").Output()
	if err != nil {
		t.Logf("getconf ARG_MAX unavailable; falling back to MaxCLIArgSize=%d", MaxCLIArgSize)
		return MaxCLIArgSize
	}

	maxArgSize, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || maxArgSize <= 0 {
		t.Logf("invalid ARG_MAX value %q; falling back to MaxCLIArgSize=%d", strings.TrimSpace(string(out)), MaxCLIArgSize)
		return MaxCLIArgSize
	}

	return maxArgSize
}
