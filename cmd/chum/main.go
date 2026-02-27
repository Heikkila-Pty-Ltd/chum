package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	exec "os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"errors"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	tclient "go.temporal.io/sdk/client"

	"github.com/antigravity-dev/chum/internal/api"
	"github.com/antigravity-dev/chum/internal/beadsfork"
	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/graph"
	"github.com/antigravity-dev/chum/internal/matrix"
	"github.com/antigravity-dev/chum/internal/store"
	"github.com/antigravity-dev/chum/internal/temporal"
)

func configureLogger(logLevel string, useDev bool) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(logLevel)) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	opts := &slog.HandlerOptions{Level: level}
	if useDev {
		return slog.New(slog.NewTextHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, opts))
}

func registerTemporalSearchAttributes(ctx context.Context, temporalHostPort, temporalNamespace string, logger *slog.Logger) error {
	if strings.TrimSpace(temporalHostPort) == "" {
		temporalHostPort = temporal.DefaultTemporalHostPort
	}
	if strings.TrimSpace(temporalNamespace) == "" {
		temporalNamespace = tclient.DefaultNamespace
	}

	opts := tclient.Options{
		HostPort:  temporalHostPort,
		Namespace: temporalNamespace,
	}
	tc, err := tclient.Dial(opts)
	if err != nil {
		return err
	}
	defer tc.Close()

	logger.Info("registering temporal search attributes", "host", temporalHostPort, "namespace", temporalNamespace)
	if err := temporal.RegisterChumSearchAttributesWithNamespace(ctx, tc, temporalNamespace); err != nil {
		return err
	}
	logger.Info("temporal search attributes ready", "host", temporalHostPort, "namespace", temporalNamespace)
	return nil
}

func resolveTemporalNamespace() string {
	namespace := strings.TrimSpace(os.Getenv("TEMPORAL_NAMESPACE"))
	if namespace == "" {
		return tclient.DefaultNamespace
	}
	return namespace
}

func validateRuntimeConfigReload(oldCfg, newCfg *config.Config) error {
	if oldCfg == nil || newCfg == nil {
		return fmt.Errorf("invalid config state during reload")
	}

	oldStateDB := strings.TrimSpace(oldCfg.General.StateDB)
	newStateDB := strings.TrimSpace(newCfg.General.StateDB)
	if oldStateDB != newStateDB {
		return fmt.Errorf("state_db changed (%q -> %q) and requires restart", oldStateDB, newStateDB)
	}

	oldAPIBind := strings.TrimSpace(oldCfg.API.Bind)
	newAPIBind := strings.TrimSpace(newCfg.API.Bind)
	if oldAPIBind != newAPIBind {
		return fmt.Errorf("api.bind changed (%q -> %q) and requires restart", oldAPIBind, newAPIBind)
	}
	return nil
}

type adminBatchOps interface {
	Drain(context.Context, string) (string, error)
	Resume(context.Context, string) (string, error)
	Reset(context.Context, string) (string, error)
	Terminate(context.Context, string) (string, error)
}

type adminBatchOpsRunner struct {
	drain     func(context.Context, string) (string, error)
	resume    func(context.Context, string) (string, error)
	reset     func(context.Context, string) (string, error)
	terminate func(context.Context, string) (string, error)
}

func (a *adminBatchOpsRunner) Drain(ctx context.Context, query string) (string, error) {
	return a.drain(ctx, query)
}

func (a *adminBatchOpsRunner) Resume(ctx context.Context, query string) (string, error) {
	return a.resume(ctx, query)
}

func (a *adminBatchOpsRunner) Reset(ctx context.Context, query string) (string, error) {
	return a.reset(ctx, query)
}

func (a *adminBatchOpsRunner) Terminate(ctx context.Context, query string) (string, error) {
	return a.terminate(ctx, query)
}

func parseAdminSubcommand(args []string, defaultQuery string) (command string, query string, err error) {
	if len(args) < 2 {
		return "", "", fmt.Errorf("admin requires a subcommand: drain | resume | reset | terminate")
	}

	command = strings.ToLower(strings.TrimSpace(args[1]))
	switch command {
	case "drain", "resume", "reset", "terminate":
	default:
		return "", "", fmt.Errorf("unknown admin command %q", command)
	}

	fs := flag.NewFlagSet(fmt.Sprintf("admin %s", command), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	q := fs.String("query", "", "visibility query")
	if err := fs.Parse(args[2:]); err != nil {
		return "", "", err
	}
	if fs.NArg() > 0 {
		return "", "", fmt.Errorf("unexpected arguments for admin %s: %v", command, fs.Args())
	}

	qry := strings.TrimSpace(*q)
	if (command == "reset" || command == "terminate") && qry == "" {
		return "", "", fmt.Errorf("--query is required for %s", command)
	}
	if qry == "" {
		qry = defaultQuery
	}

	query = qry
	return command, query, nil
}

func runAdminAction(ctx context.Context, command, query string, ops adminBatchOps) (string, error) {
	switch command {
	case "drain":
		return ops.Drain(ctx, query)
	case "resume":
		return ops.Resume(ctx, query)
	case "reset":
		return ops.Reset(ctx, query)
	case "terminate":
		return ops.Terminate(ctx, query)
	default:
		return "", fmt.Errorf("unknown admin command %q", command)
	}
}

func runAdminMode(args []string, logger *slog.Logger) error {
	adminFS := flag.NewFlagSet("admin", flag.ContinueOnError)
	adminFS.SetOutput(io.Discard)
	configPath := adminFS.String("config", "chum.toml", "path to config file")
	// args is ["./chum", "admin", "resume", ...], we want to parse starting from index 2
	if err := adminFS.Parse(args[2:]); err != nil {
		return err
	}

	args = append([]string{"admin"}, adminFS.Args()...)
	command, query, err := parseAdminSubcommand(args, temporal.ChumAgentRunningVisibilityQuery())
	if err != nil {
		return err
	}

	cfgManager, err := config.LoadManager(*configPath)
	if err != nil {
		return err
	}
	cfg := cfgManager.Get()
	if cfg == nil {
		return fmt.Errorf("failed to load config snapshot")
	}

	host := strings.TrimSpace(cfg.General.TemporalHostPort)
	if host == "" {
		host = temporal.DefaultTemporalHostPort
	}

	temporalNamespace := resolveTemporalNamespace()

	tc, err := tclient.Dial(tclient.Options{
		HostPort:  host,
		Namespace: temporalNamespace,
	})
	if err != nil {
		return err
	}
	defer tc.Close()

	ops := &adminBatchOpsRunner{
		drain: func(ctx context.Context, q string) (string, error) {
			return temporal.StartDrainAgentWorkflows(ctx, tc.WorkflowService(), temporalNamespace, q)
		},
		resume: func(ctx context.Context, q string) (string, error) {
			return temporal.StartResumeAgentWorkflows(ctx, tc.WorkflowService(), temporalNamespace, q)
		},
		reset: func(ctx context.Context, q string) (string, error) {
			return temporal.StartResetAgentWorkflows(ctx, tc.WorkflowService(), temporalNamespace, q)
		},
		terminate: func(ctx context.Context, q string) (string, error) {
			return temporal.StartTerminateAgentWorkflows(ctx, tc.WorkflowService(), temporalNamespace, q)
		},
	}

	operationID, err := runAdminAction(context.Background(), command, query, ops)
	if err != nil {
		return err
	}

	logger.Info("admin command submitted",
		"command", command,
		"query", query,
		"operation_id", operationID,
		"namespace", temporalNamespace,
		"host", host,
	)
	return nil
}

// killAllChumProcesses finds and kills ALL chum binary processes except the current one.
// Only matches processes whose executable is a chum binary (not shell commands that
// happen to mention "chum" in their arguments).
// Returns the number of processes killed.
func killAllChumProcesses(logger *slog.Logger) int {
	self := os.Getpid()
	killed := 0

	out, err := exec.Command("pgrep", "-f", `chum`).Output()
	if err != nil {
		// pgrep exits 1 when no matches — not an error.
		return 0
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pid, parseErr := strconv.Atoi(strings.TrimSpace(line))
		if parseErr != nil || pid == self || pid == os.Getppid() {
			continue
		}

		proc, findErr := os.FindProcess(pid)
		if findErr != nil {
			continue
		}
		if sigErr := proc.Signal(syscall.Signal(0)); sigErr != nil {
			continue
		}

		// Read the executable path via /proc to confirm it's actually a chum binary.
		exePath, readErr := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
		if readErr != nil {
			continue
		}
		if !strings.HasSuffix(filepath.Base(exePath), "chum") {
			continue
		}

		// Read cmdline for logging.
		cmdline, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		cmdStr := strings.ReplaceAll(string(cmdline), "\x00", " ")

		logger.Info("killing chum process", "pid", pid, "exe", exePath, "cmd", strings.TrimSpace(cmdStr))
		_ = proc.Signal(syscall.SIGTERM)

		dead := false
		for i := 0; i < 10; i++ {
			time.Sleep(500 * time.Millisecond)
			if sigErr := proc.Signal(syscall.Signal(0)); sigErr != nil {
				dead = true
				break
			}
		}
		if !dead {
			logger.Warn("process did not exit after SIGTERM, sending SIGKILL", "pid", pid)
			_ = proc.Signal(syscall.SIGKILL)
		}
		killed++
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

// runStopCommand kills ALL chum processes and terminates running Temporal workflows.
// Usage: chum stop
//
//nolint:unparam // error return kept for interface consistency with other run*Command funcs
func runStopCommand(logger *slog.Logger) error {
	fmt.Println("stopping all chum processes...")

	killed := killAllChumProcesses(logger)
	if killed == 0 {
		fmt.Println("no chum processes found")
	} else {
		fmt.Printf("killed %d chum process(es)\n", killed)
	}

	fmt.Println("terminating running temporal workflows...")
	terminateTemporalWorkflows(logger)

	fmt.Println("chum stopped")
	return nil
}

// runStatusCommand shows the current state of chum processes and Temporal workflows.
// Usage: chum status
func runStatusCommand(_ *slog.Logger) error {
	// 1. PID file check.
	pidPath := filepath.Join(dataDir(), "chum.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		fmt.Println("pid file:   not found")
	} else {
		lines := strings.SplitN(string(data), "\n", 2)
		pid, parseErr := strconv.Atoi(strings.TrimSpace(lines[0]))
		if parseErr != nil {
			fmt.Println("pid file:   invalid")
		} else {
			proc, findErr := os.FindProcess(pid)
			alive := findErr == nil && proc.Signal(syscall.Signal(0)) == nil
			binary := "unknown"
			if len(lines) > 1 {
				binary = strings.TrimSpace(lines[1])
			}
			status := "dead (stale pid file)"
			if alive {
				status = "running"
			}
			fmt.Printf("pid file:   %d (%s) — %s\n", pid, binary, status)
		}
	}

	// 2. All chum processes via pgrep, verified by /proc/PID/exe.
	out, pgrepErr := exec.Command("pgrep", "-f", `chum`).Output()
	if pgrepErr != nil {
		fmt.Println("processes:  none")
	} else {
		self := os.Getpid()
		var procs []string
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(line))
			if parseErr != nil || pid == self || pid == os.Getppid() {
				continue
			}
			exePath, readErr := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
			if readErr != nil || !strings.HasSuffix(filepath.Base(exePath), "chum") {
				continue
			}
			cmdline, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
			cmdStr := strings.ReplaceAll(string(cmdline), "\x00", " ")
			procs = append(procs, fmt.Sprintf("  pid %d: %s", pid, strings.TrimSpace(cmdStr)))
		}
		if len(procs) == 0 {
			fmt.Println("processes:  none")
		} else {
			fmt.Printf("processes:  %d found\n", len(procs))
			for _, p := range procs {
				fmt.Println(p)
			}
		}
	}

	// 3. Temporal connection check.
	namespace := resolveTemporalNamespace()
	hostPort := strings.TrimSpace(os.Getenv("TEMPORAL_HOST_PORT"))
	if hostPort == "" {
		hostPort = temporal.DefaultTemporalHostPort
	}

	tc, dialErr := tclient.Dial(tclient.Options{
		HostPort:  hostPort,
		Namespace: namespace,
	})
	if dialErr != nil {
		fmt.Printf("temporal:   cannot connect (%s)\n", dialErr)
		return nil
	}
	defer tc.Close()

	query := temporal.ChumAgentRunningVisibilityQuery()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, listErr := tc.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
		Query: query,
	})
	if listErr != nil {
		fmt.Printf("temporal:   connected but query failed (%s)\n", listErr)
		return nil
	}

	fmt.Printf("temporal:   connected (%s/%s)\n", hostPort, namespace)
	fmt.Printf("workflows:  %d running\n", len(resp.Executions))
	for _, wf := range resp.Executions {
		fmt.Printf("  %s (run: %s)\n", wf.Execution.WorkflowId, wf.Execution.RunId[:8])
	}

	return nil
}

// runDoctorCommand diagnoses common chum problems: orphaned processes, stale PID
// files, Temporal connectivity, database health, and log errors.
// Usage: chum doctor [--config chum.toml]
func runDoctorCommand(args []string, _ *slog.Logger) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	configPath := fs.String("config", "chum.toml", "path to config file")
	if len(args) > 2 {
		if parseErr := fs.Parse(args[2:]); parseErr != nil {
			return parseErr
		}
	}

	issues := 0
	fmt.Println("chum doctor — diagnosing system health")
	fmt.Println()

	// Check 1: Orphaned processes (verified via /proc/PID/exe).
	fmt.Println("[processes]")
	self := os.Getpid()
	out, pgrepErr := exec.Command("pgrep", "-f", `chum`).Output()
	if pgrepErr != nil {
		fmt.Println("  OK: no chum processes running")
	} else {
		var orphans []string
		pidPath := filepath.Join(dataDir(), "chum.pid")
		pidData, _ := os.ReadFile(pidPath)
		trackedPID := -1
		if pidData != nil {
			lines := strings.SplitN(string(pidData), "\n", 2)
			trackedPID, _ = strconv.Atoi(strings.TrimSpace(lines[0]))
		}

		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(line))
			if parseErr != nil || pid == self || pid == os.Getppid() {
				continue
			}
			exePath, readErr := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
			if readErr != nil || !strings.HasSuffix(filepath.Base(exePath), "chum") {
				continue
			}
			cmdline, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
			cmdStr := strings.TrimSpace(strings.ReplaceAll(string(cmdline), "\x00", " "))
			if pid == trackedPID {
				fmt.Printf("  OK: tracked process %d is alive\n", pid)
			} else {
				orphans = append(orphans, fmt.Sprintf("  WARN: orphaned process pid %d: %s", pid, cmdStr))
				issues++
			}
		}
		for _, o := range orphans {
			fmt.Println(o)
		}
		if len(orphans) > 0 {
			fmt.Println("  FIX: run 'chum stop' to kill all processes")
		}
	}

	// Check 2: PID file health.
	fmt.Println("\n[pid file]")
	pidPath := filepath.Join(dataDir(), "chum.pid")
	pidData, pidErr := os.ReadFile(pidPath)
	if pidErr != nil {
		fmt.Println("  OK: no pid file (chum not running)")
	} else {
		lines := strings.SplitN(string(pidData), "\n", 2)
		pid, parseErr := strconv.Atoi(strings.TrimSpace(lines[0]))
		if parseErr != nil {
			fmt.Println("  WARN: pid file contains invalid data")
			issues++
		} else {
			proc, findErr := os.FindProcess(pid)
			alive := findErr == nil && proc.Signal(syscall.Signal(0)) == nil
			if alive {
				fmt.Printf("  OK: pid %d is alive\n", pid)
			} else {
				fmt.Printf("  WARN: stale pid file (process %d is dead)\n", pid)
				fmt.Println("  FIX: run 'chum stop' to clean up")
				issues++
			}
		}
	}

	// Check 3: Temporal connectivity.
	fmt.Println("\n[temporal]")
	namespace := resolveTemporalNamespace()
	hostPort := strings.TrimSpace(os.Getenv("TEMPORAL_HOST_PORT"))
	if hostPort == "" {
		hostPort = temporal.DefaultTemporalHostPort
	}

	tc, dialErr := tclient.Dial(tclient.Options{
		HostPort:  hostPort,
		Namespace: namespace,
	})
	if dialErr != nil {
		fmt.Printf("  FAIL: cannot connect to Temporal at %s: %s\n", hostPort, dialErr)
		fmt.Println("  FIX: ensure Temporal server is running")
		issues++
	} else {
		defer tc.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		query := temporal.ChumAgentRunningVisibilityQuery()
		resp, listErr := tc.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
			Query: query,
		})
		if listErr != nil {
			fmt.Printf("  WARN: connected but workflow query failed: %s\n", listErr)
			issues++
		} else {
			fmt.Printf("  OK: connected (%s/%s), %d running workflow(s)\n", hostPort, namespace, len(resp.Executions))
		}
	}

	// Check 4: Database health.
	fmt.Println("\n[database]")
	cfgManager, cfgErr := config.LoadManager(*configPath)
	if cfgErr != nil {
		fmt.Printf("  WARN: cannot load config %s: %s\n", *configPath, cfgErr)
		issues++
	} else {
		cfg := cfgManager.Get()
		dbPath := config.ExpandHome(cfg.General.StateDB)
		if _, statErr := os.Stat(dbPath); statErr != nil {
			fmt.Printf("  WARN: database not found at %s\n", dbPath)
			issues++
		} else {
			fi, _ := os.Stat(dbPath)
			fmt.Printf("  OK: %s (%.1f MB)\n", dbPath, float64(fi.Size())/(1024*1024))
		}
	}

	// Check 5: Log tail for recent errors.
	fmt.Println("\n[recent logs]")
	logPath := filepath.Join(dataDir(), "worker.log")
	if logData, logErr := os.ReadFile(logPath); logErr == nil {
		logLines := strings.Split(string(logData), "\n")
		errorCount := 0
		start := 0
		if len(logLines) > 50 {
			start = len(logLines) - 50
		}
		for _, line := range logLines[start:] {
			if strings.Contains(line, "level=ERROR") || strings.Contains(line, `"level":"ERROR"`) {
				errorCount++
			}
		}
		if errorCount > 0 {
			fmt.Printf("  WARN: %d error(s) in last 50 log lines\n", errorCount)
			fmt.Printf("  FIX: check %s\n", logPath)
			issues++
		} else {
			fmt.Println("  OK: no recent errors")
		}
	} else {
		fmt.Printf("  OK: no log file at %s\n", logPath)
	}

	// Summary.
	fmt.Println()
	if issues == 0 {
		fmt.Println("all checks passed")
	} else {
		fmt.Printf("%d issue(s) found\n", issues)
	}

	return nil
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

// runRestartCommand kills all existing chum processes and Temporal workflows,
// rebuilds from source, and starts a fresh worker.
// Usage: chum restart [--config chum.toml] [--systemd]
func runRestartCommand(args []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("restart", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "chum.toml", "path to config file")
	useSystemd := fs.Bool("systemd", false, "restart via systemctl --user instead of PID-based restart")
	if len(args) > 2 {
		if parseErr := fs.Parse(args[2:]); parseErr != nil {
			logger.Warn("failed to parse restart flags", "error", parseErr)
		}
	}

	// Step 1: Kill everything — all processes and workflows.
	fmt.Println("killing all chum processes...")
	killed := killAllChumProcesses(logger)
	fmt.Printf("killed %d process(es)\n", killed)

	fmt.Println("terminating running temporal workflows...")
	terminateTemporalWorkflows(logger)

	// Step 2: Rebuild from source.
	fmt.Println("rebuilding from source...")
	exe, err := rebuildBinary(logger)
	if err != nil {
		return fmt.Errorf("rebuild failed: %w", err)
	}
	fmt.Printf("built %s\n", exe)

	// Step 3: Start fresh.
	if *useSystemd {
		fmt.Println("restarting via systemctl --user...")
		cmd := &execCmd{
			path: "/bin/systemctl",
			args: []string{"systemctl", "--user", "restart", "chum.service"},
			dir:  "/",
		}
		if out, restartErr := cmd.run(); restartErr != nil {
			return fmt.Errorf("systemctl restart failed: %w\n%s", restartErr, out)
		}
		fmt.Println("chum service restarted via systemd")
		return nil
	}

	logPath := filepath.Join(dataDir(), "worker.log")
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("cannot open log file %s: %w", logPath, err)
	}

	workerArgs := []string{"worker", "--config", *configPath}
	attr := &os.ProcAttr{
		Dir:   filepath.Dir(*configPath),
		Env:   os.Environ(),
		Files: []*os.File{os.Stdin, logFile, logFile},
		Sys:   &syscall.SysProcAttr{Setsid: true},
	}

	proc, err := os.StartProcess(exe, append([]string{exe}, workerArgs...), attr)
	if err != nil {
		logFile.Close()
		return fmt.Errorf("failed to start worker: %w", err)
	}

	fmt.Printf("chum worker started (pid %d, log %s)\n", proc.Pid, logPath)
	if releaseErr := proc.Release(); releaseErr != nil {
		logger.Warn("failed to release process handle", "error", releaseErr)
	}
	logFile.Close()

	// Verify it's running.
	time.Sleep(2 * time.Second)
	pidPath := filepath.Join(dataDir(), "chum.pid")
	if pidData, readErr := os.ReadFile(pidPath); readErr == nil {
		lines := strings.SplitN(string(pidData), "\n", 2)
		fmt.Printf("confirmed running (pid %s)\n", strings.TrimSpace(lines[0]))
	}

	return nil
}

// runReviewPRCommand triggers a cross-model PR review via Temporal.
// Usage: chum review-pr <number> [--reviewer agent] [--config chum.toml]
func runReviewPRCommand(args []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("review-pr", flag.ContinueOnError)
	reviewer := fs.String("reviewer", "", "reviewer agent (default: auto-select cross-model)")
	author := fs.String("author", "", "author agent for cross-model selection (default: claude)")
	workspaceFlag := fs.String("workspace", "", "workspace directory containing the git repo (default: first enabled project)")
	configPath := fs.String("config", "chum.toml", "path to config file")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: chum review-pr <pr-number> [--reviewer agent] [--author agent]")
	}
	prNumber, err := strconv.Atoi(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("invalid PR number %q: %w", fs.Arg(0), err)
	}

	cfgManager, err := config.LoadManager(*configPath)
	if err != nil {
		return err
	}
	cfg := cfgManager.Get()

	host := strings.TrimSpace(cfg.General.TemporalHostPort)
	if host == "" {
		host = temporal.DefaultTemporalHostPort
	}
	temporalNamespace := resolveTemporalNamespace()

	tc, err := tclient.Dial(tclient.Options{
		HostPort:  host,
		Namespace: temporalNamespace,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to Temporal: %w", err)
	}
	defer tc.Close()

	// Resolve workspace: explicit flag > first enabled project > cwd
	workspace := *workspaceFlag
	if workspace == "" {
		for _, proj := range cfg.Projects {
			if proj.Enabled && proj.Workspace != "" {
				workspace = config.ExpandHome(proj.Workspace)
				break
			}
		}
	}
	if workspace == "" {
		workspace = "."
	}

	authorAgent := *author
	if authorAgent == "" {
		authorAgent = "claude"
	}

	workflowID := fmt.Sprintf("pr-review-%d-manual-%d", prNumber, time.Now().Unix())
	req := temporal.PRReviewRequest{
		PRNumber:  prNumber,
		Workspace: workspace,
		Reviewer:  *reviewer,
		Author:    authorAgent,
	}

	run, err := tc.ExecuteWorkflow(context.Background(), tclient.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: temporal.DefaultTaskQueue,
	}, temporal.PRReviewWorkflow, req)
	if err != nil {
		return fmt.Errorf("failed to start PR review workflow: %w", err)
	}

	logger.Info("PR review workflow started",
		"workflow_id", run.GetID(),
		"run_id", run.GetRunID(),
		"pr", prNumber,
		"reviewer", *reviewer,
		"author", authorAgent,
	)
	fmt.Printf("PR review started: workflow_id=%s pr=#%d\n", run.GetID(), prNumber)
	return nil
}

// dataDir returns the CHUM data directory (~/.local/share/chum).
func dataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	dir := filepath.Join(home, ".local", "share", "chum")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if len(os.Args) > 1 && os.Args[1] == "admin" {
		if err := runAdminMode(os.Args, logger); err != nil {
			logger.Error("admin command failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && (os.Args[1] == "ceremony" || os.Args[1] == "plan") {
		if err := runCeremonyMode(os.Args, logger); err != nil {
			logger.Error("planning ceremony failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "stop" {
		if err := runStopCommand(logger); err != nil {
			logger.Error("stop failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "restart" {
		if err := runRestartCommand(os.Args, logger); err != nil {
			logger.Error("restart failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "status" {
		if err := runStatusCommand(logger); err != nil {
			logger.Error("status failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "doctor" {
		if err := runDoctorCommand(os.Args, logger); err != nil {
			logger.Error("doctor failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "watchdog" {
		if err := runWatchdogCommand(os.Args, logger); err != nil {
			logger.Error("watchdog failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "review-pr" {
		if err := runReviewPRCommand(os.Args, logger); err != nil {
			logger.Error("review-pr failed", "error", err)
			os.Exit(1)
		}
		return
	}

	configPath := flag.String("config", "chum.toml", "path to config file")
	dev := flag.Bool("dev", false, "use text log format (default is JSON)")
	disableAnthropic := flag.Bool("disable-anthropic", false, "remove Anthropic/Claude providers from config and exit")
	setTickInterval := flag.String("set-tick-interval", "", "set [general].tick_interval in config (e.g. 2m) and exit")
	enableBeadsFork := flag.Bool("enable-beads-fork", false, "mirror POST /tasks creates to local Beads fork scaffold")
	beadsForkWorkdir := flag.String("beads-fork-workdir", "", "working directory for bd mirror (default: directory containing --config)")
	beadsForkBinary := flag.String("beads-fork-binary", beadsfork.DefaultBinary, "bd binary to use for mirror")
	beadsForkPinnedVersion := flag.String("beads-fork-pinned-version", beadsfork.DefaultPinnedVersion, "expected bd version for mirror")
	const defaultFallbackModel = "gpt-5.3-codex"
	fallbackModel := flag.String("fallback-model", defaultFallbackModel, "fallback chief model used with -disable-anthropic")
	flag.Parse()

	exe, _ := os.Executable()
	logger.Info("chum starting", "config", *configPath, "binary", exe, "pid", os.Getpid())

	if *disableAnthropic {
		changed, err := disableAnthropicInConfigFile(*configPath, *fallbackModel)
		if err != nil {
			logger.Error("failed to disable anthropic providers in config", "config", *configPath, "error", err)
			os.Exit(1)
		}
		logger.Info("disable-anthropic complete", "config", *configPath, "changed", changed, "fallback_model", *fallbackModel)
		return
	}
	if tickInterval := strings.TrimSpace(*setTickInterval); tickInterval != "" {
		changed, err := setTickIntervalInConfigFile(*configPath, tickInterval)
		if err != nil {
			logger.Error("failed to set tick interval in config", "config", *configPath, "tick_interval", tickInterval, "error", err)
			os.Exit(1)
		}
		logger.Info("set-tick-interval complete", "config", *configPath, "changed", changed, "tick_interval", tickInterval)
		return
	}

	cfgManager, err := config.LoadManager(*configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	cfg := cfgManager.Get()
	if cfg == nil {
		logger.Error("failed to load config snapshot", "config", *configPath)
		os.Exit(1)
	}

	logger = configureLogger(cfg.General.LogLevel, *dev)
	slog.SetDefault(logger)

	var taskMirror api.TaskMirror
	if *enableBeadsFork {
		mirrorWorkDir := strings.TrimSpace(*beadsForkWorkdir)
		if mirrorWorkDir == "" {
			mirrorWorkDir = filepath.Dir(*configPath)
		}
		mirrorWorkDir = config.ExpandHome(mirrorWorkDir)

		mirrorClient, mirrorErr := beadsfork.NewClient(beadsfork.Options{
			Binary:        strings.TrimSpace(*beadsForkBinary),
			WorkDir:       mirrorWorkDir,
			PinnedVersion: strings.TrimSpace(*beadsForkPinnedVersion),
		})
		if mirrorErr != nil {
			logger.Error("failed to initialize beads fork mirror client", "error", mirrorErr)
			os.Exit(1)
		}
		if checkErr := mirrorClient.CheckPinnedVersion(context.Background()); checkErr != nil {
			logger.Error("beads fork mirror version check failed", "error", checkErr, "hint", "use --beads-fork-pinned-version to override")
			os.Exit(1)
		}

		taskMirror = api.NewBeadsForkTaskMirror(mirrorClient)
		logger.Info("beads fork mirror enabled",
			"workdir", mirrorWorkDir,
			"binary", strings.TrimSpace(*beadsForkBinary),
			"pinned_version", strings.TrimSpace(*beadsForkPinnedVersion),
		)
	}

	// Open store
	dbPath := config.ExpandHome(cfg.General.StateDB)
	st, err := store.Open(dbPath)
	if err != nil {
		logger.Error("failed to open store", "path", dbPath, "error", err)
		os.Exit(1)
	}

	// Acquire PID file to prevent duplicate CHUM instances (e.g. from worktrees).
	pidPath := filepath.Join(filepath.Dir(dbPath), "chum.pid")
	if err := acquirePIDFile(pidPath, exe, logger); err != nil {
		logger.Error("another chum instance is running", "error", err, "pidfile", pidPath)
		os.Exit(1)
	}

	dag := graph.NewDAG(st.DB())
	if schemaErr := dag.EnsureSchema(context.Background()); schemaErr != nil {
		logger.Error("failed to ensure graph schema", "error", schemaErr)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// SIGHUP config reload
	applyReload := func() error {
		updatedCfg, reloadErr := config.Reload(*configPath)
		if reloadErr != nil {
			return reloadErr
		}
		if validateErr := validateRuntimeConfigReload(cfg, updatedCfg); validateErr != nil {
			return validateErr
		}
		cfgManager.Set(updatedCfg)
		cfg = updatedCfg
		logger = configureLogger(cfg.General.LogLevel, *dev)
		slog.SetDefault(logger)
		return nil
	}

	temporalNamespace := resolveTemporalNamespace()
	if registerErr := registerTemporalSearchAttributes(context.Background(), cfg.General.TemporalHostPort, temporalNamespace, logger); registerErr != nil {
		// Non-fatal: attributes may already exist in persistent store from a prior run.
		logger.Warn("search attribute registration failed (may already exist)", "host", cfg.General.TemporalHostPort, "namespace", temporalNamespace, "error", registerErr)
	}

	// Start Temporal worker — now includes DispatcherWorkflow + ScanCandidatesActivity
	go func() {
		logger.Info("starting temporal worker")
		if workerErr := temporal.StartWorker(st, cfg.Tiers, dag, cfgManager, cfg.General.TemporalHostPort, temporalNamespace, logger); workerErr != nil {
			logger.Error("temporal worker error", "error", workerErr)
		}
	}()

	// Start Temporal Schedules for dispatcher and strategic groom
	go func() {
		// Let the worker register workflows before we start schedules
		time.Sleep(5 * time.Second)

		tc, dialErr := tclient.Dial(tclient.Options{
			HostPort:  cfg.General.TemporalHostPort,
			Namespace: temporalNamespace,
		})
		if dialErr != nil {
			logger.Error("failed to create temporal client for schedules", "error", dialErr)
			return
		}
		defer tc.Close()

		// --- Dispatcher Schedule (replaces old scheduler.Run goroutine) ---
		tickInterval := cfg.General.TickInterval.Duration
		if tickInterval <= 0 {
			tickInterval = 60 * time.Second
		}

		schedClient := tc.ScheduleClient()
		_, schedErr := schedClient.Create(ctx, tclient.ScheduleOptions{
			ID: "chum-dispatcher",
			Spec: tclient.ScheduleSpec{
				Intervals: []tclient.ScheduleIntervalSpec{
					{Every: tickInterval},
				},
			},
			Action: &tclient.ScheduleWorkflowAction{
				Workflow:  temporal.DispatcherWorkflow,
				Args:      []interface{}{struct{}{}},
				TaskQueue: temporal.DefaultTaskQueue,
				ID:        "dispatcher",
			},
			Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
		})
		if schedErr != nil {
			// Schedule may already exist from a previous run — that's fine.
			var alreadyExists *serviceerror.WorkflowExecutionAlreadyStarted
			switch {
			case errors.As(schedErr, &alreadyExists):
				logger.Info("dispatcher schedule already exists", "interval", tickInterval)
			case strings.Contains(schedErr.Error(), "already exists") ||
				strings.Contains(schedErr.Error(), "AlreadyExists") ||
				strings.Contains(schedErr.Error(), "already registered"):
				logger.Info("dispatcher schedule already exists", "interval", tickInterval)
			default:
				logger.Error("failed to create dispatcher schedule", "error", schedErr)
			}
		} else {
			logger.Info("dispatcher schedule registered", "interval", tickInterval)
		}

		// --- Strategic Groom Cron (per-project, daily at 5 AM) ---
		// Only start if chief is enabled — strategic groom depends on LLM analysis.
		if cfg.Chief.Enabled {
			for name, project := range cfg.Projects {
				if !project.Enabled {
					continue
				}

				workflowID := fmt.Sprintf("strategic-groom-%s", name)
				req := temporal.StrategicGroomRequest{
					Project: name,
					WorkDir: config.ExpandHome(project.Workspace),
					Tier:    "premium",
				}

				_, groomErr := tc.ExecuteWorkflow(ctx, tclient.StartWorkflowOptions{
					ID:           workflowID,
					TaskQueue:    "chum-task-queue",
					CronSchedule: "0 5 * * *",
				}, temporal.StrategicGroomWorkflow, req)
				if groomErr != nil {
					var alreadyStarted *serviceerror.WorkflowExecutionAlreadyStarted
					if errors.As(groomErr, &alreadyStarted) {
						logger.Info("strategic cron already running", "project", name, "workflow_id", workflowID)
						continue
					}
					logger.Error("failed to start strategic cron", "project", name, "error", groomErr)
					continue
				}
				logger.Info("strategic cron registered", "project", name, "workflow_id", workflowID, "schedule", "0 5 * * *")
			}
		} else {
			logger.Info("strategic groom disabled (chief not enabled)")
		}

		// --- Paleontologist Schedule (every 30 minutes, per-project) ---
		for name, project := range cfg.Projects {
			if !project.Enabled {
				continue
			}

			paleoReq := temporal.PaleontologistRequest{
				Project:   name,
				WorkDir:   config.ExpandHome(project.Workspace),
				LookbackH: 6,
				Tier:      "premium",
			}

			paleoID := fmt.Sprintf("paleontologist-%s", name)
			_, paleoErr := schedClient.Create(ctx, tclient.ScheduleOptions{
				ID: paleoID,
				Spec: tclient.ScheduleSpec{
					Intervals: []tclient.ScheduleIntervalSpec{
						{Every: 30 * time.Minute},
					},
				},
				Action: &tclient.ScheduleWorkflowAction{
					Workflow:  temporal.PaleontologistWorkflow,
					Args:      []interface{}{paleoReq},
					TaskQueue: "chum-task-queue",
				},
			})
			if paleoErr != nil {
				switch {
				case strings.Contains(paleoErr.Error(), "AlreadyExists") ||
					strings.Contains(paleoErr.Error(), "already registered"):
					logger.Info("paleontologist schedule already exists", "project", name)
				default:
					logger.Error("failed to create paleontologist schedule", "project", name, "error", paleoErr)
				}
			} else {
				logger.Info("paleontologist schedule registered", "project", name, "interval", "30m")
			}
		}

		// --- Janitor Schedule (hourly worktree/branch cleanup) ---
		var janitorWorkspaces []string
		for _, proj := range cfg.Projects {
			if proj.Enabled && proj.Workspace != "" {
				janitorWorkspaces = append(janitorWorkspaces, config.ExpandHome(strings.TrimSpace(proj.Workspace)))
			}
		}
		_, janitorErr := schedClient.Create(ctx, tclient.ScheduleOptions{
			ID: "chum-janitor",
			Spec: tclient.ScheduleSpec{
				Intervals: []tclient.ScheduleIntervalSpec{
					{Every: 1 * time.Hour},
				},
			},
			Action: &tclient.ScheduleWorkflowAction{
				Workflow:  temporal.JanitorWorkflow,
				Args:      []interface{}{janitorWorkspaces},
				TaskQueue: temporal.DefaultTaskQueue,
				ID:        "janitor",
			},
			Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
		})
		if janitorErr != nil {
			var alreadyExists *serviceerror.WorkflowExecutionAlreadyStarted
			switch {
			case errors.As(janitorErr, &alreadyExists):
				logger.Info("janitor schedule already exists")
			case strings.Contains(janitorErr.Error(), "already exists") ||
				strings.Contains(janitorErr.Error(), "AlreadyExists") ||
				strings.Contains(janitorErr.Error(), "already registered"):
				logger.Info("janitor schedule already exists")
			default:
				logger.Error("failed to create janitor schedule", "error", janitorErr)
			}
		} else {
			logger.Info("janitor schedule registered", "interval", "1h")
		}

		// --- PR Review Poller Schedule (every 5 minutes, per-project) ---
		// Scans for open PRs that haven't been reviewed by CHUM and spawns
		// cross-model reviews. Catches PRs from any source: sharks, humans, CI.
		for name, proj := range cfg.Projects {
			// PR review poller runs for ALL projects with a workspace,
			// even disabled ones — disabled only skips shark dispatch.
			if proj.Workspace == "" {
				continue
			}

			prPollerReq := temporal.PRReviewPollerRequest{
				Workspace: config.ExpandHome(proj.Workspace),
			}
			prPollerID := fmt.Sprintf("chum-pr-review-poller-%s", name)
			_, prPollerErr := schedClient.Create(ctx, tclient.ScheduleOptions{
				ID: prPollerID,
				Spec: tclient.ScheduleSpec{
					Intervals: []tclient.ScheduleIntervalSpec{
						{Every: 5 * time.Minute},
					},
				},
				Action: &tclient.ScheduleWorkflowAction{
					Workflow:  temporal.PRReviewPollerWorkflow,
					Args:      []interface{}{prPollerReq},
					TaskQueue: temporal.DefaultTaskQueue,
					ID:        fmt.Sprintf("pr-review-poller-%s", name),
				},
				Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
			})
			if prPollerErr != nil {
				switch {
				case strings.Contains(prPollerErr.Error(), "AlreadyExists") ||
					strings.Contains(prPollerErr.Error(), "already registered") ||
					strings.Contains(prPollerErr.Error(), "already exists"):
					logger.Info("PR review poller schedule already exists", "project", name)
				default:
					logger.Error("failed to create PR review poller schedule", "project", name, "error", prPollerErr)
				}
			} else {
				logger.Info("PR review poller schedule registered", "project", name, "interval", "5m")
			}
		}

	}()

	// Start API server
	apiSrv, err := api.NewServerWithOptions(cfg, st, dag, logger.With("component", "api"), api.ServerOptions{
		TaskMirror: taskMirror,
	})
	if err != nil {
		logger.Error("failed to create api server", "error", err)
		os.Exit(1)
	}
	defer st.Close()
	defer os.Remove(pidPath)
	defer apiSrv.Close()
	defer cancel()

	go func() {
		if err := apiSrv.Start(ctx); err != nil {
			logger.Error("api server error", "error", err)
		}
	}()

	logger.Info("chum running",
		"bind", cfg.API.Bind,
	)

	// Start turtle chat poller if configured
	if turtleRoom := strings.TrimSpace(cfg.Reporter.TurtleRoom); turtleRoom != "" {
		go func() {
			var apiToken string
			if cfg.API.Security.Enabled && len(cfg.API.Security.AllowedTokens) > 0 {
				apiToken = cfg.API.Security.AllowedTokens[0]
			}
			planningClient, planningErr := newPlanningAPIClient(cfg.API.Bind, apiToken)
			if planningErr != nil {
				logger.Warn("planning control bridge disabled", "error", planningErr)
			}

			turtleHandler := &matrix.TurtleChatHandler{
				Room:       turtleRoom,
				WorkDir:    filepath.Dir(*configPath),
				Logger:     logger.With("component", "turtle-chat"),
				Planning:   planningClient,
				BridgeRoom: strings.TrimSpace(cfg.Reporter.DefaultRoom),
				ControlBot: "spritzbot",
			}
			turtleHandler.RunPoller(ctx)
		}()
		logger.Info("turtle chat poller started", "room", cfg.Reporter.TurtleRoom)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)

	for {
		sig := <-sigCh
		switch sig {
		case syscall.SIGHUP:
			if err := applyReload(); err != nil {
				logger.Error(fmt.Sprintf("config reload failed: %v", err))
				continue
			}
			logger.Info("config reloaded")
		case syscall.SIGINT, syscall.SIGTERM:
			shutdownStart := time.Now()
			logger.Info("received signal, shutting down", "signal", sig)
			cancel()
			logger.Info("chum stopped", "shutdown_duration", time.Since(shutdownStart).String())
			return
		default:
			shutdownStart := time.Now()
			logger.Info("received unexpected signal, shutting down", "signal", sig)
			cancel()
			logger.Info("chum stopped", "shutdown_duration", time.Since(shutdownStart).String())
			return
		}
	}
}
