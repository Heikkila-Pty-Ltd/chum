package dispatch

import (
	"bytes"
	"context"
	"fmt"
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

type DockerDispatcher struct {
	mu         sync.Mutex
	cli        *client.Client
	sessions   map[int]string    
	metadata   map[string]string 
	nextHandle int
}

func NewDockerDispatcher() *DockerDispatcher {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		fmt.Printf("Warning: failed to initialize Docker client: %v\n", err)
	}

	return &DockerDispatcher{
		cli:        cli,
		sessions:   make(map[int]string),
		metadata:   make(map[string]string),
		nextHandle: 1, 
	}
}

func (d *DockerDispatcher) Dispatch(ctx context.Context, agent string, prompt string, provider string, thinkingLevel string, workDir string) (int, error) {
	d.mu.Lock()
	handle := d.nextHandle
	d.nextHandle++
	sessionName := fmt.Sprintf("chum-agent-%d-%d", handle, time.Now().UnixNano())
	d.sessions[handle] = sessionName
	d.mu.Unlock()

	hostCtxDir := filepath.Join(os.TempDir(), fmt.Sprintf("chum-ctx-%s", sessionName))
	if err := os.MkdirAll(hostCtxDir, 0755); err != nil {
		return 0, fmt.Errorf("failed to create context dir: %w", err)
	}

	os.WriteFile(filepath.Join(hostCtxDir, "prompt.txt"), []byte(prompt), 0644)
	os.WriteFile(filepath.Join(hostCtxDir, "agent.txt"), []byte(agent), 0644)
	os.WriteFile(filepath.Join(hostCtxDir, "thinking.txt"), []byte(thinkingLevel), 0644)
	os.WriteFile(filepath.Join(hostCtxDir, "provider.txt"), []byte(provider), 0644)
	os.WriteFile(filepath.Join(hostCtxDir, "script.sh"), []byte(openclawShellScript()), 0755)

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
		Env: []string{
			"ANTHROPIC_API_KEY=" + os.Getenv("ANTHROPIC_API_KEY"),
			"OPENAI_API_KEY=" + os.Getenv("OPENAI_API_KEY"),
			"GEMINI_API_KEY=" + os.Getenv("GEMINI_API_KEY"),
			"CHUM_TELEMETRY=" + os.Getenv("CHUM_TELEMETRY"),
		},
	}

	ctxPath, _ := filepath.Abs(hostCtxDir)
	workDirPath, _ := filepath.Abs(workDir)
	if err := os.MkdirAll(workDirPath, 0755); err != nil {
		// Fall back to a per-session temp workspace if the requested path is not writable
		workDirPath = filepath.Join(os.TempDir(), fmt.Sprintf("chum-workspace-%s", sessionName))
		if err2 := os.MkdirAll(workDirPath, 0755); err2 != nil {
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

func (d *DockerDispatcher) IsAlive(handle int) bool {
	d.mu.Lock()
	sessionName, ok := d.sessions[handle]
	d.mu.Unlock()
	if !ok || sessionName == "" { return false }

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	inspect, err := d.cli.ContainerInspect(ctx, sessionName)
	if err != nil { return false }
	return inspect.State.Running
}

func (d *DockerDispatcher) Kill(handle int) error {
	d.mu.Lock()
	sessionName, ok := d.sessions[handle]
	d.mu.Unlock()
	if !ok || sessionName == "" { return fmt.Errorf("invalid handle") }

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	d.cli.ContainerRemove(ctx, sessionName, container.RemoveOptions{Force: true, RemoveVolumes: true})

	d.mu.Lock()
	delete(d.sessions, handle)
	delete(d.metadata, sessionName)
	d.mu.Unlock()

	os.RemoveAll(filepath.Join(os.TempDir(), fmt.Sprintf("chum-ctx-%s", sessionName)))
	return nil
}

func (d *DockerDispatcher) GetHandleType() string { return "docker" }

func (d *DockerDispatcher) GetSessionName(handle int) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.sessions[handle]
}

func (d *DockerDispatcher) GetProcessState(handle int) ProcessState {
	d.mu.Lock()
	sessionName, ok := d.sessions[handle]
	d.mu.Unlock()
	if !ok || sessionName == "" { return ProcessState{State: "unknown", ExitCode: -1} }

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	inspect, err := d.cli.ContainerInspect(ctx, sessionName)
	if err != nil { return ProcessState{State: "unknown", ExitCode: -1} }

	state := ProcessState{ExitCode: inspect.State.ExitCode}
	if inspect.State.Running {
		state.State = "running"
	} else if inspect.State.Dead || inspect.State.OOMKilled {
		state.State = "failed"
	} else {
		state.State = "exited"
	}
	return state
}

func CaptureOutput(sessionName string) (string, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil { return "", err }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	logs, err := cli.ContainerLogs(ctx, sessionName, container.LogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil { return "", err }
	defer logs.Close()

	var stdout, stderr bytes.Buffer
	stdcopy.StdCopy(&stdout, &stderr, logs)
	return strings.TrimSpace(stdout.String() + "\n" + stderr.String()), nil
}

func CleanDeadSessions() int {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil { return 0 }
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	containers, _ := cli.ContainerList(ctx, container.ListOptions{All: true})
	killed := 0
	for _, c := range containers {
		isChum := false
		for _, name := range c.Names {
			if strings.HasPrefix(name, "/chum-agent-") { isChum = true; break }
		}
		if isChum && c.State != "running" {
			cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true, RemoveVolumes: true})
			killed++
			for _, name := range c.Names {
				if strings.HasPrefix(name, "/") { os.RemoveAll(filepath.Join(os.TempDir(), fmt.Sprintf("chum-ctx-%s", name[1:]))) }
			}
		}
	}
	return killed
}

func IsDockerAvailable() bool { return true }
func HasLiveSession(agent string) bool { return false }
