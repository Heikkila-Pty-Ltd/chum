package dispatch

import "context"

// CommandBuilder constructs an exec-compatible argv for provider commands.
type CommandBuilder func(provider, model, prompt string, flags []string) ([]string, error)

var defaultCommandBuilder CommandBuilder = BuildCommand

// BuildDispatchCommand builds provider argv using the configured command builder.
func BuildDispatchCommand(provider, model, prompt string, flags []string) ([]string, error) {
	return defaultCommandBuilder(provider, model, prompt, flags)
}

// Handle uniquely identifies a running dispatch.
type Handle struct {
	PID         int
	SessionName string
	Backend     string // "headless_cli", "openclaw"
}

// DispatchOpts holds parameters for a new dispatch.
//
//nolint:revive // Retained name for compatibility across internal package boundaries.
type DispatchOpts struct {
	Agent         string
	Prompt        string
	Model         string
	ThinkingLevel string
	WorkDir       string
	CLIConfig     string // which CLI config to use (key in config.Dispatch.CLI)
	Branch        string // git branch to work on
	LogPath       string // path to write stdout/stderr
}

// DispatchStatus represents the current state of a dispatch.
//
//nolint:revive // Retained name for compatibility across internal package boundaries.
type DispatchStatus struct {
	State    string // "running", "completed", "failed", "unknown"
	ExitCode int
	Duration float64 // seconds
}

// Backend is the pluggable interface for dispatch execution.
type Backend interface {
	// Dispatch starts a new agent dispatch and returns a handle for tracking.
	Dispatch(ctx context.Context, opts DispatchOpts) (Handle, error)

	// Status checks the current status of a dispatch.
	Status(handle Handle) (DispatchStatus, error)

	// CaptureOutput retrieves the output from a dispatch.
	CaptureOutput(handle Handle) (string, error)

	// Kill forcefully terminates a running dispatch.
	Kill(handle Handle) error

	// Cleanup releases resources associated with a completed dispatch.
	Cleanup(handle Handle) error

	// Name returns the backend name for logging/config.
	Name() string
}
