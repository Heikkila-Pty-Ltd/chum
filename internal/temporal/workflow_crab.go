package temporal

import (
	"fmt"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
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

	// Fire-and-forget notification helper.
	notifyOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	notify := func(event string, extra map[string]string) {
		nCtx := workflow.WithActivityOptions(ctx, notifyOpts)
		_ = workflow.ExecuteActivity(nCtx, a.NotifyActivity, NotifyRequest{
			Event: event, TaskID: req.PlanID, Extra: extra,
		}).Get(ctx, nil)
	}
	notify("crab_start", map[string]string{"plan_id": req.PlanID})

	// ===== PHASE 1: PARSE =====
	parseStart := workflow.Now(ctx)
	logger.Info(CrabPrefix+" Phase 1: PARSE", "PlanID", req.PlanID)

	parseCtx := workflow.WithActivityOptions(ctx, shortAO)
	var plan ParsedPlan
	if err := workflow.ExecuteActivity(parseCtx, a.ParsePlanActivity, req).Get(ctx, &plan); err != nil {
		recordStep("parse", parseStart, "failed")
		return escalateToTurtle(ctx, req, "parse plan failed: "+err.Error())
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

	// If human input is needed, wait on signal channel for answers (up to 10 minutes).
	if clarifications.NeedsHumanInput {
		logger.Info(CrabPrefix+" Waiting for human clarification", "Questions", len(clarifications.HumanQuestions))

		clarificationChan := workflow.GetSignalChannel(ctx, "crab-clarification")
		var humanAnswers string

		timer := workflow.NewTimer(ctx, 10*time.Minute)
		sel := workflow.NewSelector(ctx)
		
		sel.AddReceive(clarificationChan, func(c workflow.ReceiveChannel, more bool) {
			c.Receive(ctx, &humanAnswers)
			logger.Info(CrabPrefix + " Human clarification received")
		})
		
		sel.AddFuture(timer, func(f workflow.Future) {
			humanAnswers = "Human ignored clarification request. Proceeding with best judgement."
			logger.Warn(CrabPrefix + " Clarification timed out (10m) — proceeding blindly")
		})
		
		sel.Select(ctx)

		clarifications.HumanAnswers = humanAnswers
		clarifications.NeedsHumanInput = false
	}

	// ===== PHASE 3: DECOMPOSE =====
	decomposeStart := workflow.Now(ctx)
	logger.Info(CrabPrefix+" Phase 3: DECOMPOSE", "PlanID", req.PlanID)

	decomposeCtx := workflow.WithActivityOptions(ctx, longAO)
	var whales []CandidateWhale
	if err := workflow.ExecuteActivity(decomposeCtx, a.DecomposeActivity, req, plan, clarifications).Get(ctx, &whales); err != nil {
		recordStep("decompose", decomposeStart, "failed")
		return escalateToTurtle(ctx, req, "decomposition failed: "+err.Error())
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
		return escalateToTurtle(ctx, req, "sizing failed: "+err.Error())
	}
	recordStep("size", sizeStart, "ok")

	logger.Info(CrabPrefix+" Sizing complete", "Morsels", len(sizedMorsels))

	// ===== PHASE 6: REVIEW GATE =====
	// Auto-approve by default — human review is opt-in via RequireHumanReview.
	// Even when human review is required, a 10-minute timeout auto-approves to
	// prevent indefinite blocking (the old bug: golf crabs sat idle 53 minutes).
	reviewStart := workflow.Now(ctx)

	decision := "APPROVED" // default: auto-approve
	if req.RequireHumanReview {
		logger.Info(CrabPrefix+" Phase 6: HUMAN REVIEW — waiting for approval (10m timeout)",
			"Whales", len(scopedWhales), "Morsels", len(sizedMorsels))

		reviewChan := workflow.GetSignalChannel(ctx, "crab-review")

		// Use selector with timer — never block forever.
		timerCtx, cancelTimer := workflow.WithCancel(ctx)
		timer := workflow.NewTimer(timerCtx, 10*time.Minute)

		sel := workflow.NewSelector(ctx)
		sel.AddReceive(reviewChan, func(ch workflow.ReceiveChannel, _ bool) {
			ch.Receive(ctx, &decision)
			cancelTimer()
		})
		sel.AddFuture(timer, func(f workflow.Future) {
			decision = "APPROVED" // auto-approve on timeout
			logger.Warn(CrabPrefix + " Review gate timed out (10m) — auto-approving")
		})
		sel.Select(ctx)
	} else {
		logger.Info(CrabPrefix+" Phase 6: AUTO-APPROVED (no human review required)",
			"Whales", len(scopedWhales), "Morsels", len(sizedMorsels))
	}

	if decision != "APPROVED" {
		recordStep("review", reviewStart, "rejected")
		logger.Info(CrabPrefix+" Plan REJECTED by human", "Decision", decision)

		// Record health event so octopus can learn from rejections
		recordCrabHealth(ctx, shortAO, a, req.PlanID, req.Project, "rejected",
			fmt.Sprintf("Plan rejected by human review. Whales: %d, Morsels: %d", len(scopedWhales), len(sizedMorsels)))
		recordOrganismLog(ctx, "crab", req.PlanID, req.Project, "rejected",
			fmt.Sprintf("rejected by human review: %d whales, %d morsels", len(scopedWhales), len(sizedMorsels)),
			startTime, 6, "")

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

		// Record health event so the system knows emit failed
		recordCrabHealth(ctx, shortAO, a, req.PlanID, req.Project, "emit_failed",
			fmt.Sprintf("Emit failed: %v. Whales: %d, Morsels: %d", err, len(scopedWhales), len(sizedMorsels)))

		return escalateToTurtle(ctx, req, "emit failed: "+err.Error())
	}
	recordStep("emit", emitStart, "ok")

	logger.Info(CrabPrefix+" CrabDecomposition complete",
		"PlanID", req.PlanID,
		"WhalesEmitted", len(emitResult.WhaleIDs),
		"MorselsEmitted", len(emitResult.MorselIDs),
		"FailedCount", emitResult.FailedCount,
		"TotalDuration", workflow.Now(ctx).Sub(startTime).String(),
	)
	notify("crab_done", map[string]string{
		"whales":  fmt.Sprintf("%d", len(emitResult.WhaleIDs)),
		"morsels": fmt.Sprintf("%d", len(emitResult.MorselIDs)),
	})

	// Record health event for successful decomposition
	recordCrabHealth(ctx, shortAO, a, req.PlanID, req.Project, "completed",
		fmt.Sprintf("Emitted %d whales, %d morsels in %s",
			len(emitResult.WhaleIDs), len(emitResult.MorselIDs),
			workflow.Now(ctx).Sub(startTime).String()))
	recordOrganismLog(ctx, "crab", req.PlanID, req.Project, "completed",
		fmt.Sprintf("%d whales, %d morsels emitted", len(emitResult.WhaleIDs), len(emitResult.MorselIDs)),
		startTime, 7, "")

	return &CrabDecompositionResult{
		Status:         "completed",
		PlanID:         req.PlanID,
		WhalesEmitted:  emitResult.WhaleIDs,
		MorselsEmitted: emitResult.MorselIDs,
		StepMetrics:    stepMetrics,
		TotalTokens:    totalTokens,
	}, nil
}

// recordCrabHealth records a crab pipeline event to the health_events store
// so the octopus and stingray can observe crab outcomes (previously invisible).
func recordCrabHealth(ctx workflow.Context, opts workflow.ActivityOptions, a *Activities,
	planID, project, status, details string) {

	logger := workflow.GetLogger(ctx)
	if a.Store == nil {
		return
	}
	actCtx := workflow.WithActivityOptions(ctx, opts)
	eventType := fmt.Sprintf("crab_%s", status)
	fullDetails := fmt.Sprintf("[%s] %s: %s", project, planID, details)
	_ = workflow.ExecuteActivity(actCtx, a.RecordHealthEventActivity, eventType, fullDetails).Get(ctx, nil)
	logger.Info(CrabPrefix+" Health event recorded", "EventType", eventType, "PlanID", planID)
}

// escalateToTurtle triggers an autonomous planning ceremony (Turtle) when crab
// decomposition fails. This ensures complex plans that the crab can't slice
// get a higher-level multi-agent deliberation instead of dying silently.
func escalateToTurtle(ctx workflow.Context, req CrabDecompositionRequest, reason string) (*CrabDecompositionResult, error) {
	logger := workflow.GetLogger(ctx)
	logger.Warn(CrabPrefix+" Escalating to Turtle ceremony", "PlanID", req.PlanID, "Reason", reason)

	var a *Activities
	notifyOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	nCtx := workflow.WithActivityOptions(ctx, notifyOpts)
	_ = workflow.ExecuteActivity(nCtx, a.NotifyActivity, NotifyRequest{
		Event: "crab_escalate", TaskID: req.PlanID, Extra: map[string]string{"reason": reason},
	}).Get(ctx, nil)

	turtleReq := TurtlePlanningRequest{
		TaskID:      req.PlanID,
		Project:     req.Project,
		WorkDir:     req.WorkDir,
		Description: req.PlanMarkdown,
		Tier:        "premium", // turtles use top-tier models for deliberation
	}

	childOpts := workflow.ChildWorkflowOptions{
		WorkflowID:            "turtle-" + req.PlanID,
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
		ParentClosePolicy:     enumspb.PARENT_CLOSE_POLICY_ABANDON,
	}
	childCtx := workflow.WithChildOptions(ctx, childOpts)

	var result TurtlePlanningResult
	err := workflow.ExecuteChildWorkflow(childCtx, AutonomousPlanningCeremonyWorkflow, turtleReq).Get(ctx, &result)
	if err != nil {
		return nil, fmt.Errorf("turtle escalation failed: %w", err)
	}

	return &CrabDecompositionResult{
		Status: "escalated",
		PlanID: req.PlanID,
	}, nil
}
