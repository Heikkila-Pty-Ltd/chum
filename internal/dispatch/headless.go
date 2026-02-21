package dispatch

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/antigravity-dev/chum/internal/config"
)

type headlessProcess struct {
	cmd            *exec.Cmd
	state          string
	exitCode       int
	completedAt    time.Time
	logPath        string
	tempPromptPath string
}

// HeadlessBackend runs configured CLIs as background processes with file logs.
type HeadlessBackend struct {
	cliConfigs    map[string]config.CLIConfig
	logDir        string
	retentionDays int

	mu        sync.RWMutex
	processes map[int]*headlessProcess
}

// NewHeadlessBackend creates a backend that runs CLIs as background processes.
func NewHeadlessBackend(cliConfigs map[string]config.CLIConfig, logDir string, retentionDays int) *HeadlessBackend {
	clis := make(map[string]config.CLIConfig, len(cliConfigs))
	for k, v := range cliConfigs {
		clis[k] = v
	}
	return &HeadlessBackend{
		cliConfigs:    clis,
		logDir:        strings.TrimSpace(logDir),
		retentionDays: retentionDays,
		processes:     make(map[int]*headlessProcess),
	}
}

func (b *HeadlessBackend) Name() string {
	return "headless_cli"
}

func (b *HeadlessBackend) Dispatch(ctx context.Context, opts DispatchOpts) (Handle, error) {
	cliName := strings.TrimSpace(opts.CLIConfig)
	if cliName == "" {
		return Handle{}, fmt.Errorf("headless backend: CLI config name is required")
	}
	cliCfg, ok := b.cliConfigs[cliName]
	if !ok {
		return Handle{}, fmt.Errorf("headless backend: unknown CLI config %q", cliName)
	}
	if strings.TrimSpace(cliCfg.Cmd) == "" {
		return Handle{}, fmt.Errorf("headless backend: CLI %q has empty command", cliName)
	}

	logPath, err := b.resolveLogPath(opts)
	if err != nil {
		return Handle{}, err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return Handle{}, fmt.Errorf("headless backend: create log directory: %w", err)
	}

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return Handle{}, fmt.Errorf("headless backend: create log file: %w", err)
	}

	args, tempPromptPath, err := buildHeadlessArgs(cliCfg, opts)
	if err != nil {
		logFile.Close()
		return Handle{}, err
	}

	cmd := exec.CommandContext(ctx, cliCfg.Cmd, args...)
	if strings.TrimSpace(opts.WorkDir) != "" {
		cmd.Dir = opts.WorkDir
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	mode := strings.TrimSpace(cliCfg.PromptMode)
	if mode == "" || mode == "stdin" {
		cmd.Stdin = strings.NewReader(opts.Prompt)
	}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		if tempPromptPath != "" {
			_ = os.Remove(tempPromptPath)
		}
		return Handle{}, fmt.Errorf("headless backend: start command: %w", err)
	}
	_ = logFile.Close()

	pid := cmd.Process.Pid
	b.mu.Lock()
	b.processes[pid] = &headlessProcess{
		cmd:            cmd,
		state:          "running",
		exitCode:       -1,
		logPath:        logPath,
		tempPromptPath: tempPromptPath,
	}
	b.mu.Unlock()

	go b.waitForProcess(pid)

	return Handle{
		PID:     pid,
		Backend: b.Name(),
	}, nil
}

func (b *HeadlessBackend) waitForProcess(pid int) {
	b.mu.RLock()
	p, ok := b.processes[pid]
	if !ok {
		b.mu.RUnlock()
		return
	}
	cmd := p.cmd
	b.mu.RUnlock()

	err := cmd.Wait()

	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok = b.processes[pid]
	if !ok {
		return
	}

	p.completedAt = time.Now()
	if err == nil {
		p.state = "completed"
		p.exitCode = 0
	} else if exitErr, ok := err.(*exec.ExitError); ok {
		p.state = "failed"
		p.exitCode = exitErr.ExitCode()
	} else {
		p.state = "failed"
		p.exitCode = -1
	}

	if p.tempPromptPath != "" {
		_ = os.Remove(p.tempPromptPath)
		p.tempPromptPath = ""
	}
}

func (b *HeadlessBackend) Status(handle Handle) (DispatchStatus, error) {
	pid := handle.PID
	if pid <= 0 {
		return DispatchStatus{State: "unknown", ExitCode: -1}, nil
	}

	b.mu.RLock()
	p, ok := b.processes[pid]
	if !ok {
		b.mu.RUnlock()
		if syscall.Kill(pid, 0) == nil {
			return DispatchStatus{State: "running", ExitCode: -1}, nil
		}
		return DispatchStatus{State: "unknown", ExitCode: -1}, nil
	}

	state := p.state
	exitCode := p.exitCode
	b.mu.RUnlock()

	switch state {
		case "running":
			if syscall.Kill(pid, 0) == nil {
				return DispatchStatus{State: "running", ExitCode: -1}, nil
			}
			return DispatchStatus{State: "unknown", ExitCode: -1}, nil
		case "completed":
			return DispatchStatus{State: "completed", ExitCode: exitCode}, nil
		case "failed":
			return DispatchStatus{State: "failed", ExitCode: exitCode}, nil
		default:
			return DispatchStatus{State: "unknown", ExitCode: -1}, nil
	}
}

func (b *HeadlessBackend) CaptureOutput(handle Handle) (string, error) {
	pid := handle.PID
	if pid <= 0 {
		return "", nil
	}

	b.mu.RLock()
	p, ok := b.processes[pid]
	if !ok {
		b.mu.RUnlock()
		return "", nil
	}

	logPath := p.logPath
	b.mu.RUnlock()
	if strings.TrimSpace(logPath) == "" {
		return "", nil
	}

	output, err := os.ReadFile(logPath)
	if err != nil {
		return "", fmt.Errorf("headless backend: read output: %w", err)
	}
	return string(output), nil
}

func (b *HeadlessBackend) Kill(handle Handle) error {
	if handle.PID <= 0 {
		return nil
	}
	return KillProcess(handle.PID)
}

func (b *HeadlessBackend) Cleanup(handle Handle) error {
	pid := handle.PID
	if pid <= 0 {
		return nil
	}

	var tempPromptPath string
	var logPath string
	b.mu.Lock()
	p, ok := b.processes[pid]
	if ok {
		tempPromptPath = p.tempPromptPath
		logPath = p.logPath
		delete(b.processes, pid)
	}
	b.mu.Unlock()

	if !ok {
		return nil
	}

	if tempPromptPath != "" {
		_ = os.Remove(tempPromptPath)
	}
	if b.retentionDays <= 0 && strings.TrimSpace(logPath) != "" {
		_ = os.Remove(logPath)
	}
	return nil
}

func (b *HeadlessBackend) resolveLogPath(opts DispatchOpts) (string, error) {
	if strings.TrimSpace(opts.LogPath) != "" {
		return opts.LogPath, nil
	}

	base := b.logDir
	if strings.TrimSpace(base) == "" {
		tmp, err := os.CreateTemp("", "chum-dispatch-*.log")
		if err != nil {
			return "", fmt.Errorf("headless backend: create temp log file: %w", err)
		}
		path := tmp.Name()
		_ = tmp.Close()
		return path, nil
	}

	if err := os.MkdirAll(base, 0755); err != nil {
		return "", fmt.Errorf("headless backend: create log root: %w", err)
	}
	name := fmt.Sprintf("dispatch-%d-%s.log", time.Now().UnixNano(), sanitizeForFilename(opts.Agent))
	return filepath.Join(base, name), nil
}

func buildHeadlessArgs(cliCfg config.CLIConfig, opts DispatchOpts) ([]string, string, error) {
	flags := append([]string{}, cliCfg.Args...)

	mode := strings.TrimSpace(cliCfg.PromptMode)
	if mode == "" {
		mode = "stdin"
	}

	tempPromptPath := ""
	promptValue := opts.Prompt
	switch mode {
	case "stdin":
	case "arg":
	case "file":
		f, err := os.CreateTemp("", "chum-prompt-*.txt")
		if err != nil {
			return nil, "", fmt.Errorf("headless backend: create prompt file: %w", err)
		}
		tempPromptPath = f.Name()
		if _, err := f.WriteString(opts.Prompt); err != nil {
			_ = f.Close()
			_ = os.Remove(tempPromptPath)
			return nil, "", fmt.Errorf("headless backend: write prompt file: %w", err)
		}
		if err := f.Close(); err != nil {
			_ = os.Remove(tempPromptPath)
			return nil, "", fmt.Errorf("headless backend: close prompt file: %w", err)
		}
		promptValue = tempPromptPath
	default:
		return nil, "", fmt.Errorf("headless backend: unsupported prompt_mode %q", mode)
	}

	if strings.TrimSpace(cliCfg.ModelFlag) != "" && strings.TrimSpace(opts.Model) != "" {
		flags = append(flags, cliCfg.ModelFlag, "{model}")
	}
	if len(cliCfg.ApprovalFlags) > 0 {
		flags = append(flags, cliCfg.ApprovalFlags...)
	}

	argv, err := defaultCommandBuilder(cliCfg.Cmd, opts.Model, promptValue, flags)
	if err != nil {
		if tempPromptPath != "" {
			_ = os.Remove(tempPromptPath)
		}
		return nil, "", fmt.Errorf("headless backend: %w", err)
	}
	return argv[1:], tempPromptPath, nil
}

func sanitizeForFilename(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "dispatch"
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-", ".", "-")
	return replacer.Replace(v)
}
