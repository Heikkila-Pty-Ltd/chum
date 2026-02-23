package temporal

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// CrabDecompositionWorkflow takes a high-level markdown plan and decomposes it
// into whales (epic-level groupings) and morsels (bite-sized executable units)
// for shark consumption. The pipeline is 7 phases:
//
//  1. PARSE      — deterministic markdown parsing
//  2. CLARIFY    — 3-tier gap resolution (lessons DB, chief LLM, human escalation)
//  3. DECOMPOSE  — LLM-driven whale/morsel breakdown
//  4. SCOPE      — review and split oversized morsels
//  5. SIZE       — estimate, prioritize, and assign risk levels
//  6. HUMAN REVIEW — wait for human approval before emitting to DAG
//  7. EMIT       — write whales and morsels to the DAG
func CrabDecompositionWorkflow(ctx workflow.Context, req CrabDecompositionRequest) (*CrabDecompositionResult, error) {
	startTime := workflow.Now(ctx)
	logger := workflow.GetLogger(ctx)
	logger.Info(CrabPrefix+" CrabDecomposition starting", "PlanID", req.PlanID, "Project", req.Project)

	if req.Tier == "" {
		req.Tier = "fast"
	}

	slowThreshold := defaultSlowStepThreshold

	var stepMetrics []StepMetric
	recordStep := func(name string, stepStart time.Time, status string) {
		dur := workflow.Now(ctx).Sub(stepStart)
		slow := dur >= slowThreshold
		stepMetrics = append(stepMetrics, StepMetric{
			Name:      name,
			DurationS: dur.Seconds(),
			Status:    status,
			Slow:      slow,
		})
		if slow {
			logger.Warn(CrabPrefix+" SLOW STEP",
				"Step", name, "DurationS", dur.Seconds(), "Threshold", slowThreshold.String(), "Status", status)
		} else {
			logger.Info(CrabPrefix+" Step complete",
				"Step", name, "DurationS", dur.Seconds(), "Status", status)
		}
	}

	var totalTokens TokenUsage

	// --- Activity option presets ---
	shortAO := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	}
	longAO := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
		HeartbeatTimeout:    30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	}
	mediumAO := workflow.ActivityOptions{
		StartToCloseTimeout: 3 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	}

	var a *Activities

	// ===== PHASE 1: PARSE =====
	parseStart := workflow.Now(ctx)
	logger.Info(CrabPrefix+" Phase 1: PARSE", "PlanID", req.PlanID)

	parseCtx := workflow.WithActivityOptions(ctx, shortAO)
	var plan ParsedPlan
	if err := workflow.ExecuteActivity(parseCtx, a.ParsePlanActivity, req).Get(ctx, &plan); err != nil {
		recordStep("parse", parseStart, "failed")
		return nil, fmt.Errorf("parse plan failed: %w", err)
	}
	recordStep("parse", parseStart, "ok")

	logger.Info(CrabPrefix+" Plan parsed", "Title", plan.Title, "ScopeItems", len(plan.ScopeItems))

	// ===== PHASE 2: CLARIFY =====
	clarifyStart := workflow.Now(ctx)
	logger.Info(CrabPrefix+" Phase 2: CLARIFY", "PlanID", req.PlanID)

	clarifyCtx := workflow.WithActivityOptions(ctx, longAO)
	var clarifications ClarificationResult
	if err := workflow.ExecuteActivity(clarifyCtx, a.ClarifyGapsActivity, req, plan).Get(ctx, &clarifications); err != nil {
		logger.Warn(CrabPrefix+" Clarification failed (non-fatal), continuing with empty clarifications", "error", err)
		recordStep("clarify", clarifyStart, "failed")
		clarifications = ClarificationResult{}
	} else {
		totalTokens.Add(clarifications.Tokens)
		recordStep("clarify", clarifyStart, "ok")
	}

	// If human input is needed, wait on signal channel for answers.
	if clarifications.NeedsHumanInput {
		logger.Info(CrabPrefix+" Waiting for human clarification", "Questions", len(clarifications.HumanQuestions))

		clarificationChan := workflow.GetSignalChannel(ctx, "crab-clarification")
		var humanAnswers string
		clarificationChan.Receive(ctx, &humanAnswers)

		clarifications.HumanAnswers = humanAnswers
		clarifications.NeedsHumanInput = false

		logger.Info(CrabPrefix + " Human clarification received")
	}

	// ===== PHASE 3: DECOMPOSE =====
	decomposeStart := workflow.Now(ctx)
	logger.Info(CrabPrefix+" Phase 3: DECOMPOSE", "PlanID", req.PlanID)

	decomposeCtx := workflow.WithActivityOptions(ctx, longAO)
	var whales []CandidateWhale
	if err := workflow.ExecuteActivity(decomposeCtx, a.DecomposeActivity, req, plan, clarifications).Get(ctx, &whales); err != nil {
		recordStep("decompose", decomposeStart, "failed")
		return nil, fmt.Errorf("decomposition failed: %w", err)
	}
	recordStep("decompose", decomposeStart, "ok")

	logger.Info(CrabPrefix+" Decomposition complete", "Whales", len(whales))

	// ===== PHASE 4: SCOPE =====
	scopeStart := workflow.Now(ctx)
	logger.Info(CrabPrefix+" Phase 4: SCOPE", "Whales", len(whales))

	scopeCtx := workflow.WithActivityOptions(ctx, mediumAO)
	var scopedWhales []CandidateWhale
	if err := workflow.ExecuteActivity(scopeCtx, a.ScopeMorselsActivity, req, whales).Get(ctx, &scopedWhales); err != nil {
		logger.Warn(CrabPrefix+" Scoping failed (non-fatal), continuing with unscoped candidates", "error", err)
		recordStep("scope", scopeStart, "failed")
		scopedWhales = whales
	} else {
		recordStep("scope", scopeStart, "ok")
	}

	// ===== PHASE 5: SIZE =====
	sizeStart := workflow.Now(ctx)
	logger.Info(CrabPrefix+" Phase 5: SIZE", "Whales", len(scopedWhales))

	sizeCtx := workflow.WithActivityOptions(ctx, mediumAO)
	var sizedMorsels []SizedMorsel
	if err := workflow.ExecuteActivity(sizeCtx, a.SizeMorselsActivity, req, scopedWhales).Get(ctx, &sizedMorsels); err != nil {
		recordStep("size", sizeStart, "failed")
		return nil, fmt.Errorf("sizing failed: %w", err)
	}
	recordStep("size", sizeStart, "ok")

	logger.Info(CrabPrefix+" Sizing complete", "Morsels", len(sizedMorsels))

	// ===== PHASE 6: HUMAN REVIEW =====
	reviewStart := workflow.Now(ctx)
	logger.Info(CrabPrefix+" Phase 6: HUMAN REVIEW — waiting for approval",
		"Whales", len(scopedWhales), "Morsels", len(sizedMorsels))

	reviewChan := workflow.GetSignalChannel(ctx, "crab-review")
	var decision string
	reviewChan.Receive(ctx, &decision)

	if decision != "APPROVED" {
		recordStep("review", reviewStart, "rejected")
		logger.Info(CrabPrefix+" Plan REJECTED by human", "Decision", decision)

		return &CrabDecompositionResult{
			Status:      "rejected",
			PlanID:      req.PlanID,
			StepMetrics: stepMetrics,
			TotalTokens: totalTokens,
		}, nil
	}
	recordStep("review", reviewStart, "ok")

	logger.Info(CrabPrefix + " Plan APPROVED — emitting to DAG")

	// ===== PHASE 7: EMIT =====
	emitStart := workflow.Now(ctx)
	logger.Info(CrabPrefix+" Phase 7: EMIT", "PlanID", req.PlanID)

	emitCtx := workflow.WithActivityOptions(ctx, shortAO)
	var emitResult EmitResult
	if err := workflow.ExecuteActivity(emitCtx, a.EmitMorselsActivity, req, scopedWhales, sizedMorsels).Get(ctx, &emitResult); err != nil {
		recordStep("emit", emitStart, "failed")
		return nil, fmt.Errorf("emit failed: %w", err)
	}
	recordStep("emit", emitStart, "ok")

	logger.Info(CrabPrefix+" CrabDecomposition complete",
		"PlanID", req.PlanID,
		"WhalesEmitted", len(emitResult.WhaleIDs),
		"MorselsEmitted", len(emitResult.MorselIDs),
		"FailedCount", emitResult.FailedCount,
		"TotalDuration", workflow.Now(ctx).Sub(startTime).String(),
	)

	return &CrabDecompositionResult{
		Status:         "completed",
		PlanID:         req.PlanID,
		WhalesEmitted:  emitResult.WhaleIDs,
		MorselsEmitted: emitResult.MorselIDs,
		StepMetrics:    stepMetrics,
		TotalTokens:    totalTokens,
	}, nil
}
