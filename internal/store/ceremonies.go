package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// SprintReviewData holds metrics comparing planned vs delivered morsels for a sprint period.
type SprintReviewData struct {
	StartDate      time.Time              `json:"start_date"`
	EndDate        time.Time              `json:"end_date"`
	TotalMorsels     int                    `json:"total_morsels"`
	CompletedMorsels int                    `json:"completed_morsels"`
	PlannedMorsels   int                    `json:"planned_morsels"`
	CompletionRate float64                `json:"completion_rate"`
	ProjectStats   map[string]ProjectStat `json:"project_stats"`
}

// ProjectStat holds completion metrics for a specific project.
type ProjectStat struct {
	Project        string  `json:"project"`
	TotalMorsels     int     `json:"total_morsels"`
	CompletedMorsels int     `json:"completed_morsels"`
	CompletionRate float64 `json:"completion_rate"`
	AvgDuration    float64 `json:"avg_duration"`
}

// FailedDispatchDetail contains comprehensive information about a failed dispatch.
type FailedDispatchDetail struct {
	ID              int64      `json:"id"`
	MorselID          string     `json:"morsel_id"`
	Project         string     `json:"project"`
	AgentID         string     `json:"agent_id"`
	Provider        string     `json:"provider"`
	Tier            string     `json:"tier"`
	DispatchedAt    time.Time  `json:"dispatched_at"`
	FailedAt        time.Time  `json:"failed_at"`
	Duration        float64    `json:"duration"`
	ExitCode        int        `json:"exit_code"`
	Retries         int        `json:"retries"`
	EscalatedFrom   string     `json:"escalated_from"`
	FailureCategory string     `json:"failure_category"`
	FailureSummary  string     `json:"failure_summary"`
	LogPath         string     `json:"log_path"`
	Branch          string     `json:"branch"`
	MorselContext     *MorselStage `json:"morsel_context,omitempty"`
}

// StuckDispatchDetail contains information about dispatches that are stuck.
type StuckDispatchDetail struct {
	ID            int64      `json:"id"`
	MorselID        string     `json:"morsel_id"`
	Project       string     `json:"project"`
	AgentID       string     `json:"agent_id"`
	Provider      string     `json:"provider"`
	Tier          string     `json:"tier"`
	DispatchedAt  time.Time  `json:"dispatched_at"`
	StuckDuration float64    `json:"stuck_duration_hours"`
	PID           int        `json:"pid"`
	SessionName   string     `json:"session_name"`
	Stage         string     `json:"stage"`
	MorselContext   *MorselStage `json:"morsel_context,omitempty"`
}

// AgentPerformanceStats contains performance metrics for agents.
type AgentPerformanceStats struct {
	AgentID         string                         `json:"agent_id"`
	TotalDispatches int                            `json:"total_dispatches"`
	Completed       int                            `json:"completed"`
	Failed          int                            `json:"failed"`
	CompletionRate  float64                        `json:"completion_rate"`
	FailureRate     float64                        `json:"failure_rate"`
	AvgDuration     float64                        `json:"avg_duration"`
	TierStats       map[string]TierStat            `json:"tier_stats"`
	ProviderStats   map[string]ProviderPerformance `json:"provider_stats"`
	TokenUsage      TokenUsageStats                `json:"token_usage"`
	CostStats       CostStats                      `json:"cost_stats"`
}

// TierStat holds performance metrics for a specific tier.
type TierStat struct {
	Tier           string  `json:"tier"`
	Total          int     `json:"total"`
	Completed      int     `json:"completed"`
	CompletionRate float64 `json:"completion_rate"`
	AvgDuration    float64 `json:"avg_duration"`
}

// ProviderPerformance holds performance metrics for a specific provider.
type ProviderPerformance struct {
	Provider       string  `json:"provider"`
	Total          int     `json:"total"`
	Completed      int     `json:"completed"`
	CompletionRate float64 `json:"completion_rate"`
	AvgDuration    float64 `json:"avg_duration"`
}

// TokenUsageStats holds token usage statistics.
type TokenUsageStats struct {
	TotalInputTokens  int     `json:"total_input_tokens"`
	TotalOutputTokens int     `json:"total_output_tokens"`
	AvgInputTokens    float64 `json:"avg_input_tokens"`
	AvgOutputTokens   float64 `json:"avg_output_tokens"`
}

// CostStats holds cost statistics.
type CostStats struct {
	TotalCost float64 `json:"total_cost"`
	AvgCost   float64 `json:"avg_cost"`
}

// GetSprintReviewData returns comprehensive data for sprint review comparing planned vs delivered morsels.
func (s *Store) GetSprintReviewData(startDate, endDate time.Time) (*SprintReviewData, error) {
	data := &SprintReviewData{
		StartDate:    startDate,
		EndDate:      endDate,
		ProjectStats: make(map[string]ProjectStat),
	}

	// Get overall morsel and completion statistics
	err := s.db.QueryRow(`
		SELECT 
			COUNT(DISTINCT d.morsel_id) as total_morsels,
			COUNT(DISTINCT CASE WHEN d.status = 'completed' THEN d.morsel_id END) as completed_morsels
		FROM dispatches d
		WHERE d.dispatched_at >= ? AND d.dispatched_at <= ?
	`, startDate.Format(time.DateTime), endDate.Format(time.DateTime)).Scan(
		&data.TotalMorsels, &data.CompletedMorsels)
	if err != nil {
		return nil, fmt.Errorf("get sprint review totals: %w", err)
	}

	// Calculate completion rate
	if data.TotalMorsels > 0 {
		data.CompletionRate = float64(data.CompletedMorsels) / float64(data.TotalMorsels) * 100
	}

	// Get planned morsels from morsel_stages (morsels that were in the system during the sprint)
	err = s.db.QueryRow(`
		SELECT COUNT(DISTINCT morsel_id)
		FROM morsel_stages 
		WHERE created_at <= ? 
		AND (updated_at >= ? OR current_stage != 'completed')
	`, endDate.Format(time.DateTime), startDate.Format(time.DateTime)).Scan(&data.PlannedMorsels)
	if err != nil {
		return nil, fmt.Errorf("get planned morsels count: %w", err)
	}

	// Get per-project statistics
	rows, err := s.db.Query(`
		SELECT 
			d.project,
			COUNT(DISTINCT d.morsel_id) as total_morsels,
			COUNT(DISTINCT CASE WHEN d.status = 'completed' THEN d.morsel_id END) as completed_morsels,
			AVG(CASE WHEN d.status = 'completed' THEN d.duration_s END) as avg_duration
		FROM dispatches d
		WHERE d.dispatched_at >= ? AND d.dispatched_at <= ?
		GROUP BY d.project
	`, startDate.Format(time.DateTime), endDate.Format(time.DateTime))
	if err != nil {
		return nil, fmt.Errorf("get project stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var stat ProjectStat
		var avgDur sql.NullFloat64
		err := rows.Scan(&stat.Project, &stat.TotalMorsels, &stat.CompletedMorsels, &avgDur)
		if err != nil {
			return nil, fmt.Errorf("scan project stat: %w", err)
		}
		if avgDur.Valid {
			stat.AvgDuration = avgDur.Float64
		}
		if stat.TotalMorsels > 0 {
			stat.CompletionRate = float64(stat.CompletedMorsels) / float64(stat.TotalMorsels) * 100
		}
		data.ProjectStats[stat.Project] = stat
	}

	return data, rows.Err()
}

// GetFailedDispatchDetails returns detailed information about failed dispatches within a time window.
func (s *Store) GetFailedDispatchDetails(startDate, endDate time.Time) ([]FailedDispatchDetail, error) {
	rows, err := s.db.Query(`
		SELECT 
			d.id, d.morsel_id, d.project, d.agent_id, d.provider, d.tier,
			d.dispatched_at, d.completed_at, d.duration_s, d.exit_code,
			d.retries, d.escalated_from_tier, d.failure_category, d.failure_summary,
			d.log_path, d.branch,
			bs.workflow, bs.current_stage, bs.stage_index, bs.total_stages, bs.stage_history
		FROM dispatches d
		LEFT JOIN morsel_stages bs ON d.morsel_id = bs.morsel_id AND d.project = bs.project
		WHERE d.status = 'failed' 
		AND d.dispatched_at >= ? 
		AND d.dispatched_at <= ?
		ORDER BY d.dispatched_at DESC
	`, startDate.Format(time.DateTime), endDate.Format(time.DateTime))
	if err != nil {
		return nil, fmt.Errorf("query failed dispatches: %w", err)
	}
	defer rows.Close()

	var details []FailedDispatchDetail
	for rows.Next() {
		var detail FailedDispatchDetail
		var completedAt sql.NullTime
		var workflow, currentStage, stageHistory sql.NullString
		var stageIndex, totalStages sql.NullInt64

		err := rows.Scan(
			&detail.ID, &detail.MorselID, &detail.Project, &detail.AgentID,
			&detail.Provider, &detail.Tier, &detail.DispatchedAt, &completedAt,
			&detail.Duration, &detail.ExitCode, &detail.Retries,
			&detail.EscalatedFrom, &detail.FailureCategory, &detail.FailureSummary,
			&detail.LogPath, &detail.Branch,
			&workflow, &currentStage, &stageIndex, &totalStages, &stageHistory,
		)
		if err != nil {
			return nil, fmt.Errorf("scan failed dispatch detail: %w", err)
		}

		if completedAt.Valid {
			detail.FailedAt = completedAt.Time
		}

		// Add morsel context if available
		if workflow.Valid {
			morselStage := &MorselStage{
				Project:      detail.Project,
				MorselID:       detail.MorselID,
				Workflow:     workflow.String,
				CurrentStage: currentStage.String,
			}
			if stageIndex.Valid {
				morselStage.StageIndex = int(stageIndex.Int64)
			}
			if totalStages.Valid {
				morselStage.TotalStages = int(totalStages.Int64)
			}
			if stageHistory.Valid {
				json.Unmarshal([]byte(stageHistory.String), &morselStage.StageHistory)
			}
			detail.MorselContext = morselStage
		}

		details = append(details, detail)
	}

	return details, rows.Err()
}

// GetStuckDispatchDetails returns information about dispatches that have been running longer than the timeout.
func (s *Store) GetStuckDispatchDetails(timeout time.Duration) ([]StuckDispatchDetail, error) {
	cutoff := time.Now().Add(-timeout)
	rows, err := s.db.Query(`
		SELECT 
			d.id, d.morsel_id, d.project, d.agent_id, d.provider, d.tier,
			d.dispatched_at, d.pid, d.session_name, d.stage,
			bs.workflow, bs.current_stage, bs.stage_index, bs.total_stages, bs.stage_history
		FROM dispatches d
		LEFT JOIN morsel_stages bs ON d.morsel_id = bs.morsel_id AND d.project = bs.project
		WHERE d.status = 'running' 
		AND d.dispatched_at < ?
		ORDER BY d.dispatched_at ASC
	`, cutoff.Format(time.DateTime))
	if err != nil {
		return nil, fmt.Errorf("query stuck dispatches: %w", err)
	}
	defer rows.Close()

	var details []StuckDispatchDetail
	now := time.Now()
	for rows.Next() {
		var detail StuckDispatchDetail
		var workflow, currentStage, stageHistory sql.NullString
		var stageIndex, totalStages sql.NullInt64

		err := rows.Scan(
			&detail.ID, &detail.MorselID, &detail.Project, &detail.AgentID,
			&detail.Provider, &detail.Tier, &detail.DispatchedAt, &detail.PID,
			&detail.SessionName, &detail.Stage,
			&workflow, &currentStage, &stageIndex, &totalStages, &stageHistory,
		)
		if err != nil {
			return nil, fmt.Errorf("scan stuck dispatch detail: %w", err)
		}

		detail.StuckDuration = now.Sub(detail.DispatchedAt).Hours()

		// Add morsel context if available
		if workflow.Valid {
			morselStage := &MorselStage{
				Project:      detail.Project,
				MorselID:       detail.MorselID,
				Workflow:     workflow.String,
				CurrentStage: currentStage.String,
			}
			if stageIndex.Valid {
				morselStage.StageIndex = int(stageIndex.Int64)
			}
			if totalStages.Valid {
				morselStage.TotalStages = int(totalStages.Int64)
			}
			if stageHistory.Valid {
				json.Unmarshal([]byte(stageHistory.String), &morselStage.StageHistory)
			}
			detail.MorselContext = morselStage
		}

		details = append(details, detail)
	}

	return details, rows.Err()
}

// GetAgentPerformanceStats returns comprehensive performance statistics for agents within a time window.
func (s *Store) GetAgentPerformanceStats(startDate, endDate time.Time) (map[string]AgentPerformanceStats, error) {
	// Get basic agent stats
	rows, err := s.db.Query(`
		SELECT 
			agent_id,
			COUNT(*) as total,
			COUNT(CASE WHEN status = 'completed' THEN 1 END) as completed,
			COUNT(CASE WHEN status = 'failed' THEN 1 END) as failed,
			AVG(CASE WHEN status = 'completed' THEN duration_s END) as avg_duration,
			SUM(input_tokens) as total_input_tokens,
			SUM(output_tokens) as total_output_tokens,
			SUM(cost_usd) as total_cost
		FROM dispatches
		WHERE dispatched_at >= ? AND dispatched_at <= ?
		GROUP BY agent_id
	`, startDate.Format(time.DateTime), endDate.Format(time.DateTime))
	if err != nil {
		return nil, fmt.Errorf("query agent performance stats: %w", err)
	}
	defer rows.Close()

	stats := make(map[string]AgentPerformanceStats)
	for rows.Next() {
		var agentStats AgentPerformanceStats
		var avgDur sql.NullFloat64
		var totalCost sql.NullFloat64

		err := rows.Scan(
			&agentStats.AgentID, &agentStats.TotalDispatches, &agentStats.Completed,
			&agentStats.Failed, &avgDur, &agentStats.TokenUsage.TotalInputTokens,
			&agentStats.TokenUsage.TotalOutputTokens, &totalCost,
		)
		if err != nil {
			return nil, fmt.Errorf("scan agent stats: %w", err)
		}

		if avgDur.Valid {
			agentStats.AvgDuration = avgDur.Float64
		}
		if totalCost.Valid {
			agentStats.CostStats.TotalCost = totalCost.Float64
		}

		// Calculate rates
		if agentStats.TotalDispatches > 0 {
			agentStats.CompletionRate = float64(agentStats.Completed) / float64(agentStats.TotalDispatches) * 100
			agentStats.FailureRate = float64(agentStats.Failed) / float64(agentStats.TotalDispatches) * 100
			agentStats.TokenUsage.AvgInputTokens = float64(agentStats.TokenUsage.TotalInputTokens) / float64(agentStats.TotalDispatches)
			agentStats.TokenUsage.AvgOutputTokens = float64(agentStats.TokenUsage.TotalOutputTokens) / float64(agentStats.TotalDispatches)
			agentStats.CostStats.AvgCost = agentStats.CostStats.TotalCost / float64(agentStats.TotalDispatches)
		}

		agentStats.TierStats = make(map[string]TierStat)
		agentStats.ProviderStats = make(map[string]ProviderPerformance)
		stats[agentStats.AgentID] = agentStats
	}

	// Get per-tier stats for each agent
	for agentID := range stats {
		agentStats := stats[agentID]

		// Tier stats
		tierRows, err := s.db.Query(`
			SELECT 
				tier,
				COUNT(*) as total,
				COUNT(CASE WHEN status = 'completed' THEN 1 END) as completed,
				AVG(CASE WHEN status = 'completed' THEN duration_s END) as avg_duration
			FROM dispatches
			WHERE agent_id = ? AND dispatched_at >= ? AND dispatched_at <= ?
			GROUP BY tier
		`, agentID, startDate.Format(time.DateTime), endDate.Format(time.DateTime))
		if err != nil {
			continue
		}

		for tierRows.Next() {
			var tierStat TierStat
			var avgDur sql.NullFloat64
			err := tierRows.Scan(&tierStat.Tier, &tierStat.Total, &tierStat.Completed, &avgDur)
			if err != nil {
				continue
			}
			if avgDur.Valid {
				tierStat.AvgDuration = avgDur.Float64
			}
			if tierStat.Total > 0 {
				tierStat.CompletionRate = float64(tierStat.Completed) / float64(tierStat.Total) * 100
			}
			agentStats.TierStats[tierStat.Tier] = tierStat
		}
		tierRows.Close()

		// Provider stats
		providerRows, err := s.db.Query(`
			SELECT 
				provider,
				COUNT(*) as total,
				COUNT(CASE WHEN status = 'completed' THEN 1 END) as completed,
				AVG(CASE WHEN status = 'completed' THEN duration_s END) as avg_duration
			FROM dispatches
			WHERE agent_id = ? AND dispatched_at >= ? AND dispatched_at <= ?
			GROUP BY provider
		`, agentID, startDate.Format(time.DateTime), endDate.Format(time.DateTime))
		if err != nil {
			continue
		}

		for providerRows.Next() {
			var providerPerf ProviderPerformance
			var avgDur sql.NullFloat64
			err := providerRows.Scan(&providerPerf.Provider, &providerPerf.Total, &providerPerf.Completed, &avgDur)
			if err != nil {
				continue
			}
			if avgDur.Valid {
				providerPerf.AvgDuration = avgDur.Float64
			}
			if providerPerf.Total > 0 {
				providerPerf.CompletionRate = float64(providerPerf.Completed) / float64(providerPerf.Total) * 100
			}
			agentStats.ProviderStats[providerPerf.Provider] = providerPerf
		}
		providerRows.Close()

		stats[agentID] = agentStats
	}

	return stats, nil
}
