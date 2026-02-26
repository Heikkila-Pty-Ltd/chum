package main

import (
	"compress/gzip"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/antigravity-dev/chum/cmd/internal/dbutil"
	_ "modernc.org/sqlite"
)

func main() {
	var (
		dbPath     = flag.String("db", "", "source database path (required)")
		backupPath = flag.String("backup", "", "backup destination path (optional, auto-generated if not provided)")
		verify     = flag.Bool("verify", true, "run integrity check on backup")
		compress   = flag.Bool("compress", false, "compress backup with gzip")
		checkpoint = flag.Bool("checkpoint", true, "run checkpoint before backup to merge WAL")
	)
	flag.Parse()

	if *dbPath == "" {
		dbutil.Die("--db path is required")
	}

	// Expand tilde in paths
	*dbPath = dbutil.ExpandPath(*dbPath)

	// Auto-generate backup path if not provided
	if *backupPath == "" {
		timestamp := time.Now().Format("20060102-150405")
		base := strings.TrimSuffix(filepath.Base(*dbPath), filepath.Ext(*dbPath))
		ext := ".db"
		if *compress {
			ext = ".db.gz"
		}
		*backupPath = fmt.Sprintf("%s-backup-%s%s", base, timestamp, ext)
	}
	*backupPath = dbutil.ExpandPath(*backupPath)

	fmt.Printf("SQLite Backup Tool\n")
	fmt.Printf("Source: %s\n", *dbPath)
	fmt.Printf("Destination: %s\n", *backupPath)

	// Ensure backup directory exists
	if err := os.MkdirAll(filepath.Dir(*backupPath), 0o755); err != nil {
		dbutil.Die("create backup directory: %v", err)
	}

	// Open source database
	db, err := sql.Open("sqlite", *dbPath+"?mode=ro")
	if err != nil {
		dbutil.Die("open source database: %v", err)
	}
	defer db.Close()

	// Run checkpoint if requested (flushes WAL to main DB)
	if *checkpoint {
		fmt.Printf("Running WAL checkpoint...\n")
		if _, err := db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			fmt.Printf("Warning: checkpoint failed: %v\n", err)
		}
	}

	// Perform backup using file copy after checkpoint
	fmt.Printf("Creating backup...\n")
	start := time.Now()

	if err := performBackup(*dbPath, *backupPath, *compress); err != nil {
		dbutil.Die("backup failed: %v", err)
	}

	duration := time.Since(start)
	fmt.Printf("Backup completed in %v\n", duration)

	// Verify backup integrity
	if *verify {
		fmt.Printf("Verifying backup integrity...\n")
		if err := verifyBackup(*backupPath, *compress); err != nil {
			dbutil.Die("backup verification failed: %v", err)
		}
		fmt.Printf("Backup verification successful\n")
	}

	// Show backup info
	info, err := os.Stat(*backupPath)
	if err == nil {
		fmt.Printf("Backup size: %d bytes (%.2f MB)\n", info.Size(), float64(info.Size())/1024/1024)
	}

	fmt.Printf("✅ Backup completed successfully\n")
}

func performBackup(srcPath, dstPath string, compress bool) error {
	if !compress {
		return dbutil.CopyFile(srcPath, dstPath)
	}

	srcFile, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	defer dstFile.Close()

	gz, err := gzip.NewWriterLevel(dstFile, gzip.BestCompression)
	if err != nil {
		return fmt.Errorf("create gzip writer: %w", err)
	}
	gz.Name = filepath.Base(srcPath)
	gz.ModTime = time.Now()

	if _, err := io.Copy(gz, srcFile); err != nil {
		gz.Close()
		return fmt.Errorf("gzip copy: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("gzip close: %w", err)
	}

	return dstFile.Sync()
}

func verifyBackup(backupPath string, compress bool) error {
	verifyPath := backupPath

	// For compressed backups, decompress to a temp file for integrity checking.
	if compress {
		tmp, err := decompressToTemp(backupPath)
		if err != nil {
			return fmt.Errorf("decompress for verification: %w", err)
		}
		defer os.Remove(tmp)
		verifyPath = tmp
	}

	if err := dbutil.CheckIntegrity(verifyPath); err != nil {
		return err
	}

	counts, err := dbutil.CountTableRows(verifyPath, dbutil.KnownTables)
	if err != nil {
		return fmt.Errorf("count table rows: %w", err)
	}
	for table, count := range counts {
		if count >= 0 {
			fmt.Printf("Verified table %s: %d rows\n", table, count)
		} else {
			fmt.Printf("Warning: could not count rows in %s\n", table)
		}
	}

	return nil
}

// decompressToTemp decompresses a gzip file to a temporary file and returns
// the temp file path. The caller is responsible for removing the temp file.
func decompressToTemp(gzPath string) (string, error) {
	f, err := os.Open(gzPath)
	if err != nil {
		return "", fmt.Errorf("open gzip file: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("create gzip reader: %w", err)
	}
	defer gz.Close()

	tmp, err := os.CreateTemp("", "chum-backup-verify-*.db")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}

	if _, err := io.Copy(tmp, gz); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", fmt.Errorf("decompress: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("close temp file: %w", err)
	}

	return tmp.Name(), nil
}
