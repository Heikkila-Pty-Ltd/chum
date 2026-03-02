// Package chief implements the Chief Scrum Master for multi-team sprint planning ceremonies.
package chief

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/dispatch"
	"github.com/antigravity-dev/chum/internal/graph"
	"github.com/antigravity-dev/chum/internal/portfolio"
	"github.com/antigravity-dev/chum/internal/store"
)

// Chief handles multi-team sprint planning ceremonies
type Chief struct {
	cfg        *config.Config
	store      *store.Store
	dag        *graph.DAG
	dispatcher dispatch.DispatcherInterface
	logger     *slog.Logger
	allocator  *AllocationRecorder
	retro      *RetrospectiveRecorder
	nowFunc    func() time.Time // injectable clock for testing
}

type multiTeamPortfolioContextKey struct{}
type crossProjectRetroContextKey struct{}

// WithMultiTeamPortfolioContext attaches scheduler-prepared portfolio context for sprint_planning_multi prompts.
func WithMultiTeamPortfolioContext(ctx context.Context, payload string) context.Context {
	return context.WithValue(ctx, multiTeamPortfolioContextKey{}, payload)
}

// MultiTeamPortfolioContextFromContext returns scheduler-prepared portfolio context, if present.
func MultiTeamPortfolioContextFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	payload, ok := ctx.Value(multiTeamPortfolioContextKey{}).(string)
	return strings.TrimSpace(payload), ok && strings.TrimSpace(payload) != ""
}

// WithCrossProjectRetroContext attaches scheduler-prepared cross-project retrospective context.
func WithCrossProjectRetroContext(ctx context.Context, payload string) context.Context {
	return context.WithValue(ctx, crossProjectRetroContextKey{}, payload)
}

// CrossProjectRetroContextFromContext returns scheduler-prepared retrospective context, if present.
func CrossProjectRetroContextFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	payload, ok := ctx.Value(crossProjectRetroContextKey{}).(string)
	return strings.TrimSpace(payload), ok && strings.TrimSpace(payload) != ""
}

// CeremonyType represents different types of ceremonies
type CeremonyType string

const (
	CeremonyMultiTeamPlanning CeremonyType = "multi_team_planning"
	CeremonyRetrospective     CeremonyType = "retrospective"
	CeremonySprintReview      CeremonyType = "sprint_review"
	CeremonySprintRetro       CeremonyType = "sprint_retrospective"
)

// CeremonySchedule defines when ceremonies should run
type CeremonySchedule struct {
	Type        CeremonyType
	Cadence     time.Duration // How often to check (e.g., daily)
	DayOfWeek   time.Weekday  // Which day of week to run
	TimeOfDay   time.Time     // What time to run (date ignored, only hour:minute used)
	LastChecked time.Time     // When we last checked if ceremony should run
	LastRan     time.Time     // When ceremony last ran successfully
}

// New creates a new Chief instance
func New(cfg *config.Config, store *store.Store, dag *graph.DAG, dispatcher dispatch.DispatcherInterface, logger *slog.Logger) *Chief {
	allocator := NewAllocationRecorder(cfg, store, dispatcher, logger)
	retroRecorder := NewRetrospectiveRecorder(cfg, store, dag, dispatcher, logger)
	return &Chief{
		nowFunc:    time.Now,
		cfg:        cfg,
		store:      store,
		dag:        dag,
		dispatcher: dispatcher,
		logger:     logger,
		allocator:  allocator,
		retro:      retroRecorder,
	}
}

// ShouldRunCeremony checks if a ceremony should run based on its schedule
func (c *Chief) ShouldRunCeremony(ctx context.Context, schedule CeremonySchedule) bool {
	if !c.cfg.Chief.Enabled {
		c.logger.Debug("chief sm disabled, skipping ceremony check")
		return false
	}

	now := c.nowFunc()

	// Check if we've already checked recently (within last hour to avoid spam)
	if now.Sub(schedule.LastChecked) < time.Hour {
		return false
	}

	// Check if today is the right day of week
	if now.Weekday() != schedule.DayOfWeek {
		return false
	}

	// Check if we're past the target time today
	targetTime := time.Date(now.Year(), now.Month(), now.Day(),
		schedule.TimeOfDay.Hour(), schedule.TimeOfDay.Minute(), 0, 0, now.Location())
	if now.Before(targetTime) {
		return false
	}

	// Check if we already ran today
	if schedule.LastRan.Year() == now.Year() &&
		schedule.LastRan.YearDay() == now.YearDay() {
		return false
	}

	c.logger.Info("ceremony should run",
		"type", schedule.Type,
		"target_time", targetTime.Format("15:04"),
		"current_time", now.Format("15:04"))

	return true
}

// RunMultiTeamPlanning executes the multi-team sprint planning ceremony
func (c *Chief) RunMultiTeamPlanning(ctx context.Context) error {
	if !c.cfg.Chief.Enabled {
		return fmt.Errorf("chief sm not enabled")
	}

	c.logger.Info("starting multi-team sprint planning ceremony")

	// Create a ceremony dispatch morsel to track this work
	ceremonyMorsel := c.createCeremonyMorsel("Multi-team sprint planning ceremony", "multi-team-planning")

	// Dispatch the Chief SM with portfolio context
	dispatchID, err := c.dispatchChiefSM(ctx, ceremonyMorsel, "sprint_planning_multi")
	if err != nil {
		return fmt.Errorf("failed to dispatch chief sm: %w", err)
	}

	c.logger.Info("multi-team planning ceremony dispatched",
		"dispatch_id", dispatchID,
		"morsel_id", ceremonyMorsel.ID)

	// Start a background process to monitor completion and record allocations
	go c.monitorCeremonyCompletion(ctx, ceremonyMorsel.ID, CeremonyMultiTeamPlanning, dispatchID)

	return nil
}

// RunOverallRetrospective executes the Chief SM overall retrospective ceremony after per-project retros.
func (c *Chief) RunOverallRetrospective(ctx context.Context) error {
	if !c.cfg.Chief.Enabled {
		return fmt.Errorf("chief sm not enabled")
	}

	c.logger.Info("starting overall retrospective ceremony")

	ceremonyMorsel := c.createCeremonyMorsel("Overall cross-project retrospective ceremony", "overall-retrospective")
	dispatchID, err := c.dispatchChiefSM(ctx, ceremonyMorsel, "overall_retrospective")
	if err != nil {
		return fmt.Errorf("failed to dispatch chief sm overall retrospective: %w", err)
	}

	c.logger.Info("overall retrospective ceremony dispatched",
		"dispatch_id", dispatchID,
		"morsel_id", ceremonyMorsel.ID)

	go c.monitorCeremonyCompletion(ctx, ceremonyMorsel.ID, CeremonyRetrospective, dispatchID)

	return nil
}

// monitorCeremonyCompletion monitors a ceremony dispatch and processes results when complete
func (c *Chief) monitorCeremonyCompletion(ctx context.Context, ceremonyID string, ceremonyType CeremonyType, dispatchID int64) {
	c.logger.Info("monitoring ceremony completion",
		"ceremony_id", ceremonyID,
		"ceremony_type", ceremonyType,
		"dispatch_id", dispatchID)

	// Poll for completion (in production, this would use event-driven completion)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	timeout := time.NewTimer(2 * time.Hour) // Max ceremony duration
	defer timeout.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("ceremony monitoring canceled", "ceremony_id", ceremonyID)
			return
		case <-timeout.C:
			c.logger.Warn("ceremony monitoring timed out", "ceremony_id", ceremonyID)
			return
		case <-ticker.C:
			dispatch, err := c.store.GetDispatchByID(dispatchID)
			if err != nil {
				c.logger.Error("failed to check ceremony dispatch status",
					"ceremony_id", ceremonyID,
					"dispatch_id", dispatchID,
					"error", err)
				continue
			}

			if dispatch.Status == "completed" {
				c.logger.Info("ceremony completed, processing results",
					"ceremony_id", ceremonyID,
					"dispatch_id", dispatchID)

				if err := c.processCeremonyResults(ctx, ceremonyID, ceremonyType, dispatchID); err != nil {
					c.logger.Error("failed to process ceremony results",
						"ceremony_id", ceremonyID,
						"error", err)
				}
				return
			} else if dispatch.Status == "failed" {
				c.logger.Error("ceremony dispatch failed",
					"ceremony_id", ceremonyID,
					"dispatch_id", dispatchID,
					"exit_code", dispatch.ExitCode)
				return
			}
			// Continue polling if still running
		}
	}
}

// processCeremonyResults processes the output of a completed ceremony dispatch
func (c *Chief) processCeremonyResults(ctx context.Context, ceremonyID string, ceremonyType CeremonyType, dispatchID int64) error {
	// Get the ceremony output
	output, err := c.store.GetOutput(dispatchID)
	if err != nil {
		return fmt.Errorf("get ceremony output: %w", err)
	}

	c.logger.Debug("processing ceremony output",
		"ceremony_id", ceremonyID,
		"output_length", len(output))

	switch ceremonyType {
	case CeremonyMultiTeamPlanning:
		// Parse allocation decisions from the Chief SM output.
		allocation, parseErr := c.allocator.ParseAllocationFromOutput(ctx, ceremonyID, output)
		if parseErr != nil {
			return fmt.Errorf("parse allocation from output: %w", parseErr)
		}

		// Record the allocation decision and send to Matrix room.
		if recordErr := c.allocator.RecordAllocationDecision(ctx, ceremonyID, allocation); recordErr != nil {
			return fmt.Errorf("record allocation decision: %w", recordErr)
		}

		c.logger.Info("ceremony results processed successfully",
			"ceremony_id", ceremonyID,
			"ceremony_type", ceremonyType,
			"allocation_id", allocation.ID)
	case CeremonyRetrospective:
		if err := c.retro.RecordRetrospectiveResults(ctx, ceremonyID, output); err != nil {
			return fmt.Errorf("record retrospective results: %w", err)
		}
		c.logger.Info("ceremony results processed successfully",
			"ceremony_id", ceremonyID,
			"ceremony_type", ceremonyType)
	default:
		c.logger.Info("ceremony completed with no post-processor",
			"ceremony_id", ceremonyID,
			"ceremony_type", ceremonyType)
	}

	return nil
}

// createCeremonyMorsel creates a synthetic task to track ceremony work
func (c *Chief) createCeremonyMorsel(title, ceremonySlug string) graph.Task {
	now := time.Now()
	return graph.Task{
		ID:          fmt.Sprintf("ceremony-%s-%d", strings.TrimSpace(ceremonySlug), now.Unix()),
		Title:       title,
		Description: "Synthetic task for tracking Chief SM ceremony dispatch",
		Type:        "task",
		Status:      "open",
		Priority:    1, // High priority for ceremonies
		CreatedAt:   now,
	}
}

// dispatchChiefSM dispatches the Chief SM for a ceremony
func (c *Chief) dispatchChiefSM(ctx context.Context, morsel graph.Task, promptTemplate string) (int64, error) {
	purpose := chiefPurpose(promptTemplate)
	provider, tier := dispatch.SelectProviderForPurpose(c.cfg, purpose)
	if provider == "" {
		return 0, fmt.Errorf("no available providers for Chief SM dispatch")
	}

	agentID := c.cfg.Chief.AgentID
	if agentID == "" {
		agentID = "chum-chief"
	}

	workspace := c.cfg.Projects["chum"].Workspace
	if workspace == "" {
		workspace = "~/projects/chum" // fallback
	}

	prompt := c.buildCeremonyPrompt(ctx, promptTemplate)

	// Record the dispatch in the store first
	dispatchID, err := c.store.RecordDispatch(
		morsel.ID,
		"chum", // project name
		agentID,
		provider,
		tier,
		-1, // handle (will be set by dispatcher)
		"", // session name (will be set by dispatcher)
		prompt,
		"",             // log path (will be set by dispatcher)
		"",             // branch (not used for ceremonies)
		"headless_cli", // backend
	)
	if err != nil {
		return 0, fmt.Errorf("failed to record ceremony dispatch: %w", err)
	}

	// Trigger the actual dispatch
	handle, err := c.dispatcher.Dispatch(ctx, agentID, prompt, provider, "low", workspace)
	if err != nil {
		c.store.UpdateDispatchStatus(dispatchID, "failed", 1, 0) //nolint:errcheck // best-effort status update before returning actual error
		return 0, fmt.Errorf("failed to dispatch ceremony: %w", err)
	}

	c.logger.Info("ceremony dispatch successful",
		"dispatch_id", dispatchID,
		"handle", handle,
		"provider", provider,
		"tier", tier)

	return dispatchID, nil
}

func chiefPurpose(promptTemplate string) string {
	switch strings.TrimSpace(promptTemplate) {
	case "sprint_planning_multi":
		return dispatch.ScrumPurposePlanning
	case "overall_retrospective":
		return dispatch.ScrumPurposeReporting
	default:
		return dispatch.ScrumPurposeReview
	}
}

// buildCeremonyPrompt constructs the prompt for ceremony dispatches
func (c *Chief) buildCeremonyPrompt(ctx context.Context, template string) string {
	switch template {
	case "sprint_planning_multi":
		return c.buildMultiTeamPlanningPrompt(ctx)
	case "overall_retrospective":
		return c.buildOverallRetrospectivePrompt(ctx)
	default:
		return fmt.Sprintf("Execute Chief SM ceremony: %s", template)
	}
}

// buildMultiTeamPlanningPrompt creates the multi-team sprint planning prompt
func (c *Chief) buildMultiTeamPlanningPrompt(ctx context.Context) string {
	if injectedContext, ok := MultiTeamPortfolioContextFromContext(ctx); ok {
		return fmt.Sprintf(`# Multi-Team Sprint Planning Ceremony

You are the **Chief Scrum Master** conducting a unified sprint planning across all projects.

## Portfolio Context (Authoritative, Scheduler-Prepared)

Use this JSON as the source of truth for this ceremony. It already includes all project backlogs, cross-project dependencies, capacity budgets, provider profiles, and per-project sprint planning status.

`+"```json"+`
%s
`+"```"+`

## Your Mission

1. Review cross-project dependencies and prioritize upstream unblockers.
2. Allocate capacity across projects based on urgency, dependencies, and budget constraints.
3. Identify provider-tier conflicts and recommend staggering where needed.
4. Produce a unified sprint plan for coordination and rationale.

## Required Outcomes

- Record allocations in store.
- Apply any recommended budget rebalancing updates.
- Send unified sprint plan to the coordination Matrix room.
- Ensure this planning runs before per-project sprint planning.
`, injectedContext)
	}

	// **GATHER PORTFOLIO CONTEXT** - This is the missing integration!
	c.logger.Info("gathering portfolio context for multi-team planning")

	portfolioData, err := portfolio.GatherPortfolioBacklogs(ctx, c.cfg, c.dag, c.logger)
	if err != nil {
		c.logger.Error("failed to gather portfolio backlogs", "error", err)
		// Continue with basic prompt if gathering fails
		return c.buildBasicMultiTeamPlanningPrompt(ctx)
	}

	c.logger.Info("portfolio context gathered successfully",
		"active_projects", portfolioData.Summary.ActiveProjects,
		"total_morsels", portfolioData.Summary.TotalOpenMorsels,
		"cross_project_blockers", portfolioData.Summary.CrossProjectBlockers)

	// Build enriched prompt with actual portfolio data
	promptBuilder := fmt.Sprintf(`# Multi-Team Sprint Planning Ceremony

You are the **Chief Scrum Master** conducting a unified sprint planning across all projects.

## Portfolio Context (Pre-gathered)

**Active Projects:** %d
**Total Open Morsels:** %d (Refined: %d, Unrefined: %d, Ready: %d)
**Cross-Project Blockers:** %d

### Project Priorities (High to Low)
`, portfolioData.Summary.ActiveProjects,
		portfolioData.Summary.TotalOpenMorsels,
		portfolioData.Summary.TotalRefinedMorsels,
		portfolioData.Summary.TotalUnrefinedMorsels,
		portfolioData.Summary.TotalReadyToWork,
		portfolioData.Summary.CrossProjectBlockers)

	// Add project details
	for _, projectName := range portfolioData.Summary.ProjectsByPriority {
		if backlog, exists := portfolioData.ProjectBacklogs[projectName]; exists {
			budget := portfolioData.CapacityBudgets[projectName]
			promptBuilder += fmt.Sprintf("- **%s** (Priority %d, Budget: %d%%): %d morsels (%d ready), ~%d min\n",
				projectName, backlog.Priority, budget, len(backlog.AllMorsels),
				len(backlog.ReadyToWork), backlog.TotalEstimate)
		}
	}

	// Add cross-project dependencies
	if len(portfolioData.CrossProjectDeps) > 0 {
		promptBuilder += "\n### Cross-Project Dependencies\n"
		for _, dep := range portfolioData.CrossProjectDeps {
			status := "✅ RESOLVED"
			if !dep.IsResolved {
				status = "🚫 BLOCKING"
			}
			promptBuilder += fmt.Sprintf("- %s:%s → %s:%s %s\n",
				dep.SourceProject, dep.SourceMorselID,
				dep.TargetProject, dep.TargetMorselID, status)
		}
	}

	// Add strategic guidance
	promptBuilder += `

## Your Mission

1. **Review Portfolio Context Above** ✅ (already gathered)

2. **Strategic Allocation** (your LLM reasoning):
   - Prioritize projects based on cross-project dependencies
   - Balance capacity across high-priority work
   - Identify provider tier conflicts and suggest staggering
   - Balance technical debt vs features across portfolio

3. **Deliver Unified Plan**:
   - Record allocations in store using the allocator
   - Update rate limit budgets if rebalancing needed
   - Send coordination summary to Matrix room (if configured)
   - Ensure this runs BEFORE individual project sprint planning

## Context
- **Timing**: Sprint start, before per-project scrum master planning  
- **Priority**: Use premium-tier reasoning for strategic decisions
- **Available Tools**: Use Chief SM allocator functions to record decisions

Execute strategic allocation and unified planning now.`

	return promptBuilder
}

// buildBasicMultiTeamPlanningPrompt creates a fallback prompt when portfolio gathering fails
func (c *Chief) buildBasicMultiTeamPlanningPrompt(ctx context.Context) string {
	return `# Multi-Team Sprint Planning Ceremony

You are the **Chief Scrum Master** conducting a unified sprint planning across all projects.

⚠️ **Note**: Portfolio context gathering failed. You'll need to gather project backlogs manually.

## Your Mission

1. **Gather Portfolio Context** (you must do this):
   - Use tools to collect backlogs from all active projects
   - Build cross-project dependency graph  
   - Calculate per-project capacity budgets from rate_limits.budget

2. **Strategic Allocation** (your LLM reasoning):
   - Review cross-project dependencies: "Project B needs endpoint from Project A — prioritize A's endpoint morsel"
   - Allocate capacity: "Project A gets 60% this sprint (critical deadline), B gets 40%"  
   - Identify conflicts: "Both projects want premium tier — stagger them"
   - Balance technical debt vs features across portfolio

3. **Deliver Unified Plan**:
   - Record allocations in store
   - Update rate limit budgets if rebalancing needed
   - Send coordination summary to Matrix room (if configured)
   - Ensure this runs BEFORE individual project sprint planning

## Context
- **Timing**: Sprint start, before per-project scrum master planning
- **Priority**: Use premium-tier reasoning for strategic decisions
- **Output**: Unified sprint plan with capacity allocations and dependency prioritization

Execute the full ceremony workflow now.`
}

func (c *Chief) buildOverallRetrospectivePrompt(ctx context.Context) string {
	if injectedContext, ok := CrossProjectRetroContextFromContext(ctx); ok {
		return fmt.Sprintf(`# Overall Sprint Retrospective Ceremony

You are the **Chief Scrum Master** leading the end-of-sprint portfolio retrospective after per-project retrospectives have completed.

## Cross-Project Retrospective Context (Authoritative, Scheduler-Prepared)

Use this JSON as the source of truth.

`+"```json"+`
%s
`+"```"+`

## Required Outcomes

1. Summarize systemic wins and recurring issues across projects.
2. Publish a concise coordination update suitable for the Matrix room.
3. Produce explicit follow-up action items in this exact section format:
   - `+"```"+`
## Action Items
- [P1] Improve dependency handoff checklist | project:chum | owner:chief-sm | why:handoff ambiguity caused delays
- [P2] Rebalance provider usage for retries | project:chum | owner:ops | why:failure concentration
`+"```"+`
4. Keep each action item concrete, scoped, and executable as a morsel.
`, injectedContext)
	}

	return `# Overall Sprint Retrospective Ceremony

You are the **Chief Scrum Master** leading the end-of-sprint portfolio retrospective after per-project retrospectives have completed.

## Required Outcomes

1. Gather key outcomes from all project retros.
2. Identify systemic patterns across teams.
3. Send a concise coordination summary for the Matrix room.
4. Produce follow-up action items using:

## Action Items
- [P1] <title> | project:<project> | owner:<owner> | why:<reason>

Only include actionable, concrete items that should become follow-up morsels.`
}

// GetMultiTeamPlanningSchedule returns the default schedule for multi-team planning
func (c *Chief) GetMultiTeamPlanningSchedule() CeremonySchedule {
	// Default: Monday at 9:00 AM (before per-project planning), overridden by shared cadence.
	weekday := time.Monday
	hour := 9
	minute := 0
	loc := time.UTC

	if c.cfg != nil {
		if rawDay := strings.TrimSpace(c.cfg.Cadence.SprintStartDay); rawDay != "" {
			if parsedDay, err := c.cfg.Cadence.StartWeekday(); err == nil {
				weekday = parsedDay
			} else {
				c.logger.Warn("invalid cadence sprint_start_day; using default Monday", "error", err)
			}
		}
		if rawTime := strings.TrimSpace(c.cfg.Cadence.SprintStartTime); rawTime != "" {
			if parsedHour, parsedMinute, err := c.cfg.Cadence.StartClock(); err == nil {
				hour = parsedHour
				minute = parsedMinute
			} else {
				c.logger.Warn("invalid cadence sprint_start_time; using default 09:00", "error", err)
			}
		}
		if parsedLoc, err := c.cfg.Cadence.LoadLocation(); err == nil {
			loc = parsedLoc
		} else {
			c.logger.Warn("invalid cadence timezone; using UTC", "error", err)
		}
	}

	targetTime := time.Date(0, 1, 1, hour, minute, 0, 0, loc)

	return CeremonySchedule{
		Type:      CeremonyMultiTeamPlanning,
		Cadence:   24 * time.Hour, // Check daily
		DayOfWeek: weekday,
		TimeOfDay: targetTime,
	}
}

// GetCurrentAllocation returns the currently active allocation decision
func (c *Chief) GetCurrentAllocation(ctx context.Context) (*store.AllocationDecision, error) {
	return c.allocator.GetCurrentAllocation(ctx)
}

// GetProjectAllocation returns the current capacity allocation for a specific project
func (c *Chief) GetProjectAllocation(ctx context.Context, projectName string) (*store.ProjectAllocation, error) {
	allocation, err := c.GetCurrentAllocation(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current allocation: %w", err)
	}

	if projectAlloc, exists := allocation.ProjectAllocations[projectName]; exists {
		return &projectAlloc, nil
	}

	return nil, fmt.Errorf("no allocation found for project: %s", projectName)
}

// GetCrossProjectDependencies returns current cross-project dependencies
func (c *Chief) GetCrossProjectDependencies(ctx context.Context) ([]store.CrossProjectDependency, error) {
	allocation, err := c.GetCurrentAllocation(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current allocation: %w", err)
	}

	return allocation.CrossProjectDeps, nil
}

// IsProjectCapacityAvailable checks if a project has available capacity for new work
func (c *Chief) IsProjectCapacityAvailable(ctx context.Context, projectName string) (available bool, percent float64, err error) {
	projectAlloc, allocErr := c.GetProjectAllocation(ctx, projectName)
	if allocErr != nil {
		// If no allocation exists, assume capacity is available
		return true, 100.0, nil //nolint:nilerr // fallback when no allocation exists
	}

	// Simple capacity check - in production this would consider current workload
	availablePercent := projectAlloc.CapacityPercent
	return availablePercent > 0, availablePercent, nil
}
