package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // register sqlite driver
)

// NewCoreStore opens a store with only the core tables needed for basic
// dispatch tracking and metrics. This is the portable subset suitable for
// Chum v2 — no evolution, planning, MCTS, or organism tables.
//
// Core tables: dispatches, dod_results, dispatch_output, health_events,
// tick_metrics, provider_usage, token_usage, step_metrics.
//
// For existing databases the constructor applies the same compatibility
// migrations that Open() does (bead→morsel renames, column backfills)
// so that methods like RecordDispatch don't hit missing-column errors.
func NewCoreStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(30000)")
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", dbPath, err)
	}
	db.SetMaxOpenConns(1)

	// Apply compatibility migrations BEFORE the DDL schema — migrateBeadToMorsel
	// must run first because the DDL references morsel_id, not bead_id.
	if err := migrateBeadToMorsel(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: bead→morsel migration: %w", err)
	}

	if _, err := db.Exec(schemaCore); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: create core schema: %w", err)
	}

	// Backfill columns that may be missing on databases created before these
	// columns were added to the DDL. This is the core-specific subset of the
	// full migrate() function — only tables included in schemaCore.
	if err := migrateCoreTables(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: core migration: %w", err)
	}

	return &Store{db: db}, nil
}

// migrateCoreTables applies column backfills for the core tables only.
// This is the subset of migrate() relevant to NewCoreStore — it covers
// dispatches, provider_usage, health_events, and token_usage columns/indexes.
func migrateCoreTables(db *sql.DB) error {
	// Legacy databases used "agent" instead of "agent_id". The DDL and all
	// queries now expect agent_id, so rename if the old column exists.
	var hasOldAgent int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('dispatches') WHERE name = 'agent'`,
	).Scan(&hasOldAgent); err == nil && hasOldAgent > 0 {
		var hasNewAgent int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('dispatches') WHERE name = 'agent_id'`,
		).Scan(&hasNewAgent); err != nil {
			return fmt.Errorf("check dispatches.agent_id column: %w", err)
		}
		if hasNewAgent == 0 {
			if _, err := db.Exec(`ALTER TABLE dispatches RENAME COLUMN agent TO agent_id`); err != nil {
				return fmt.Errorf("rename dispatches.agent → agent_id: %w", err)
			}
		}
	}

	// Backfill every dispatches column that might be absent in any vintage of
	// legacy DB. addColumnIfNotExists is a no-op when the column already exists,
	// so listing all of them is safe and keeps this list in sync with schemaCore.
	dispatchColumns := []struct {
		column string
		ddl    string
	}{
		{"tier", "tier TEXT NOT NULL DEFAULT ''"},
		{"pid", "pid INTEGER NOT NULL DEFAULT 0"},
		{"session_name", "session_name TEXT NOT NULL DEFAULT ''"},
		{"stage", "stage TEXT NOT NULL DEFAULT 'dispatched'"},
		{"labels", "labels TEXT NOT NULL DEFAULT ''"},
		{"next_retry_at", "next_retry_at DATETIME"},
		{"exit_code", "exit_code INTEGER NOT NULL DEFAULT 0"},
		{"duration_s", "duration_s REAL NOT NULL DEFAULT 0"},
		{"retries", "retries INTEGER NOT NULL DEFAULT 0"},
		{"escalated_from_tier", "escalated_from_tier TEXT NOT NULL DEFAULT ''"},
		{"failure_category", "failure_category TEXT NOT NULL DEFAULT ''"},
		{"failure_summary", "failure_summary TEXT NOT NULL DEFAULT ''"},
		{"log_path", "log_path TEXT NOT NULL DEFAULT ''"},
		{"branch", "branch TEXT NOT NULL DEFAULT ''"},
		{"backend", "backend TEXT NOT NULL DEFAULT ''"},
		{"pr_url", "pr_url TEXT NOT NULL DEFAULT ''"},
		{"pr_number", "pr_number INTEGER NOT NULL DEFAULT 0"},
		{"input_tokens", "input_tokens INTEGER NOT NULL DEFAULT 0"},
		{"output_tokens", "output_tokens INTEGER NOT NULL DEFAULT 0"},
		{"cost_usd", "cost_usd REAL NOT NULL DEFAULT 0"},
	}
	for _, col := range dispatchColumns {
		if err := addColumnIfNotExists(db, "dispatches", col.column, col.ddl); err != nil {
			return err
		}
	}

	// provider_usage column backfills.
	for _, col := range []struct {
		column string
		ddl    string
	}{
		{"input_tokens", "input_tokens INTEGER NOT NULL DEFAULT 0"},
		{"output_tokens", "output_tokens INTEGER NOT NULL DEFAULT 0"},
	} {
		if err := addColumnIfNotExists(db, "provider_usage", col.column, col.ddl); err != nil {
			return err
		}
	}

	// health_events column backfills + indexes.
	for _, col := range []struct {
		column string
		ddl    string
	}{
		{"dispatch_id", "dispatch_id INTEGER NOT NULL DEFAULT 0"},
		{"morsel_id", "morsel_id TEXT NOT NULL DEFAULT ''"},
	} {
		if err := addColumnIfNotExists(db, "health_events", col.column, col.ddl); err != nil {
			return err
		}
	}
	for _, idx := range []string{
		`CREATE INDEX IF NOT EXISTS idx_health_events_dispatch ON health_events(dispatch_id)`,
		`CREATE INDEX IF NOT EXISTS idx_health_events_morsel ON health_events(morsel_id)`,
		`CREATE INDEX IF NOT EXISTS idx_health_events_created_at ON health_events(created_at)`,
	} {
		if _, err := db.Exec(idx); err != nil {
			return fmt.Errorf("create health_events index: %w", err)
		}
	}

	// token_usage column backfills for legacy schemas that predate cache metrics.
	// Older DBs lack cache_read_tokens and cache_creation_tokens, which causes
	// StoreTokenUsage() to fail with "no such column" errors.
	for _, col := range []struct {
		column string
		ddl    string
	}{
		{"cache_read_tokens", "cache_read_tokens INTEGER NOT NULL DEFAULT 0"},
		{"cache_creation_tokens", "cache_creation_tokens INTEGER NOT NULL DEFAULT 0"},
	} {
		if err := addColumnIfNotExists(db, "token_usage", col.column, col.ddl); err != nil {
			return err
		}
	}

	return nil
}
