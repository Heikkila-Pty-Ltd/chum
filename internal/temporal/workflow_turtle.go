package temporal

import (
	"fmt"
	"strings"
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

// AutonomousPlanningCeremonyWorkflow produces a high-level plan for complex
// tasks via a single LLM call. The result is a TurtleConsensus that callers
// (typically TurtleToCrabWorkflow) feed to a crab for decomposition into morsels.
//
// This replaces the old 3-agent deliberation ceremony (Explore→Deliberate→Converge)
// with a single-stage approach: one agent, one call, one plan.
func AutonomousPlanningCeremonyWorkflow(ctx workflow.Context, req TurtlePlanningRequest) (*TurtlePlanningResult, error) {
	startTime := workflow.Now(ctx)
	logger := workflow.GetLogger(ctx)
	logger.Info(TurtlePrefix+" Single-stage planning starting",
		"TaskID", req.TaskID, "Project", req.Project)

	if req.Tier == "" {
		req.Tier = "balanced"
	}

	var stepMetrics []StepMetric
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
		"task":        req.TaskID,
		"description": truncate(req.Description, 200),
	})

	// ===== SINGLE-STAGE PLAN =====
	planStart := workflow.Now(ctx)
	logger.Info(TurtlePrefix+" Planning task", "TaskID", req.TaskID)

	planCtx := workflow.WithActivityOptions(ctx, longAO)
	var consensus TurtleConsensus
	if err := workflow.ExecuteActivity(planCtx, a.TurtlePlanActivity, req).Get(ctx, &consensus); err != nil {
		dur := workflow.Now(ctx).Sub(planStart)
		stepMetrics = append(stepMetrics, StepMetric{Name: "plan", DurationS: dur.Seconds(), Status: "failed"})
		notify("turtle_failed", map[string]string{"phase": "plan", "error": err.Error()})
		return &TurtlePlanningResult{Status: "failed", TaskID: req.TaskID, StepMetrics: stepMetrics}, nil
	}
	dur := workflow.Now(ctx).Sub(planStart)
	stepMetrics = append(stepMetrics, StepMetric{Name: "plan", DurationS: dur.Seconds(), Status: "ok"})

	logger.Info(TurtlePrefix+" Plan produced",
		"Score", consensus.ConfidenceScore, "Items", len(consensus.Items))

	// ===== DONE — Turtle has defined the plan. Callers feed it to a crab. =====
	duration := workflow.Now(ctx).Sub(startTime)
	logger.Info(TurtlePrefix+" Planning complete — ready for crab decomposition",
		"TaskID", req.TaskID, "Items", len(consensus.Items),
		"Duration", duration.String(), "Confidence", consensus.ConfidenceScore)

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

	recordOrganismLog(ctx, "turtle", req.TaskID, req.Project, "completed",
		fmt.Sprintf("%d items, confidence %d%%, 1 stage", len(consensus.Items), consensus.ConfidenceScore),
		startTime, 1, "")

	return &TurtlePlanningResult{
		Status:          "completed",
		TaskID:          req.TaskID,
		Consensus:       &consensus,
		Rounds:          1,
		ConfidenceScore: consensus.ConfidenceScore,
		StepMetrics:     stepMetrics,
	}, nil
}

// TurtleToCrabWorkflow chains turtle planning → human gate → crab decomposition.
// Turtles define (single-stage plan). Crabs slice (decompose, emit).
// The human gate between turtle and crab allows plan review before work is broken
// into morsels. A 10-minute timeout auto-approves to prevent indefinite blocking.
func TurtleToCrabWorkflow(ctx workflow.Context, req TurtlePlanningRequest) (*TurtlePlanningResult, error) {
	startTime := workflow.Now(ctx)
	logger := workflow.GetLogger(ctx)
	logger.Info(TurtlePrefix+" Turtle→Gate→Crab pipeline starting", "TaskID", req.TaskID)

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
		logger.Warn(TurtlePrefix+" Turtle produced no plan, skipping crab",
			"Status", result.Status)
		return &result, nil
	}

	// Phase 2: Human gate — review the plan before crab slices it
	logger.Info(TurtlePrefix+" Plan gate — waiting for approval (10m timeout)",
		"TaskID", req.TaskID, "Items", len(result.Consensus.Items),
		"Score", result.Consensus.ConfidenceScore)

	// Notify for visibility (post plan summary to Matrix)
	var a *Activities
	notifyOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	nCtx := workflow.WithActivityOptions(ctx, notifyOpts)
	_ = workflow.ExecuteActivity(nCtx, a.NotifyActivity, NotifyRequest{
		Event:  "turtle_plan_gate",
		TaskID: req.TaskID,
		Extra: map[string]string{
			"items":    fmt.Sprintf("%d", len(result.Consensus.Items)),
			"score":    fmt.Sprintf("%d", result.Consensus.ConfidenceScore),
			"plan":     truncate(result.Consensus.MergedPlan, 500),
		},
	}).Get(ctx, nil)

	// Wait for signal or auto-approve after 10 minutes
	reviewChan := workflow.GetSignalChannel(ctx, "turtle-plan-review")
	decision := "APPROVED" // default

	timerCtx, cancelTimer := workflow.WithCancel(ctx)
	timer := workflow.NewTimer(timerCtx, 10*time.Minute)

	sel := workflow.NewSelector(ctx)
	sel.AddReceive(reviewChan, func(ch workflow.ReceiveChannel, _ bool) {
		ch.Receive(ctx, &decision)
		cancelTimer()
		logger.Info(TurtlePrefix+" Plan review received", "Decision", decision)
	})
	sel.AddFuture(timer, func(f workflow.Future) {
		decision = "APPROVED"
		logger.Info(TurtlePrefix + " Plan gate timed out (10m) — auto-approving")
	})
	sel.Select(ctx)

	if decision != "APPROVED" {
		logger.Info(TurtlePrefix+" Plan REJECTED — aborting crab pipeline", "Decision", decision)
		result.Status = "rejected"

		recordOrganismLog(ctx, "turtle_crab", req.TaskID, req.Project, "rejected",
			fmt.Sprintf("plan rejected at gate: %s", decision),
			startTime, 2, "")

		return &result, nil
	}

	logger.Info(TurtlePrefix + " Plan APPROVED — handing to crab for decomposition")

	// Phase 3: Crab slices
	crabReq := CrabDecompositionRequest{
		PlanID:       req.TaskID,
		Project:      req.Project,
		WorkDir:      req.WorkDir,
		PlanMarkdown: formatConsensusAsPlanMarkdown(req.TaskID, result.Consensus),
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
	logger.Info(TurtlePrefix+" Turtle→Gate→Crab pipeline complete",
		"TaskID", req.TaskID, "Morsels", len(result.MorselsEmitted))

	recordOrganismLog(ctx, "turtle_crab", req.TaskID, req.Project, "completed",
		fmt.Sprintf("turtle→gate→crab pipeline: %d morsels emitted", len(result.MorselsEmitted)),
		startTime, 3, "")

	return &result, nil
}

// formatConsensusAsPlanMarkdown converts a TurtleConsensus into structured
// markdown that the crab parser can parse (requires # title + ## Scope checklist).
func formatConsensusAsPlanMarkdown(taskID string, c *TurtleConsensus) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Plan: %s\n\n", taskID))
	sb.WriteString("## Context\n")
	sb.WriteString(c.MergedPlan)
	sb.WriteString("\n\n## Scope\n")
	if len(c.Items) > 0 {
		for _, item := range c.Items {
			sb.WriteString(fmt.Sprintf("- [ ] %s: %s\n", item.Title, item.Description))
		}
	} else {
		// Fallback: synthesize a single scope item from the plan title
		sb.WriteString(fmt.Sprintf("- [ ] Implement %s\n", taskID))
	}
	if len(c.Disagreements) > 0 {
		sb.WriteString("\n## Notes\n")
		for _, d := range c.Disagreements {
			sb.WriteString(fmt.Sprintf("- %s\n", d))
		}
	}
	return sb.String()
}
