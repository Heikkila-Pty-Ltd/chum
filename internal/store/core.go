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
func NewCoreStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(30000)")
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", dbPath, err)
	}
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schemaCore); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: create core schema: %w", err)
	}

	return &Store{db: db}, nil
}
