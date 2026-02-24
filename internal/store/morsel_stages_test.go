package store

import (
	"strings"
	"testing"
)

func TestMorselStageProjectIsolation(t *testing.T) {
	s := tempStore(t)

	// Create identical morsel IDs in different projects
	stage1 := &MorselStage{
		Project:      "project-a",
		MorselID:     "morsel-123",
		Workflow:     "dev-workflow",
		CurrentStage: "coding",
		StageIndex:   1,
		TotalStages:  3,
	}

	stage2 := &MorselStage{
		Project:      "project-b",
		MorselID:     "morsel-123", // Same morsel ID, different project
		Workflow:     "content-workflow",
		CurrentStage: "review",
		StageIndex:   2,
		TotalStages:  4,
	}

	// Upsert both stages - should not conflict
	if err := s.UpsertMorselStage(stage1); err != nil {
		t.Fatalf("Failed to upsert stage1: %v", err)
	}

	if err := s.UpsertMorselStage(stage2); err != nil {
		t.Fatalf("Failed to upsert stage2: %v", err)
	}

	// Retrieve each stage separately - should get correct one
	retrieved1, err := s.GetMorselStage("project-a", "morsel-123")
	if err != nil {
		t.Fatalf("Failed to get stage for project-a: %v", err)
	}

	if retrieved1.Workflow != "dev-workflow" {
		t.Errorf("Expected dev-workflow, got %s", retrieved1.Workflow)
	}
	if retrieved1.CurrentStage != "coding" {
		t.Errorf("Expected coding, got %s", retrieved1.CurrentStage)
	}

	retrieved2, err := s.GetMorselStage("project-b", "morsel-123")
	if err != nil {
		t.Fatalf("Failed to get stage for project-b: %v", err)
	}

	if retrieved2.Workflow != "content-workflow" {
		t.Errorf("Expected content-workflow, got %s", retrieved2.Workflow)
	}
	if retrieved2.CurrentStage != "review" {
		t.Errorf("Expected review, got %s", retrieved2.CurrentStage)
	}
}

func TestMorselStageUpsertConflictResolution(t *testing.T) {
	s := tempStore(t)

	stage := &MorselStage{
		Project:      "test-project",
		MorselID:     "morsel-456",
		Workflow:     "initial-workflow",
		CurrentStage: "start",
		StageIndex:   0,
		TotalStages:  2,
	}

	// First upsert
	if err := s.UpsertMorselStage(stage); err != nil {
		t.Fatalf("Failed to upsert initial stage: %v", err)
	}

	// Update the stage and upsert again
	stage.Workflow = "updated-workflow"
	stage.CurrentStage = "finish"
	stage.StageIndex = 1

	if err := s.UpsertMorselStage(stage); err != nil {
		t.Fatalf("Failed to upsert updated stage: %v", err)
	}

	// Verify the update
	retrieved, err := s.GetMorselStage("test-project", "morsel-456")
	if err != nil {
		t.Fatalf("Failed to retrieve updated stage: %v", err)
	}

	if retrieved.Workflow != "updated-workflow" {
		t.Errorf("Expected updated-workflow, got %s", retrieved.Workflow)
	}
	if retrieved.CurrentStage != "finish" {
		t.Errorf("Expected finish, got %s", retrieved.CurrentStage)
	}
	if retrieved.StageIndex != 1 {
		t.Errorf("Expected stage index 1, got %d", retrieved.StageIndex)
	}
}

func TestMorselStageAmbiguityDetection(t *testing.T) {
	s := tempStore(t)

	// Create the same morsel ID in multiple projects
	stage1 := &MorselStage{
		Project:      "project-alpha",
		MorselID:     "shared-morsel",
		Workflow:     "alpha-workflow",
		CurrentStage: "alpha-stage",
		StageIndex:   1,
		TotalStages:  3,
	}

	stage2 := &MorselStage{
		Project:      "project-beta",
		MorselID:     "shared-morsel",
		Workflow:     "beta-workflow",
		CurrentStage: "beta-stage",
		StageIndex:   2,
		TotalStages:  4,
	}

	// Insert both stages
	if err := s.UpsertMorselStage(stage1); err != nil {
		t.Fatalf("Failed to upsert stage1: %v", err)
	}

	if err := s.UpsertMorselStage(stage2); err != nil {
		t.Fatalf("Failed to upsert stage2: %v", err)
	}

	// Attempt morsel-only lookup should detect ambiguity
	_, err := s.GetMorselStagesByMorselIDOnly("shared-morsel")
	if err == nil {
		t.Fatal("Expected ambiguity error, but got none")
	}

	expectedErrText := "ambiguous morsel_id=shared-morsel found in multiple projects"
	if !contains(err.Error(), expectedErrText) {
		t.Errorf("Expected error to contain '%s', got: %s", expectedErrText, err.Error())
	}
}

func TestMorselStageNonAmbiguousLookup(t *testing.T) {
	s := tempStore(t)

	// Create a morsel ID that exists in only one project
	stage := &MorselStage{
		Project:      "single-project",
		MorselID:     "unique-morsel",
		Workflow:     "unique-workflow",
		CurrentStage: "unique-stage",
		StageIndex:   1,
		TotalStages:  2,
	}

	if err := s.UpsertMorselStage(stage); err != nil {
		t.Fatalf("Failed to upsert stage: %v", err)
	}

	// Morsel-only lookup should succeed when there's no ambiguity
	stages, err := s.GetMorselStagesByMorselIDOnly("unique-morsel")
	if err != nil {
		t.Fatalf("Unexpected error for non-ambiguous lookup: %v", err)
	}

	if len(stages) != 1 {
		t.Fatalf("Expected 1 stage, got %d", len(stages))
	}

	if stages[0].Project != "single-project" {
		t.Errorf("Expected single-project, got %s", stages[0].Project)
	}
}

func TestMorselStageListByProject(t *testing.T) {
	s := tempStore(t)

	// Create multiple morsels in the same project
	stages := []*MorselStage{
		{
			Project:      "list-project",
			MorselID:     "morsel-1",
			Workflow:     "workflow-1",
			CurrentStage: "stage-1",
			StageIndex:   0,
			TotalStages:  2,
		},
		{
			Project:      "list-project",
			MorselID:     "morsel-2",
			Workflow:     "workflow-2",
			CurrentStage: "stage-2",
			StageIndex:   1,
			TotalStages:  3,
		},
		{
			Project:      "other-project", // Different project
			MorselID:     "morsel-3",
			Workflow:     "workflow-3",
			CurrentStage: "stage-3",
			StageIndex:   0,
			TotalStages:  1,
		},
	}

	for _, stage := range stages {
		if err := s.UpsertMorselStage(stage); err != nil {
			t.Fatalf("Failed to upsert stage %s: %v", stage.MorselID, err)
		}
	}

	// List stages for specific project
	projectStages, err := s.ListMorselStagesForProject("list-project")
	if err != nil {
		t.Fatalf("Failed to list stages for project: %v", err)
	}

	if len(projectStages) != 2 {
		t.Fatalf("Expected 2 stages for list-project, got %d", len(projectStages))
	}

	// Verify only stages from the requested project are returned
	for _, stage := range projectStages {
		if stage.Project != "list-project" {
			t.Errorf("Unexpected project in results: %s", stage.Project)
		}
	}

	// Verify morsel IDs are correct
	morselIDs := make([]string, len(projectStages))
	for i, stage := range projectStages {
		morselIDs[i] = stage.MorselID
	}

	if !containsAll(morselIDs, []string{"morsel-1", "morsel-2"}) {
		t.Errorf("Expected morsel-1 and morsel-2, got %v", morselIDs)
	}
}

func TestMorselStageUpdateProgress(t *testing.T) {
	s := tempStore(t)

	stage := &MorselStage{
		Project:      "progress-project",
		MorselID:     "progress-morsel",
		Workflow:     "progress-workflow",
		CurrentStage: "initial",
		StageIndex:   0,
		TotalStages:  3,
	}

	if err := s.UpsertMorselStage(stage); err != nil {
		t.Fatalf("Failed to upsert initial stage: %v", err)
	}

	// Update progress
	dispatchID := int64(12345)
	if err := s.UpdateMorselStageProgress("progress-project", "progress-morsel", "middle", 1, 3, dispatchID); err != nil {
		t.Fatalf("Failed to update progress: %v", err)
	}

	// Verify the update
	updated, err := s.GetMorselStage("progress-project", "progress-morsel")
	if err != nil {
		t.Fatalf("Failed to retrieve updated stage: %v", err)
	}

	if updated.CurrentStage != "middle" {
		t.Errorf("Expected middle, got %s", updated.CurrentStage)
	}
	if updated.StageIndex != 1 {
		t.Errorf("Expected stage index 1, got %d", updated.StageIndex)
	}
}

func TestMorselStageDelete(t *testing.T) {
	s := tempStore(t)

	stage := &MorselStage{
		Project:      "delete-project",
		MorselID:     "delete-morsel",
		Workflow:     "delete-workflow",
		CurrentStage: "delete-stage",
		StageIndex:   0,
		TotalStages:  1,
	}

	if err := s.UpsertMorselStage(stage); err != nil {
		t.Fatalf("Failed to upsert stage: %v", err)
	}

	// Verify it exists
	_, err := s.GetMorselStage("delete-project", "delete-morsel")
	if err != nil {
		t.Fatalf("Stage should exist before deletion: %v", err)
	}

	// Delete it
	if err := s.DeleteMorselStage("delete-project", "delete-morsel"); err != nil {
		t.Fatalf("Failed to delete stage: %v", err)
	}

	// Verify it's gone
	_, err = s.GetMorselStage("delete-project", "delete-morsel")
	if err == nil {
		t.Fatal("Stage should not exist after deletion")
	}

	expectedErrText := "morsel stage not found"
	if !contains(err.Error(), expectedErrText) {
		t.Errorf("Expected error to contain '%s', got: %s", expectedErrText, err.Error())
	}
}

func TestMorselStageDeleteNonExistent(t *testing.T) {
	s := tempStore(t)

	// Try to delete a non-existent stage
	err := s.DeleteMorselStage("nonexistent-project", "nonexistent-morsel")
	if err == nil {
		t.Fatal("Expected error when deleting non-existent stage")
	}

	expectedErrText := "morsel stage not found"
	if !contains(err.Error(), expectedErrText) {
		t.Errorf("Expected error to contain '%s', got: %s", expectedErrText, err.Error())
	}
}

// Helper functions for testing

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func containsAll(slice []string, items []string) bool {
	found := make(map[string]bool)
	for _, item := range slice {
		found[item] = true
	}

	for _, item := range items {
		if !found[item] {
			return false
		}
	}
	return true
}
