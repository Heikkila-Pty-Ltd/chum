package temporal

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// PaleontologistRequest configures a paleontologist analysis run.
type PaleontologistRequest struct {
	Project     string
	WorkDir     string
	LookbackH   int    // how far back to look (default: 6 hours)
	Tier        string // LLM tier for protein synthesis (default: "premium")
}

// PaleontologistPrefix is the log prefix for paleontologist operations.
const PaleontologistPrefix = "🦴 PALEO"

// PaleontologistWorkflow runs periodically (every 30 minutes via Temporal Schedule)
// to mine the fossil record for evolutionary insights.
//
// Pipeline:
//  1. ProviderFitnessAnalysis — update genome provider_genes from dispatch data
//  2. AntibodyDiscovery — recurring UBS patterns → genome antibodies
//  3. ProteinisationScan — high-success species → nominate for proteinisation
//  4. SpeciesHealthAudit — detect anomalies (stuck, stale, newborn)
//  5. CostTrendAnalysis — alert on cost-per-success spikes
//
// All steps are non-fatal. Paleontologist failure never blocks the pipeline.
func PaleontologistWorkflow(ctx workflow.Context, req PaleontologistRequest) error {
	logger := workflow.GetLogger(ctx)
	logger.Info(PaleontologistPrefix+" Starting paleontological analysis", "Project", req.Project)

	if req.LookbackH <= 0 {
		req.LookbackH = 6
	}
	if req.Tier == "" {
		req.Tier = "premium"
	}

	var a *Activities

	// Short timeout for SQL-only activities
	sqlOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	}
	// Longer timeout for LLM-backed activities (protein synthesis)
	llmOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 3 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}

	var totalAntibodies, totalGenes, totalProteins, totalAudited, totalAlerts int

	// Step 1: Provider Fitness Analysis
	sqlCtx := workflow.WithActivityOptions(ctx, sqlOpts)
	var fitnessGenes int
	if err := workflow.ExecuteActivity(sqlCtx, a.AnalyzeProviderFitnessActivity, req).Get(ctx, &fitnessGenes); err != nil {
		logger.Warn(PaleontologistPrefix+" Provider fitness analysis failed (non-fatal)", "error", err)
	} else {
		totalGenes += fitnessGenes
		logger.Info(PaleontologistPrefix+" Provider fitness analysis complete", "GenesMutated", fitnessGenes)
	}

	// Step 2: Antibody Discovery
	var antibodies int
	if err := workflow.ExecuteActivity(sqlCtx, a.DiscoverAntibodiesActivity, req).Get(ctx, &antibodies); err != nil {
		logger.Warn(PaleontologistPrefix+" Antibody discovery failed (non-fatal)", "error", err)
	} else {
		totalAntibodies += antibodies
		logger.Info(PaleontologistPrefix+" Antibody discovery complete", "AntibodiesDiscovered", antibodies)
	}

	// Step 3: Proteinisation Scan (uses LLM for synthesis)
	llmCtx := workflow.WithActivityOptions(ctx, llmOpts)
	var proteins int
	if err := workflow.ExecuteActivity(llmCtx, a.ScanProteinCandidatesActivity, req).Get(ctx, &proteins); err != nil {
		logger.Warn(PaleontologistPrefix+" Proteinisation scan failed (non-fatal)", "error", err)
	} else {
		totalProteins += proteins
		logger.Info(PaleontologistPrefix+" Proteinisation scan complete", "ProteinsNominated", proteins)
	}

	// Step 4: Species Health Audit
	var audited int
	if err := workflow.ExecuteActivity(sqlCtx, a.AuditSpeciesHealthActivity, req).Get(ctx, &audited); err != nil {
		logger.Warn(PaleontologistPrefix+" Species health audit failed (non-fatal)", "error", err)
	} else {
		totalAudited += audited
		logger.Info(PaleontologistPrefix+" Species health audit complete", "SpeciesAudited", audited)
	}

	// Step 5: Cost Trend Analysis
	var alerts int
	if err := workflow.ExecuteActivity(sqlCtx, a.AnalyzeCostTrendsActivity, req).Get(ctx, &alerts); err != nil {
		logger.Warn(PaleontologistPrefix+" Cost trend analysis failed (non-fatal)", "error", err)
	} else {
		totalAlerts += alerts
		logger.Info(PaleontologistPrefix+" Cost trend analysis complete", "CostAlerts", alerts)
	}

	// Record the run
	summary := fmt.Sprintf("Antibodies:%d Genes:%d Proteins:%d Audited:%d Alerts:%d",
		totalAntibodies, totalGenes, totalProteins, totalAudited, totalAlerts)

	_ = workflow.ExecuteActivity(sqlCtx, a.RecordPaleontologyRunActivity,
		totalAntibodies, totalGenes, totalProteins, totalAudited, totalAlerts, summary).Get(ctx, nil)

	logger.Info(PaleontologistPrefix+" Paleontological analysis complete", "Summary", summary)

	return nil
}
