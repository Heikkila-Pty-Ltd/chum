package store

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

// Store provides SQLite-backed persistence for CHUM state.
type Store struct {
	db                  *sql.DB
	dispatchPersistHook func(point string) error
}

var crystalCandidatesEnabled = parseStoreBoolEnv("CHUM_ENABLE_CRYSTAL_CANDIDATES")

func parseStoreBoolEnv(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// schema concatenates all domain schemas into the full DDL for Cortex.
// Individual domain schemas are defined in schema_*.go files.
var schema = schemaCore + schemaScheduling + schemaEvolution + schemaPlanning + schemaTraces

// Open creates or opens a SQLite database at the given path and ensures the schema exists.
// Uses WAL mode for concurrent reads, busy_timeout of 30s to survive Cambrian Explosion
// multi-writer contention, and MaxOpenConns=1 to serialize writes at the Go level.
func Open(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(30000)")
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", dbPath, err)
	}

	// Single-writer enforcement: serialize all writes through one connection.
	// SQLite WAL supports concurrent reads but only one writer at a time.
	// Without this, Cambrian Explosion child workflows race for the write lock
	// and get SQLITE_BUSY errors even with busy_timeout.
	db.SetMaxOpenConns(1)

	// Rename bead→morsel columns in existing databases BEFORE schema DDL runs.
	// The schema uses morsel_id but pre-rename databases have bead_id.
	if err := migrateBeadToMorsel(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrate bead→morsel: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: create schema: %w", err)
	}

	s := &Store{db: db}

	// Ensure evolutionary genome table exists BEFORE migrations
	// (migrate() adds columns like 'hibernating' to genomes)
	if err := s.ensureGenomesTable(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: genomes table: %w", err)
	}

	// Run migrations for existing databases
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}

	// Pre-flight check: verify critical columns exist after all migrations.
	// Catches schema drift when multiple binaries hit the same DB.
	if err := s.verifySchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: schema verification failed: %w", err)
	}

	// Seed initial proteins (deterministic workflow sequences)
	if err := s.SeedProteins(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: seed proteins: %w", err)
	}

	return s, nil
}

// verifySchema runs post-migration sanity checks on the DB schema.
// Catches drift caused by multiple binaries hitting the same DB file.
func (s *Store) verifySchema() error {
	criticalTables := []string{"morsel_stages", "dispatches", "lessons", "genomes", "execution_traces", "trace_events", "crystal_candidates"}
	for _, table := range criticalTables {
		var count int
		err := s.db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('` + table + `')`,
		).Scan(&count)
		if err != nil {
			return fmt.Errorf("verify table %s: %w", table, err)
		}
		if count == 0 {
			return fmt.Errorf("critical table %q is missing — schema migration may have failed", table)
		}
	}

	// Verify critical columns exist (catches schema drift from older binaries)
	colChecks := []struct {
		table  string
		column string
	}{
		{"dispatches", "morsel_id"},
		{"dispatches", "failure_category"},
		{"dispatches", "backend"},
		{"dispatches", "labels"},
		{"genomes", "provider_genes"},
		{"genomes", "hibernating"},
		{"execution_traces", "success_rate"},
		{"execution_traces", "goal_signature"},
		{"execution_traces", "attempt_count"},
		{"trace_events", "trace_id"},
		{"crystal_candidates", "success_rate"},
	}
	for _, c := range colChecks {
		var hasCol int
		if err := s.db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('`+c.table+`') WHERE name = ?`, c.column,
		).Scan(&hasCol); err != nil {
			return fmt.Errorf("verify %s.%s: %w", c.table, c.column, err)
		}
		if hasCol == 0 {
			return fmt.Errorf("schema drift: %s.%s column missing — run migrations or update binary", c.table, c.column)
		}
	}

	return nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB returns the underlying sql.DB for advanced queries.
func (s *Store) DB() *sql.DB {
	return s.db
}
