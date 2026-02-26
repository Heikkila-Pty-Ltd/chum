// Package dbutil provides shared utilities for the db-backup and db-restore commands.
package dbutil

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// KnownTables lists the core tables expected in a chum database.
var KnownTables = []string{"dispatches", "health_events"}

// ExpandPath resolves a leading ~/ to the user's home directory.
func ExpandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

// Die prints a formatted error message to stderr and exits with code 1.
func Die(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// CopyFile copies src to dst using a 1 MB buffer and fsyncs the destination.
func CopyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	defer dstFile.Close()

	buf := make([]byte, 1024*1024) // 1 MB buffer
	if _, err := io.CopyBuffer(dstFile, srcFile, buf); err != nil {
		return fmt.Errorf("copy: %w", err)
	}

	return dstFile.Sync()
}

// CheckIntegrity opens a SQLite database at dbPath in read-only mode and runs
// PRAGMA integrity_check. It returns a non-nil error when the check fails.
func CheckIntegrity(dbPath string) error {
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	var result string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("integrity check query: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("integrity check failed: %s", result)
	}
	return nil
}

// CountTableRows opens the database read-only and returns row counts for each
// of the supplied tables. If a table does not exist the count is -1.
func CountTableRows(dbPath string, tables []string) (map[string]int, error) {
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	counts := make(map[string]int, len(tables))
	for _, table := range tables {
		var count int
		query := fmt.Sprintf("SELECT COUNT(*) FROM %s", table)
		if err := db.QueryRow(query).Scan(&count); err != nil {
			counts[table] = -1
		} else {
			counts[table] = count
		}
	}
	return counts, nil
}
