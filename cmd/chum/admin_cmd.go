package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strings"

	tclient "go.temporal.io/sdk/client"

	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/temporal"
)

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
