// Package temporal — DispatcherWorkflow replaces the old scheduler tick loop
// with a Temporal-native workflow that runs on a Schedule.
package temporal

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/graph"
	"github.com/antigravity-dev/chum/internal/store"
)

// DispatcherWorkflow scans for ready tasks and dispatches ChumAgentWorkflow
// children. Designed to run on a Temporal Schedule (every tick_interval).
//
// Unlike the old scheduler goroutine, this is durable — survives crashes,
// visible in the Temporal UI, and doesn't pile up via SKIP overlap policy.
func DispatcherWorkflow(ctx workflow.Context, _ struct{}) error {
	logger := workflow.GetLogger(ctx)
	logger.Info(SharkPrefix + " Dispatcher: scanning for ready tasks")

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	}
	actCtx := workflow.WithActivityOptions(ctx, ao)

	var da *DispatchActivities
	var result ScanCandidatesResult
	if err := workflow.ExecuteActivity(actCtx, da.ScanCandidatesActivity).Get(ctx, &result); err != nil {
		logger.Error(SharkPrefix+" Dispatcher: scan failed", "error", err)
		return fmt.Errorf("scan candidates: %w", err)
	}

	if len(result.Candidates) == 0 {
		logger.Debug(SharkPrefix+" Dispatcher: nothing to dispatch", "running", result.Running)
		return nil
	}

	// Agent rotation — distribute load across available CLI agents.
	// This prevents burning through any single provider's weekly quota.
	availableAgents := []string{"claude", "codex", "gemini"}

	// Start child workflows for each candidate.
	dispatched := 0
	for i := range result.Candidates {
		c := &result.Candidates[i]
		timeout := workflowTimeout(c.EstimateMinutes)

		childOpts := workflow.ChildWorkflowOptions{
			WorkflowID:               c.TaskID,
			TaskQueue:                DefaultTaskQueue,
			WorkflowExecutionTimeout: timeout,
			// ALLOW_DUPLICATE_FAILED_ONLY allows retry after failure/termination
			// but rejects if a workflow with this task ID is currently running.
			WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
			// ABANDON keeps child workflows alive after the dispatcher parent completes.
			ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_ABANDON,
		}
		childCtx := workflow.WithChildOptions(ctx, childOpts)

		slowStep := c.SlowStepThreshold
		if slowStep <= 0 {
			slowStep = defaultSlowStepThreshold
		}

		// Round-robin: pick agent based on dispatch index
		agent := availableAgents[i%len(availableAgents)]

		req := TaskRequest{
			TaskID:            c.TaskID,
			Project:           c.Project,
			Title:             c.Title,
			Priority:          c.Priority,
			Prompt:            c.Prompt,
			Agent:             agent,
			WorkDir:           c.WorkDir,
			Provider:          c.Provider,
			DoDChecks:         c.DoDChecks,
			SlowStepThreshold: slowStep,
			WebDoD:            c.WebDoD,
		}

		// Fire-and-forget — we don't wait for the child to complete.
		// The dispatcher's job is to START workflows, not babysit them.
		future := workflow.ExecuteChildWorkflow(childCtx, ChumAgentWorkflow, req)

		// Wait for the child to actually start (avoid ABANDON killing it).
		var childExec workflow.Execution
		if err := future.GetChildWorkflowExecution().Get(ctx, &childExec); err != nil {
			// Expected when the task already has a running workflow (REJECT_DUPLICATE).
			logger.Debug(SharkPrefix+" Dispatcher: skipped (already running or recently completed)",
				"task", c.TaskID, "error", err)
			continue
		}

		logger.Info(SharkPrefix+" Dispatcher: shark dispatched",
			"task", c.TaskID,
			"project", c.Project,
			"title", c.Title,
			"timeout", timeout,
			"workflow_id", childExec.ID,
		)
		dispatched++
	}

	logger.Info(SharkPrefix+" Dispatcher: tick complete",
		"dispatched", dispatched,
		"running", result.Running,
	)
	return nil
}

// workflowTimeout calculates WorkflowExecutionTimeout from task estimate.
// Buffer: estimate × 3 (covers plan + execute + review + handoffs + DoD).
// Minimum 30m, maximum 4h.
func workflowTimeout(estimateMinutes int) time.Duration {
	const (
		minTimeout = 30 * time.Minute
		maxTimeout = 4 * time.Hour
		multiplier = 3
	)

	if estimateMinutes <= 0 {
		return minTimeout
	}

	timeout := time.Duration(estimateMinutes*multiplier) * time.Minute
	if timeout < minTimeout {
		timeout = minTimeout
	}
	if timeout > maxTimeout {
		timeout = maxTimeout
	}
	return timeout
}

// --- Dispatch Activities ---

// DispatchActivities holds dependencies for the dispatcher. Separate from the
// main Activities struct because the dispatcher needs ConfigManager and a
// Temporal client for listing workflows — things the regular activities don't need.
type DispatchActivities struct {
	CfgMgr config.ConfigManager
	TC     client.Client
	DAG    *graph.DAG
	Store  *store.Store
}

// ScanCandidatesActivity does all the I/O-heavy work of discovering ready tasks.
// This is the domain logic from the old scheduler.tick(), wrapped in an activity.
func (da *DispatchActivities) ScanCandidatesActivity(ctx context.Context) (*ScanCandidatesResult, error) {
	cfg := da.CfgMgr.Get()

	// --- List open workflows ---
	openWFs, err := listOpenAgentWorkflows(ctx, da.TC)
	if err != nil {
		return nil, fmt.Errorf("list open workflows: %w", err)
	}

	running := len(openWFs)
	maxTotal := cfg.General.MaxConcurrentTotal
	if maxTotal <= 0 {
		maxTotal = 3
	}
	if running >= maxTotal {
		return &ScanCandidatesResult{Running: running, MaxTotal: maxTotal}, nil
	}

	slots := maxTotal - running
	maxPerTick := cfg.General.MaxPerTick
	if maxPerTick <= 0 {
		maxPerTick = 3
	}
	if slots > maxPerTick {
		slots = maxPerTick
	}

	// Build set of running workflow IDs to skip re-dispatch, and per-project counts.
	runningSet := make(map[string]struct{}, len(openWFs))
	projectRunning := make(map[string]int)
	for _, wf := range openWFs {
		runningSet[wf.workflowID] = struct{}{}
		if idx := strings.LastIndex(wf.workflowID, "-"); idx > 0 {
			projectRunning[wf.workflowID[:idx]]++
		}
	}

	maxPerProject := cfg.Dispatch.Git.MaxConcurrentPerProject
	if maxPerProject <= 0 {
		maxPerProject = 3
	}

	// --- Gather ready tasks across all enabled projects ---
	type candidate struct {
		task     graph.Task
		project  string
		workDir  string
		deferred bool
	}
	var candidates []candidate

	for name := range cfg.Projects {
		proj := cfg.Projects[name]
		if !proj.Enabled {
			continue
		}
		if projectRunning[name] >= maxPerProject {
			continue
		}

		// --- Circuit breaker pre-check ---
		// If a circuit_breaker safety block is active for this project, skip it entirely.
		if da.Store != nil {
			if block, _ := da.Store.GetBlock(name, "circuit_breaker"); block != nil {
				if time.Now().Before(block.BlockedUntil) {
					activity.GetLogger(ctx).Warn(SharkPrefix+" Circuit breaker ACTIVE — skipping project",
						"Project", name,
						"BlockedUntil", block.BlockedUntil.Format(time.RFC3339),
						"Reason", block.Reason,
					)
					continue
				}
			}
		}

		all, listErr := da.DAG.ListTasks(ctx, name)
		if listErr != nil {
			continue
		}

		depGraph := graph.BuildDepGraph(all)
		ready := graph.FilterUnblockedOpen(all, depGraph)

		workDir := config.ExpandHome(strings.TrimSpace(proj.Workspace))
		for j := range ready {
			candidates = append(candidates, candidate{
				task:     ready[j],
				project:  name,
				workDir:  workDir,
				deferred: isStrategicDeferredTask(ready[j]),
			})
		}
	}

	// --- Filter deferred tasks when non-deferred work exists ---
	hasNonDeferred := false
	for i := range candidates {
		if !candidates[i].deferred {
			hasNonDeferred = true
			break
		}
	}
	if hasNonDeferred {
		filtered := candidates[:0]
		for i := range candidates {
			if !candidates[i].deferred {
				filtered = append(filtered, candidates[i])
			}
		}
		candidates = filtered
	}

	// --- Sort: priority → DAG (parent tasks first) → estimate ---
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].task.Priority != candidates[j].task.Priority {
			return candidates[i].task.Priority < candidates[j].task.Priority
		}
		iHasParent := candidates[i].task.ParentID != ""
		jHasParent := candidates[j].task.ParentID != ""
		if iHasParent != jHasParent {
			return iHasParent
		}
		return candidates[i].task.EstimateMinutes < candidates[j].task.EstimateMinutes
	})

	// --- Per-project round-robin dispatch ---
	// Partition candidates into per-project buckets (already sorted by priority).
	// Round-robin across projects to guarantee at least 1 shark per project.
	projectBuckets := make(map[string][]int) // project -> indices into candidates
	projectOrder := make([]string, 0)        // deterministic iteration order
	for i := range candidates {
		c := &candidates[i]
		if _, alreadyRunning := runningSet[c.task.ID]; alreadyRunning {
			continue
		}
		if _, seen := projectBuckets[c.project]; !seen {
			projectOrder = append(projectOrder, c.project)
		}
		projectBuckets[c.project] = append(projectBuckets[c.project], i)
	}
	sort.Strings(projectOrder) // deterministic ordering

	result := make([]DispatchCandidate, 0, slots)
	bucketIdx := make(map[string]int) // tracks position within each project's bucket

	for len(result) < slots {
		added := false
		for _, proj := range projectOrder {
			if len(result) >= slots {
				break
			}
			if projectRunning[proj] >= maxPerProject {
				continue
			}
			bucket := projectBuckets[proj]
			idx := bucketIdx[proj]

			// Find next valid candidate in this project's bucket
			for idx < len(bucket) {
				c := &candidates[bucket[idx]]
				idx++
				bucketIdx[proj] = idx

				// Resolve DoD checks and web DoD config from project.
				var dodChecks []string
				var webDoD *WebVerifyRequest
				if projCfg, ok := cfg.Projects[c.project]; ok {
					dodChecks = projCfg.DoD.Checks
					if projCfg.DoD.Web.Enabled {
						webDoD = &WebVerifyRequest{
							Project:           c.project,
							URLs:              projCfg.DoD.Web.URLs,
							ExpectStatus:      projCfg.DoD.Web.ExpectStatus,
							ExpectContains:    projCfg.DoD.Web.ExpectContains,
							LighthouseEnabled: projCfg.DoD.Web.LighthouseEnabled,
							LighthouseMinPerf: projCfg.DoD.Web.LighthouseMinPerf,
							LighthouseMinSEO:  projCfg.DoD.Web.LighthouseMinSEO,
							LighthouseMinA11y: projCfg.DoD.Web.LighthouseMinA11y,
							CrawlBrokenLinks:  projCfg.DoD.Web.CrawlBrokenLinks,
							TimeoutSeconds:    projCfg.DoD.Web.TimeoutSeconds,
						}
					}
				}
				slowStepThreshold := cfg.General.SlowStepThreshold.Duration
				if slowStepThreshold <= 0 {
					slowStepThreshold = defaultSlowStepThreshold
				}

				result = append(result, DispatchCandidate{
					TaskID:            c.task.ID,
					Title:             c.task.Title,
					Project:           c.project,
					Priority:          c.task.Priority,
					WorkDir:           c.workDir,
					Prompt:            buildPrompt(c.task),
					Provider:          resolveProvider(cfg),
					DoDChecks:         dodChecks,
					SlowStepThreshold: slowStepThreshold,
					EstimateMinutes:   c.task.EstimateMinutes,
					PreviousErrors:    lastDoDFailures(da.Store, c.task.ID),
					WebDoD:            webDoD,
				})
				projectRunning[c.project]++
				added = true
				break // move to next project (round-robin)
			}
		}
		if !added {
			break // all projects exhausted
		}
	}

	// --- Circuit breaker trip check ---
	// After building candidates, check if any project should be circuit-broken.
	cc := cfg.Dispatch.CostControl
	if cc.PauseOnChurn && da.Store != nil && cc.ChurnPauseFailure > 0 {
		window := cc.ChurnPauseWindow.Duration
		if window <= 0 {
			window = time.Hour
		}
		cooldown := cc.StageCooldown.Duration
		if cooldown <= 0 {
			cooldown = time.Hour
		}

		// Collect unique projects from candidates
		projectsInResult := make(map[string]bool)
		for _, dc := range result {
			projectsInResult[dc.Project] = true
		}

		for proj := range projectsInResult {
			failures, total, err := da.Store.GetRecentDispatchHealth(proj, window)
			if err != nil {
				activity.GetLogger(ctx).Warn(SharkPrefix+" Circuit breaker health check failed", "Project", proj, "error", err)
				continue
			}

			if failures >= cc.ChurnPauseFailure || (cc.ChurnPauseTotal > 0 && total >= cc.ChurnPauseTotal && failures > 0) {
				blockedUntil := time.Now().Add(cooldown)
				reason := fmt.Sprintf("Circuit breaker tripped: %d/%d dispatches failed in last %s", failures, total, window)

				if err := da.Store.SetBlockWithMetadata(proj, "circuit_breaker", blockedUntil, reason, map[string]interface{}{
					"failures": failures,
					"total":    total,
					"window":   window.String(),
				}); err != nil {
					activity.GetLogger(ctx).Warn(SharkPrefix+" Failed to set circuit breaker block", "Project", proj, "error", err)
				} else {
					activity.GetLogger(ctx).Warn(SharkPrefix+" ⚡ Circuit breaker TRIPPED",
						"Project", proj,
						"Failures", failures,
						"Total", total,
						"Window", window.String(),
						"CooldownUntil", blockedUntil.Format(time.RFC3339),
					)
				}

				// Remove this project's candidates from the result
				filtered := result[:0]
				for _, dc := range result {
					if dc.Project != proj {
						filtered = append(filtered, dc)
					}
				}
				result = filtered
			}
		}
	}

	return &ScanCandidatesResult{
		Candidates: result,
		Running:    running,
		MaxTotal:   maxTotal,
	}, nil
}

// listOpenAgentWorkflows returns all currently running ChumAgentWorkflow
// executions. Extracted from the old scheduler for reuse in the activity.
func listOpenAgentWorkflows(ctx context.Context, tc client.Client) ([]openWorkflowExecution, error) {
	query := `WorkflowType = 'ChumAgentWorkflow' AND ExecutionStatus = 'Running'`

	var pageToken []byte
	executions := make([]openWorkflowExecution, 0, 200)
	for {
		resp, err := tc.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
			Query:         query,
			PageSize:      200,
			NextPageToken: pageToken,
		})
		if err != nil {
			return nil, err
		}
		if resp == nil {
			return nil, fmt.Errorf("temporal list workflow returned nil response")
		}

		for _, exec := range resp.Executions {
			execInfo := exec.GetExecution()
			if execInfo == nil {
				continue
			}
			wfID := execInfo.GetWorkflowId()
			if wfID == "" {
				continue
			}
			startTime := time.Time{}
			if exec.StartTime != nil {
				startTime = exec.StartTime.AsTime()
			}
			executions = append(executions, openWorkflowExecution{
				workflowID: wfID,
				runID:      execInfo.GetRunId(),
				startTime:  startTime,
			})
		}

		if len(resp.NextPageToken) == 0 {
			break
		}
		pageToken = resp.NextPageToken
	}
	return executions, nil
}

// openWorkflowExecution is metadata about a running workflow. Kept package-private.
type openWorkflowExecution struct {
	workflowID string
	runID      string
	startTime  time.Time
}

// isStrategicDeferredTask checks whether the task has the strategic deferred label.
func isStrategicDeferredTask(t graph.Task) bool {
	for _, label := range t.Labels {
		if strings.EqualFold(strings.TrimSpace(label), StrategicDeferredLabel) {
			return true
		}
	}
	return false
}

// buildPrompt constructs the agent prompt from task metadata.
func buildPrompt(t graph.Task) string {
	var sb strings.Builder
	sb.WriteString(t.Title)
	sb.WriteString("\n\n")

	if t.Description != "" {
		sb.WriteString(t.Description)
		sb.WriteString("\n\n")
	}
	if t.Acceptance != "" {
		sb.WriteString("ACCEPTANCE CRITERIA:\n")
		sb.WriteString(t.Acceptance)
		sb.WriteString("\n\n")
	}
	if t.Design != "" {
		sb.WriteString("DESIGN:\n")
		sb.WriteString(t.Design)
		sb.WriteString("\n\n")
	}
	return strings.TrimSpace(sb.String())
}

// resolveProvider picks the first fast-tier provider from config.
func resolveProvider(cfg *config.Config) string {
	if len(cfg.Tiers.Fast) > 0 {
		return cfg.Tiers.Fast[0]
	}
	for name := range cfg.Providers {
		return name
	}
	return ""
}

// lastDoDFailures returns learner-distilled lessons from prior failed dispatches
// for a task. This gives re-dispatched workflows memory of what went wrong.
func lastDoDFailures(s *store.Store, taskID string) []string {
	if s == nil {
		return nil
	}

	lessons, err := s.GetLessonsByBead(taskID)
	if err != nil || len(lessons) == 0 {
		return nil
	}

	var errs []string
	for _, l := range lessons {
		if l.Category == "antipattern" || l.Category == "rule" {
			errs = append(errs, fmt.Sprintf("[%s] %s", l.Category, l.Summary))
		}
	}
	return errs
}
