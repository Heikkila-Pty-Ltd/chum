// Package allocations manages budget tracking and recording.

package chief

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/dispatch"
	"github.com/antigravity-dev/chum/internal/store"
)

// AllocationRecorder handles recording and reporting of Chief SM allocation decisions
type AllocationRecorder struct {
	cfg        *config.Config
	store      *store.Store
	dispatcher dispatch.DispatcherInterface
	logger     *slog.Logger
}

// NewAllocationRecorder creates a new allocation recorder
func NewAllocationRecorder(cfg *config.Config, store *store.Store, dispatcher dispatch.DispatcherInterface, logger *slog.Logger) *AllocationRecorder {
	return &AllocationRecorder{
		cfg:        cfg,
		store:      store,
		dispatcher: dispatcher,
		logger:     logger,
	}
}

// RecordAllocationDecision processes and stores a Chief SM allocation decision
func (ar *AllocationRecorder) RecordAllocationDecision(ctx context.Context, ceremonyID string, decision *store.AllocationDecision) error {
	// Set ceremony linkage and status
	decision.CeremonyID = ceremonyID
	decision.Status = "active"

	// Mark any existing active allocations as completed
	if err := ar.completeActiveAllocations(ctx); err != nil {
		ar.logger.Warn("failed to complete active allocations", "error", err)
	}

	// Store the allocation decision
	if err := ar.store.RecordAllocationDecision(decision); err != nil {
		return fmt.Errorf("record allocation decision: %w", err)
	}

	ar.logger.Info("allocation decision recorded", 
		"ceremony_id", ceremonyID,
		"allocation_id", decision.ID,
		"total_capacity", decision.TotalCapacity,
		"projects", len(decision.ProjectAllocations))

	// Apply budget updates if any
	if len(decision.BudgetUpdates) > 0 {
		if err := ar.applyBudgetUpdates(ctx, decision.BudgetUpdates); err != nil {
			ar.logger.Error("failed to apply budget updates", "error", err)
			return fmt.Errorf("apply budget updates: %w", err)
		}
	}

	// Send unified sprint plan to Matrix room
	if ar.cfg.Chief.MatrixRoom != "" {
		if err := ar.sendUnifiedSprintPlan(ctx, decision); err != nil {
			ar.logger.Error("failed to send unified sprint plan", "error", err)
			// Don't fail the whole process if Matrix messaging fails
		}
	}

	return nil
}

// completeActiveAllocations marks existing active allocations as completed
func (ar *AllocationRecorder) completeActiveAllocations(_ context.Context) error {
	// This is a simple implementation - could be made more sophisticated
	// to handle overlapping sprint periods differently
	
	activeAllocation, err := ar.store.GetActiveAllocation()
	if err != nil {
		// If no active allocation exists, that's fine
		return nil //nolint:nilerr // missing active allocation is expected
	}

	return ar.store.UpdateAllocationStatus(activeAllocation.ID, "completed")
}

// applyBudgetUpdates applies rate limit budget changes recommended by Chief SM
func (ar *AllocationRecorder) applyBudgetUpdates(_ context.Context, updates []store.BudgetUpdate) error { //nolint:unparam // NOTE: budget changes are persisted inside the AllocationDecision record; runtime config reload is not yet wired
	ar.logger.Info("applying budget updates", "count", len(updates))

	// In a real implementation, this would update the configuration
	// For now, we'll log the changes and potentially trigger a config reload
	
	for _, update := range updates {
		ar.logger.Info("budget update applied",
			"project", update.Project,
			"old_percentage", update.OldPercentage,
			"new_percentage", update.NewPercentage,
			"reason", update.ChangeReason)

		// Record the budget change as a health event for tracking
		if err := ar.store.RecordHealthEvent("budget_update", 
			fmt.Sprintf("Project %s budget updated from %d%% to %d%% - %s",
				update.Project, update.OldPercentage, update.NewPercentage, update.ChangeReason)); err != nil {
			ar.logger.Warn("failed to record budget update event", "error", err)
		}
	}

	// NOTE: Budget updates are currently logged and recorded as health events.
	// Actual runtime config reload and scheduler notification are not yet wired;
	// budget values are persisted as part of the AllocationDecision record.
	
	return nil
}

// sendUnifiedSprintPlan sends the allocation decision to the Matrix coordination room
func (ar *AllocationRecorder) sendUnifiedSprintPlan(ctx context.Context, decision *store.AllocationDecision) error {
	ar.logger.Info("sending unified sprint plan to Matrix room", "room", ar.cfg.Chief.MatrixRoom)

	// Build the unified sprint plan message
	message := ar.buildSprintPlanMessage(decision)

	// Send via agent dispatch to Matrix room
	agentID := ar.cfg.Chief.AgentID
	if agentID == "" {
		agentID = "chum-coordinator"
	}

	// Create a dispatch to send the message
	prompt := fmt.Sprintf(`# Matrix Room Coordination Message

Send the following unified sprint plan to the Matrix room: %s

---

%s

---

This message should be sent to the coordination Matrix room to inform all teams about the multi-team sprint allocation decisions.`, ar.cfg.Chief.MatrixRoom, message)

	// Use a lightweight tier for simple messaging
	provider := ""
	if len(ar.cfg.Tiers.Fast) > 0 {
		provider = ar.cfg.Tiers.Fast[0]
	}

	workspace := "/tmp" // Simple workspace for messaging tasks

	_, err := ar.dispatcher.Dispatch(ctx, agentID, prompt, provider, "none", workspace)
	if err != nil {
		return fmt.Errorf("dispatch Matrix message: %w", err)
	}

	ar.logger.Info("unified sprint plan dispatched to Matrix room",
		"agent", agentID,
		"provider", provider)

	return nil
}

// buildSprintPlanMessage creates a formatted message for the Matrix room
func (ar *AllocationRecorder) buildSprintPlanMessage(decision *store.AllocationDecision) string {
	var b strings.Builder

	// Header
	fmt.Fprintf(&b, "# 🎯 Unified Sprint Plan — %s to %s\n\n",
		decision.SprintStartDate.Format("Jan 2"),
		decision.SprintEndDate.Format("Jan 2, 2006"))

	// Executive summary
	fmt.Fprintf(&b, "**Chief SM Allocation Summary:**\n")
	fmt.Fprintf(&b, "- Total Capacity: %d points\n", decision.TotalCapacity)
	fmt.Fprintf(&b, "- Projects Allocated: %d\n", len(decision.ProjectAllocations))
	
	if len(decision.CrossProjectDeps) > 0 {
		fmt.Fprintf(&b, "- Cross-Project Dependencies: %d\n", len(decision.CrossProjectDeps))
	}
	
	if len(decision.BudgetUpdates) > 0 {
		fmt.Fprintf(&b, "- Budget Adjustments: %d\n", len(decision.BudgetUpdates))
	}
	
	fmt.Fprintf(&b, "\n")

	// Project allocations
	fmt.Fprintf(&b, "## 📊 Project Capacity Allocation\n\n")
	for project, allocation := range decision.ProjectAllocations {
		fmt.Fprintf(&b, "### %s\n", project)
		fmt.Fprintf(&b, "- **Capacity:** %d points (%.1f%% of total)\n", 
			allocation.AllocatedCapacity, allocation.CapacityPercent)
		fmt.Fprintf(&b, "- **Provider Tier:** %s\n", allocation.ProviderTier)
		
		if len(allocation.PriorityMorsels) > 0 {
			fmt.Fprintf(&b, "- **Priority Morsels:** %s\n", 
				strings.Join(allocation.PriorityMorsels, ", "))
		}
		
		if allocation.Notes != "" {
			fmt.Fprintf(&b, "- **Notes:** %s\n", allocation.Notes)
		}
		fmt.Fprintf(&b, "\n")
	}

	// Cross-project dependencies
	if len(decision.CrossProjectDeps) > 0 {
		fmt.Fprintf(&b, "## 🔗 Cross-Project Dependencies\n\n")
		for _, dep := range decision.CrossProjectDeps {
			fmt.Fprintf(&b, "- **%s** depends on **%s**\n", dep.FromProject, dep.ToProject)
			fmt.Fprintf(&b, "  - Morsel: `%s` (%s priority)\n", dep.MorselID, dep.Priority)
			if dep.Description != "" {
				fmt.Fprintf(&b, "  - %s\n", dep.Description)
			}
			fmt.Fprintf(&b, "\n")
		}
	}

	// Budget updates
	if len(decision.BudgetUpdates) > 0 {
		fmt.Fprintf(&b, "## 💰 Rate Limit Budget Adjustments\n\n")
		for _, update := range decision.BudgetUpdates {
			fmt.Fprintf(&b, "- **%s:** %d%% → %d%%\n", 
				update.Project, update.OldPercentage, update.NewPercentage)
			if update.ChangeReason != "" {
				fmt.Fprintf(&b, "  - Reason: %s\n", update.ChangeReason)
			}
		}
		fmt.Fprintf(&b, "\n")
	}

	// Rationale
	if decision.Rationale != "" {
		fmt.Fprintf(&b, "## 🧠 Chief SM Rationale\n\n")
		fmt.Fprintf(&b, "%s\n\n", decision.Rationale)
	}

	// Footer
	fmt.Fprintf(&b, "---\n")
	fmt.Fprintf(&b, "*Generated by Chief Scrum Master at %s*\n", 
		decision.CreatedAt.Format("2006-01-02 15:04:05 MST"))

	return b.String()
}

// GetCurrentAllocation returns the currently active allocation decision
func (ar *AllocationRecorder) GetCurrentAllocation(ctx context.Context) (*store.AllocationDecision, error) {
	return ar.store.GetActiveAllocation()
}

// ParseAllocationFromOutput parses Chief SM allocation output into structured data
// This would be called after the Chief SM LLM completes its reasoning
func (ar *AllocationRecorder) ParseAllocationFromOutput(_ context.Context, ceremonyID, chiefOutput string) (*store.AllocationDecision, error) {
	// This is a simplified parser - in production this would be more sophisticated
	// and potentially use structured output from the LLM
	
	now := time.Now()
	decision := &store.AllocationDecision{
		CeremonyID:         ceremonyID,
		SprintStartDate:    now, // Would be parsed from output
		SprintEndDate:      now.AddDate(0, 0, 14), // 2-week sprint
		TotalCapacity:      100, // Would be calculated from context
		ProjectAllocations: make(map[string]store.ProjectAllocation),
		CrossProjectDeps:   []store.CrossProjectDependency{},
		BudgetUpdates:      []store.BudgetUpdate{},
		Rationale:          chiefOutput, // Store full output as rationale
		Status:             "draft",
	}

	// NOTE: LLM output parsing is not yet implemented. The function currently
	// creates a uniform allocation across all enabled projects. When structured
	// output parsing is added, it should extract per-project capacity percentages,
	// cross-project dependencies, and budget update recommendations from chiefOutput.

	// Create a basic allocation based on configured projects
	totalProjects := len(ar.cfg.Projects)
	if totalProjects > 0 {
		basePercent := 100.0 / float64(totalProjects)
		capacity := int(float64(decision.TotalCapacity) * basePercent / 100.0)
		
		for projectName := range ar.cfg.Projects {
			if ar.cfg.Projects[projectName].Enabled {
				decision.ProjectAllocations[projectName] = store.ProjectAllocation{
					Project:           projectName,
					AllocatedCapacity: capacity,
					CapacityPercent:   basePercent,
					ProviderTier:      "balanced", // Default tier
					Notes:             "Auto-generated allocation",
				}
			}
		}
	}

	ar.logger.Debug("parsed allocation from Chief SM output",
		"ceremony_id", ceremonyID,
		"projects", len(decision.ProjectAllocations),
		"total_capacity", decision.TotalCapacity)

	return decision, nil
}