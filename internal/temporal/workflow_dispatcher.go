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

	if result.Throttled {
		logger.Info(SharkPrefix+" ⏸️  Dispatcher: throttled", "reason", result.ThrottleReason)
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
			Prompt:            c.Prompt,
			Agent:             agent,
			WorkDir:           c.WorkDir,
			Provider:          c.Provider,
			DoDChecks:         c.DoDChecks,
			SlowStepThreshold: slowStep,
			EscalationChain:   result.EscalationTiers,
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

		result = append(result, DispatchCandidate{
			TaskID:            c.task.ID,
			Title:             c.task.Title,
			Project:           c.project,
			WorkDir:           c.workDir,
			Prompt:            buildPrompt(c.task),
			Provider:          resolveProvider(cfg),
			DoDChecks:         dodChecks,
			SlowStepThreshold: slowStepThreshold,
			EstimateMinutes:   c.task.EstimateMinutes,
		})
		projectRunning[c.project]++
	}

	return &ScanCandidatesResult{
		Candidates:      result,
		Running:         running,
		MaxTotal:        maxTotal,
		AvailableAgents: enabledCLIAgents(cfg),
		EscalationTiers: buildEscalationTiers(cfg),
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

// buildEscalationTiers creates the ordered escalation chain from config.
func buildEscalationTiers(cfg *config.Config) []EscalationTier {
	chain := EscalationChain(cfg.Tiers, "fast")
	tiers := make([]EscalationTier, 0, len(chain))
	for i, providerKey := range chain {
		cli, model := ResolveProviderCLI(cfg.Providers, providerKey)
		prov, exists := cfg.Providers[providerKey]
		enabled := true
		if exists {
			enabled = prov.IsEnabled()
		}
		tiers = append(tiers, EscalationTier{
			ProviderKey: providerKey,
			CLI:         cli,
			Model:       model,
			Tier:        tierForIndex(i),
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
