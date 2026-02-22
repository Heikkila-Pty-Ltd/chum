package store

import (
	"testing"
	"time"
)

func TestAllocationDecision(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open test store: %v", err)
	}
	defer store.Close()

	// Test data
	now := time.Now()
	decision := &AllocationDecision{
		CeremonyID:      "ceremony-123",
		SprintStartDate: now,
		SprintEndDate:   now.AddDate(0, 0, 14),
		TotalCapacity:   100,
		ProjectAllocations: map[string]ProjectAllocation{
			"project-a": {
				Project:           "project-a",
				AllocatedCapacity: 60,
				CapacityPercent:   60.0,
				ProviderTier:      "premium",
				PriorityMorsels:     []string{"morsel-1", "morsel-2"},
				Notes:             "Critical deadline",
			},
			"project-b": {
				Project:           "project-b",
				AllocatedCapacity: 40,
				CapacityPercent:   40.0,
				ProviderTier:      "balanced",
				PriorityMorsels:     []string{"morsel-3"},
				Notes:             "Maintenance work",
			},
		},
		CrossProjectDeps: []CrossProjectDependency{
			{
				FromProject: "project-b",
				ToProject:   "project-a",
				MorselID:      "morsel-1",
				Priority:    "critical",
				Description: "Needs API endpoint",
			},
		},
		BudgetUpdates: []BudgetUpdate{
			{
				Project:       "project-a",
				OldPercentage: 50,
				NewPercentage: 60,
				ChangeReason:  "Critical deadline requires more capacity",
			},
		},
		Rationale: "Project A has critical deadline, allocating more resources",
		Status:    "active",
	}

	// Test recording allocation
	if err := store.RecordAllocationDecision(decision); err != nil {
		t.Fatalf("failed to record allocation decision: %v", err)
	}

	if decision.ID == 0 {
		t.Error("expected allocation ID to be set after recording")
	}

	// Test retrieving by ID
	retrieved, err := store.GetAllocationDecision(decision.ID)
	if err != nil {
		t.Fatalf("failed to get allocation decision: %v", err)
	}

	// Verify data
	if retrieved.CeremonyID != decision.CeremonyID {
		t.Errorf("expected ceremony ID %s, got %s", decision.CeremonyID, retrieved.CeremonyID)
	}

	if retrieved.TotalCapacity != decision.TotalCapacity {
		t.Errorf("expected total capacity %d, got %d", decision.TotalCapacity, retrieved.TotalCapacity)
	}

	if len(retrieved.ProjectAllocations) != 2 {
		t.Errorf("expected 2 project allocations, got %d", len(retrieved.ProjectAllocations))
	}

	projectA, exists := retrieved.ProjectAllocations["project-a"]
	if !exists {
		t.Error("expected project-a allocation to exist")
	} else {
		if projectA.AllocatedCapacity != 60 {
			t.Errorf("expected project-a capacity 60, got %d", projectA.AllocatedCapacity)
		}
		if projectA.ProviderTier != "premium" {
			t.Errorf("expected project-a tier premium, got %s", projectA.ProviderTier)
		}
	}

	if len(retrieved.CrossProjectDeps) != 1 {
		t.Errorf("expected 1 cross-project dependency, got %d", len(retrieved.CrossProjectDeps))
	}

	if len(retrieved.BudgetUpdates) != 1 {
		t.Errorf("expected 1 budget update, got %d", len(retrieved.BudgetUpdates))
	}

	// Test retrieving by ceremony
	byCeremony, err := store.GetAllocationDecisionByCeremony("ceremony-123")
	if err != nil {
		t.Fatalf("failed to get allocation by ceremony: %v", err)
	}

	if byCeremony.ID != decision.ID {
		t.Errorf("expected same allocation ID %d, got %d", decision.ID, byCeremony.ID)
	}

	// Test getting active allocation
	active, err := store.GetActiveAllocation()
	if err != nil {
		t.Fatalf("failed to get active allocation: %v", err)
	}

	if active.ID != decision.ID {
		t.Errorf("expected active allocation ID %d, got %d", decision.ID, active.ID)
	}

	// Test updating status
	if err := store.UpdateAllocationStatus(decision.ID, "completed"); err != nil {
		t.Fatalf("failed to update allocation status: %v", err)
	}

	updated, err := store.GetAllocationDecision(decision.ID)
	if err != nil {
		t.Fatalf("failed to get updated allocation: %v", err)
	}

	if updated.Status != "completed" {
		t.Errorf("expected status 'completed', got %s", updated.Status)
	}

	// Test listing allocations
	start := now.Add(-1 * time.Hour)
	end := now.Add(1 * time.Hour)
	
	allocations, err := store.ListAllocationDecisions(start, end)
	if err != nil {
		t.Fatalf("failed to list allocations: %v", err)
	}

	if len(allocations) != 1 {
		t.Errorf("expected 1 allocation in range, got %d", len(allocations))
	}
}

func TestProjectCapacityHistory(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open test store: %v", err)
	}
	defer store.Close()

	now := time.Now()
	decision := &AllocationDecision{
		CeremonyID:      "ceremony-456",
		SprintStartDate: now,
		SprintEndDate:   now.AddDate(0, 0, 14),
		TotalCapacity:   200,
		ProjectAllocations: map[string]ProjectAllocation{
			"test-project": {
				Project:           "test-project",
				AllocatedCapacity: 120,
				CapacityPercent:   60.0,
				ProviderTier:      "balanced",
			},
		},
		Status: "active",
	}

	// Record allocation
	if err := store.RecordAllocationDecision(decision); err != nil {
		t.Fatalf("failed to record allocation: %v", err)
	}

	// Test capacity history
	history, err := store.GetProjectCapacityHistory("test-project", 1)
	if err != nil {
		t.Fatalf("failed to get capacity history: %v", err)
	}

	if len(history) != 1 {
		t.Errorf("expected 1 history record, got %d", len(history))
	}

	record := history[0]
	if record.CapacityPoints != 120 {
		t.Errorf("expected capacity points 120, got %d", record.CapacityPoints)
	}

	if record.CapacityPercent != 60.0 {
		t.Errorf("expected capacity percent 60.0, got %f", record.CapacityPercent)
	}

	if record.ProviderTier != "balanced" {
		t.Errorf("expected provider tier 'balanced', got %s", record.ProviderTier)
	}

	if record.CeremonyID != "ceremony-456" {
		t.Errorf("expected ceremony ID 'ceremony-456', got %s", record.CeremonyID)
	}
}

func setupTestDB(t *testing.T) *Store {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}
	return store
}