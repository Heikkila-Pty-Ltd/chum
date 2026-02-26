package store

import (
	"database/sql"
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

	// Record token usage (including cache tokens to verify migration works)
	err = s.StoreTokenUsage(id, "morsel-1", "proj", "execute", "agent-1", TokenUsage{
		InputTokens:         100,
		OutputTokens:        50,
		CacheReadTokens:     20,
		CacheCreationTokens: 10,
		CostUSD:             0.01,
	})
	if err != nil {
		t.Fatalf("StoreTokenUsage failed: %v", err)
	}
}

// TestNewCoreStoreUpgradesLegacyDB verifies that NewCoreStore can open a
// database created with an older schema (bead_id columns, missing log_path
// etc.) and successfully migrate it so RecordDispatch works.
func TestNewCoreStoreUpgradesLegacyDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-upgrade.db")

	// Simulate a legacy DB: create the dispatches table with bead_id
	// and WITHOUT newer columns like log_path, branch, backend.
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS dispatches (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		bead_id TEXT NOT NULL,
		project TEXT NOT NULL,
		agent TEXT NOT NULL DEFAULT '',
		provider TEXT NOT NULL DEFAULT '',
		mode TEXT NOT NULL DEFAULT '',
		timeout_seconds INTEGER NOT NULL DEFAULT 300,
		prompt TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'running',
		exit_code INTEGER NOT NULL DEFAULT -1,
		duration REAL NOT NULL DEFAULT 0,
		dispatched_at DATETIME NOT NULL DEFAULT (datetime('now')),
		completed_at DATETIME
	)`)
	if err != nil {
		t.Fatalf("create legacy dispatches: %v", err)
	}
	// Insert a legacy row so we can verify the rename worked.
	_, err = db.Exec(`INSERT INTO dispatches (bead_id, project, agent) VALUES ('legacy-1', 'proj', 'agent-1')`)
	if err != nil {
		t.Fatalf("insert legacy dispatch: %v", err)
	}
	db.Close()

	// Re-open with NewCoreStore — this should apply bead→morsel rename
	// and backfill missing columns.
	s, err := NewCoreStore(dbPath)
	if err != nil {
		t.Fatalf("NewCoreStore on legacy DB failed: %v", err)
	}
	defer s.Close()

	// Verify the bead_id column was renamed to morsel_id.
	var morselID string
	err = s.db.QueryRow(`SELECT morsel_id FROM dispatches WHERE id = 1`).Scan(&morselID)
	if err != nil {
		t.Fatalf("query morsel_id: %v (bead→morsel rename likely failed)", err)
	}
	if morselID != "legacy-1" {
		t.Errorf("morsel_id = %q, want legacy-1", morselID)
	}

	// RecordDispatch should succeed now — it uses log_path, branch, backend
	// which were missing from the legacy schema.
	id, err := s.RecordDispatch("morsel-2", "proj", "agent-2", "openai", "fast", 100, "", "test", "", "", "")
	if err != nil {
		t.Fatalf("RecordDispatch on upgraded DB failed: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero dispatch ID")
	}
}

// TestNewCoreStoreUpgradesLegacyTokenUsage verifies that NewCoreStore applies
// cache token column migrations to legacy token_usage tables.
func TestNewCoreStoreUpgradesLegacyTokenUsage(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-token-usage.db")

	// Simulate a legacy DB with token_usage table missing cache columns.
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS token_usage (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		dispatch_id INTEGER NOT NULL DEFAULT 0,
		morsel_id TEXT NOT NULL DEFAULT '',
		project TEXT NOT NULL DEFAULT '',
		activity_name TEXT NOT NULL DEFAULT '',
		agent TEXT NOT NULL DEFAULT '',
		input_tokens INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		cost_usd REAL NOT NULL DEFAULT 0,
		recorded_at DATETIME NOT NULL DEFAULT (datetime('now'))
	)`)
	if err != nil {
		t.Fatalf("create legacy token_usage: %v", err)
	}
	// Create minimal dispatches table for foreign key constraint.
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS dispatches (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		morsel_id TEXT NOT NULL,
		project TEXT NOT NULL,
		agent_id TEXT NOT NULL,
		provider TEXT NOT NULL,
		tier TEXT NOT NULL,
		prompt TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'running'
	)`)
	if err != nil {
		t.Fatalf("create dispatches: %v", err)
	}
	db.Close()

	// Re-open with NewCoreStore — should backfill cache token columns.
	s, err := NewCoreStore(dbPath)
	if err != nil {
		t.Fatalf("NewCoreStore on legacy token_usage DB failed: %v", err)
	}
	defer s.Close()

	// Verify cache columns exist by inserting a record with cache tokens.
	dispatchID, err := s.RecordDispatch("morsel-cache-test", "proj", "agent-1", "cerebras", "fast", 100, "", "test", "", "", "")
	if err != nil {
		t.Fatalf("RecordDispatch: %v", err)
	}

	// This should NOT fail with "no such column: cache_read_tokens".
	err = s.StoreTokenUsage(dispatchID, "morsel-cache-test", "proj", "execute", "agent-1", TokenUsage{
		InputTokens:         100,
		OutputTokens:        50,
		CacheReadTokens:     20,
		CacheCreationTokens: 10,
		CostUSD:             0.01,
	})
	if err != nil {
		t.Fatalf("StoreTokenUsage with cache tokens failed: %v", err)
	}
}
