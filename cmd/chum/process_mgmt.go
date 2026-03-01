package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	exec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"go.temporal.io/api/serviceerror"
	tclient "go.temporal.io/sdk/client"

	"github.com/antigravity-dev/chum/internal/temporal"
)

// chumProcess holds info about a verified chum binary process.
type chumProcess struct {
	pid     int
	exePath string
	cmdline string
}

// findChumProcesses returns all running chum binary processes except the
// current process and its parent, verified via /proc/PID/exe readlink.
func findChumProcesses() []chumProcess {
	self := os.Getpid()
	parent := os.Getppid()

	out, err := exec.Command("pgrep", "-x", "chum").Output()
	if err != nil {
		// pgrep exits 1 when no matches — not an error.
		return nil
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	procs := make([]chumProcess, 0, len(lines))
	for _, line := range lines {
		pid, parseErr := strconv.Atoi(strings.TrimSpace(line))
		if parseErr != nil || pid == self || pid == parent {
			continue
		}

		proc, findErr := os.FindProcess(pid)
		if findErr != nil {
			continue
		}
		if sigErr := proc.Signal(syscall.Signal(0)); sigErr != nil {
			continue
		}

		// Verify via /proc that this is actually a chum binary.
		exePath, readErr := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
		if readErr != nil || !strings.HasSuffix(filepath.Base(exePath), "chum") {
			continue
		}

		cmdline, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		cmdStr := strings.TrimSpace(strings.ReplaceAll(string(cmdline), "\x00", " "))

		procs = append(procs, chumProcess{pid: pid, exePath: exePath, cmdline: cmdStr})
	}
	return procs
}

// killAllChumProcesses finds and kills ALL chum binary processes except the current one.
// Sends SIGTERM to all processes first, then waits once, then SIGKILLs survivors.
// Returns the number of processes killed.
func killAllChumProcesses(logger *slog.Logger) int {
	procs := findChumProcesses()
	if len(procs) == 0 {
		return 0
	}

	// Phase 1: SIGTERM all at once.
	handles := make([]*os.Process, 0, len(procs))
	for _, p := range procs {
		logger.Info("killing chum process", "pid", p.pid, "exe", p.exePath, "cmd", p.cmdline)
		proc, err := os.FindProcess(p.pid)
		if err != nil {
			continue
		}
		_ = proc.Signal(syscall.SIGTERM)
		handles = append(handles, proc)
	}

	// Phase 2: Single combined wait (up to 5s) for all to exit.
	time.Sleep(2 * time.Second)
	for _, proc := range handles {
		if sigErr := proc.Signal(syscall.Signal(0)); sigErr == nil {
			// Still alive after initial wait — give a bit more time.
			time.Sleep(3 * time.Second)
			break
		}
	}

	// Phase 3: SIGKILL any survivors.
	killed := len(handles)
	for _, proc := range handles {
		if sigErr := proc.Signal(syscall.Signal(0)); sigErr == nil {
			logger.Warn("process did not exit after SIGTERM, sending SIGKILL", "pid", proc.Pid)
			_ = proc.Signal(syscall.SIGKILL)
		}
	}

	// Clean up PID file.
	pidPath := filepath.Join(dataDir(), "chum.pid")
	if _, statErr := os.Stat(pidPath); statErr == nil {
		os.Remove(pidPath)
		logger.Info("removed pid file", "path", pidPath)
	}

	return killed
}

// terminateTemporalWorkflows terminates all running ChumAgentWorkflows via the
// Temporal batch API. Best-effort: logs errors but does not fail the caller.
func terminateTemporalWorkflows(logger *slog.Logger) {
	namespace := resolveTemporalNamespace()
	hostPort := strings.TrimSpace(os.Getenv("TEMPORAL_HOST_PORT"))
	if hostPort == "" {
		hostPort = temporal.DefaultTemporalHostPort
	}

	tc, err := tclient.Dial(tclient.Options{
		HostPort:  hostPort,
		Namespace: namespace,
	})
	if err != nil {
		logger.Warn("cannot connect to temporal to terminate workflows", "error", err)
		return
	}
	defer tc.Close()

	query := temporal.ChumAgentRunningVisibilityQuery()
	opID, termErr := temporal.StartTerminateAgentWorkflows(context.Background(), tc.WorkflowService(), namespace, query)
	if termErr != nil {
		// serviceerror.NotFound means no matching workflows — that's fine.
		var notFound *serviceerror.NotFound
		if errors.As(termErr, &notFound) {
			logger.Info("no running temporal workflows to terminate")
			return
		}
		logger.Warn("failed to terminate temporal workflows", "error", termErr)
		return
	}
	logger.Info("temporal workflows terminated", "operation_id", opID)
}

// acquirePIDFile writes a new PID file after verifying no other instance holds it.
func acquirePIDFile(pidPath, exe string, logger *slog.Logger) error {
	data, err := os.ReadFile(pidPath)
	if err == nil {
		lines := strings.SplitN(string(data), "\n", 2)
		if pid, parseErr := strconv.Atoi(strings.TrimSpace(lines[0])); parseErr == nil {
			if process, findErr := os.FindProcess(pid); findErr == nil {
				if signalErr := process.Signal(syscall.Signal(0)); signalErr == nil {
					otherBinary := "unknown"
					if len(lines) > 1 {
						otherBinary = strings.TrimSpace(lines[1])
					}
					return fmt.Errorf("pid %d is still running (binary: %s, this binary: %s)", pid, otherBinary, exe)
				}
			}
		}
		logger.Info("removing stale pid file", "pidfile", pidPath)
	}

	content := fmt.Sprintf("%d\n%s\n", os.Getpid(), exe)
	return os.WriteFile(pidPath, []byte(content), 0644)
}

// execCmd is a minimal exec wrapper that doesn't import os/exec (keeping the
// main package's import footprint small).
type execCmd struct {
	path string
	args []string
	dir  string
}

func (c *execCmd) run() (string, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	proc, err := os.StartProcess(c.path, c.args, &os.ProcAttr{
		Dir:   c.dir,
		Env:   os.Environ(),
		Files: []*os.File{os.Stdin, w, w},
	})
	if err != nil {
		w.Close()
		r.Close()
		return "", err
	}
	w.Close()
	outBytes, _ := io.ReadAll(r)
	r.Close()
	state, waitErr := proc.Wait()
	if waitErr != nil {
		return string(outBytes), waitErr
	}
	if !state.Success() {
		return string(outBytes), fmt.Errorf("exit code %d", state.ExitCode())
	}
	return string(outBytes), nil
}

// rebuildBinary builds the chum binary from source. It locates the source
// directory by walking up from the current executable looking for go.mod,
// then runs "go build" to produce a fresh binary at the same path.
// This prevents the recurring stale-binary-on-restart problem.
func rebuildBinary(logger *slog.Logger) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("cannot find own executable: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("cannot resolve executable symlink: %w", err)
	}

	// Walk up from executable to find the module root (directory containing go.mod).
	srcDir := filepath.Dir(exe)
	for d := srcDir; d != "/" && d != "."; d = filepath.Dir(d) {
		if _, statErr := os.Stat(filepath.Join(d, "go.mod")); statErr == nil {
			srcDir = d
			break
		}
	}

	logger.Info("rebuilding chum from source", "src", srcDir, "target", exe)

	goExe, lookErr := findGo()
	if lookErr != nil {
		return "", lookErr
	}

	cmd := &execCmd{
		path: goExe,
		args: []string{goExe, "build", "-o", exe, "./cmd/chum"},
		dir:  srcDir,
	}
	out, buildErr := cmd.run()
	if buildErr != nil {
		return "", fmt.Errorf("go build failed: %w\n%s", buildErr, out)
	}

	logger.Info("rebuild complete", "binary", exe)
	return exe, nil
}

// findGo locates the go binary, checking common non-standard locations.
func findGo() (string, error) {
	// Check PATH first.
	if p, err := exec.LookPath("go"); err == nil {
		return p, nil
	}
	// Common install locations.
	for _, candidate := range []string{
		filepath.Join(os.Getenv("HOME"), ".local", "bin", "go"),
		"/usr/local/go/bin/go",
		"/usr/local/bin/go",
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("go binary not found in PATH or common locations")
}
