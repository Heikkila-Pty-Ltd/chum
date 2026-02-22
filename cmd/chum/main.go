package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"errors"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	tclient "go.temporal.io/sdk/client"

	"github.com/antigravity-dev/chum/internal/api"
	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/graph"
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
	opts := tclient.Options{
		HostPort: temporalHostPort,
	}
	if temporalNamespace != "" {
		opts.Namespace = temporalNamespace
	}
	tc, err := tclient.Dial(opts)
	if err != nil {
		return err
	}
	defer tc.Close()

	logger.Info("registering temporal search attributes", "host", temporalHostPort, "namespace", temporalNamespace)
	if err := temporal.RegisterSearchAttributes(ctx, tc, temporalNamespace); err != nil {
		return err
	}
	logger.Info("temporal search attributes ready", "host", temporalHostPort, "namespace", temporalNamespace)
	return nil
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

func parseAdminSubcommand(args []string, defaultQuery string) (string, string, error) {
	if len(args) < 2 {
		return "", "", fmt.Errorf("admin requires a subcommand: drain | resume | reset | terminate")
	}

	subcommand := strings.ToLower(strings.TrimSpace(args[1]))
	switch subcommand {
	case "drain", "resume", "reset", "terminate":
	default:
		return "", "", fmt.Errorf("unknown admin command %q", subcommand)
	}

	fs := flag.NewFlagSet(fmt.Sprintf("admin %s", subcommand), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	query := fs.String("query", "", "visibility query")
	if err := fs.Parse(args[2:]); err != nil {
		return "", "", err
	}
	if fs.NArg() > 0 {
		return "", "", fmt.Errorf("unexpected arguments for admin %s: %v", subcommand, fs.Args())
	}

	q := strings.TrimSpace(*query)
	if (subcommand == "reset" || subcommand == "terminate") && q == "" {
		return "", "", fmt.Errorf("--query is required for %s", subcommand)
	}
	if q == "" {
		q = defaultQuery
	}

	return subcommand, q, nil
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
	if err := adminFS.Parse(args[1:]); err != nil {
		return err
	}

	args = append([]string{"admin"}, adminFS.Args()...)
	command, query, err := parseAdminSubcommand(args, temporal.ChumAgentRunningVisibilityQuery(""))
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

	tc, err := tclient.Dial(tclient.Options{HostPort: host})
	if err != nil {
		return err
	}
	defer tc.Close()

	namespace := strings.TrimSpace(os.Getenv("TEMPORAL_NAMESPACE"))

	ops := adminBatchOps{
		Drain: func(ctx context.Context, q string) (string, error) {
			return temporal.StartDrainAgentWorkflows(ctx, tc.WorkflowService(), namespace, q)
		},
		Resume: func(ctx context.Context, q string) (string, error) {
			return temporal.StartResumeAgentWorkflows(ctx, tc.WorkflowService(), namespace, q)
		},
		Reset: func(ctx context.Context, q string) (string, error) {
			return temporal.StartResetAgentWorkflows(ctx, tc.WorkflowService(), namespace, q)
		},
		Terminate: func(ctx context.Context, q string) (string, error) {
			return temporal.StartTerminateAgentWorkflows(ctx, tc.WorkflowService(), namespace, q)
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
		"namespace", namespace,
		"host", host,
	)
	return nil
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

	configPath := flag.String("config", "chum.toml", "path to config file")
	dev := flag.Bool("dev", false, "use text log format (default is JSON)")
	disableAnthropic := flag.Bool("disable-anthropic", false, "remove Anthropic/Claude providers from config and exit")
	setTickInterval := flag.String("set-tick-interval", "", "set [general].tick_interval in config (e.g. 2m) and exit")
	const defaultFallbackModel = "gpt-5.3-codex"
	fallbackModel := flag.String("fallback-model", defaultFallbackModel, "fallback chief model used with -disable-anthropic")
	flag.Parse()

	logger.Info("chum starting", "config", *configPath)

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

	// Open store
	dbPath := config.ExpandHome(cfg.General.StateDB)
	st, err := store.Open(dbPath)
	if err != nil {
		logger.Error("failed to open store", "path", dbPath, "error", err)
		os.Exit(1)
	}
	defer st.Close()

	dag := graph.NewDAG(st.DB())
	if schemaErr := dag.EnsureSchema(context.Background()); schemaErr != nil {
		logger.Error("failed to ensure graph schema", "error", schemaErr)
		os.Exit(1) //nolint:gocritic // exitAfterDefer: acceptable in main() startup
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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

	temporalNamespace := strings.TrimSpace(os.Getenv("TEMPORAL_NAMESPACE"))
	if err := registerTemporalSearchAttributes(context.Background(), cfg.General.TemporalHostPort, temporalNamespace, logger); err != nil {
		logger.Error("failed to register temporal search attributes", "host", cfg.General.TemporalHostPort, "namespace", temporalNamespace, "error", err)
		os.Exit(1)
	}

	// Start Temporal worker — now includes DispatcherWorkflow + ScanCandidatesActivity
	go func() {
		logger.Info("starting temporal worker")
		if workerErr := temporal.StartWorker(st, cfg.Tiers, dag, cfgManager, cfg.General.TemporalHostPort, logger); workerErr != nil {
			logger.Error("temporal worker error", "error", workerErr)
		}
	}()

	// Start Temporal Schedules for dispatcher and strategic groom
	go func() {
		// Let the worker register workflows before we start schedules
		time.Sleep(5 * time.Second)

		tc, dialErr := tclient.Dial(tclient.Options{
			HostPort: cfg.General.TemporalHostPort,
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
		for name, project := range cfg.Projects { //nolint:gocritic // rangeValCopy: config.Project is a small value type used briefly
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
	}()

	// Start API server
	apiSrv, err := api.NewServer(cfg, st, logger.With("component", "api"))
	if err != nil {
		logger.Error("failed to create api server", "error", err)
		os.Exit(1)
	}
	defer apiSrv.Close()

	go func() {
		if err := apiSrv.Start(ctx); err != nil {
			logger.Error("api server error", "error", err)
		}
	}()

	logger.Info("chum running",
		"bind", cfg.API.Bind,
	)

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
