package dispatch

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/antigravity-dev/chum/internal/config"
)

func TestHeadlessBackend_DispatchEchoHelloWorld(t *testing.T) {
	t.Parallel()

	backend := NewHeadlessBackend(
		map[string]config.CLIConfig{
			"test": {
				Cmd: "sh",
				Args: []string{"-c", "echo hello world"},
			},
		},
		"",
		0,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	completed := false
	handle, err := backend.Dispatch(ctx, DispatchOpts{
		Agent:     "test-agent",
		Prompt:    "payload",
		CLIConfig: "test",
	})
	if err != nil {
		t.Fatalf("Dispatch failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, err := backend.Status(handle)
		if err != nil {
			t.Fatalf("Status failed: %v", err)
		}
		switch status.State {
		case "running":
		case "completed":
			if status.ExitCode != 0 {
				t.Fatalf("dispatch exited with code %d", status.ExitCode)
			}
			completed = true
		case "failed":
			t.Fatalf("dispatch failed with exit code %d", status.ExitCode)
		case "unknown":
			t.Fatalf("dispatch status unknown: %+v", status)
		default:
			t.Fatalf("unexpected dispatch status: %s", status.State)
		}
		if completed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !completed {
		t.Fatalf("dispatch did not complete in time")
	}

	output, err := backend.CaptureOutput(handle)
	if err != nil {
		t.Fatalf("CaptureOutput failed: %v", err)
	}
	if strings.TrimSpace(output) != "hello world" {
		t.Fatalf("expected output %q, got %q", "hello world", output)
	}

	if err := backend.Cleanup(handle); err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}
}
