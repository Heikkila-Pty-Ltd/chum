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
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/graph"
	"github.com/antigravity-dev/chum/internal/store"
)

// DispatcherWorkflow scans for ready tasks and dispatches child workflows.
// Designed to run on a Temporal Schedule (every tick_interval).
//
// Unlike the old scheduler goroutine, this is durable — survives crashes,
// visible in the Temporal UI, and doesn't pile up via SKIP overlap policy.
func DispatcherWorkflow(ctx workflow.Context, _ struct{}) error {
	startTime := workflow.Now(ctx)
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

		// File investigation task — the pipeline eats its own failures
		var a *Activities
		investigateCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: 10 * time.Second,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
		})
		_ = workflow.ExecuteActivity(investigateCtx, a.FileInvestigationTaskActivity, InvestigationRequest{
			Category:    "dispatcher",
			Title:       fmt.Sprintf("Dispatcher scan failure: %s", truncate(err.Error(), 80)),
			Description: fmt.Sprintf("ScanCandidatesActivity failed:\n\n%s", err.Error()),
			Source:      workflow.GetInfo(ctx).WorkflowExecution.ID,
			Project:     "chum",
			Severity:    "critical",
		}).Get(ctx, nil)

		recordOrganismLog(ctx, "dispatcher", "", "", "failed",
			"scan failed: "+truncate(err.Error(), 200), startTime, 0, err.Error())

		return fmt.Errorf("scan candidates: %w", err)
	}

	if result.Throttled {
		logger.Info(SharkPrefix+" ⏸️  Dispatcher: throttled", "reason", result.ThrottleReason)
		recordOrganismLog(ctx, "dispatcher", "", "", "throttled",
			result.ThrottleReason, startTime, 0, "")
		return nil
	}

	if len(result.Candidates) == 0 {
		logger.Debug(SharkPrefix+" Dispatcher: nothing to dispatch", "running", result.Running)
		return nil
	}

	// Agent rotation — use enabled agents from the scan result.
	availableAgents := result.AvailableAgents
	if len(availableAgents) == 0 {
		availableAgents = []string{"codex"} // fallback
	}

	// Start child workflows for each candidate.
	dispatched := 0
	planningDispatchedThisTick := false
	for i := range result.Candidates {
		c := &result.Candidates[i]
		timeout := workflowTimeout(c.EstimateMinutes)

		// Each dispatch is a fresh organism — unique workflow ID per attempt.
		// If it fails, the task dies and a new one is born from its ashes.
		wfID := fmt.Sprintf("%s-%d", c.TaskID, workflow.Now(ctx).Unix())
		childOpts := workflow.ChildWorkflowOptions{
			WorkflowID:               wfID,
			TaskQueue:                DefaultTaskQueue,
			WorkflowExecutionTimeout: timeout,
			// ALLOW_DUPLICATE — every dispatch attempt is a unique organism.
			// No task ID ever persists. Failed tasks die; new ones are born.
			WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
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
			TaskID:              c.TaskID,
			Project:             c.Project,
			TaskTitle:           c.TaskTitle,
			Prompt:              c.Prompt,
			Agent:               agent,
			WorkDir:             c.WorkDir,
			Provider:            c.Provider,
			DoDChecks:           c.DoDChecks,
			SlowStepThreshold:   slowStep,
			Priority:            clampTaskPriority(c.Priority),
			EscalationChain:     result.EscalationTiers,
			MaxRetriesOverride:  result.MaxRetriesOverride,
			MaxHandoffsOverride: result.MaxHandoffsOverride,
			PreviousErrors:      c.PreviousErrors,
		}

		var future workflow.ChildWorkflowFuture
		requiresPlanning := false
		if result.EnablePlannerV2 {
			plannerReq := PlannerV2Request{
				Candidate:       *c,
				Task:            req,
				EscalationTiers: result.EscalationTiers,
				ParentNodeKey:   plannerV2RootNodeKey,
			}
			future = workflow.ExecuteChildWorkflow(childCtx, PlannerV2Workflow, plannerReq)
		} else {
			// Planning-first routing:
			// - New work (generation 0)
			// - Escalated/failure-heavy work (previous errors present)
			// - High-complexity work
			if shouldRouteToPlanningCeremony(*c) {
				if result.PlanningRunning > 0 || planningDispatchedThisTick {
					logger.Info(SharkPrefix+" Dispatcher: planning ceremony already active; deferring candidate",
						"task", c.TaskID,
						"project", c.Project,
						"planning_running", result.PlanningRunning,
					)
					continue
				}
				planningReq := seededPlanningRequestFromCandidate(
					*c,
					agent,
					slowStep,
					result.PlanningSignalTimeout,
					result.PlanningSessionTimeout,
				)
				future = workflow.ExecuteChildWorkflow(childCtx, PlanningCeremonyWorkflow, planningReq)
				requiresPlanning = true
			} else {
				// Familiar/simple work executes directly.
				// Standard single-agent execution loop.
				future = workflow.ExecuteChildWorkflow(childCtx, ChumAgentWorkflow, req)
			}
		}

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
		if requiresPlanning {
			planningDispatchedThisTick = true
		}
		dispatched++
	}

	logger.Info(SharkPrefix+" Dispatcher: tick complete",
		"dispatched", dispatched,
		"running", result.Running,
	)

	recordOrganismLog(ctx, "dispatcher", "", "", "completed",
		fmt.Sprintf("%d dispatched, %d running, %d candidates",
			dispatched, result.Running, len(result.Candidates)),
		startTime, dispatched, "")

	return nil
}

// workflowTimeout calculates WorkflowExecutionTimeout from task estimate.
// Buffer: estimate × 3 (covers plan + execute + review + checks).
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
	TC     workflowListClient
	DAG    *graph.DAG
	Store  *store.Store
}

type workflowListClient interface {
	ListWorkflow(context.Context, *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error)
}

// ScanCandidatesActivity does all the I/O-heavy work of discovering ready tasks.
// This is the domain logic from the old scheduler.tick(), wrapped in an activity.
func (da *DispatchActivities) ScanCandidatesActivity(ctx context.Context) (*ScanCandidatesResult, error) {
	cfg := da.CfgMgr.Get()
	logger := activity.GetLogger(ctx)

	// --- Token budget gate ---
	// Check rolling 5h window and weekly cap before dispatching.
	if da.Store != nil {
		now := time.Now()

		// 5-hour rolling window check (output tokens — the scarce resource on auth plans).
		if cap5h := cfg.RateLimits.Window5hCap; cap5h > 0 {
			burn, err := da.Store.TokenBurnSince("claude", now.Add(-5*time.Hour))
			if err != nil {
				logger.Warn(SharkPrefix+" Dispatcher: token burn query failed", "error", err)
			} else {
				used := burn.OutputTokens
				pct := float64(used) * 100 / float64(cap5h)
				if used >= int64(cap5h) {
					logger.Info(SharkPrefix+" ⏸️  Dispatcher: 5h token ceiling hit — cooling",
						"used", used, "cap", cap5h, "pct", fmt.Sprintf("%.0f%%", pct))
					return &ScanCandidatesResult{Throttled: true, ThrottleReason: fmt.Sprintf("5h claude output ceiling: %d/%d (%.0f%%)", used, cap5h, pct)}, nil
				}
				if pct > 80 {
					logger.Info(SharkPrefix+" ⚠️  Dispatcher: 5h token budget at "+fmt.Sprintf("%.0f%%", pct),
						"used", used, "cap", cap5h)
				}
			}
		}

		// Weekly cap check (total output tokens since the configured reset day).
		if weeklyCap := cfg.RateLimits.WeeklyCap; weeklyCap > 0 {
			weekStart := lastWeeklyReset(now)
			burn, err := da.Store.TokenBurnSince("claude", weekStart)
			if err != nil {
				logger.Warn(SharkPrefix+" Dispatcher: weekly burn query failed", "error", err)
			} else {
				used := burn.OutputTokens
				pct := float64(used) * 100 / float64(weeklyCap)
				headroom := cfg.RateLimits.WeeklyHeadroomPct
				if headroom <= 0 {
					headroom = 20 // default 20% reserved for human use
				}
				ceiling := float64(weeklyCap) * float64(100-headroom) / 100
				if float64(used) >= ceiling {
					logger.Info(SharkPrefix+" ⏸️  Dispatcher: weekly token ceiling hit — cooling",
						"used", used, "cap", weeklyCap, "headroom_pct", headroom, "pct", fmt.Sprintf("%.0f%%", pct))
					return &ScanCandidatesResult{Throttled: true, ThrottleReason: fmt.Sprintf("weekly claude ceiling: %d/%d (%.0f%%, %d%% headroom reserved)", used, weeklyCap, pct, headroom)}, nil
				}
				if pct > 50 {
					logger.Info(SharkPrefix+" 📊 Dispatcher: weekly token budget at "+fmt.Sprintf("%.0f%%", pct),
						"used", used, "cap", weeklyCap)
				}
			}
		}
	}

	// --- List open workflows ---
	openWFs, err := listOpenAgentWorkflowsForAgent(ctx, da.TC, "", "")
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

	// Build set of TASK IDs that are currently running (extract task ID from
	// workflow IDs like "w1-1-1708654321" → "w1-1"). Skip re-dispatch for
	// tasks with an active organism, but allow re-dispatch after death.
	runningSet := make(map[string]struct{}, len(openWFs))
	for _, wf := range openWFs {
		// Extract task ID from workflow ID (strip timestamp suffix)
		taskID := extractTaskIDFromWorkflowID(wf.workflowID)
		runningSet[taskID] = struct{}{}
	}

	planningSignalTimeout := cfg.Dispatch.CostControl.PlanningSignalTimeout.Duration
	if planningSignalTimeout <= 0 {
		planningSignalTimeout = 10 * time.Minute
	}
	planningSessionTimeout := cfg.Dispatch.CostControl.PlanningSessionTimeout.Duration
	if planningSessionTimeout <= 0 {
		planningSessionTimeout = 30 * time.Minute
	}
	planningStaleBlockThreshold := cfg.Dispatch.CostControl.PlanningStaleBlockThreshold.Duration
	if planningStaleBlockThreshold <= 0 {
		planningStaleBlockThreshold = 35 * time.Minute
	}

	// Include active planning workflows so seeded tasks are not re-dispatched
	// while a ceremony is still running. Ignore stale planning sessions so one
	// abandoned run cannot block the planning queue forever.
	planningWFs, err := listOpenPlanningWorkflows(ctx, da.TC)
	planningRunning := 0
	if err != nil {
		logger.Warn(SharkPrefix+" Dispatcher: failed to list planning workflows", "error", err)
	} else {
		stalePlanning := 0
		now := time.Now()
		for _, wf := range planningWFs {
			if isStalePlanningWorkflow(wf, now, planningStaleBlockThreshold) {
				stalePlanning++
				continue
			}
			planningRunning++
			taskID := extractTaskIDFromWorkflowID(wf.workflowID)
			runningSet[taskID] = struct{}{}
		}
		if stalePlanning > 0 {
			logger.Warn(SharkPrefix+" Dispatcher: ignoring stale planning sessions",
				"stale_count", stalePlanning,
				"stale_threshold", planningStaleBlockThreshold,
			)
		}
	}

	// Exclude beached sharks: tasks that were escalated (failed all retries)
	// within the configured window. Without this, the DAG keeps serving them as
	// "ready" candidates and we burn tokens re-dispatching doomed tasks.
	beachedWindow := cfg.Dispatch.CostControl.BeachedSharkWindow.Duration
	if beachedWindow > 0 && da.Store != nil {
		cutoff := time.Now().Add(-beachedWindow).Format("2006-01-02 15:04:05")
		rows, err := da.Store.DB().QueryContext(ctx,
			`SELECT DISTINCT morsel_id FROM dispatches
			 WHERE status = 'escalated' AND dispatched_at > ?`, cutoff)
		if err != nil {
			logger.Warn(SharkPrefix+" Dispatcher: failed to query beached tasks", "error", err)
		} else {
			beached := 0
			for rows.Next() {
				var morselID string
				if rows.Scan(&morselID) == nil {
					runningSet[morselID] = struct{}{}
					beached++
				}
			}
			rows.Close()
			if beached > 0 {
				logger.Info(SharkPrefix+" Dispatcher: excluding beached sharks", "count", beached, "window", beachedWindow)
			}
		}
	}

	maxPerProject := cfg.Dispatch.Git.MaxConcurrentPerProject
	if maxPerProject <= 0 {
		maxPerProject = 3
	}
	enablePlannerV2 := cfg.Dispatch.CostControl.EnablePlannerV2
	projectRunning := make(map[string]int)

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

		projectOpenWFs, err := listOpenAgentWorkflowsForAgent(ctx, da.TC, name, "")
		if err != nil {
			logger.Warn(SharkPrefix+" Dispatcher: failed to count project running workflows", "project", name, "error", err)
			projectOpenWFs = nil
		}
		projectRunning[name] = len(projectOpenWFs)

		if projectRunning[name] >= maxPerProject {
			continue
		}

		all, listErr := da.DAG.ListTasks(ctx, name)
		if listErr != nil {
			logger.Warn(SharkPrefix+" Dispatcher: DAG ListTasks failed", "project", name, "error", listErr)
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

	// --- Build dispatch candidates (up to slots) ---
	result := make([]DispatchCandidate, 0, slots)
	for i := range candidates {
		c := &candidates[i]
		if len(result) >= slots {
			break
		}
		if _, alreadyRunning := runningSet[c.task.ID]; alreadyRunning {
			continue
		}
		if projectRunning[c.project] >= maxPerProject {
			continue
		}

		// Resolve DoD checks from project config.
		var dodChecks []string
		if proj, ok := cfg.Projects[c.project]; ok {
			dodChecks = proj.DoD.Checks
		}
		slowStepThreshold := cfg.General.SlowStepThreshold.Duration
		if slowStepThreshold <= 0 {
			slowStepThreshold = defaultSlowStepThreshold
		}

		prompt := buildPrompt(c.task)
		species := classifySpecies(c.task.ID, prompt, nil) // no plan files yet

		var generation int
		// Check hibernation (skip if hibernating unless it's the golf project per user override)
		if genome, err := da.Store.GetGenome(species); err == nil {
			if c.project != "golf-directory" && genome.Hibernating {
				// Species is hibernating — skip dispatching this organism.
				continue
			}
			generation = genome.Generation
		}

		var plannerEdgeStats []PlannerEdgeStat
		if enablePlannerV2 && da.Store != nil {
			stats, statsErr := da.Store.ListMCTSEdgeStats(plannerV2RootNodeKey, species, 32)
			if statsErr != nil {
				logger.Warn(SharkPrefix+" Dispatcher: failed to load planner edge stats",
					"task", c.task.ID,
					"species", species,
					"error", statsErr)
			} else {
				plannerEdgeStats = make([]PlannerEdgeStat, 0, len(stats))
				for _, stat := range stats {
					plannerEdgeStats = append(plannerEdgeStats, PlannerEdgeStat{
						ActionKey:   stat.ActionKey,
						Visits:      stat.Visits,
						Wins:        stat.Wins,
						TotalReward: stat.TotalReward,
						UpdatedAt:   stat.UpdatedAt,
					})
				}
			}
		}

		result = append(result, DispatchCandidate{
			TaskID:            c.task.ID,
			Title:             c.task.Title,
			TaskTitle:         c.task.Title,
			Project:           c.project,
			WorkDir:           c.workDir,
			Prompt:            prompt,
			Species:           species,
			Labels:            append([]string(nil), c.task.Labels...),
			Provider:          resolveProvider(cfg),
			DoDChecks:         dodChecks,
			SlowStepThreshold: slowStepThreshold,
			Priority:          clampTaskPriority(c.task.Priority),
			EstimateMinutes:   c.task.EstimateMinutes,
			PreviousErrors:    parseTaskErrorLog(c.task.ErrorLog),
			Generation:        generation,
			Complexity:        ScoreTaskComplexity(c.task.Title, prompt, c.task.Acceptance, c.task.EstimateMinutes),
			HasCrabSeal:       hasCrabSeal(c.task),
			PlannerEdgeStats:  plannerEdgeStats,
		})
		projectRunning[c.project]++
	}

	return &ScanCandidatesResult{
		Candidates:             result,
		Running:                running,
		MaxTotal:               maxTotal,
		PlanningRunning:        planningRunning,
		PlanningSignalTimeout:  planningSignalTimeout,
		PlanningSessionTimeout: planningSessionTimeout,
		AvailableAgents:        enabledCLIAgents(cfg),
		EscalationTiers:        buildEscalationTiers(cfg, da.Store, logger),
		EnablePlannerV2:        enablePlannerV2,
		MaxRetriesOverride:     higherLearningMaxRetries(cfg),
		MaxHandoffsOverride:    higherLearningMaxHandoffs(cfg),
	}, nil
}

// listOpenAgentWorkflows returns all currently running ChumAgentWorkflow
// executions. Extracted from the old scheduler for reuse in the activity.
// listOpenAgentWorkflowsForAgent returns running Chum workflows filtered by project
// and optional agent. It is future-ready for targeted drain/review batches.
func listOpenAgentWorkflowsForAgent(ctx context.Context, tc workflowListClient, project, agent string) ([]openWorkflowExecution, error) {
	query := buildOpenAgentWorkflowQueryForAgent(project, agent)

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

func listOpenAgentWorkflows(ctx context.Context, tc workflowListClient, project string) ([]openWorkflowExecution, error) {
	return listOpenAgentWorkflowsForAgent(ctx, tc, project, "")
}

// listOpenExplosionWorkflows returns all currently running CambrianExplosionWorkflow
// instances. The dispatcher must check these to prevent re-dispatching tasks that
// already have an in-flight explosion (which spawns child ChumAgentWorkflows).
func listOpenExplosionWorkflows(ctx context.Context, tc workflowListClient) ([]openWorkflowExecution, error) {
	query := "WorkflowType = 'CambrianExplosionWorkflow' AND ExecutionStatus = 'Running'"

	var pageToken []byte
	executions := make([]openWorkflowExecution, 0, 50)
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

// listOpenPlanningWorkflows returns running planning workflows that can own a
// seeded task and therefore should block duplicate dispatch.
func listOpenPlanningWorkflows(ctx context.Context, tc workflowListClient) ([]openWorkflowExecution, error) {
	query := "(WorkflowType = 'PlanningCeremonyWorkflow' OR WorkflowType = 'AutonomousPlanningCeremonyWorkflow') AND ExecutionStatus = 'Running'"

	var pageToken []byte
	executions := make([]openWorkflowExecution, 0, 50)
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

func buildOpenAgentWorkflowQuery(project string) string {
	return ChumAgentRunningVisibilityQueryForProject(project)
}

func buildOpenAgentWorkflowQueryForAgent(project, agent string) string {
	return ChumAgentRunningVisibilityQueryForProjectAndAgent(project, agent)
}

// listRecentlyCompletedWorkflows returns IDs of ChumAgentWorkflows that
// completed in the last 24 hours. The dispatcher skips these to avoid
// re-dispatching tasks that already succeeded.
func listRecentlyCompletedWorkflows(ctx context.Context, tc workflowListClient) (map[string]struct{}, error) {
	cutoff := time.Now().Add(-24 * time.Hour).Format(time.RFC3339)
	query := fmt.Sprintf(
		`WorkflowType = 'ChumAgentWorkflow' AND ExecutionStatus = 'Completed' AND CloseTime > '%s'`,
		cutoff,
	)

	result := make(map[string]struct{})
	var pageToken []byte
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
			break
		}
		for _, exec := range resp.Executions {
			if wfID := exec.GetExecution().GetWorkflowId(); wfID != "" {
				result[wfID] = struct{}{}
			}
		}
		if len(resp.NextPageToken) == 0 {
			break
		}
		pageToken = resp.NextPageToken
	}
	return result, nil
}

func clampTaskPriority(priority int) int {
	return normalizePriority(priority)
}

func shouldRouteToPlanningCeremony(c DispatchCandidate) bool {
	if isCrabEmittedCandidate(c) {
		// Crab-emitted morsels have already completed planning/decomposition and
		// should move directly into the shark loop.
		return false
	}
	if c.Generation == 0 {
		return true
	}
	if len(c.PreviousErrors) > 0 {
		return true
	}
	if c.Complexity > 70 {
		return true
	}
	return false
}

func isCrabEmittedCandidate(c DispatchCandidate) bool {
	return hasCandidateLabel(c.Labels, "source:crab")
}

func hasCandidateLabel(labels []string, needle string) bool {
	needle = strings.TrimSpace(strings.ToLower(needle))
	if needle == "" {
		return false
	}
	for i := range labels {
		if strings.TrimSpace(strings.ToLower(labels[i])) == needle {
			return true
		}
	}
	return false
}

func seededPlanningRequestFromCandidate(
	c DispatchCandidate,
	agent string,
	slowStep time.Duration,
	signalTimeout time.Duration,
	sessionTimeout time.Duration,
) PlanningRequest {
	if signalTimeout <= 0 {
		signalTimeout = defaultPlanningSignalTimeout
	}
	if sessionTimeout <= 0 {
		sessionTimeout = defaultPlanningSessionTimeout
	}
	title := strings.TrimSpace(c.TaskTitle)
	if title == "" {
		title = strings.TrimSpace(c.Title)
	}
	return PlanningRequest{
		Project:           c.Project,
		Agent:             agent,
		Tier:              planningTierForCandidate(c),
		WorkDir:           c.WorkDir,
		SlowStepThreshold: slowStep,
		SignalTimeout:     signalTimeout,
		SessionTimeout:    sessionTimeout,
		SeedTaskID:        c.TaskID,
		SeedTaskTitle:     title,
		SeedTaskPrompt:    c.Prompt,
		AutoMode:          false,
		TraceSessionID:    fmt.Sprintf("dispatch-planning-%s", c.TaskID),
	}
}

func planningTierForCandidate(c DispatchCandidate) string {
	if c.Complexity > 70 || len(c.PreviousErrors) > 0 {
		return "premium"
	}
	return "fast"
}

func parseTaskErrorLog(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(raw, "\n")

	flush := func(current []string, out []string) ([]string, []string) {
		joined := strings.TrimSpace(strings.Join(current, "\n"))
		if joined != "" {
			out = append(out, joined)
		}
		return nil, out
	}

	current := make([]string, 0, len(lines))
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "---" {
			current, out = flush(current, out)
			continue
		}
		current = append(current, line)
	}
	_, out = flush(current, out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func isStalePlanningWorkflow(wf openWorkflowExecution, now time.Time, staleThreshold time.Duration) bool {
	if staleThreshold <= 0 || wf.startTime.IsZero() {
		return false
	}
	return now.Sub(wf.startTime) > staleThreshold
}

// openWorkflowExecution is metadata about a running workflow. Kept package-private.
type openWorkflowExecution struct {
	workflowID string
	runID      string
	startTime  time.Time
}

// extractTaskIDFromWorkflowID strips the unix timestamp suffix from a workflow ID
// to recover the original task ID. E.g. "w1-1-1708654321" → "w1-1".
// Falls back to the full workflow ID if no timestamp suffix is found.
func extractTaskIDFromWorkflowID(wfID string) string {
	wfID = strings.TrimSpace(wfID)
	if wfID == "" {
		return ""
	}

	// Find the last dash — if the part after it is all digits, strip it.
	idx := strings.LastIndex(wfID, "-")
	if idx <= 0 {
		return wfID
	}
	suffix := wfID[idx+1:]
	for _, ch := range suffix {
		if ch < '0' || ch > '9' {
			return wfID // not a timestamp suffix
		}
	}
	if len(suffix) < 8 {
		return wfID // too short to be a unix timestamp
	}
	taskID := wfID[:idx]
	for _, laneSuffix := range []string{"-direct", "-cambrian", "-planning", "-turtle"} {
		if strings.HasSuffix(taskID, laneSuffix) {
			taskID = strings.TrimSuffix(taskID, laneSuffix)
			break
		}
	}
	return taskID
}

func buildEscalationTiers(cfg *config.Config, st *store.Store, logger interface{ Warn(string, ...any) }) []EscalationTier {
	chain := EscalationChain(cfg.Tiers, "fast")
	tiers := make([]EscalationTier, 0, len(chain))
	for i, providerKey := range chain {
		cli, model := ResolveProviderCLI(cfg.Providers, providerKey)
		prov, exists := cfg.Providers[providerKey]
		enabled := true
		if exists {
			enabled = prov.IsEnabled()
		}

		// Per-provider token cap check (M4)
		if enabled && exists && prov.TokenCap > 0 && st != nil {
			since := time.Now().UTC().Truncate(24 * time.Hour) // today midnight UTC
			burn, err := st.TokenBurnSince(cli, since)
			if err != nil {
				if logger != nil {
					logger.Warn("token cap check failed (fail-open)", "provider", providerKey, "error", err)
				}
			} else if burn.OutputTokens >= int64(prov.TokenCap) {
				enabled = false
				if logger != nil {
					logger.Warn("provider exceeded token cap — disabled for this tick",
						"provider", providerKey, "cli", cli,
						"output_tokens", burn.OutputTokens, "cap", prov.TokenCap)
				}
			}
		}

		tiers = append(tiers, EscalationTier{
			ProviderKey: providerKey,
			CLI:         cli,
			Model:       model,
			Tier:        tierForIndex(i),
			Reviewer:    prov.Reviewer,
			Enabled:     enabled,
		})
	}
	return tiers
}

// enabledCLIAgents returns deduplicated CLI agent names from enabled providers.
func enabledCLIAgents(cfg *config.Config) []string {
	seen := make(map[string]bool)
	var agents []string
	for _, prov := range cfg.Providers {
		if !prov.IsEnabled() {
			continue
		}
		cli := prov.CLI
		if cli == "" {
			continue
		}
		if !seen[cli] {
			seen[cli] = true
			agents = append(agents, cli)
		}
	}
	return agents
}

// lastWeeklyReset returns the most recent Friday 14:00 local time (when Claude Max resets).
func lastWeeklyReset(now time.Time) time.Time {
	// Walk back to the most recent Friday.
	daysBack := int(now.Weekday()-time.Friday+7) % 7
	if daysBack == 0 && now.Hour() < 14 {
		daysBack = 7 // before 2pm Friday = use previous Friday
	}
	friday := now.AddDate(0, 0, -daysBack)
	return time.Date(friday.Year(), friday.Month(), friday.Day(), 14, 0, 0, 0, now.Location())
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

// hasCrabSeal checks whether a task has been properly decomposed and sized.
// Tasks without the seal are rerouted to the turtle→crab pipeline instead of
// being dispatched directly to sharks.
func hasCrabSeal(t graph.Task) bool {
	// Morsels with acceptance criteria and time estimates are pre-approved.
	if t.Type == "morsel" && t.Acceptance != "" && t.EstimateMinutes > 0 {
		return true
	}
	// Explicit crab:approved label overrides all checks.
	for _, l := range t.Labels {
		if strings.TrimSpace(l) == "crab:approved" {
			return true
		}
	}
	// Small tasks (type "task") with estimates are likely human-created morsels.
	if t.Type == "task" && t.EstimateMinutes > 0 && t.EstimateMinutes <= 120 {
		return true
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

// higherLearningMaxRetries returns the per-tier retry override when
// higher-learning mode is enabled, or 0 (no override) when disabled.
func higherLearningMaxRetries(cfg *config.Config) int {
	if cfg != nil && cfg.Dispatch.CostControl.HigherLearning.Enabled {
		return cfg.Dispatch.CostControl.HigherLearning.MaxRetries
	}
	return 0
}

// higherLearningMaxHandoffs returns the cross-model handoff override when
// higher-learning mode is enabled, or 0 (no override) when disabled.
func higherLearningMaxHandoffs(cfg *config.Config) int {
	if cfg != nil && cfg.Dispatch.CostControl.HigherLearning.Enabled {
		return cfg.Dispatch.CostControl.HigherLearning.MaxHandoffs
	}
	return 0
}
