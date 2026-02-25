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

// TurtlePlanningRequest starts an autonomous planning ceremony.
// Turtle now runs a single high-tier planner that produces a structured
// markdown artifact for crab decomposition.
type TurtlePlanningRequest struct {
	TaskID      string   `json:"task_id"`
	Project     string   `json:"project"`
	WorkDir     string   `json:"work_dir"`
	Description string   `json:"description"`           // full task description
	Context     []string `json:"context_files"`         // key file paths to include as context
	MatrixRoom  string   `json:"matrix_room,omitempty"` // optional room for turtle status messages
	Tier        string   `json:"tier"`                  // requested LLM tier (turtle enforces premium)
}

// TurtleProposal is one agent's independent analysis of the task.
// Retained for compatibility with existing turtle activities.
type TurtleProposal struct {
	Agent      string   `json:"agent"`      // which agent produced this
	Approach   string   `json:"approach"`   // proposed implementation approach
	Scope      string   `json:"scope"`      // estimated scope and effort
	Risks      []string `json:"risks"`      // identified risks
	Morsels    []string `json:"morsels"`    // suggested morsel breakdown
	Confidence int      `json:"confidence"` // 0-100 confidence in this approach
}

// TurtleCritique is one agent's review of all proposals from a deliberation round.
// Retained for compatibility with existing turtle activities.
type TurtleCritique struct {
	Agent         string `json:"agent"`
	Round         int    `json:"round"`
	Synthesis     string `json:"synthesis"`     // merged perspective after reviewing all proposals
	Agreements    string `json:"agreements"`    // areas of consensus
	Disagreements string `json:"disagreements"` // areas of divergence
	Revised       string `json:"revised"`       // revised approach after deliberation
}

// TurtleConsensus is the merged plan after deliberation.
// Retained for compatibility with existing turtle activities.
type TurtleConsensus struct {
	MergedPlan      string          `json:"merged_plan"`
	ConfidenceScore int             `json:"confidence_score"` // 0-100 overall
	Items           []ConsensusItem `json:"items"`
	Disagreements   []string        `json:"disagreements,omitempty"` // unresolved items
}

// ConsensusItem is one deliverable in the merged plan with a confidence score.
// Retained for compatibility with existing turtle activities.
type ConsensusItem struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Confidence  int    `json:"confidence"` // 0-100 per-item
	Effort      string `json:"effort"`
}

// TurtleMorsel is a decomposed morsel ready for emission to the DAG.
// Retained for compatibility with existing turtle activities.
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

// TurtlePlanArtifact is the crab-parseable planning document produced by turtle.
type TurtlePlanArtifact struct {
	Title        string     `json:"title"`
	PlanMarkdown string     `json:"plan_markdown"`
	ScopeItems   int        `json:"scope_items"`
	Tokens       TokenUsage `json:"tokens,omitempty"`
}

// TurtlePlanningResult is the output of the autonomous planning ceremony.
type TurtlePlanningResult struct {
	Status          string       `json:"status"` // "completed", "failed"
	TaskID          string       `json:"task_id"`
	MorselsEmitted  []string     `json:"morsels_emitted"`
	Rounds          int          `json:"rounds"`
	ConfidenceScore int          `json:"confidence_score"`
	StepMetrics     []StepMetric `json:"step_metrics"`
	TotalTokens     TokenUsage   `json:"total_tokens"`
}

// AutonomousPlanningCeremonyWorkflow runs a single high-tier planner that
// generates a markdown artifact and immediately hands it to the crab workflow
// for deterministic decomposition and emission.
func AutonomousPlanningCeremonyWorkflow(ctx workflow.Context, req TurtlePlanningRequest) (*TurtlePlanningResult, error) {
	startTime := workflow.Now(ctx)
	logger := workflow.GetLogger(ctx)
	logger.Info(TurtlePrefix+" Autonomous planning ceremony starting",
		"TaskID", req.TaskID, "Project", req.Project)

	// Hotfix behavior: turtle planning always uses a single high-tier planner.
	if strings.TrimSpace(strings.ToLower(req.Tier)) != "premium" {
		logger.Info(TurtlePrefix+" Overriding requested tier for turtle planning",
			"RequestedTier", req.Tier, "EffectiveTier", "premium")
		req.Tier = "premium"
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

	// Activity option presets.
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

	// ===== PHASE 1: PLAN ARTIFACT =====
	planStart := workflow.Now(ctx)
	logger.Info(TurtlePrefix+" Phase 1: PLAN ARTIFACT — single premium planner drafting crab-ready markdown",
		"TaskID", req.TaskID)

	planCtx := workflow.WithActivityOptions(ctx, longAO)
	var artifact TurtlePlanArtifact
	if err := workflow.ExecuteActivity(planCtx, a.TurtlePlanArtifactActivity, req).Get(ctx, &artifact); err != nil {
		recordStep("plan_artifact", planStart, "failed")
		notify("turtle_failed", map[string]string{"phase": "plan_artifact", "error": err.Error()})
		return &TurtlePlanningResult{Status: "failed", TaskID: req.TaskID, StepMetrics: stepMetrics}, nil
	}
	if strings.TrimSpace(artifact.PlanMarkdown) == "" {
		recordStep("plan_artifact", planStart, "failed")
		notify("turtle_failed", map[string]string{"phase": "plan_artifact", "error": "empty plan artifact"})
		return &TurtlePlanningResult{Status: "failed", TaskID: req.TaskID, StepMetrics: stepMetrics}, nil
	}
	totalTokens.Add(artifact.Tokens)
	recordStep("plan_artifact", planStart, "ok")

	logger.Info(TurtlePrefix+" Plan artifact ready",
		"Title", artifact.Title, "ScopeItems", artifact.ScopeItems, "Bytes", len(artifact.PlanMarkdown))
	notify("turtle_artifact", map[string]string{
		"title":       artifact.Title,
		"scope_items": fmt.Sprintf("%d", artifact.ScopeItems),
	})

	// ===== PHASE 2: HANDOFF TO CRABS =====
	crabStart := workflow.Now(ctx)
	logger.Info(TurtlePrefix+" Phase 2: HANDOFF — sending turtle artifact to crab decomposition",
		"TaskID", req.TaskID)

	crabReq := CrabDecompositionRequest{
		PlanID:                  req.TaskID,
		Project:                 req.Project,
		WorkDir:                 req.WorkDir,
		PlanMarkdown:            artifact.PlanMarkdown,
		Tier:                    "premium",
		RequireHumanReview:      false,
		DisableTurtleEscalation: true, // Prevent turtle->crab->turtle recursion loops.
	}

	childOpts := workflow.ChildWorkflowOptions{
		WorkflowID:            fmt.Sprintf("crab-from-turtle-%s-%d", req.TaskID, workflow.Now(ctx).Unix()),
		WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
		ParentClosePolicy:     enumspb.PARENT_CLOSE_POLICY_ABANDON,
	}
	childCtx := workflow.WithChildOptions(ctx, childOpts)

	var crabResult CrabDecompositionResult
	if err := workflow.ExecuteChildWorkflow(childCtx, CrabDecompositionWorkflow, crabReq).Get(ctx, &crabResult); err != nil {
		recordStep("crab_handoff", crabStart, "failed")
		notify("turtle_failed", map[string]string{"phase": "crab_handoff", "error": err.Error()})
		return &TurtlePlanningResult{
			Status:      "failed",
			TaskID:      req.TaskID,
			StepMetrics: stepMetrics,
			TotalTokens: totalTokens,
		}, nil
	}
	totalTokens.Add(crabResult.TotalTokens)

	if crabResult.Status != "completed" {
		recordStep("crab_handoff", crabStart, "failed")
		notify("turtle_failed", map[string]string{
			"phase": "crab_handoff",
			"error": fmt.Sprintf("crab returned status %q", crabResult.Status),
		})
		return &TurtlePlanningResult{
			Status:      "failed",
			TaskID:      req.TaskID,
			StepMetrics: stepMetrics,
			TotalTokens: totalTokens,
		}, nil
	}
	recordStep("crab_handoff", crabStart, "ok")

	duration := workflow.Now(ctx).Sub(startTime)
	logger.Info(TurtlePrefix+" Ceremony complete",
		"TaskID", req.TaskID,
		"Whales", len(crabResult.WhalesEmitted),
		"Morsels", len(crabResult.MorselsEmitted),
		"Duration", duration.String())

	notify("turtle_done", map[string]string{
		"whales":   fmt.Sprintf("%d", len(crabResult.WhalesEmitted)),
		"morsels":  fmt.Sprintf("%d", len(crabResult.MorselsEmitted)),
		"duration": fmtDuration(duration),
	})

	// Record health event for the fossil record.
	recordCtx := workflow.WithActivityOptions(ctx, shortAO)
	_ = workflow.ExecuteActivity(recordCtx, a.RecordHealthEventActivity,
		"turtle_completed",
		fmt.Sprintf("[%s] Turtle planned %s artifact and crabs emitted %d whales/%d morsels in %s",
			req.Project, req.TaskID, len(crabResult.WhalesEmitted), len(crabResult.MorselsEmitted), fmtDuration(duration)),
	).Get(ctx, nil)

	return &TurtlePlanningResult{
		Status:          "completed",
		TaskID:          req.TaskID,
		MorselsEmitted:  crabResult.MorselsEmitted,
		Rounds:          1,   // single planning pass
		ConfidenceScore: 100, // no consensus phase in hotfix flow
		StepMetrics:     stepMetrics,
		TotalTokens:     totalTokens,
	}, nil
}
