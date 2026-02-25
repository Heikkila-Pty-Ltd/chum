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

	// Seed operational lessons (octopus) on startup
	SeedLessonsIfNeeded(st, logger)
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
		logger.Warn("search attribute registration failed in worker (may already exist)", "namespace", ns, "error", err)
	}

	w := worker.New(c, DefaultTaskQueue, worker.Options{
		// Concurrency tuning for Cambrian Explosion (6 concurrent workflows).
		// Default MaxConcurrentActivityExecutionSize is 1000 but the single
		// poller can't keep up — bump pollers so activities get picked up faster.
		MaxConcurrentActivityExecutionSize:      20,
		MaxConcurrentWorkflowTaskExecutionSize:  10,
		MaxConcurrentActivityTaskPollers:         4,
		MaxConcurrentWorkflowTaskPollers:         2,
	})

	// Wire Matrix notifications (nil sender = notifications disabled).
	cfg := cfgMgr.Get()
	var sender matrix.Sender
	if cfg.Reporter.MatrixBotAccount != "" && cfg.Reporter.DefaultRoom != "" {
		sender = matrix.NewHTTPSender(&http.Client{}, cfg.Reporter.MatrixBotAccount)
		logger.Info("matrix notifications enabled", "account", cfg.Reporter.MatrixBotAccount, "room", cfg.Reporter.DefaultRoom)
	}

	// Preflight: validate CLI binaries exist for enabled providers.
	if warnings := PreflightCLIs(cfg, logger); len(warnings) > 0 {
		logger.Warn("CLI preflight warnings", "count", len(warnings))
	}

	acts := &Activities{
		Store:       st,
		Tiers:       tiers,
		CfgMgr:      cfgMgr,
		DAG:         dag,
		Sender:      sender,
		DefaultRoom: cfg.Reporter.DefaultRoom,
		AdminRoom:   cfg.Reporter.AdminRoom,
		TurtleRoom:  cfg.Reporter.TurtleRoom,
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
	w.RegisterActivity(acts.RecordOrganismLogActivity)
	w.RegisterActivity(acts.EscalateActivity)
	w.RegisterActivity(acts.GroomBacklogActivity)
	w.RegisterActivity(acts.GenerateQuestionsActivity)
	w.RegisterActivity(acts.SummarizePlanActivity)
	w.RegisterActivity(acts.NotifyActivity)
	w.RegisterActivity(acts.MergeToMainActivity)
	w.RegisterActivity(acts.GetWorktreeDiffActivity)
	w.RegisterActivity(acts.ReviewExplosionCandidatesActivity)

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
	w.RegisterActivity(acts.FailureTriageActivity)

	// --- Paleontologist Activities ---
	w.RegisterWorkflow(PaleontologistWorkflow)
	w.RegisterActivity(acts.AnalyzeProviderFitnessActivity)
	w.RegisterActivity(acts.DiscoverAntibodiesActivity)
	w.RegisterActivity(acts.ScanProteinCandidatesActivity)
	w.RegisterActivity(acts.AuditSpeciesHealthActivity)
	w.RegisterActivity(acts.AnalyzeCostTrendsActivity)
	w.RegisterActivity(acts.RecordPaleontologyRunActivity)

	// --- UBS (Ultimate Bug Scanner) ---
	w.RegisterActivity(acts.RunUBSScanActivity)
	w.RegisterActivity(acts.UBSBaselineScanActivity)
	w.RegisterActivity(acts.GetBugPrimingActivity)

	// --- Proteins (Deterministic Workflow Sequences) ---
	w.RegisterActivity(acts.GetProteinInstructionsActivity)
	w.RegisterActivity(acts.RecordProteinFoldActivity)
	w.RegisterActivity(acts.MoleculeRetroActivity)
	w.RegisterActivity(acts.SynthesizeProteinActivity)
	w.RegisterActivity(acts.MutateProteinActivity)
	w.RegisterActivity(acts.SynthesizeProteinCandidatesActivity)

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
	w.RegisterActivity(acts.ApplyStrategicMutationsActivity)
	w.RegisterActivity(acts.RecordFailureActivity)
	w.RegisterActivity(acts.FileInvestigationTaskActivity)
	w.RegisterActivity(acts.SentinelScanActivity)

	// --- Crab Decomposition ---
	w.RegisterWorkflow(CrabDecompositionWorkflow)
	w.RegisterActivity(acts.ParsePlanActivity)
	w.RegisterActivity(acts.ClarifyGapsActivity)
	w.RegisterActivity(acts.DecomposeActivity)
	w.RegisterActivity(acts.ScopeMorselsActivity)
	w.RegisterActivity(acts.SizeMorselsActivity)
	w.RegisterActivity(acts.EmitMorselsActivity)

	// --- Turtle (Planning → Gate → Crab) ---
	// Single-stage planning replaces the old 3-agent ceremony.
	w.RegisterWorkflow(AutonomousPlanningCeremonyWorkflow)
	w.RegisterWorkflow(TurtleToCrabWorkflow)
	w.RegisterActivity(acts.TurtlePlanActivity)
	w.RegisterActivity(acts.TurtleExploreActivity)     // deprecated but kept for running workflows
	w.RegisterActivity(acts.TurtleDeliberateActivity)   // deprecated but kept for running workflows
	w.RegisterActivity(acts.TurtleConvergeActivity)     // deprecated but kept for running workflows
	w.RegisterActivity(acts.TurtleSendAsActivity)

	// --- Calcifier (Stochastic→Deterministic) ---
	w.RegisterWorkflow(CalcificationWorkflow)
	w.RegisterActivity(acts.DetectCalcificationCandidatesActivity)
	w.RegisterActivity(acts.CompileCalcifiedScriptActivity)
	w.RegisterActivity(acts.PromoteCalcifiedScriptActivity)
	w.RegisterActivity(acts.QuarantineAndRewireActivity)

	// --- Janitor (Worktree/Branch Cleanup) ---
	w.RegisterWorkflow(JanitorWorkflow)
	w.RegisterActivity(acts.JanitorSweepActivity)

	logger.Info("temporal worker started", "task_queue", DefaultTaskQueue)
	return w.Run(worker.InterruptCh())
}
