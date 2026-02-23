package store

import (
	"strings"
	"testing"
)

func TestBeadStageProjectIsolation(t *testing.T) {
	s := tempStore(t)

	// Create identical bead IDs in different projects
	stage1 := &BeadStage{
		Project:      "project-a",
		BeadID:       "bead-123",
		Workflow:     "dev-workflow",
		CurrentStage: "coding",
		StageIndex:   1,
		TotalStages:  3,
	}

	stage2 := &BeadStage{
		Project:      "project-b",
		BeadID:       "bead-123", // Same bead ID, different project
		Workflow:     "content-workflow",
		CurrentStage: "review",
		StageIndex:   2,
		TotalStages:  4,
	}

	// Upsert both stages - should not conflict
	if err := s.UpsertBeadStage(stage1); err != nil {
		t.Fatalf("Failed to upsert stage1: %v", err)
	}

	if err := s.UpsertBeadStage(stage2); err != nil {
		t.Fatalf("Failed to upsert stage2: %v", err)
	}

	// Retrieve each stage separately - should get correct one
	retrieved1, err := s.GetBeadStage("project-a", "bead-123")
	if err != nil {
		t.Fatalf("Failed to get stage for project-a: %v", err)
	}

	if retrieved1.Workflow != "dev-workflow" {
		t.Errorf("Expected dev-workflow, got %s", retrieved1.Workflow)
	}
	if retrieved1.CurrentStage != "coding" {
		t.Errorf("Expected coding, got %s", retrieved1.CurrentStage)
	}

	retrieved2, err := s.GetBeadStage("project-b", "bead-123")
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

func TestBeadStageUpsertConflictResolution(t *testing.T) {
	s := tempStore(t)

	stage := &BeadStage{
		Project:      "test-project",
		BeadID:       "bead-456",
		Workflow:     "initial-workflow",
		CurrentStage: "start",
		StageIndex:   0,
		TotalStages:  2,
	}

	// First upsert
	if err := s.UpsertBeadStage(stage); err != nil {
		t.Fatalf("Failed to upsert initial stage: %v", err)
	}

	// Update the stage and upsert again
	stage.Workflow = "updated-workflow"
	stage.CurrentStage = "finish"
	stage.StageIndex = 1

	if err := s.UpsertBeadStage(stage); err != nil {
		t.Fatalf("Failed to upsert updated stage: %v", err)
	}

	// Verify the update
	retrieved, err := s.GetBeadStage("test-project", "bead-456")
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

func TestBeadStageAmbiguityDetection(t *testing.T) {
	s := tempStore(t)

	// Create the same bead ID in multiple projects
	stage1 := &BeadStage{
		Project:      "project-alpha",
		BeadID:       "shared-bead",
		Workflow:     "alpha-workflow",
		CurrentStage: "alpha-stage",
		StageIndex:   1,
		TotalStages:  3,
	}

	stage2 := &BeadStage{
		Project:      "project-beta",
		BeadID:       "shared-bead",
		Workflow:     "beta-workflow",
		CurrentStage: "beta-stage",
		StageIndex:   2,
		TotalStages:  4,
	}

	// Insert both stages
	if err := s.UpsertBeadStage(stage1); err != nil {
		t.Fatalf("Failed to upsert stage1: %v", err)
	}

	if err := s.UpsertBeadStage(stage2); err != nil {
		t.Fatalf("Failed to upsert stage2: %v", err)
	}

	// Attempt bead-only lookup should detect ambiguity
	_, err := s.GetBeadStagesByBeadIDOnly("shared-bead")
	if err == nil {
		t.Fatal("Expected ambiguity error, but got none")
	}

	expectedErrText := "ambiguous bead_id=shared-bead found in multiple projects"
	if !contains(err.Error(), expectedErrText) {
		t.Errorf("Expected error to contain '%s', got: %s", expectedErrText, err.Error())
	}
}

func TestBeadStageNonAmbiguousLookup(t *testing.T) {
	s := tempStore(t)

	// Create a bead ID that exists in only one project
	stage := &BeadStage{
		Project:      "single-project",
		BeadID:       "unique-bead",
		Workflow:     "unique-workflow",
		CurrentStage: "unique-stage",
		StageIndex:   1,
		TotalStages:  2,
	}

	if err := s.UpsertBeadStage(stage); err != nil {
		t.Fatalf("Failed to upsert stage: %v", err)
	}

	// Bead-only lookup should succeed when there's no ambiguity
	stages, err := s.GetBeadStagesByBeadIDOnly("unique-bead")
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

func TestBeadStageListByProject(t *testing.T) {
	s := tempStore(t)

	// Create multiple beads in the same project
	stages := []*BeadStage{
		{
			Project:      "list-project",
			BeadID:       "bead-1",
			Workflow:     "workflow-1",
			CurrentStage: "stage-1",
			StageIndex:   0,
			TotalStages:  2,
		},
		{
			Project:      "list-project",
			BeadID:       "bead-2",
			Workflow:     "workflow-2",
			CurrentStage: "stage-2",
			StageIndex:   1,
			TotalStages:  3,
		},
		{
			Project:      "other-project", // Different project
			BeadID:       "bead-3",
			Workflow:     "workflow-3",
			CurrentStage: "stage-3",
			StageIndex:   0,
			TotalStages:  1,
		},
	}

	for _, stage := range stages {
		if err := s.UpsertBeadStage(stage); err != nil {
			t.Fatalf("Failed to upsert stage %s: %v", stage.BeadID, err)
		}
	}

	// List stages for specific project
	projectStages, err := s.ListBeadStagesForProject("list-project")
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

	// Verify bead IDs are correct
	beadIDs := make([]string, len(projectStages))
	for i, stage := range projectStages {
		beadIDs[i] = stage.BeadID
	}

	if !containsAll(beadIDs, []string{"bead-1", "bead-2"}) {
		t.Errorf("Expected bead-1 and bead-2, got %v", beadIDs)
	}
}

func TestBeadStageUpdateProgress(t *testing.T) {
	s := tempStore(t)

	stage := &BeadStage{
		Project:      "progress-project",
		BeadID:       "progress-bead",
		Workflow:     "progress-workflow",
		CurrentStage: "initial",
		StageIndex:   0,
		TotalStages:  3,
	}

	if err := s.UpsertBeadStage(stage); err != nil {
		t.Fatalf("Failed to upsert initial stage: %v", err)
	}

	// Update progress
	dispatchID := int64(12345)
	if err := s.UpdateBeadStageProgress("progress-project", "progress-bead", "middle", 1, 3, dispatchID); err != nil {
		t.Fatalf("Failed to update progress: %v", err)
	}

	// Verify the update
	updated, err := s.GetBeadStage("progress-project", "progress-bead")
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

func TestBeadStageDelete(t *testing.T) {
	s := tempStore(t)

	stage := &BeadStage{
		Project:      "delete-project",
		BeadID:       "delete-bead",
		Workflow:     "delete-workflow",
		CurrentStage: "delete-stage",
		StageIndex:   0,
		TotalStages:  1,
	}

	if err := s.UpsertBeadStage(stage); err != nil {
		t.Fatalf("Failed to upsert stage: %v", err)
	}

	// Verify it exists
	_, err := s.GetBeadStage("delete-project", "delete-bead")
	if err != nil {
		t.Fatalf("Stage should exist before deletion: %v", err)
	}

	// Delete it
	if err := s.DeleteBeadStage("delete-project", "delete-bead"); err != nil {
		t.Fatalf("Failed to delete stage: %v", err)
	}

	// Verify it's gone
	_, err = s.GetBeadStage("delete-project", "delete-bead")
	if err == nil {
		t.Fatal("Stage should not exist after deletion")
	}

	expectedErrText := "bead stage not found"
	if !contains(err.Error(), expectedErrText) {
		t.Errorf("Expected error to contain '%s', got: %s", expectedErrText, err.Error())
	}
}

func TestBeadStageDeleteNonExistent(t *testing.T) {
	s := tempStore(t)

	// Try to delete a non-existent stage
	err := s.DeleteBeadStage("nonexistent-project", "nonexistent-bead")
	if err == nil {
		t.Fatal("Expected error when deleting non-existent stage")
	}

	expectedErrText := "bead stage not found"
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
