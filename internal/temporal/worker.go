package temporal

import (
	"context"
	"log/slog"
	"net/http"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/graph"
	"github.com/antigravity-dev/chum/internal/matrix"
	"github.com/antigravity-dev/chum/internal/store"
)

// StartWorker connects to Temporal and starts the chum task queue worker.
// The store, tiers, dag, and cfgMgr are injected so activities can record
// outcomes, resolve agents, and scan for ready tasks.
func StartWorker(st *store.Store, tiers config.Tiers, dag *graph.DAG, cfgMgr config.ConfigManager, temporalHostPort, temporalNamespace string, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	if temporalHostPort == "" {
		temporalHostPort = DefaultTemporalHostPort
	}
	ns := normalizeSearchAttributeNamespace(temporalNamespace)
	c, err := client.Dial(client.Options{
		HostPort:  temporalHostPort,
		Namespace: ns,
	})
	if err != nil {
		return err
	}
	defer c.Close()

	if err := RegisterChumSearchAttributesWithNamespace(context.Background(), c, ns); err != nil {
		return err
	}

	w := worker.New(c, DefaultTaskQueue, worker.Options{})

	// Wire Matrix notifications (nil sender = notifications disabled).
	cfg := cfgMgr.Get()
	var sender matrix.Sender
	if cfg.Reporter.MatrixBotAccount != "" && cfg.Reporter.DefaultRoom != "" {
		sender = matrix.NewHTTPSender(&http.Client{}, cfg.Reporter.MatrixBotAccount)
		logger.Info("matrix notifications enabled", "account", cfg.Reporter.MatrixBotAccount, "room", cfg.Reporter.DefaultRoom)
	}

	acts := &Activities{
		Store:       st,
		Tiers:       tiers,
		DAG:         dag,
		Sender:      sender,
		DefaultRoom: cfg.Reporter.DefaultRoom,
	}
	dispatchActs := &DispatchActivities{
		CfgMgr: cfgMgr,
		TC:     c,
		DAG:    dag,
		Store:  st,
	}

	// --- Primary Workflows ---
	w.RegisterWorkflow(ChumAgentWorkflow)
	w.RegisterWorkflow(PlanningCeremonyWorkflow)
	w.RegisterWorkflow(CambrianExplosionWorkflow)
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
	w.RegisterActivity(acts.SetupWorktreeActivity)
	w.RegisterActivity(acts.PushWorktreeActivity)
	w.RegisterActivity(acts.CleanupWorktreeActivity)
	w.RegisterActivity(acts.RecordOutcomeActivity)
	w.RegisterActivity(acts.CloseTaskActivity)
	w.RegisterActivity(acts.RecordHealthEventActivity)
	w.RegisterActivity(acts.EscalateActivity)
	w.RegisterActivity(acts.GroomBacklogActivity)
	w.RegisterActivity(acts.GenerateQuestionsActivity)
	w.RegisterActivity(acts.SummarizePlanActivity)
	w.RegisterActivity(acts.NotifyActivity)

	// --- Dispatcher Activities ---
	w.RegisterActivity(dispatchActs.ScanCandidatesActivity)

	// --- CHUM Learner Activities ---
	w.RegisterActivity(acts.ExtractLessonsActivity)
	w.RegisterActivity(acts.StoreLessonActivity)
	w.RegisterActivity(acts.GenerateSemgrepRuleActivity)
	w.RegisterActivity(acts.SynthesizeCLAUDEmdActivity)
	w.RegisterActivity(acts.CalcifyPatternActivity)
	w.RegisterActivity(acts.CommitAndPushLearnerOutputsActivity)
	w.RegisterActivity(acts.RecordEscalationActivity)
	w.RegisterActivity(acts.AutoFixLintActivity)

	// --- UBS (Ultimate Bug Scanner) ---
	w.RegisterActivity(acts.RunUBSScanActivity)
	w.RegisterActivity(acts.GetBugPrimingActivity)

	// --- Proteins (Deterministic Workflow Sequences) ---
	w.RegisterActivity(acts.GetProteinInstructionsActivity)
	w.RegisterActivity(acts.RecordProteinFoldActivity)
	w.RegisterActivity(acts.MoleculeRetroActivity)

	// --- Genome Evolution ---
	w.RegisterActivity(acts.EvolveGenomeActivity)
	w.RegisterActivity(acts.HibernateGenomeActivity)
	w.RegisterActivity(acts.GetGenomeForPromptActivity)

	// --- CHUM Groom Activities ---
	w.RegisterActivity(acts.MutateTasksActivity)
	w.RegisterActivity(acts.GenerateRepoMapActivity)
	w.RegisterActivity(acts.GetMorselStateSummaryActivity)
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

	// --- Calcifier (Stochastic→Deterministic) ---
	w.RegisterWorkflow(CalcificationWorkflow)
	w.RegisterActivity(acts.DetectCalcificationCandidatesActivity)
	w.RegisterActivity(acts.CompileCalcifiedScriptActivity)
	w.RegisterActivity(acts.PromoteCalcifiedScriptActivity)
	w.RegisterActivity(acts.QuarantineAndRewireActivity)

	logger.Info("temporal worker started", "task_queue", DefaultTaskQueue)
	return w.Run(worker.InterruptCh())
}
