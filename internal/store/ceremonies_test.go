package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestGetSprintReviewData(t *testing.T) {
	// Create a temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_sprint_review.db")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open store: %v", err)
	}
	defer store.Close()

	// Set up test data
	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 7, 23, 59, 59, 0, time.UTC)

	// Insert test dispatches
	id1, err := store.RecordDispatch("morsel-1", "test-project", "agent-1", "openai", "fast", 0, "session1", "Test prompt 1", "/logs/morsel1.log", "main", "openclaw")
	if err != nil {
		t.Fatalf("Failed to record dispatch1: %v", err)
	}
	// Update dispatch time to be within test range
	_, err = store.DB().Exec("UPDATE dispatches SET dispatched_at = ? WHERE id = ?",
		time.Date(2024, 1, 3, 12, 0, 0, 0, time.UTC).Format(time.DateTime), id1)
	if err != nil {
		t.Fatalf("Failed to update dispatch1 time: %v", err)
	}
	// Update to completed status
	if err := store.UpdateDispatchStatus(id1, "completed", 0, 300); err != nil {
		t.Fatalf("Failed to update dispatch1 status: %v", err)
	}

	id2, err := store.RecordDispatch("morsel-2", "test-project", "agent-1", "anthropic", "premium", 0, "session2", "Test prompt 2", "/logs/morsel2.log", "main", "openclaw")
	if err != nil {
		t.Fatalf("Failed to record dispatch2: %v", err)
	}
	// Update dispatch time to be within test range
	_, err = store.DB().Exec("UPDATE dispatches SET dispatched_at = ? WHERE id = ?",
		time.Date(2024, 1, 4, 10, 0, 0, 0, time.UTC).Format(time.DateTime), id2)
	if err != nil {
		t.Fatalf("Failed to update dispatch2 time: %v", err)
	}
	// Update to completed status
	if err := store.UpdateDispatchStatus(id2, "completed", 0, 600); err != nil {
		t.Fatalf("Failed to update dispatch2 status: %v", err)
	}

	id3, err := store.RecordDispatch("morsel-3", "another-project", "agent-2", "openai", "fast", 0, "session3", "Test prompt 3", "/logs/morsel3.log", "main", "openclaw")
	if err != nil {
		t.Fatalf("Failed to record dispatch3: %v", err)
	}
	// Update dispatch time to be within test range
	_, err = store.DB().Exec("UPDATE dispatches SET dispatched_at = ? WHERE id = ?",
		time.Date(2024, 1, 5, 14, 0, 0, 0, time.UTC).Format(time.DateTime), id3)
	if err != nil {
		t.Fatalf("Failed to update dispatch3 time: %v", err)
	}
	// Update to failed status
	if err := store.UpdateDispatchStatus(id3, "failed", 1, 0); err != nil {
		t.Fatalf("Failed to update dispatch3 status: %v", err)
	}

	// Insert some morsel stages for planned morsels
	stage1 := &MorselStage{
		Project:      "test-project",
		MorselID:     "morsel-1",
		Workflow:     "standard",
		CurrentStage: "completed",
		StageIndex:   2,
		TotalStages:  3,
	}

	stage2 := &MorselStage{
		Project:      "test-project",
		MorselID:     "morsel-2",
		Workflow:     "standard",
		CurrentStage: "completed",
		StageIndex:   2,
		TotalStages:  3,
	}

	if err := store.UpsertMorselStage(stage1); err != nil {
		t.Fatalf("Failed to upsert morsel stage 1: %v", err)
	}
	if err := store.UpsertMorselStage(stage2); err != nil {
		t.Fatalf("Failed to upsert morsel stage 2: %v", err)
	}

	// Get sprint review data
	data, err := store.GetSprintReviewData(startDate, endDate)
	if err != nil {
		t.Fatalf("Failed to get sprint review data: %v", err)
	}

	// Verify results
	if data.TotalMorsels != 3 {
		t.Errorf("Expected TotalMorsels = 3, got %d", data.TotalMorsels)
	}
	if data.CompletedMorsels != 2 {
		t.Errorf("Expected CompletedMorsels = 2, got %d", data.CompletedMorsels)
	}
	if data.CompletionRate != 66.66666666666666 {
		t.Errorf("Expected CompletionRate = 66.67, got %f", data.CompletionRate)
	}

	// Check project stats
	if len(data.ProjectStats) != 2 {
		t.Errorf("Expected 2 project stats, got %d", len(data.ProjectStats))
	}

	testProjectStat, exists := data.ProjectStats["test-project"]
	if !exists {
		t.Error("Expected test-project in project stats")
	} else {
		if testProjectStat.CompletedMorsels != 2 {
			t.Errorf("Expected test-project CompletedMorsels = 2, got %d", testProjectStat.CompletedMorsels)
		}
		if testProjectStat.CompletionRate != 100.0 {
			t.Errorf("Expected test-project CompletionRate = 100.0, got %f", testProjectStat.CompletionRate)
		}
	}
}

func TestGetFailedDispatchDetails(t *testing.T) {
	// Create a temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_failed_dispatches.db")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open store: %v", err)
	}
	defer store.Close()

	// Set up test data
	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 7, 23, 59, 59, 0, time.UTC)

	// Insert failed dispatch
	failedID, err := store.RecordDispatch("failed-morsel", "test-project", "agent-1", "openai", "fast", 0, "failed-session", "Test failed prompt", "/logs/failed-morsel.log", "main", "openclaw")
	if err != nil {
		t.Fatalf("Failed to record failed dispatch: %v", err)
	}
	// Update dispatch time to be within test range
	_, err = store.DB().Exec("UPDATE dispatches SET dispatched_at = ? WHERE id = ?",
		time.Date(2024, 1, 3, 12, 0, 0, 0, time.UTC).Format(time.DateTime), failedID)
	if err != nil {
		t.Fatalf("Failed to update failed dispatch time: %v", err)
	}
	// Update to failed status
	if err := store.UpdateDispatchStatus(failedID, "failed", 1, 300); err != nil {
		t.Fatalf("Failed to update failed dispatch status: %v", err)
	}
	// Update failure diagnosis
	if err := store.UpdateFailureDiagnosis(failedID, "timeout", "Task timed out after 5 minutes"); err != nil {
		t.Fatalf("Failed to update failure diagnosis: %v", err)
	}

	// Insert morsel stage for context
	stage := &MorselStage{
		Project:      "test-project",
		MorselID:     "failed-morsel",
		Workflow:     "standard",
		CurrentStage: "failed",
		StageIndex:   1,
		TotalStages:  3,
	}

	if err := store.UpsertMorselStage(stage); err != nil {
		t.Fatalf("Failed to upsert morsel stage: %v", err)
	}

	// Get failed dispatch details
	details, err := store.GetFailedDispatchDetails(startDate, endDate)
	if err != nil {
		t.Fatalf("Failed to get failed dispatch details: %v", err)
	}

	// Verify results
	if len(details) != 1 {
		t.Fatalf("Expected 1 failed dispatch, got %d", len(details))
	}

	detail := details[0]
	if detail.MorselID != "failed-morsel" {
		t.Errorf("Expected MorselID = failed-morsel, got %s", detail.MorselID)
	}
	if detail.FailureCategory != "timeout" {
		t.Errorf("Expected FailureCategory = timeout, got %s", detail.FailureCategory)
	}
	if detail.FailureSummary != "Task timed out after 5 minutes" {
		t.Errorf("Expected FailureSummary = 'Task timed out after 5 minutes', got %s", detail.FailureSummary)
	}
	if detail.MorselContext == nil {
		t.Error("Expected morsel context to be present")
	} else {
		if detail.MorselContext.CurrentStage != "failed" {
			t.Errorf("Expected morsel context CurrentStage = failed, got %s", detail.MorselContext.CurrentStage)
		}
	}
}

func TestGetStuckDispatchDetails(t *testing.T) {
	// Create a temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_stuck_dispatches.db")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open store: %v", err)
	}
	defer store.Close()

	// Set up test data - dispatch that started 3 hours ago
	stuckDispatchTime := time.Now().Add(-3 * time.Hour)

	// Insert stuck dispatch (still running)
	stuckID, err := store.RecordDispatch("stuck-morsel", "test-project", "agent-1", "openai", "premium", 12345, "test-session", "Test stuck prompt", "/logs/stuck-morsel.log", "main", "openclaw")
	if err != nil {
		t.Fatalf("Failed to record stuck dispatch: %v", err)
	}
	// Leave it in running status (don't call UpdateDispatchStatus)

	// Manually update the dispatched_at time to be 3 hours ago
	_, err = store.DB().Exec("UPDATE dispatches SET dispatched_at = ? WHERE id = ?",
		stuckDispatchTime.UTC().Format(time.DateTime), stuckID)
	if err != nil {
		t.Fatalf("Failed to update stuck dispatch time: %v", err)
	}

	// Insert morsel stage for context
	stage := &MorselStage{
		Project:      "test-project",
		MorselID:     "stuck-morsel",
		Workflow:     "standard",
		CurrentStage: "running",
		StageIndex:   1,
		TotalStages:  3,
	}

	if err := store.UpsertMorselStage(stage); err != nil {
		t.Fatalf("Failed to upsert morsel stage: %v", err)
	}

	// Get stuck dispatch details with 2-hour timeout (should catch our 3-hour old dispatch)
	timeout := 2 * time.Hour
	details, err := store.GetStuckDispatchDetails(timeout)
	if err != nil {
		t.Fatalf("Failed to get stuck dispatch details: %v", err)
	}

	// Verify results
	if len(details) != 1 {
		t.Fatalf("Expected 1 stuck dispatch, got %d", len(details))
	}

	detail := details[0]
	if detail.MorselID != "stuck-morsel" {
		t.Errorf("Expected MorselID = stuck-morsel, got %s", detail.MorselID)
	}
	if detail.PID != 12345 {
		t.Errorf("Expected PID = 12345, got %d", detail.PID)
	}
	if detail.StuckDuration < 2.9 || detail.StuckDuration > 3.1 {
		t.Errorf("Expected StuckDuration around 3 hours, got %f", detail.StuckDuration)
	}
	if detail.MorselContext == nil {
		t.Error("Expected morsel context to be present")
	} else {
		if detail.MorselContext.CurrentStage != "running" {
			t.Errorf("Expected morsel context CurrentStage = running, got %s", detail.MorselContext.CurrentStage)
		}
	}
}

func TestGetAgentPerformanceStats(t *testing.T) {
	// Create a temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test_agent_performance.db")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open store: %v", err)
	}
	defer store.Close()

	// Set up test data
	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 7, 23, 59, 59, 0, time.UTC)

	// Insert multiple dispatches for agent performance testing
	// Dispatch 1 - completed
	id1, err := store.RecordDispatch("morsel-1", "test-project", "agent-1", "openai", "fast", 0, "session1", "Test prompt 1", "/logs/morsel1.log", "main", "openclaw")
	if err != nil {
		t.Fatalf("Failed to record dispatch 1: %v", err)
	}
	// Update dispatch time to be within test range
	_, err = store.DB().Exec("UPDATE dispatches SET dispatched_at = ? WHERE id = ?",
		time.Date(2024, 1, 3, 12, 0, 0, 0, time.UTC).Format(time.DateTime), id1)
	if err != nil {
		t.Fatalf("Failed to update dispatch 1 time: %v", err)
	}
	if err := store.UpdateDispatchStatus(id1, "completed", 0, 300); err != nil {
		t.Fatalf("Failed to update dispatch 1 status: %v", err)
	}
	if err := store.RecordDispatchCost(id1, 1000, 500, 0.05); err != nil {
		t.Fatalf("Failed to record dispatch 1 cost: %v", err)
	}

	// Dispatch 2 - completed
	id2, err := store.RecordDispatch("morsel-2", "test-project", "agent-1", "anthropic", "premium", 0, "session2", "Test prompt 2", "/logs/morsel2.log", "main", "openclaw")
	if err != nil {
		t.Fatalf("Failed to record dispatch 2: %v", err)
	}
	// Update dispatch time to be within test range
	_, err = store.DB().Exec("UPDATE dispatches SET dispatched_at = ? WHERE id = ?",
		time.Date(2024, 1, 4, 10, 0, 0, 0, time.UTC).Format(time.DateTime), id2)
	if err != nil {
		t.Fatalf("Failed to update dispatch 2 time: %v", err)
	}
	if err := store.UpdateDispatchStatus(id2, "completed", 0, 600); err != nil {
		t.Fatalf("Failed to update dispatch 2 status: %v", err)
	}
	if err := store.RecordDispatchCost(id2, 2000, 1000, 0.10); err != nil {
		t.Fatalf("Failed to record dispatch 2 cost: %v", err)
	}

	// Dispatch 3 - failed
	id3, err := store.RecordDispatch("morsel-3", "test-project", "agent-1", "openai", "fast", 0, "session3", "Test prompt 3", "/logs/morsel3.log", "main", "openclaw")
	if err != nil {
		t.Fatalf("Failed to record dispatch 3: %v", err)
	}
	// Update dispatch time to be within test range
	_, err = store.DB().Exec("UPDATE dispatches SET dispatched_at = ? WHERE id = ?",
		time.Date(2024, 1, 5, 14, 0, 0, 0, time.UTC).Format(time.DateTime), id3)
	if err != nil {
		t.Fatalf("Failed to update dispatch 3 time: %v", err)
	}
	if err := store.UpdateDispatchStatus(id3, "failed", 1, 0); err != nil {
		t.Fatalf("Failed to update dispatch 3 status: %v", err)
	}
	if err := store.RecordDispatchCost(id3, 500, 0, 0.01); err != nil {
		t.Fatalf("Failed to record dispatch 3 cost: %v", err)
	}

	// Get agent performance stats
	stats, err := store.GetAgentPerformanceStats(startDate, endDate)
	if err != nil {
		t.Fatalf("Failed to get agent performance stats: %v", err)
	}

	// Verify results
	if len(stats) != 1 {
		t.Fatalf("Expected 1 agent in stats, got %d", len(stats))
	}

	agentStats, exists := stats["agent-1"]
	if !exists {
		t.Fatal("Expected agent-1 in stats")
	}

	if agentStats.TotalDispatches != 3 {
		t.Errorf("Expected TotalDispatches = 3, got %d", agentStats.TotalDispatches)
	}
	if agentStats.Completed != 2 {
		t.Errorf("Expected Completed = 2, got %d", agentStats.Completed)
	}
	if agentStats.Failed != 1 {
		t.Errorf("Expected Failed = 1, got %d", agentStats.Failed)
	}
	if agentStats.CompletionRate != 66.66666666666666 {
		t.Errorf("Expected CompletionRate = 66.67, got %f", agentStats.CompletionRate)
	}
	expectedFailureRate := 33.333333333333336
	if agentStats.FailureRate < expectedFailureRate-0.001 || agentStats.FailureRate > expectedFailureRate+0.001 {
		t.Errorf("Expected FailureRate ~= 33.33, got %f", agentStats.FailureRate)
	}

	// Check token usage
	if agentStats.TokenUsage.TotalInputTokens != 3500 {
		t.Errorf("Expected TotalInputTokens = 3500, got %d", agentStats.TokenUsage.TotalInputTokens)
	}
	if agentStats.TokenUsage.TotalOutputTokens != 1500 {
		t.Errorf("Expected TotalOutputTokens = 1500, got %d", agentStats.TokenUsage.TotalOutputTokens)
	}

	// Check cost stats
	if agentStats.CostStats.TotalCost != 0.16 {
		t.Errorf("Expected TotalCost = 0.16, got %f", agentStats.CostStats.TotalCost)
	}

	// Check tier stats
	if len(agentStats.TierStats) != 2 {
		t.Errorf("Expected 2 tier stats, got %d", len(agentStats.TierStats))
	}

	fastTier, exists := agentStats.TierStats["fast"]
	if !exists {
		t.Error("Expected fast tier in stats")
	} else {
		if fastTier.Total != 2 {
			t.Errorf("Expected fast tier Total = 2, got %d", fastTier.Total)
		}
		if fastTier.Completed != 1 {
			t.Errorf("Expected fast tier Completed = 1, got %d", fastTier.Completed)
		}
		if fastTier.CompletionRate != 50.0 {
			t.Errorf("Expected fast tier CompletionRate = 50.0, got %f", fastTier.CompletionRate)
		}
	}

	// Check provider stats
	if len(agentStats.ProviderStats) != 2 {
		t.Errorf("Expected 2 provider stats, got %d", len(agentStats.ProviderStats))
	}

	openaiProvider, exists := agentStats.ProviderStats["openai"]
	if !exists {
		t.Error("Expected openai provider in stats")
	} else {
		if openaiProvider.Total != 2 {
			t.Errorf("Expected openai Total = 2, got %d", openaiProvider.Total)
		}
		if openaiProvider.Completed != 1 {
			t.Errorf("Expected openai Completed = 1, got %d", openaiProvider.Completed)
		}
		if openaiProvider.CompletionRate != 50.0 {
			t.Errorf("Expected openai CompletionRate = 50.0, got %f", openaiProvider.CompletionRate)
		}
	}
}
