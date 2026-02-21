package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/antigravity-dev/chum/cmd/internal/dbutil"
	_ "modernc.org/sqlite"
)

func main() {
	var (
		backupPath = flag.String("backup", "", "backup file path (required)")
		dbPath     = flag.String("db", "", "target database path (required)")
		verify     = flag.Bool("verify", true, "verify restore integrity")
		dryRun     = flag.Bool("dry-run", false, "validate backup without actually restoring")
		force      = flag.Bool("force", false, "overwrite existing database")
	)
	flag.Parse()

	if *backupPath == "" {
		dbutil.Die("--backup path is required")
	}
	if *dbPath == "" {
		dbutil.Die("--db path is required")
	}

	*backupPath = dbutil.ExpandPath(*backupPath)
	*dbPath = dbutil.ExpandPath(*dbPath)

	fmt.Printf("SQLite Restore Tool\n")
	fmt.Printf("Backup: %s\n", *backupPath)
	fmt.Printf("Target: %s\n", *dbPath)

	// Verify backup exists and is readable
	if _, err := os.Stat(*backupPath); os.IsNotExist(err) {
		dbutil.Die("backup file does not exist: %s", *backupPath)
	}

	// Verify backup integrity first
	fmt.Printf("Verifying backup integrity...\n")
	backupInfo, err := verifyBackupIntegrity(*backupPath)
	if err != nil {
		dbutil.Die("backup verification failed: %v", err)
	}
	fmt.Printf("Backup verification passed: %v\n", backupInfo)

	if *dryRun {
		fmt.Printf("✅ Dry run completed - backup is valid\n")
		return
	}

	// Check if target exists
	if _, err := os.Stat(*dbPath); err == nil && !*force {
		dbutil.Die("target database exists (use --force to overwrite): %s", *dbPath)
	}

	// Create target directory
	if err := os.MkdirAll(filepath.Dir(*dbPath), 0o755); err != nil {
		dbutil.Die("create target directory: %v", err)
	}

	// Create safety backup of existing DB if it exists
	var safetyBackup string
	if _, err := os.Stat(*dbPath); err == nil {
		safetyBackup = *dbPath + ".pre-restore-" + time.Now().Format("20060102-150405")
		fmt.Printf("Creating safety backup: %s\n", safetyBackup)
		if err := dbutil.CopyFile(*dbPath, safetyBackup); err != nil {
			dbutil.Die("create safety backup: %v", err)
		}
	}

	// Perform restore
	fmt.Printf("Restoring database...\n")
	start := time.Now()

	if err := dbutil.CopyFile(*backupPath, *dbPath); err != nil {
		// Attempt rollback if we have safety backup
		if safetyBackup != "" {
			fmt.Printf("Restore failed, attempting rollback...\n")
			if rollbackErr := dbutil.CopyFile(safetyBackup, *dbPath); rollbackErr != nil {
				dbutil.Die("restore failed AND rollback failed: %v (original error: %v)", rollbackErr, err)
			}
			fmt.Printf("Rollback completed\n")
		}
		dbutil.Die("restore failed: %v", err)
	}

	duration := time.Since(start)
	fmt.Printf("Restore completed in %v\n", duration)

	// Verify restored database
	if *verify {
		fmt.Printf("Verifying restored database...\n")
		if err := verifyRestoredDatabase(*dbPath); err != nil {
			dbutil.Die("restored database verification failed: %v", err)
		}
		fmt.Printf("Restored database verification successful\n")
	}

	// Clean up safety backup on success
	if safetyBackup != "" {
		if err := os.Remove(safetyBackup); err != nil {
			fmt.Printf("Warning: could not clean up safety backup %s: %v\n", safetyBackup, err)
		} else {
			fmt.Printf("Safety backup cleaned up\n")
		}
	}

	fmt.Printf("✅ Restore completed successfully\n")
}

func verifyBackupIntegrity(backupPath string) (map[string]interface{}, error) {
	info := make(map[string]interface{})

	if err := dbutil.CheckIntegrity(backupPath); err != nil {
		return nil, err
	}
	info["integrity"] = "ok"

	counts, err := dbutil.CountTableRows(backupPath, dbutil.KnownTables)
	if err != nil {
		return nil, fmt.Errorf("count table rows: %v", err)
	}
	info["table_counts"] = counts

	return info, nil
}

func verifyRestoredDatabase(dbPath string) error {
	if err := dbutil.CheckIntegrity(dbPath); err != nil {
		return err
	}

	counts, err := dbutil.CountTableRows(dbPath, dbutil.KnownTables)
	if err != nil {
		return fmt.Errorf("count table rows: %v", err)
	}
	for table, count := range counts {
		if count >= 0 {
			fmt.Printf("Restored table %s: %d rows\n", table, count)
		} else {
			fmt.Printf("Warning: could not query %s\n", table)
		}
	}

	return nil
}
