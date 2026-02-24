package temporal

import (
	"context"
	"log/slog"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/dispatch"
	"github.com/antigravity-dev/chum/internal/graph"
	"github.com/antigravity-dev/chum/internal/store"
)

// RegisterSearchAttributes idempotently registers custom search attributes
// with the Temporal server. Safe to call on every startup — if attributes
// already exist, the error is silently ignored.
func RegisterSearchAttributes(ctx context.Context, c client.Client, logger *slog.Logger) {
	attrs := map[string]enumspb.IndexedValueType{
		SAProject:      enumspb.INDEXED_VALUE_TYPE_KEYWORD,
		SAPriority:     enumspb.INDEXED_VALUE_TYPE_INT,
		SAAgent:        enumspb.INDEXED_VALUE_TYPE_KEYWORD,
		SACurrentStage: enumspb.INDEXED_VALUE_TYPE_KEYWORD,
		SATaskTitle:    enumspb.INDEXED_VALUE_TYPE_TEXT,
	}

	_, err := c.OperatorService().AddSearchAttributes(ctx, &operatorservice.AddSearchAttributesRequest{
		SearchAttributes: attrs,
		Namespace:        "default",
	})
	if err != nil {
		// "already exists" is expected on restarts — not an error.
		if strings.Contains(err.Error(), "already exists") {
			logger.Debug("search attributes already registered")
			return
		}
		logger.Warn("failed to register search attributes (non-fatal)", "error", err)
		return
	}
	logger.Info("custom search attributes registered with Temporal",
		"attributes", []string{SAProject, SAPriority, SAAgent, SACurrentStage, SATaskTitle})
}

// StartWorker connects to Temporal and starts the chum task queue worker.
// The store, tiers, dag, and cfgMgr are injected so activities can record
// outcomes, resolve agents, and scan for ready tasks.
func StartWorker(st *store.Store, tiers config.Tiers, dag *graph.DAG, cfgMgr config.ConfigManager, temporalHostPort string, taskQueue string, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	if temporalHostPort == "" {
		temporalHostPort = DefaultTemporalHostPort
	}
	ResolvedTaskQueue = ResolveTaskQueue(taskQueue)
	c, err := client.Dial(client.Options{
		HostPort: temporalHostPort,
	})
	if err != nil {
		return err
	}
	defer c.Close()

	// Register custom search attributes (idempotent).
	regCtx, regCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer regCancel()
	RegisterSearchAttributes(regCtx, c, logger)

	w := worker.New(c, ResolvedTaskQueue, worker.Options{})

	acts := &Activities{Store: st, Tiers: tiers, DAG: dag}
	rl := dispatch.NewRateLimiter(st, cfgMgr.Get().RateLimits)
	dispatchActs := &DispatchActivities{
		CfgMgr:      cfgMgr,
		TC:          c,
		DAG:         dag,
		Store:       st,
		RateLimiter: rl,
	}

	// --- Core Workflows ---
	w.RegisterWorkflow(ChumAgentWorkflow)
	w.RegisterWorkflow(PlanningCeremonyWorkflow)

	// --- Dispatcher Workflow ---
	w.RegisterWorkflow(DispatcherWorkflow)

	// --- CHUM Workflows ---
	w.RegisterWorkflow(ContinuousLearnerWorkflow)
	w.RegisterWorkflow(TacticalGroomWorkflow)
	w.RegisterWorkflow(StrategicGroomWorkflow)

	// --- Core Activities ---
	w.RegisterActivity(acts.StructuredPlanActivity)
	w.RegisterActivity(acts.ExecuteActivity)
	w.RegisterActivity(acts.CodeReviewActivity)
	w.RegisterActivity(acts.DoDVerifyActivity)
	w.RegisterActivity(acts.GatherMetricsActivity)
	w.RegisterActivity(acts.WebVerifyActivity)
	w.RegisterActivity(acts.ResetWorkspaceActivity)
	w.RegisterActivity(acts.RecordOutcomeActivity)
	w.RegisterActivity(acts.EscalateActivity)
	w.RegisterActivity(acts.GroomBacklogActivity)
	w.RegisterActivity(acts.GenerateQuestionsActivity)
	w.RegisterActivity(acts.SummarizePlanActivity)

	// --- Dispatcher Activities ---
	w.RegisterActivity(dispatchActs.ScanCandidatesActivity)

	// --- CHUM Learner Activities ---
	w.RegisterActivity(acts.ExtractLessonsActivity)
	w.RegisterActivity(acts.StoreLessonActivity)
	w.RegisterActivity(acts.GenerateSemgrepRuleActivity)
	w.RegisterActivity(acts.RunSemgrepScanActivity)

	// --- Pre-flight Activities ---
	w.RegisterActivity(acts.FilePreflightFailureActivity)

	// --- CHUM Groom Activities ---
	w.RegisterActivity(acts.MutateTasksActivity)
	w.RegisterActivity(acts.GenerateRepoMapActivity)
	w.RegisterActivity(acts.GetBeadStateSummaryActivity)
	w.RegisterActivity(acts.StrategicAnalysisActivity)
	w.RegisterActivity(acts.GenerateMorningBriefingActivity)

	// --- Crab Decomposition ---
	w.RegisterWorkflow(CrabDecompositionWorkflow)
	w.RegisterActivity(acts.ParsePlanActivity)
	w.RegisterActivity(acts.ClarifyGapsActivity)
	w.RegisterActivity(acts.DecomposeActivity)
	w.RegisterActivity(acts.ScopeMorselsActivity)
	w.RegisterActivity(acts.SizeMorselsActivity)
	w.RegisterActivity(acts.EmitMorselsActivity)

	logger.Info("temporal worker started", "task_queue", ResolvedTaskQueue)
	return w.Run(worker.InterruptCh())
}
