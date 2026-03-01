package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
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
