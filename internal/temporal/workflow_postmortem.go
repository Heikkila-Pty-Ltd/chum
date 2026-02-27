package temporal

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/antigravity-dev/chum/internal/graph"
	"github.com/antigravity-dev/chum/internal/store"
)

// PostMortemWorkflow investigates a failed workflow via LLM root-cause analysis
// and auto-files antibody morsels for the shark pipeline to fix.
func PostMortemWorkflow(ctx workflow.Context, req PostMortemRequest) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("Post-mortem started",
		"workflow_id", req.Failure.WorkflowID,
		"task_id", req.Failure.TaskID,
		"error", truncate(req.Failure.ErrorMessage, 200),
	)

	bestEffort := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	bestEffortCtx := workflow.WithActivityOptions(ctx, bestEffort)

	var a *Activities

	// Record health event for observability.
	_ = workflow.ExecuteActivity(bestEffortCtx, a.RecordHealthEventActivity,
		"postmortem_started",
		fmt.Sprintf("wf=%s task=%s err=%s",
			req.Failure.WorkflowID,
			req.Failure.TaskID,
			truncate(req.Failure.ErrorMessage, 300)),
	).Get(ctx, nil)

	// LLM investigation — ask a fast-tier model to analyze the failure.
	investigateOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	investigateCtx := workflow.WithActivityOptions(ctx, investigateOpts)

	var investigation PostMortemInvestigation
	investigateErr := workflow.ExecuteActivity(investigateCtx,
		a.InvestigateFailureActivity, req.Failure).Get(ctx, &investigation)

	if investigateErr != nil {
		logger.Warn("Post-mortem investigation failed (non-fatal)", "error", investigateErr)
	}

	// File antibody morsel if investigation found something actionable.
	if investigateErr == nil && investigation.Severity != "low" && investigation.RootCause != "" {
		antibodyOpts := workflow.ActivityOptions{
			StartToCloseTimeout: 30 * time.Second,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
		}
		antibodyCtx := workflow.WithActivityOptions(ctx, antibodyOpts)
		antibodyReq := FileAntibodyRequest{
			Investigation: investigation,
			Failure:       req.Failure,
			Project:       req.Project,
		}
		_ = workflow.ExecuteActivity(antibodyCtx, a.FileAntibodyActivity, antibodyReq).Get(ctx, nil)
	}

	// Notify via Matrix with root-cause summary if available.
	extra := map[string]string{
		"workflow_id": req.Failure.WorkflowID,
		"task_id":     req.Failure.TaskID,
		"error":       truncate(req.Failure.ErrorMessage, 200),
	}
	if investigateErr == nil && investigation.RootCause != "" {
		extra["root_cause"] = truncate(investigation.RootCause, 200)
		extra["severity"] = investigation.Severity
		extra["category"] = investigation.Category
	}

	notifyOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
	}
	nCtx := workflow.WithActivityOptions(ctx, notifyOpts)
	_ = workflow.ExecuteActivity(nCtx, a.NotifyActivity, NotifyRequest{
		Event: "postmortem",
		Extra: extra,
	}).Get(ctx, nil)

	outcome := "completed"
	summary := fmt.Sprintf("investigated failure: %s", truncate(req.Failure.ErrorMessage, 200))
	if investigateErr == nil && investigation.RootCause != "" {
		summary = fmt.Sprintf("root_cause=%s severity=%s fix=%s",
			truncate(investigation.RootCause, 100),
			investigation.Severity,
			truncate(investigation.ProposedFix, 100))
	}

	logger.Info("Post-mortem complete",
		"workflow_id", req.Failure.WorkflowID,
		"task_id", req.Failure.TaskID,
		"outcome", outcome,
	)

	recordOrganismLog(ctx, "postmortem", req.Failure.TaskID, req.Project, outcome,
		summary, workflow.Now(ctx), 1, "")

	return nil
}

// InvestigateFailureActivity calls a fast-tier LLM to analyze a workflow
// failure and produce a structured root-cause analysis.
func (a *Activities) InvestigateFailureActivity(ctx context.Context, failure FailureContext) (*PostMortemInvestigation, error) {
	logger := activity.GetLogger(ctx)

	var contextParts []string
	contextParts = append(contextParts,
		fmt.Sprintf("FAILED WORKFLOW: %s (run: %s)", failure.WorkflowID, failure.RunID))
	if failure.TaskID != "" {
		contextParts = append(contextParts,
			fmt.Sprintf("TASK ID: %s", failure.TaskID))
	}
	if failure.FailedActivity != "" {
		contextParts = append(contextParts,
			fmt.Sprintf("FAILED ACTIVITY: %s (attempts: %d, duration: %.1fs)",
				failure.FailedActivity, failure.AttemptCount, failure.DurationS))
	}
	contextParts = append(contextParts,
		fmt.Sprintf("ERROR MESSAGE:\n%s", truncate(failure.ErrorMessage, 3000)))
	if failure.RecentCommits != "" {
		contextParts = append(contextParts,
			fmt.Sprintf("RECENT COMMITS:\n%s", truncate(failure.RecentCommits, 1000)))
	}

	prompt := fmt.Sprintf(`You are a post-mortem investigator for an automated CI/CD pipeline. A workflow just failed. Analyze the failure and identify the root cause.

%s

Respond with ONLY a JSON object:
{
  "root_cause": "concise description of what went wrong",
  "severity": "critical" or "high" or "medium" or "low",
  "proposed_fix": "specific, actionable fix (code change, config change, etc.)",
  "affected_files": ["file1.go", "file2.go"],
  "category": "infrastructure" or "logic" or "config" or "scope",
  "antibodies": ["1-3 short anti-patterns to remember"]
}

SEVERITY RULES:
- critical: system-wide breakage, data loss risk, cascading failures
- high: specific feature broken, blocking other work
- medium: non-blocking issue, workaround exists
- low: cosmetic, logging noise, non-actionable (e.g. transient network error)

CATEGORY RULES:
- infrastructure: CLI not found, OOM, network timeout, Docker failure
- logic: wrong code, missing import, test failure, type error
- config: wrong model name, missing env var, bad TOML
- scope: task too broad, missing prerequisite, needs decomposition`, strings.Join(contextParts, "\n\n"))

	agent := ResolveTierAgent(a.Tiers, "fast")
	cliResult, err := a.runAgent(ctx, agent, prompt, ".")
	if err != nil {
		logger.Warn("Post-mortem LLM call failed", "error", err)
		return &PostMortemInvestigation{
			RootCause: truncate(failure.ErrorMessage, 200),
			Severity:  "medium",
			Category:  "unknown",
		}, nil
	}

	jsonStr := extractJSON(cliResult.Output)
	if jsonStr == "" {
		logger.Warn("Post-mortem LLM produced no JSON")
		return &PostMortemInvestigation{
			RootCause: truncate(failure.ErrorMessage, 200),
			Severity:  "medium",
			Category:  "unknown",
		}, nil
	}

	jsonStr = sanitizeLLMJSON(jsonStr)
	var result PostMortemInvestigation
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		logger.Warn("Post-mortem JSON parse failed", "error", err)
		return &PostMortemInvestigation{
			RootCause: truncate(failure.ErrorMessage, 200),
			Severity:  "medium",
			Category:  "unknown",
		}, nil
	}

	// Validate severity.
	switch result.Severity {
	case "critical", "high", "medium", "low":
	default:
		result.Severity = "medium"
	}

	logger.Info("Post-mortem investigation complete",
		"root_cause", truncate(result.RootCause, 100),
		"severity", result.Severity,
		"category", result.Category)

	return &result, nil
}

// FileAntibodyActivity creates a bugfix morsel from a post-mortem investigation.
// Deduplicates by root-cause hash to avoid flooding the backlog.
func (a *Activities) FileAntibodyActivity(ctx context.Context, req FileAntibodyRequest) (string, error) {
	logger := activity.GetLogger(ctx)

	if a.DAG == nil {
		logger.Warn("No DAG configured, cannot file antibody")
		return "", nil
	}

	// Dedup: hash root_cause + affected_files.
	dedupKey := antibodyDedupKey(req.Investigation.RootCause, req.Investigation.AffectedFiles)
	if a.Store != nil && a.Store.HasRecentHealthEvent("postmortem_antibody", dedupKey, 24*time.Hour) {
		logger.Info("Antibody already filed for this root cause, skipping", "dedup_key", dedupKey)
		return "", nil
	}

	project := req.Project
	if project == "" {
		project = "chum"
	}

	priority := severityToPriority(req.Investigation.Severity)

	title := fmt.Sprintf("[antibody] %s", truncate(req.Investigation.RootCause, 80))

	description := fmt.Sprintf("**Auto-filed by CHUM post-mortem system**\n\n"+
		"Root Cause: %s\n"+
		"Category: %s\n"+
		"Severity: %s\n"+
		"Failed Workflow: %s\n"+
		"Task ID: %s\n\n"+
		"**Proposed Fix:**\n%s\n\n"+
		"**Error:**\n%s",
		req.Investigation.RootCause,
		req.Investigation.Category,
		req.Investigation.Severity,
		req.Failure.WorkflowID,
		req.Failure.TaskID,
		req.Investigation.ProposedFix,
		truncate(req.Failure.ErrorMessage, 500))

	acceptance := "- Root cause fixed and verified with `go build ./... && go test ./...`\n" +
		"- Regression guard added (test or validation)\n" +
		"- No repeat failures from this root cause in subsequent dispatches"

	taskID, err := a.DAG.CreateTask(ctx, graph.Task{
		Title:           title,
		Description:     description,
		Acceptance:      acceptance,
		Type:            "bugfix",
		Priority:        priority,
		EstimateMinutes: 30,
		Labels:          []string{"self-heal", "antibody", req.Investigation.Category},
		Project:         project,
	})
	if err != nil {
		logger.Error("Failed to create antibody task", "error", err)
		return "", fmt.Errorf("create antibody task: %w", err)
	}

	// Record health event for dedup.
	if a.Store != nil {
		_ = a.Store.RecordHealthEvent("postmortem_antibody", dedupKey)
	}

	// Evolve genome with antibody pattern.
	if a.Store != nil && len(req.Investigation.Antibodies) > 0 {
		for _, ab := range req.Investigation.Antibodies {
			_ = a.Store.EvolveGenome(req.Investigation.Category, false, store.GenomeEntry{
				Pattern:     ab,
				Reason:      req.Investigation.RootCause,
				Alternative: req.Investigation.ProposedFix,
				Files:       req.Investigation.AffectedFiles,
				Agent:       "postmortem",
			})
		}
	}

	logger.Info("Antibody task filed",
		"task_id", taskID,
		"root_cause", truncate(req.Investigation.RootCause, 80),
		"severity", req.Investigation.Severity)

	return taskID, nil
}

// antibodyDedupKey produces a short hash of root cause + affected files for dedup.
func antibodyDedupKey(rootCause string, files []string) string {
	h := sha256.New()
	h.Write([]byte(rootCause))
	for _, f := range files {
		h.Write([]byte(f))
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

// severityToPriority maps LLM severity strings to morsel priority integers.
func severityToPriority(severity string) int {
	switch severity {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	default:
		return 3
	}
}
