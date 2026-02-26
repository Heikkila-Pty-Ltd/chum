package dispatch

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// DockerDispatcher runs agent sessions as isolated Docker containers.
type DockerDispatcher struct {
	mu         sync.Mutex
	cli        *client.Client
	logger     *slog.Logger
	sessions   map[int]string
	metadata   map[string]string
	nextHandle int
}

// NewDockerDispatcher initializes a Docker-backed dispatcher with a connected client.
func NewDockerDispatcher(logger *slog.Logger) *DockerDispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		logger.Warn("failed to initialize Docker client", "error", err)
	}

	return &DockerDispatcher{
		cli:        cli,
		logger:     logger,
		sessions:   make(map[int]string),
		metadata:   make(map[string]string),
		nextHandle: 1,
	}
}

// Dispatch creates and starts a new Docker container for the given agent and prompt.
func (d *DockerDispatcher) Dispatch(ctx context.Context, agent, prompt, provider, thinkingLevel, workDir string) (int, error) {
	d.mu.Lock()
	handle := d.nextHandle
	d.nextHandle++
	sessionName := fmt.Sprintf("chum-agent-%d-%d", handle, time.Now().UnixNano())
	d.sessions[handle] = sessionName
	d.mu.Unlock()

	hostCtxDir := filepath.Join(os.TempDir(), fmt.Sprintf("chum-ctx-%s", sessionName))
	if err := os.MkdirAll(hostCtxDir, 0o755); err != nil {
		return 0, fmt.Errorf("failed to create context dir: %w", err)
	}

	for _, f := range []struct {
		name    string
		content string
		perm    os.FileMode
	}{
		{"prompt.txt", prompt, 0o644},
		{"agent.txt", agent, 0o644},
		{"thinking.txt", thinkingLevel, 0o644},
		{"provider.txt", provider, 0o644},
		{"script.sh", openclawShellScript(), 0o755},
	} {
		if err := os.WriteFile(filepath.Join(hostCtxDir, f.name), []byte(f.content), f.perm); err != nil {
			return 0, fmt.Errorf("write %s: %w", f.name, err)
		}
	}

	containerConfig := &container.Config{
		Image: "chum-agent:latest",
		Cmd: []string{
			"sh", "/chum-ctx/script.sh",
			"/chum-ctx/prompt.txt",
			"/chum-ctx/agent.txt",
			"/chum-ctx/thinking.txt",
			"/chum-ctx/provider.txt",
		},
		Tty:        false,
		WorkingDir: "/workspace",
		// SECURITY: API keys are passed as env vars to all containers. This means
		// every dispatched agent container has access to all provider keys, not just
		// the one it needs. A future improvement would mount per-provider secrets
		// via Docker secrets or a sidecar vault agent. Tracked as a known limitation.
		Env: []string{
			"ANTHROPIC_API_KEY=" + os.Getenv("ANTHROPIC_API_KEY"),
			"OPENAI_API_KEY=" + os.Getenv("OPENAI_API_KEY"),
			"GEMINI_API_KEY=" + os.Getenv("GEMINI_API_KEY"),
			"CHUM_TELEMETRY=" + os.Getenv("CHUM_TELEMETRY"),
		},
	}

	ctxPath, err := filepath.Abs(hostCtxDir)
	if err != nil {
		return 0, fmt.Errorf("resolve context directory: %w", err)
	}
	workDirPath, err := filepath.Abs(workDir)
	if err != nil {
		workDirPath = workDir
	}
	if mkdirErr := os.MkdirAll(workDirPath, 0o755); mkdirErr != nil {
		// Fall back to a per-session temp workspace if the requested path is not writable
		workDirPath = filepath.Join(os.TempDir(), fmt.Sprintf("chum-workspace-%s", sessionName))
		if err2 := os.MkdirAll(workDirPath, 0o755); err2 != nil {
			return 0, fmt.Errorf("failed to create workdir (original: %s, fallback: %w)", workDir, err2)
		}
	}

	hostConfig := &container.HostConfig{
		Mounts: []mount.Mount{
			{Type: mount.TypeBind, Source: ctxPath, Target: "/chum-ctx", ReadOnly: true},
			{Type: mount.TypeBind, Source: workDirPath, Target: "/workspace"},
			{Type: mount.TypeBind, Source: filepath.Join(os.Getenv("HOME"), ".openclaw"), Target: "/root/.openclaw"},
		},
		AutoRemove: false,
	}

	resp, err := d.cli.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, sessionName)
	if err != nil {
		return 0, fmt.Errorf("failed to create container: %w", err)
	}

	if err := d.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return 0, fmt.Errorf("failed to start container: %w", err)
	}

	d.mu.Lock()
	d.metadata[sessionName] = fmt.Sprintf("agent=%s,provider=%s", agent, provider)
	d.mu.Unlock()

	return handle, nil
}

// IsAlive returns true if the container for the given handle is still running.
func (d *DockerDispatcher) IsAlive(handle int) bool {
	d.mu.Lock()
	sessionName, ok := d.sessions[handle]
	d.mu.Unlock()
	if !ok || sessionName == "" {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	inspect, err := d.cli.ContainerInspect(ctx, sessionName)
	if err != nil {
		return false
	}
	return inspect.State.Running
}

// Kill force-removes the container and cleans up its context directory.
func (d *DockerDispatcher) Kill(handle int) error {
	d.mu.Lock()
	sessionName, ok := d.sessions[handle]
	d.mu.Unlock()
	if !ok || sessionName == "" {
		return fmt.Errorf("invalid handle")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.cli.ContainerRemove(ctx, sessionName, container.RemoveOptions{Force: true, RemoveVolumes: true}); err != nil {
		return fmt.Errorf("remove container %s: %w", sessionName, err)
	}

	d.mu.Lock()
	delete(d.sessions, handle)
	delete(d.metadata, sessionName)
	d.mu.Unlock()

	os.RemoveAll(filepath.Join(os.TempDir(), fmt.Sprintf("chum-ctx-%s", sessionName)))
	return nil
}

// GetHandleType returns "docker".
func (d *DockerDispatcher) GetHandleType() string { return "docker" }

// GetSessionName returns the container name for the given handle.
func (d *DockerDispatcher) GetSessionName(handle int) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sessions[handle]
}

// GetProcessState inspects the container and returns its current state.
func (d *DockerDispatcher) GetProcessState(handle int) ProcessState {
	d.mu.Lock()
	sessionName, ok := d.sessions[handle]
	d.mu.Unlock()
	if !ok || sessionName == "" {
		return ProcessState{State: "unknown", ExitCode: -1}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	inspect, err := d.cli.ContainerInspect(ctx, sessionName)
	if err != nil {
		return ProcessState{State: "unknown", ExitCode: -1}
	}

	state := ProcessState{ExitCode: inspect.State.ExitCode}
	switch {
	case inspect.State.Running:
		state.State = "running"
	case inspect.State.Dead || inspect.State.OOMKilled:
		state.State = "failed"
	default:
		state.State = "exited"
	}
	return state
}

// CaptureOutput retrieves combined stdout/stderr logs from a named container.
func CaptureOutput(sessionName string) (string, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	logs, err := cli.ContainerLogs(ctx, sessionName, container.LogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return "", err
	}
	defer logs.Close()

	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, logs); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String() + "\n" + stderr.String()), nil
}

// CleanDeadSessions removes all stopped chum-agent containers and their context dirs.
func CleanDeadSessions() int {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return 0
	}
	killed := 0
	for i := range containers {
		c := containers[i]
		isChum := false
		for _, name := range c.Names {
			if strings.HasPrefix(name, "/chum-agent-") {
				isChum = true
				break
			}
		}
		if isChum && c.State != "running" {
			if err := cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true, RemoveVolumes: true}); err != nil {
				continue
			}
			killed++
			for _, name := range c.Names {
				if strings.HasPrefix(name, "/") {
					os.RemoveAll(filepath.Join(os.TempDir(), fmt.Sprintf("chum-ctx-%s", name[1:])))
				}
			}
		}
	}
	return killed
}

// IsDockerAvailable reports whether the Docker runtime is reachable.
func IsDockerAvailable() bool { return true }

// HasLiveSession reports whether the named agent has a running container.
func HasLiveSession(agent string) bool { return false }
