package dispatch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestPIDDispatcherZeroExit tests that a process exiting with code 0 is marked as completed.
func TestPIDDispatcherZeroExit(t *testing.T) {
	d := NewDispatcher()

	// Create a simple script that exits with code 0
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "success.sh")
	scriptContent := `#!/bin/bash
echo "Task completed successfully"
exit 0`

	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write test script: %v", err)
	}

	ctx := t.Context()

	// Override the openclaw command to run our test script instead
	pid, err := d.dispatchTestProcess(ctx, scriptPath)
	if err != nil {
		t.Fatalf("Failed to dispatch test process: %v", err)
	}

	// Wait for process to complete
	waitForProcessCompletion(t, d, pid, 5*time.Second)

	// Check process state
	state := d.GetProcessState(pid)

	if state.State != "exited" {
		t.Errorf("Expected state 'exited', got '%s'", state.State)
	}

	if state.ExitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", state.ExitCode)
	}

	// Verify output was captured
	if state.OutputPath == "" {
		t.Error("Expected output path to be set")
	} else {
		output, err := os.ReadFile(state.OutputPath)
		if err != nil {
			t.Errorf("Failed to read output file: %v", err)
		} else if !containsString(string(output), "Task completed successfully") {
			t.Errorf("Expected output to contain success message, got: %s", string(output))
		}
	}

	// Clean up
	d.CleanupProcess(pid)
}

// TestPIDDispatcherNonZeroExit tests that a process exiting with non-zero code is marked as failed.
func TestPIDDispatcherNonZeroExit(t *testing.T) {
	d := NewDispatcher()

	// Create a script that exits with code 42
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "failure.sh")
	scriptContent := `#!/bin/bash
echo "Task failed with error"
echo "Error details on stderr" >&2
exit 42`

	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write test script: %v", err)
	}

	ctx := t.Context()

	pid, err := d.dispatchTestProcess(ctx, scriptPath)
	if err != nil {
		t.Fatalf("Failed to dispatch test process: %v", err)
	}

	// Wait for process to complete
	waitForProcessCompletion(t, d, pid, 5*time.Second)

	// Check process state
	state := d.GetProcessState(pid)

	if state.State != "exited" {
		t.Errorf("Expected state 'exited', got '%s'", state.State)
	}

	if state.ExitCode != 42 {
		t.Errorf("Expected exit code 42, got %d", state.ExitCode)
	}

	// Verify output was captured (both stdout and stderr)
	if state.OutputPath != "" {
		output, err := os.ReadFile(state.OutputPath)
		if err != nil {
			t.Errorf("Failed to read output file: %v", err)
		} else {
			outputStr := string(output)
			if !containsString(outputStr, "Task failed with error") {
				t.Errorf("Expected output to contain failure message, got: %s", outputStr)
			}
			if !containsString(outputStr, "Error details on stderr") {
				t.Errorf("Expected output to contain stderr content, got: %s", outputStr)
			}
		}
	}

	// Clean up
	d.CleanupProcess(pid)
}

// TestPIDDispatcherKilledProcess tests that a killed process is marked as failed.
func TestPIDDispatcherKilledProcess(t *testing.T) {
	d := NewDispatcher()

	// Create a long-running script that can be killed
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "longrunning.sh")
	scriptContent := `#!/bin/bash
echo "Starting long task"
# Sleep for a long time
sleep 30
echo "This should not be reached"
exit 0`

	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write test script: %v", err)
	}

	ctx := t.Context()

	pid, err := d.dispatchTestProcess(ctx, scriptPath)
	if err != nil {
		t.Fatalf("Failed to dispatch test process: %v", err)
	}

	// Wait a moment for the process to start
	time.Sleep(100 * time.Millisecond)

	// Verify process is running
	if !d.IsAlive(pid) {
		t.Fatal("Process should be alive before killing")
	}

	// Kill the process
	if err := d.Kill(pid); err != nil {
		t.Fatalf("Failed to kill process: %v", err)
	}

	// Wait for process death to be registered
	waitForProcessCompletion(t, d, pid, 5*time.Second)

	// Check process state
	state := d.GetProcessState(pid)

	if state.State != "exited" {
		t.Errorf("Expected state 'exited', got '%s'", state.State)
	}

	if state.ExitCode != -1 {
		t.Errorf("Expected exit code -1 for killed process, got %d", state.ExitCode)
	}

	// Clean up
	d.CleanupProcess(pid)
}

// TestPIDDispatcherProcessNotTracked tests handling of processes that aren't being tracked.
func TestPIDDispatcherProcessNotTracked(t *testing.T) {
	d := NewDispatcher()

	// Test getting state of non-existent PID
	nonExistentPID := 999999 // Very unlikely to exist
	state := d.GetProcessState(nonExistentPID)

	if state.State != "unknown" {
		t.Errorf("Expected state 'unknown' for non-existent PID, got '%s'", state.State)
	}

	if state.ExitCode != -1 {
		t.Errorf("Expected exit code -1 for non-existent PID, got %d", state.ExitCode)
	}

	if state.OutputPath != "" {
		t.Errorf("Expected empty output path for non-existent PID, got '%s'", state.OutputPath)
	}
}

// TestPIDDispatcherOutputCapture tests that process output is properly captured.
func TestPIDDispatcherOutputCapture(t *testing.T) {
	d := NewDispatcher()

	// Create a script with specific output
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "output.sh")
	scriptContent := `#!/bin/bash
echo "Line 1: stdout message"
echo "Line 2: stderr message" >&2
echo "Line 3: more stdout"
exit 5`

	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("Failed to write test script: %v", err)
	}

	ctx := t.Context()

	pid, err := d.dispatchTestProcess(ctx, scriptPath)
	if err != nil {
		t.Fatalf("Failed to dispatch test process: %v", err)
	}

	// Wait for process to complete
	waitForProcessCompletion(t, d, pid, 5*time.Second)

	// Check that output was captured
	state := d.GetProcessState(pid)

	if state.OutputPath == "" {
		t.Fatal("Expected output path to be set")
	}

	output, err := os.ReadFile(state.OutputPath)
	if err != nil {
		t.Fatalf("Failed to read output file: %v", err)
	}

	outputStr := string(output)
	expectedStrings := []string{
		"Line 1: stdout message",
		"Line 2: stderr message",
		"Line 3: more stdout",
	}

	for _, expected := range expectedStrings {
		if !containsString(outputStr, expected) {
			t.Errorf("Expected output to contain '%s', got: %s", expected, outputStr)
		}
	}

	// Verify exit code
	if state.ExitCode != 5 {
		t.Errorf("Expected exit code 5, got %d", state.ExitCode)
	}

	// Clean up
	d.CleanupProcess(pid)
}

// dispatchTestProcess is a helper that runs a shell script instead of openclaw.
func (d *Dispatcher) dispatchTestProcess(ctx context.Context, scriptPath string) (int, error) {
	// Create a dummy prompt file (not used by test scripts)
	tmpFile, err := os.CreateTemp("", "chum-test-prompt-*.txt")
	if err != nil {
		return 0, err
	}
	tmpPath := tmpFile.Name()
	tmpFile.WriteString("test prompt")
	tmpFile.Close()

	// Create output capture file
	outputFile, err := os.CreateTemp("", "chum-test-output-*.log")
	if err != nil {
		os.Remove(tmpPath)
		return 0, err
	}
	outputPath := outputFile.Name()

	// Run the test script directly instead of openclaw
	cmd := exec.Command("bash", scriptPath)
	cmd.Stdout = outputFile
	cmd.Stderr = outputFile

	if err := cmd.Start(); err != nil {
		outputFile.Close()
		os.Remove(tmpPath)
		os.Remove(outputPath)
		return 0, err
	}

	outputFile.Close()
	pid := cmd.Process.Pid

	// Store process info like the real Dispatch method
	d.mu.Lock()
	d.processes[pid] = &processInfo{
		cmd:        cmd,
		startedAt:  time.Now(),
		state:      "running",
		exitCode:   -1,
		outputPath: outputPath,
		tmpPath:    tmpPath,
	}
	d.mu.Unlock()

	// Monitor the process
	go d.monitorProcess(pid)

	return pid, nil
}

// waitForProcessCompletion waits for a process to complete with a timeout.
func waitForProcessCompletion(t *testing.T, d *Dispatcher, pid int, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state := d.GetProcessState(pid)
		if state.State != "running" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("Process %d did not complete within %v", pid, timeout)
}

// containsString checks if a string contains a substring.
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
