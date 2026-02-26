package store

import (
	"path/filepath"
	"testing"
)

func TestNewCoreStoreCreatesOnlyCoreSchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "core-test.db")
	s, err := NewCoreStore(dbPath)
	if err != nil {
		t.Fatalf("NewCoreStore failed: %v", err)
	}
	defer s.Close()

	// Core tables must exist
	coreTables := []string{
		"dispatches",
		"dod_results",
		"dispatch_output",
		"health_events",
		"tick_metrics",
		"provider_usage",
		"token_usage",
		"step_metrics",
	}
	for _, table := range coreTables {
		var count int
		err := s.db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('` + table + `')`,
		).Scan(&count)
		if err != nil {
			t.Fatalf("query %s columns: %v", table, err)
		}
		if count == 0 {
			t.Errorf("core table %q missing from NewCoreStore", table)
		}
	}

	// Non-core tables must NOT exist
	nonCoreTables := []string{
		"proteins",
		"protein_folds",
		"mcts_nodes",
		"mcts_edges",
		"planning_trace_events",
		"execution_traces",
		"crystal_candidates",
		"genomes",
		"ubs_findings",
		"paleontology_runs",
		"organism_logs",
	}
	for _, table := range nonCoreTables {
		var count int
		err := s.db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('` + table + `')`,
		).Scan(&count)
		if err != nil {
			t.Fatalf("query %s columns: %v", table, err)
		}
		if count != 0 {
			t.Errorf("non-core table %q should NOT exist in NewCoreStore, but has %d columns", table, count)
		}
	}
}

func TestCoreStoreDispatchLifecycle(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "core-lifecycle.db")
	s, err := NewCoreStore(dbPath)
	if err != nil {
		t.Fatalf("NewCoreStore failed: %v", err)
	}
	defer s.Close()

	// Record a dispatch
	id, err := s.RecordDispatch("morsel-1", "proj", "agent-1", "cerebras", "fast", 100, "", "test prompt", "", "", "")
	if err != nil {
		t.Fatalf("RecordDispatch failed: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero dispatch ID")
	}

	// Verify running
	running, err := s.GetRunningDispatches()
	if err != nil {
		t.Fatalf("GetRunningDispatches failed: %v", err)
	}
	if len(running) != 1 {
		t.Fatalf("expected 1 running, got %d", len(running))
	}
	if running[0].MorselID != "morsel-1" {
		t.Errorf("expected morsel-1, got %s", running[0].MorselID)
	}

	// Complete it
	err = s.UpdateDispatchStatus(id, "completed", 0, 30.5)
	if err != nil {
		t.Fatalf("UpdateDispatchStatus failed: %v", err)
	}

	// No longer running
	running, err = s.GetRunningDispatches()
	if err != nil {
		t.Fatal(err)
	}
	if len(running) != 0 {
		t.Fatalf("expected 0 running after completion, got %d", len(running))
	}

	// Record DoD result
	err = s.RecordDoDResult(id, "morsel-1", "proj", true, "", "all checks passed")
	if err != nil {
		t.Fatalf("RecordDoDResult failed: %v", err)
	}

	// Capture output
	err = s.CaptureOutput(id, "build succeeded\nall tests passed")
	if err != nil {
		t.Fatalf("CaptureOutput failed: %v", err)
	}
	output, err := s.GetOutput(id)
	if err != nil {
		t.Fatalf("GetOutput failed: %v", err)
	}
	if output != "build succeeded\nall tests passed" {
		t.Errorf("unexpected output: %q", output)
	}

	// Record health event
	err = s.RecordHealthEvent("test_event", "test details")
	if err != nil {
		t.Fatalf("RecordHealthEvent failed: %v", err)
	}

	// Record token usage
	err = s.StoreTokenUsage(id, "morsel-1", "proj", "execute", "agent-1", TokenUsage{
		InputTokens:  100,
		OutputTokens: 50,
		CostUSD:      0.01,
	})
	if err != nil {
		t.Fatalf("StoreTokenUsage failed: %v", err)
	}
}
