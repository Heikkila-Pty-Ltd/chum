package temporal

import (
	"fmt"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// TurtlePrefix is the ANSI-colored log prefix for turtle planning ceremonies.
const TurtlePrefix = "\033[32m🐢 TURTLE\033[0m"

// TurtlePlanningRequest starts an autonomous multi-agent planning ceremony.
type TurtlePlanningRequest struct {
	TaskID      string   `json:"task_id"`
	Project     string   `json:"project"`
	WorkDir     string   `json:"work_dir"`
	Description string   `json:"description"`          // full task description
	Context     []string `json:"context_files"`         // key file paths to include as context
	MatrixRoom  string   `json:"matrix_room,omitempty"` // override default room for deliberation
	Tier        string   `json:"tier"`                  // LLM tier for planning agents
}

// TurtleProposal is one agent's independent analysis of the task.
type TurtleProposal struct {
	Agent      string   `json:"agent"`       // which agent produced this
	Approach   string   `json:"approach"`    // proposed implementation approach
	Scope      string   `json:"scope"`       // estimated scope and effort
	Risks      []string `json:"risks"`       // identified risks
	Morsels    []string `json:"morsels"`     // suggested morsel breakdown
	Confidence int      `json:"confidence"`  // 0-100 confidence in this approach
}

// TurtleCritique is one agent's review of all proposals from a deliberation round.
type TurtleCritique struct {
	Agent       string `json:"agent"`
	Round       int    `json:"round"`
	Synthesis   string `json:"synthesis"`    // merged perspective after reviewing all proposals
	Agreements  string `json:"agreements"`   // areas of consensus
	Disagreements string `json:"disagreements"` // areas of divergence
	Revised     string `json:"revised"`      // revised approach after deliberation
}

// TurtleConsensus is the merged plan after deliberation.
type TurtleConsensus struct {
	MergedPlan      string          `json:"merged_plan"`
	ConfidenceScore int             `json:"confidence_score"` // 0-100 overall
	Items           []ConsensusItem `json:"items"`
	Disagreements   []string        `json:"disagreements,omitempty"` // unresolved items
}

// ConsensusItem is one deliverable in the merged plan with a confidence score.
type ConsensusItem struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Confidence  int    `json:"confidence"` // 0-100 per-item
	Effort      string `json:"effort"`
}

// TurtleMorsel is a decomposed morsel ready for emission to the DAG.
type TurtleMorsel struct {
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	AcceptanceCriteria string   `json:"acceptance_criteria"`
	DoDChecks          []string `json:"dod_checks"`
	Priority           int      `json:"priority"`
	EstimateMinutes    int      `json:"estimate_minutes"`
	Labels             []string `json:"labels"`
	DependsOn          []string `json:"depends_on,omitempty"`
}

// TurtlePlanningResult is the output of the autonomous planning ceremony.
type TurtlePlanningResult struct {
	Status          string           `json:"status"` // "completed", "escalated", "no_consensus"
	TaskID          string           `json:"task_id"`
	Consensus       *TurtleConsensus `json:"consensus,omitempty"` // the merged plan — callers feed this to a crab
	MorselsEmitted  []string         `json:"morsels_emitted"`     // deprecated: crabs now handle emission
	Rounds          int              `json:"rounds"`
	ConfidenceScore int              `json:"confidence_score"`
	StepMetrics     []StepMetric     `json:"step_metrics"`
	TotalTokens     TokenUsage       `json:"total_tokens"`
}

// AutonomousPlanningCeremonyWorkflow runs a 3-agent deliberation to decompose
// complex tasks into shark-sized morsels. The ceremony runs autonomously:
// - Phase 1: EXPLORE — 3 agents independently analyze the task
// - Phase 2: DELIBERATE — up to 5 rounds of cross-review
// - Phase 3: CONVERGE — consensus check (≥80% → auto-proceed)
// - Phase 4: DECOMPOSE — break into morsels (recursive if complex)
// - Phase 5: EMIT — write to DAG
//
// All phases are posted to a Matrix channel for human visibility.
// Only disagreements escalate to human — consensus auto-proceeds.
func AutonomousPlanningCeremonyWorkflow(ctx workflow.Context, req TurtlePlanningRequest) (*TurtlePlanningResult, error) {
	startTime := workflow.Now(ctx)
	logger := workflow.GetLogger(ctx)
	logger.Info(TurtlePrefix+" Autonomous planning ceremony starting",
		"TaskID", req.TaskID, "Project", req.Project)

	if req.Tier == "" {
		req.Tier = "balanced" // use balanced tier for planning — quality matters
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
			logger.Warn(TurtlePrefix+" SLOW STEP",
				"Step", name, "DurationS", dur.Seconds(), "Status", status)
		} else {
			logger.Info(TurtlePrefix+" Step complete",
				"Step", name, "DurationS", dur.Seconds(), "Status", status)
		}
	}

	var totalTokens TokenUsage
	var a *Activities

	// Activity option presets
	longAO := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		HeartbeatTimeout:    60 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	}
	shortAO := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	}

	// Fire-and-forget notification helper.
	notifyOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	notify := func(event string, extra map[string]string) {
		nCtx := workflow.WithActivityOptions(ctx, notifyOpts)
		_ = workflow.ExecuteActivity(nCtx, a.NotifyActivity, NotifyRequest{
			Event: event, TaskID: req.TaskID, Extra: extra,
		}).Get(ctx, nil)
	}
	notify("turtle_start", map[string]string{
		"task": req.TaskID,
		"description": truncate(req.Description, 200),
	})

	// Per-agent Matrix send helper — posts as the agent's dedicated bot persona.
	sendAs := func(agent, message string) {
		sendCtx := workflow.WithActivityOptions(ctx, notifyOpts)
		_ = workflow.ExecuteActivity(sendCtx, a.TurtleSendAsActivity, TurtleSendAsRequest{
			Agent:   agent,
			Room:    req.MatrixRoom,
			Message: message,
		}).Get(ctx, nil)
	}

	// ===== PHASE 1: EXPLORE =====
	exploreStart := workflow.Now(ctx)
	logger.Info(TurtlePrefix+" Phase 1: EXPLORE — 3 agents analyzing independently", "TaskID", req.TaskID)

	exploreCtx := workflow.WithActivityOptions(ctx, longAO)
	var proposals []TurtleProposal
	if err := workflow.ExecuteActivity(exploreCtx, a.TurtleExploreActivity, req).Get(ctx, &proposals); err != nil {
		recordStep("explore", exploreStart, "failed")
		notify("turtle_failed", map[string]string{"phase": "explore", "error": err.Error()})
		return &TurtlePlanningResult{Status: "failed", TaskID: req.TaskID, StepMetrics: stepMetrics}, nil
	}
	recordStep("explore", exploreStart, "ok")

	logger.Info(TurtlePrefix+" Exploration complete", "Proposals", len(proposals))

	// Post each proposal as its agent's bot persona
	for _, p := range proposals {
		if p.Confidence == 0 && len(p.Morsels) == 0 {
			continue // skip stub proposals from failed agents
		}
		msg := fmt.Sprintf("**Proposal for `%s`**\n\n%s\n\nScope: %s | Confidence: %d%%\nMorsels: %s",
			req.TaskID, truncate(p.Approach, 500), p.Scope, p.Confidence,
			truncate(fmt.Sprintf("%v", p.Morsels), 300))
		sendAs(p.Agent, msg)
	}

	// ===== PHASE 2: DELIBERATE (up to 5 rounds) =====
	const maxRounds = 2
	const convergenceThreshold = 80

	var allCritiques []TurtleCritique
	currentProposals := proposals

	for round := 1; round <= maxRounds; round++ {
		deliberateStart := workflow.Now(ctx)
		logger.Info(TurtlePrefix+" Phase 2: DELIBERATE", "Round", round, "MaxRounds", maxRounds)

		deliberateCtx := workflow.WithActivityOptions(ctx, longAO)
		var critiques []TurtleCritique
		if err := workflow.ExecuteActivity(deliberateCtx, a.TurtleDeliberateActivity, req, currentProposals, allCritiques, round).Get(ctx, &critiques); err != nil {
			logger.Warn(TurtlePrefix+" Deliberation round failed (non-fatal)", "Round", round, "error", err)
			recordStep(fmt.Sprintf("deliberate_r%d", round), deliberateStart, "failed")
			break
		}
		allCritiques = append(allCritiques, critiques...)
		recordStep(fmt.Sprintf("deliberate_r%d", round), deliberateStart, "ok")

		// Post each critique as its agent's bot persona
		for _, c := range critiques {
			msg := fmt.Sprintf("**Round %d review**\n\n%s\n\nAgreed: %s\nDisagreed: %s",
				c.Round, truncate(c.Synthesis, 400),
				truncate(c.Agreements, 200), truncate(c.Disagreements, 200))
			sendAs(c.Agent, msg)
		}

		// Check convergence — if all agents are mostly aligned, exit early
		converging := true
		for _, c := range critiques {
			if c.Disagreements != "" && len(c.Disagreements) > 20 {
				converging = false
				break
			}
		}
		if converging {
			logger.Info(TurtlePrefix+" Convergence detected, exiting deliberation early", "Round", round)
			break
		}

		logger.Info(TurtlePrefix+" Round complete, continuing deliberation",
			"Round", round, "Critiques", len(critiques))
	}

	// ===== PHASE 3: CONVERGE =====
	convergeStart := workflow.Now(ctx)
	logger.Info(TurtlePrefix+" Phase 3: CONVERGE — synthesizing consensus")

	convergeCtx := workflow.WithActivityOptions(ctx, longAO)
	var consensus TurtleConsensus
	if err := workflow.ExecuteActivity(convergeCtx, a.TurtleConvergeActivity, req, proposals, allCritiques).Get(ctx, &consensus); err != nil {
		recordStep("converge", convergeStart, "failed")
		notify("turtle_failed", map[string]string{"phase": "converge", "error": err.Error()})
		return &TurtlePlanningResult{Status: "failed", TaskID: req.TaskID, StepMetrics: stepMetrics}, nil
	}
	recordStep("converge", convergeStart, "ok")

	logger.Info(TurtlePrefix+" Consensus result",
		"Score", consensus.ConfidenceScore, "Items", len(consensus.Items), "Disagreements", len(consensus.Disagreements))

	// If consensus is low, wait for human tiebreak via signal
	if consensus.ConfidenceScore < convergenceThreshold && len(consensus.Disagreements) > 0 {
		logger.Info(TurtlePrefix+" Low consensus — requesting human tiebreak",
			"Score", consensus.ConfidenceScore)

		notify("turtle_disagreement", map[string]string{
			"task":          req.TaskID,
			"score":         fmt.Sprintf("%d", consensus.ConfidenceScore),
			"disagreements": truncate(fmt.Sprintf("%v", consensus.Disagreements), 300),
		})

		// Wait for human signal (up to 30 minutes, then proceed with majority)
		tiebreakChan := workflow.GetSignalChannel(ctx, "turtle-tiebreak")
		var humanDecision string

		timer := workflow.NewTimer(ctx, 30*time.Minute)
		sel := workflow.NewSelector(ctx)

		sel.AddReceive(tiebreakChan, func(c workflow.ReceiveChannel, more bool) {
			c.Receive(ctx, &humanDecision)
			logger.Info(TurtlePrefix+" Human tiebreak received", "Decision", humanDecision)
		})

		sel.AddFuture(timer, func(f workflow.Future) {
			humanDecision = "proceed-majority"
			logger.Warn(TurtlePrefix + " Tiebreak timed out (30m) — proceeding with majority")
		})

		sel.Select(ctx)
	}

	// ===== DONE — Turtle has defined the plan. Callers feed it to a crab. =====
	// The old phases 4 (DECOMPOSE) and 5 (EMIT) have been removed.
	// Turtles define. Crabs slice. Clean separation of concerns.

	duration := workflow.Now(ctx).Sub(startTime)
	rounds := len(allCritiques) / max(len(PlanningAgents), 1)
	logger.Info(TurtlePrefix+" Ceremony complete — consensus ready for crab decomposition",
		"TaskID", req.TaskID, "Items", len(consensus.Items),
		"Duration", duration.String(), "Consensus", consensus.ConfidenceScore, "Rounds", rounds)

	notify("turtle_done", map[string]string{
		"task":     req.TaskID,
		"items":    fmt.Sprintf("%d", len(consensus.Items)),
		"score":    fmt.Sprintf("%d", consensus.ConfidenceScore),
		"duration": fmtDuration(duration),
	})

	// Record health event for the fossil record
	recordCtx := workflow.WithActivityOptions(ctx, shortAO)
	_ = workflow.ExecuteActivity(recordCtx, a.RecordHealthEventActivity,
		"turtle_completed",
		fmt.Sprintf("[%s] Turtle planned %s: %d items, confidence %d%%, %s — awaiting crab decomposition",
			req.Project, req.TaskID, len(consensus.Items), consensus.ConfidenceScore, fmtDuration(duration)),
	).Get(ctx, nil)

	return &TurtlePlanningResult{
		Status:          "completed",
		TaskID:          req.TaskID,
		Consensus:       &consensus,
		Rounds:          rounds,
		ConfidenceScore: consensus.ConfidenceScore,
		StepMetrics:     stepMetrics,
		TotalTokens:     totalTokens,
	}, nil
}

// TurtleToCrabWorkflow chains turtle planning → crab decomposition.
// Turtles define (explore, deliberate, converge). Crabs slice (decompose, emit).
// This is the pipeline for complex tasks: the dispatcher fires this as one
// child workflow, and the turtle→crab handoff happens internally.
func TurtleToCrabWorkflow(ctx workflow.Context, req TurtlePlanningRequest) (*TurtlePlanningResult, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info(TurtlePrefix+" Turtle→Crab pipeline starting", "TaskID", req.TaskID)

	// Phase 1: Turtle plans
	turtleOpts := workflow.ChildWorkflowOptions{
		ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_ABANDON,
	}
	turtleCtx := workflow.WithChildOptions(ctx, turtleOpts)

	var result TurtlePlanningResult
	if err := workflow.ExecuteChildWorkflow(turtleCtx, AutonomousPlanningCeremonyWorkflow, req).Get(ctx, &result); err != nil {
		return nil, fmt.Errorf("turtle planning failed: %w", err)
	}

	if result.Status != "completed" || result.Consensus == nil || result.Consensus.MergedPlan == "" {
		logger.Warn(TurtlePrefix+" Turtle produced no consensus, skipping crab",
			"Status", result.Status)
		return &result, nil
	}

	// Phase 2: Crab slices
	crabReq := CrabDecompositionRequest{
		PlanID:       req.TaskID,
		Project:      req.Project,
		WorkDir:      req.WorkDir,
		PlanMarkdown: result.Consensus.MergedPlan,
		Tier:         "balanced",
	}
	crabOpts := workflow.ChildWorkflowOptions{
		ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_ABANDON,
	}
	crabCtx := workflow.WithChildOptions(ctx, crabOpts)

	var crabResult CrabDecompositionResult
	if err := workflow.ExecuteChildWorkflow(crabCtx, CrabDecompositionWorkflow, crabReq).Get(ctx, &crabResult); err != nil {
		logger.Warn(TurtlePrefix+" Post-turtle crab decomposition failed", "error", err)
		// Still return the turtle result — the plan exists even if crab failed
		return &result, nil
	}

	result.MorselsEmitted = crabResult.MorselsEmitted
	logger.Info(TurtlePrefix+" Turtle→Crab pipeline complete",
		"TaskID", req.TaskID, "Morsels", len(result.MorselsEmitted))

	return &result, nil
}
