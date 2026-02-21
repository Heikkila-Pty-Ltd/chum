package temporal

import (
	"log/slog"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/graph"
	"github.com/antigravity-dev/chum/internal/store"
)

// StartWorker connects to Temporal and starts the chum task queue worker.
// The store, tiers, dag, and cfgMgr are injected so activities can record
// outcomes, resolve agents, and scan for ready tasks.
func StartWorker(st *store.Store, tiers config.Tiers, dag *graph.DAG, cfgMgr config.ConfigManager, temporalHostPort string, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	if temporalHostPort == "" {
		temporalHostPort = DefaultTemporalHostPort
	}
	c, err := client.Dial(client.Options{
		HostPort: temporalHostPort,
	})
	if err != nil {
		return err
	}
	defer c.Close()

	w := worker.New(c, DefaultTaskQueue, worker.Options{})

	acts := &Activities{Store: st, Tiers: tiers, DAG: dag}
	dispatchActs := &DispatchActivities{
		CfgMgr: cfgMgr,
		TC:     c,
		DAG:    dag,
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

	// --- CHUM Groom Activities ---
	w.RegisterActivity(acts.MutateTasksActivity)
	w.RegisterActivity(acts.GenerateRepoMapActivity)
	w.RegisterActivity(acts.GetBeadStateSummaryActivity)
	w.RegisterActivity(acts.StrategicAnalysisActivity)
	w.RegisterActivity(acts.GenerateMorningBriefingActivity)

	logger.Info("temporal worker started", "task_queue", DefaultTaskQueue)
	return w.Run(worker.InterruptCh())
}
