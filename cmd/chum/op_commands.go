package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"go.temporal.io/api/workflowservice/v1"
	tclient "go.temporal.io/sdk/client"

	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/temporal"
)

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

	// 2. All chum processes, verified by /proc/PID/exe.
	procs := findChumProcesses()
	if len(procs) == 0 {
		fmt.Println("processes:  none")
	} else {
		fmt.Printf("processes:  %d found\n", len(procs))
		for _, p := range procs {
			fmt.Printf("  pid %d: %s\n", p.pid, p.cmdline)
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
	procs := findChumProcesses()
	if len(procs) == 0 {
		fmt.Println("  OK: no chum processes running")
	} else {
		pidFilePath := filepath.Join(dataDir(), "chum.pid")
		pidData, _ := os.ReadFile(pidFilePath)
		trackedPID := -1
		if pidData != nil {
			lines := strings.SplitN(string(pidData), "\n", 2)
			trackedPID, _ = strconv.Atoi(strings.TrimSpace(lines[0]))
		}

		var orphans []string
		for _, p := range procs {
			if p.pid == trackedPID {
				fmt.Printf("  OK: tracked process %d is alive\n", p.pid)
			} else {
				orphans = append(orphans, fmt.Sprintf("  WARN: orphaned process pid %d: %s", p.pid, p.cmdline))
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

// registerTemporalSearchAttributes registers custom search attributes in Temporal.
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
