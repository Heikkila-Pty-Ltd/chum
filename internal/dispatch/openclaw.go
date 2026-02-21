package dispatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// OpenClawBackend adapts the existing openclaw Dispatcher to the pluggable Backend interface.
type OpenClawBackend struct {
	dispatcher *Dispatcher

	mu         sync.RWMutex
	outputPath map[int]string
	logPath    map[int]string
}

// NewOpenClawBackend wraps an existing Dispatcher as a pluggable Backend.
func NewOpenClawBackend(d *Dispatcher) *OpenClawBackend {
	if d == nil {
		d = NewDispatcher()
	}
	return &OpenClawBackend{
		dispatcher: d,
		outputPath: make(map[int]string),
		logPath:    make(map[int]string),
	}
}

func (b *OpenClawBackend) Name() string {
	return "openclaw"
}

func (b *OpenClawBackend) Dispatch(ctx context.Context, opts DispatchOpts) (Handle, error) {
	pid, err := b.dispatcher.Dispatch(ctx, opts.Agent, opts.Prompt, opts.Model, opts.ThinkingLevel, opts.WorkDir)
	if err != nil {
		return Handle{}, fmt.Errorf("openclaw backend: dispatch: %w", err)
	}

	state := b.dispatcher.GetProcessState(pid)
	sourcePath := strings.TrimSpace(state.OutputPath)
	logPath := strings.TrimSpace(opts.LogPath)

	b.mu.Lock()
	if sourcePath != "" {
		b.outputPath[pid] = sourcePath
	}
	if logPath != "" {
		b.logPath[pid] = logPath
	}
	b.mu.Unlock()

	if sourcePath != "" && logPath != "" {
		if err := os.MkdirAll(filepath.Dir(logPath), 0755); err == nil {
			_ = os.Remove(logPath)
			if err := os.Symlink(sourcePath, logPath); err != nil {
				if output, readErr := os.ReadFile(sourcePath); readErr == nil {
					_ = os.WriteFile(logPath, output, 0644)
				}
			}
		}
	}

	return Handle{
		PID:     pid,
		Backend: b.Name(),
	}, nil
}

func (b *OpenClawBackend) Status(handle Handle) (DispatchStatus, error) {
	state := b.dispatcher.GetProcessState(handle.PID)
	switch state.State {
	case "running":
		return DispatchStatus{State: "running", ExitCode: -1}, nil
	case "exited":
		if state.ExitCode == 0 {
			return DispatchStatus{State: "completed", ExitCode: 0}, nil
		}
		return DispatchStatus{State: "failed", ExitCode: state.ExitCode}, nil
	default:
		return DispatchStatus{State: "unknown", ExitCode: -1}, nil
	}
}

func (b *OpenClawBackend) CaptureOutput(handle Handle) (string, error) {
	pid := handle.PID
	if pid <= 0 {
		return "", nil
	}

	path := b.pathForPID(pid)
	if strings.TrimSpace(path) == "" {
		return "", nil
	}

	output, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("openclaw backend: read output %q: %w", path, err)
	}
	return string(output), nil
}

func (b *OpenClawBackend) Kill(handle Handle) error {
	return b.dispatcher.Kill(handle.PID)
}

// Cleanup releases resources for a finished dispatch.
func (b *OpenClawBackend) Cleanup(handle Handle) error {
	pid := handle.PID
	if pid <= 0 {
		return nil
	}
	b.dispatcher.CleanupProcess(pid)

	b.mu.Lock()
	delete(b.outputPath, pid)
	delete(b.logPath, pid)
	b.mu.Unlock()
	return nil
}

func (b *OpenClawBackend) pathForPID(pid int) string {
	b.mu.RLock()
	if p := strings.TrimSpace(b.logPath[pid]); p != "" {
		b.mu.RUnlock()
		return p
	}
	if p := strings.TrimSpace(b.outputPath[pid]); p != "" {
		b.mu.RUnlock()
		return p
	}
	b.mu.RUnlock()

	state := b.dispatcher.GetProcessState(pid)
	return strings.TrimSpace(state.OutputPath)
}
