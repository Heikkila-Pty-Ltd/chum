// Package store provides SQLite-backed persistence.

package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// AllocationDecision represents a Chief SM allocation decision for a sprint planning ceremony
type AllocationDecision struct {
	ID               int64                      `json:"id"`
	CeremonyID       string                     `json:"ceremony_id"`       // Links to ceremony morsel ID
	SprintStartDate  time.Time                  `json:"sprint_start_date"` 
	SprintEndDate    time.Time                  `json:"sprint_end_date"`
	TotalCapacity    int                        `json:"total_capacity"`    // Total capacity points available
	ProjectAllocations map[string]ProjectAllocation `json:"project_allocations"`
	CrossProjectDeps []CrossProjectDependency   `json:"cross_project_deps"`
	BudgetUpdates    []BudgetUpdate             `json:"budget_updates"`    // Rate limit budget changes
	Rationale        string                     `json:"rationale"`         // Chief SM reasoning
	CreatedAt        time.Time                  `json:"created_at"`
	Status           string                     `json:"status"`            // "draft", "active", "completed"
}

// ProjectAllocation represents capacity allocation for a specific project
type ProjectAllocation struct {
	Project           string  `json:"project"`
	AllocatedCapacity int     `json:"allocated_capacity"` // Capacity points
	CapacityPercent   float64 `json:"capacity_percent"`   // Percentage of total
	PriorityMorsels     []string `json:"priority_morsels"`     // Morsel IDs to prioritize
	ProviderTier      string  `json:"provider_tier"`      // "fast", "balanced", "premium"
	Notes             string  `json:"notes"`              // Additional context
}

// CrossProjectDependency represents dependencies between projects
type CrossProjectDependency struct {
	FromProject string `json:"from_project"` // Dependent project
	ToProject   string `json:"to_project"`   // Dependency project
	MorselID      string `json:"morsel_id"`      // Specific morsel that's needed
	Priority    string `json:"priority"`     // "critical", "high", "medium"
	Description string `json:"description"`  // Dependency description
}

// BudgetUpdate represents a rate limit budget change
type BudgetUpdate struct {
	Project        string `json:"project"`
	OldPercentage  int    `json:"old_percentage"`
	NewPercentage  int    `json:"new_percentage"`
	ChangeReason   string `json:"change_reason"`
}

// initAllocationSchema ensures allocation tables exist
func (s *Store) initAllocationSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS allocation_decisions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ceremony_id TEXT NOT NULL,
		sprint_start_date DATETIME NOT NULL,
		sprint_end_date DATETIME NOT NULL,
		total_capacity INTEGER NOT NULL DEFAULT 0,
		project_allocations TEXT NOT NULL DEFAULT '{}',
		cross_project_deps TEXT NOT NULL DEFAULT '[]',
		budget_updates TEXT NOT NULL DEFAULT '[]',
		rationale TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL DEFAULT (datetime('now')),
		status TEXT NOT NULL DEFAULT 'draft'
	);

	CREATE INDEX IF NOT EXISTS idx_allocation_decisions_ceremony ON allocation_decisions(ceremony_id);
	CREATE INDEX IF NOT EXISTS idx_allocation_decisions_sprint_start ON allocation_decisions(sprint_start_date);
	CREATE INDEX IF NOT EXISTS idx_allocation_decisions_status ON allocation_decisions(status);
	
	CREATE TABLE IF NOT EXISTS project_capacity_history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		project TEXT NOT NULL,
		allocation_id INTEGER NOT NULL REFERENCES allocation_decisions(id),
		capacity_points INTEGER NOT NULL DEFAULT 0,
		capacity_percent REAL NOT NULL DEFAULT 0.0,
		provider_tier TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL DEFAULT (datetime('now'))
	);
	
	CREATE INDEX IF NOT EXISTS idx_capacity_history_project ON project_capacity_history(project, created_at);
	CREATE INDEX IF NOT EXISTS idx_capacity_history_allocation ON project_capacity_history(allocation_id);
	`
	
	_, err := s.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("create allocation schema: %w", err)
	}
	return nil
}

// RecordAllocationDecision stores a Chief SM allocation decision
func (s *Store) RecordAllocationDecision(decision *AllocationDecision) error {
	// Ensure schema exists
	if err := s.initAllocationSchema(); err != nil {
		return fmt.Errorf("init allocation schema: %w", err)
	}

	// Serialize JSON fields
	projectAllocationsJSON, err := json.Marshal(decision.ProjectAllocations)
	if err != nil {
		return fmt.Errorf("marshal project allocations: %w", err)
	}
	
	crossDepsJSON, err := json.Marshal(decision.CrossProjectDeps)
	if err != nil {
		return fmt.Errorf("marshal cross project deps: %w", err)
	}
	
	budgetUpdatesJSON, err := json.Marshal(decision.BudgetUpdates)
	if err != nil {
		return fmt.Errorf("marshal budget updates: %w", err)
	}

	// Set created_at if not already set
	if decision.CreatedAt.IsZero() {
		decision.CreatedAt = time.Now()
	}

	// Insert main record
	result, err := s.db.Exec(`
		INSERT INTO allocation_decisions 
		(ceremony_id, sprint_start_date, sprint_end_date, total_capacity, 
		 project_allocations, cross_project_deps, budget_updates, rationale, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		decision.CeremonyID,
		decision.SprintStartDate.UTC().Format("2006-01-02 15:04:05"),
		decision.SprintEndDate.UTC().Format("2006-01-02 15:04:05"),
		decision.TotalCapacity,
		string(projectAllocationsJSON),
		string(crossDepsJSON),
		string(budgetUpdatesJSON),
		decision.Rationale,
		decision.Status,
		decision.CreatedAt.UTC().Format("2006-01-02 15:04:05"),
	)
	if err != nil {
		return fmt.Errorf("insert allocation decision: %w", err)
	}

	allocationID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get allocation ID: %w", err)
	}
	decision.ID = allocationID

	// Insert capacity history records
	for project, allocation := range decision.ProjectAllocations {
		_, err = s.db.Exec(`
			INSERT INTO project_capacity_history 
			(project, allocation_id, capacity_points, capacity_percent, provider_tier)
			VALUES (?, ?, ?, ?, ?)`,
			project, allocationID, allocation.AllocatedCapacity, 
			allocation.CapacityPercent, allocation.ProviderTier,
		)
		if err != nil {
			return fmt.Errorf("insert capacity history for %s: %w", project, err)
		}
	}

	return nil
}

// GetAllocationDecision retrieves an allocation decision by ID
func (s *Store) GetAllocationDecision(id int64) (*AllocationDecision, error) {
	var decision AllocationDecision
	var projectAllocationsJSON, crossDepsJSON, budgetUpdatesJSON string

	err := s.db.QueryRow(`
		SELECT id, ceremony_id, sprint_start_date, sprint_end_date, total_capacity,
		       project_allocations, cross_project_deps, budget_updates, rationale, 
		       created_at, status
		FROM allocation_decisions 
		WHERE id = ?`, id).Scan(
		&decision.ID, &decision.CeremonyID, &decision.SprintStartDate, &decision.SprintEndDate,
		&decision.TotalCapacity, &projectAllocationsJSON, &crossDepsJSON, &budgetUpdatesJSON,
		&decision.Rationale, &decision.CreatedAt, &decision.Status,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("allocation decision not found: %d", id)
		}
		return nil, fmt.Errorf("get allocation decision: %w", err)
	}

	// Deserialize JSON fields
	if err := json.Unmarshal([]byte(projectAllocationsJSON), &decision.ProjectAllocations); err != nil {
		return nil, fmt.Errorf("unmarshal project allocations: %w", err)
	}
	
	if err := json.Unmarshal([]byte(crossDepsJSON), &decision.CrossProjectDeps); err != nil {
		return nil, fmt.Errorf("unmarshal cross project deps: %w", err)
	}
	
	if err := json.Unmarshal([]byte(budgetUpdatesJSON), &decision.BudgetUpdates); err != nil {
		return nil, fmt.Errorf("unmarshal budget updates: %w", err)
	}

	return &decision, nil
}

// GetAllocationDecisionByCeremony retrieves the most recent allocation decision for a ceremony
func (s *Store) GetAllocationDecisionByCeremony(ceremonyID string) (*AllocationDecision, error) {
	var decision AllocationDecision
	var projectAllocationsJSON, crossDepsJSON, budgetUpdatesJSON string

	err := s.db.QueryRow(`
		SELECT id, ceremony_id, sprint_start_date, sprint_end_date, total_capacity,
		       project_allocations, cross_project_deps, budget_updates, rationale, 
		       created_at, status
		FROM allocation_decisions 
		WHERE ceremony_id = ? 
		ORDER BY created_at DESC 
		LIMIT 1`, ceremonyID).Scan(
		&decision.ID, &decision.CeremonyID, &decision.SprintStartDate, &decision.SprintEndDate,
		&decision.TotalCapacity, &projectAllocationsJSON, &crossDepsJSON, &budgetUpdatesJSON,
		&decision.Rationale, &decision.CreatedAt, &decision.Status,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no allocation decision found for ceremony: %s", ceremonyID)
		}
		return nil, fmt.Errorf("get allocation decision by ceremony: %w", err)
	}

	// Deserialize JSON fields
	if err := json.Unmarshal([]byte(projectAllocationsJSON), &decision.ProjectAllocations); err != nil {
		return nil, fmt.Errorf("unmarshal project allocations: %w", err)
	}
	
	if err := json.Unmarshal([]byte(crossDepsJSON), &decision.CrossProjectDeps); err != nil {
		return nil, fmt.Errorf("unmarshal cross project deps: %w", err)
	}
	
	if err := json.Unmarshal([]byte(budgetUpdatesJSON), &decision.BudgetUpdates); err != nil {
		return nil, fmt.Errorf("unmarshal budget updates: %w", err)
	}

	return &decision, nil
}

// UpdateAllocationStatus updates the status of an allocation decision
func (s *Store) UpdateAllocationStatus(id int64, status string) error {
	_, err := s.db.Exec(`
		UPDATE allocation_decisions 
		SET status = ? 
		WHERE id = ?`, status, id)
	if err != nil {
		return fmt.Errorf("update allocation status: %w", err)
	}
	return nil
}

// GetActiveAllocation returns the currently active allocation decision
func (s *Store) GetActiveAllocation() (*AllocationDecision, error) {
	var decision AllocationDecision
	var projectAllocationsJSON, crossDepsJSON, budgetUpdatesJSON string

	err := s.db.QueryRow(`
		SELECT id, ceremony_id, sprint_start_date, sprint_end_date, total_capacity,
		       project_allocations, cross_project_deps, budget_updates, rationale, 
		       created_at, status
		FROM allocation_decisions 
		WHERE status = 'active' 
		ORDER BY created_at DESC 
		LIMIT 1`).Scan(
		&decision.ID, &decision.CeremonyID, &decision.SprintStartDate, &decision.SprintEndDate,
		&decision.TotalCapacity, &projectAllocationsJSON, &crossDepsJSON, &budgetUpdatesJSON,
		&decision.Rationale, &decision.CreatedAt, &decision.Status,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no active allocation found")
		}
		return nil, fmt.Errorf("get active allocation: %w", err)
	}

	// Deserialize JSON fields
	if err := json.Unmarshal([]byte(projectAllocationsJSON), &decision.ProjectAllocations); err != nil {
		return nil, fmt.Errorf("unmarshal project allocations: %w", err)
	}
	
	if err := json.Unmarshal([]byte(crossDepsJSON), &decision.CrossProjectDeps); err != nil {
		return nil, fmt.Errorf("unmarshal cross project deps: %w", err)
	}
	
	if err := json.Unmarshal([]byte(budgetUpdatesJSON), &decision.BudgetUpdates); err != nil {
		return nil, fmt.Errorf("unmarshal budget updates: %w", err)
	}

	return &decision, nil
}

// ListAllocationDecisions returns allocation decisions within a date range
func (s *Store) ListAllocationDecisions(startDate, endDate time.Time) ([]*AllocationDecision, error) {
	rows, err := s.db.Query(`
		SELECT id, ceremony_id, sprint_start_date, sprint_end_date, total_capacity,
		       project_allocations, cross_project_deps, budget_updates, rationale, 
		       created_at, status
		FROM allocation_decisions 
		WHERE created_at >= ? AND created_at <= ?
		ORDER BY created_at DESC`, 
		startDate.UTC().Format("2006-01-02 15:04:05"), endDate.UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, fmt.Errorf("list allocation decisions: %w", err)
	}
	defer rows.Close()

	var decisions []*AllocationDecision
	for rows.Next() {
		var decision AllocationDecision
		var projectAllocationsJSON, crossDepsJSON, budgetUpdatesJSON string

		err := rows.Scan(
			&decision.ID, &decision.CeremonyID, &decision.SprintStartDate, &decision.SprintEndDate,
			&decision.TotalCapacity, &projectAllocationsJSON, &crossDepsJSON, &budgetUpdatesJSON,
			&decision.Rationale, &decision.CreatedAt, &decision.Status,
		)
		if err != nil {
			return nil, fmt.Errorf("scan allocation decision: %w", err)
		}

		// Deserialize JSON fields
		if err := json.Unmarshal([]byte(projectAllocationsJSON), &decision.ProjectAllocations); err != nil {
			return nil, fmt.Errorf("unmarshal project allocations: %w", err)
		}
		
		if err := json.Unmarshal([]byte(crossDepsJSON), &decision.CrossProjectDeps); err != nil {
			return nil, fmt.Errorf("unmarshal cross project deps: %w", err)
		}
		
		if err := json.Unmarshal([]byte(budgetUpdatesJSON), &decision.BudgetUpdates); err != nil {
			return nil, fmt.Errorf("unmarshal budget updates: %w", err)
		}

		decisions = append(decisions, &decision)
	}

	return decisions, rows.Err()
}

// GetProjectCapacityHistory returns capacity allocation history for a project
func (s *Store) GetProjectCapacityHistory(project string, days int) ([]ProjectCapacityRecord, error) {
	cutoff := time.Now().AddDate(0, 0, -days)
	
	rows, err := s.db.Query(`
		SELECT pch.capacity_points, pch.capacity_percent, pch.provider_tier, pch.created_at,
		       ad.ceremony_id, ad.sprint_start_date, ad.sprint_end_date
		FROM project_capacity_history pch
		JOIN allocation_decisions ad ON pch.allocation_id = ad.id
		WHERE pch.project = ? AND pch.created_at >= ?
		ORDER BY pch.created_at DESC`, 
		project, cutoff.Format(time.DateTime))
	if err != nil {
		return nil, fmt.Errorf("get project capacity history: %w", err)
	}
	defer rows.Close()

	var history []ProjectCapacityRecord
	for rows.Next() {
		var record ProjectCapacityRecord
		err := rows.Scan(
			&record.CapacityPoints, &record.CapacityPercent, &record.ProviderTier,
			&record.CreatedAt, &record.CeremonyID, &record.SprintStartDate, &record.SprintEndDate,
		)
		if err != nil {
			return nil, fmt.Errorf("scan capacity record: %w", err)
		}
		history = append(history, record)
	}

	return history, rows.Err()
}

// ProjectCapacityRecord represents a project's capacity allocation at a point in time
type ProjectCapacityRecord struct {
	CapacityPoints   int       `json:"capacity_points"`
	CapacityPercent  float64   `json:"capacity_percent"`
	ProviderTier     string    `json:"provider_tier"`
	CreatedAt        time.Time `json:"created_at"`
	CeremonyID       string    `json:"ceremony_id"`
	SprintStartDate  time.Time `json:"sprint_start_date"`
	SprintEndDate    time.Time `json:"sprint_end_date"`
}