package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestRecordRunAndGetLatest(t *testing.T) {
	s := tempStore(t)

	id, err := s.RecordRun("proj", 5, 3, 1, `{"coverage":62}`)
	if err != nil {
		t.Fatalf("RecordRun failed: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive run ID, got %d", id)
	}

	run, err := s.GetLatestRun("proj")
	if err != nil {
		t.Fatalf("GetLatestRun failed: %v", err)
	}
	if run == nil {
		t.Fatal("expected run, got nil")
	}
	if run.ID != id {
		t.Errorf("run ID = %d, want %d", run.ID, id)
	}
	if run.Project != "proj" {
		t.Errorf("project = %q, want %q", run.Project, "proj")
	}
	if run.FindingsTotal != 5 {
		t.Errorf("findings_total = %d, want 5", run.FindingsTotal)
	}
	if run.FindingsNew != 3 {
		t.Errorf("findings_new = %d, want 3", run.FindingsNew)
	}
	if run.FindingsResolved != 1 {
		t.Errorf("findings_resolved = %d, want 1", run.FindingsResolved)
	}
	if run.MetricsJSON != `{"coverage":62}` {
		t.Errorf("metrics_json = %q, want %q", run.MetricsJSON, `{"coverage":62}`)
	}
}

func TestGetLatestRunNoRows(t *testing.T) {
	s := tempStore(t)

	run, err := s.GetLatestRun("nonexistent")
	if err != nil {
		t.Fatalf("GetLatestRun failed: %v", err)
	}
	if run != nil {
		t.Fatalf("expected nil run for nonexistent project, got %+v", run)
	}
}

func TestRecordRunDefaultMetrics(t *testing.T) {
	s := tempStore(t)

	id, err := s.RecordRun("proj", 0, 0, 0, "")
	if err != nil {
		t.Fatalf("RecordRun failed: %v", err)
	}

	run, err := s.GetLatestRun("proj")
	if err != nil {
		t.Fatalf("GetLatestRun failed: %v", err)
	}
	if run.ID != id {
		t.Errorf("run ID = %d, want %d", run.ID, id)
	}
	if run.MetricsJSON != "{}" {
		t.Errorf("metrics_json = %q, want %q", run.MetricsJSON, "{}")
	}
}

func TestRecordFindingAndGetRecent(t *testing.T) {
	s := tempStore(t)

	runID, err := s.RecordRun("proj", 2, 2, 0, "{}")
	if err != nil {
		t.Fatalf("RecordRun failed: %v", err)
	}

	f1, err := s.RecordFinding(runID, "proj", "god_object", "high", "Store too large", "47 methods on *Store", "internal/store/store.go", "wc -l output")
	if err != nil {
		t.Fatalf("RecordFinding 1 failed: %v", err)
	}
	if f1 <= 0 {
		t.Fatalf("expected positive finding ID, got %d", f1)
	}

	f2, err := s.RecordFinding(runID, "proj", "tech_debt", "medium", "Stale TODOs", "12 TODOs older than 90 days", "internal/dispatch/", "grep output")
	if err != nil {
		t.Fatalf("RecordFinding 2 failed: %v", err)
	}

	findings, err := s.GetRecentFindings("proj", 10)
	if err != nil {
		t.Fatalf("GetRecentFindings failed: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}

	// Most recent last_seen first (both inserted ~same time, so order by id desc effectively)
	found := false
	for _, f := range findings {
		if f.ID == f1 {
			found = true
			if f.Category != "god_object" {
				t.Errorf("category = %q, want %q", f.Category, "god_object")
			}
			if f.Severity != "high" {
				t.Errorf("severity = %q, want %q", f.Severity, "high")
			}
			if f.Title != "Store too large" {
				t.Errorf("title = %q, want %q", f.Title, "Store too large")
			}
			if f.FilePath != "internal/store/store.go" {
				t.Errorf("file_path = %q, want %q", f.FilePath, "internal/store/store.go")
			}
			if f.Status != "open" {
				t.Errorf("status = %q, want %q", f.Status, "open")
			}
		}
	}
	if !found {
		t.Error("finding f1 not found in results")
	}

	// Verify finding f2 is present
	found = false
	for _, f := range findings {
		if f.ID == f2 {
			found = true
		}
	}
	if !found {
		t.Error("finding f2 not found in results")
	}
}

func TestRecordFindingUsesDefaults(t *testing.T) {
	s := tempStore(t)

	runID, err := s.RecordRun("proj", 1, 1, 0, "{}")
	if err != nil {
		t.Fatalf("RecordRun failed: %v", err)
	}

	findingID, err := s.RecordFinding(runID, "proj", "coverage", "low", "low coverage", "coverage is low", "", "")
	if err != nil {
		t.Fatalf("RecordFinding failed: %v", err)
	}

	var scannedRunID int64
	var filePath, evidence, beadID, status string
	if err := s.DB().QueryRow(`
		SELECT run_id, file_path, evidence, bead_id, status
		FROM stingray_findings
		WHERE id = ?
	`, findingID).Scan(&scannedRunID, &filePath, &evidence, &beadID, &status); err != nil {
		t.Fatalf("query finding failed: %v", err)
	}

	if scannedRunID != runID {
		t.Fatalf("run_id = %d, want %d", scannedRunID, runID)
	}
	if filePath != "" {
		t.Errorf("file_path = %q, want empty", filePath)
	}
	if evidence != "" {
		t.Errorf("evidence = %q, want empty", evidence)
	}
	if beadID != "" {
		t.Errorf("bead_id = %q, want empty", beadID)
	}
	if status != "open" {
		t.Errorf("status = %q, want open", status)
	}
}

func TestGetRecentFindingsDefaultLimit(t *testing.T) {
	s := tempStore(t)

	runID, _ := s.RecordRun("proj", 0, 0, 0, "{}")
	for i := 0; i < 25; i++ {
		if _, err := s.RecordFinding(runID, "proj", "tech_debt", "low", "finding", "detail", "", ""); err != nil {
			t.Fatalf("RecordFinding %d failed: %v", i, err)
		}
	}

	findings, err := s.GetRecentFindings("proj", 0)
	if err != nil {
		t.Fatalf("GetRecentFindings failed: %v", err)
	}
	if len(findings) != 20 {
		t.Errorf("expected default limit 20, got %d", len(findings))
	}
}

func TestGetRecentFindingsProjectIsolation(t *testing.T) {
	s := tempStore(t)

	r1, _ := s.RecordRun("proj-a", 1, 1, 0, "{}")
	r2, _ := s.RecordRun("proj-b", 1, 1, 0, "{}")
	s.RecordFinding(r1, "proj-a", "tech_debt", "low", "finding A", "", "", "")
	s.RecordFinding(r2, "proj-b", "tech_debt", "low", "finding B", "", "", "")

	findings, err := s.GetRecentFindings("proj-a", 10)
	if err != nil {
		t.Fatalf("GetRecentFindings failed: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding for proj-a, got %d", len(findings))
	}
	if findings[0].Title != "finding A" {
		t.Errorf("title = %q, want %q", findings[0].Title, "finding A")
	}
}

func TestUpdateFindingStatus(t *testing.T) {
	s := tempStore(t)

	runID, _ := s.RecordRun("proj", 1, 1, 0, "{}")
	fID, _ := s.RecordFinding(runID, "proj", "coverage", "medium", "Low coverage", "23% in scheduler", "", "")

	if err := s.UpdateFindingStatus(fID, "resolved"); err != nil {
		t.Fatalf("UpdateFindingStatus failed: %v", err)
	}

	findings, _ := s.GetRecentFindings("proj", 10)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].Status != "resolved" {
		t.Errorf("status = %q, want %q", findings[0].Status, "resolved")
	}
}

func TestUpdateFindingBeadID(t *testing.T) {
	s := tempStore(t)

	runID, _ := s.RecordRun("proj", 1, 1, 0, "{}")
	fID, _ := s.RecordFinding(runID, "proj", "god_object", "high", "Big file", "too many methods", "store.go", "")

	if err := s.UpdateFindingBeadID(fID, "beads-abc123"); err != nil {
		t.Fatalf("UpdateFindingBeadID failed: %v", err)
	}

	findings, _ := s.GetRecentFindings("proj", 10)
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].BeadID != "beads-abc123" {
		t.Errorf("bead_id = %q, want %q", findings[0].BeadID, "beads-abc123")
	}
	if findings[0].Status != "filed" {
		t.Errorf("status = %q, want %q (should auto-set to filed)", findings[0].Status, "filed")
	}
}

func TestUpdateFindingLastSeen(t *testing.T) {
	s := tempStore(t)

	runID, _ := s.RecordRun("proj", 1, 1, 0, "{}")
	fID, _ := s.RecordFinding(runID, "proj", "tech_debt", "low", "Old TODO", "stale", "", "")

	// Get initial last_seen
	findings, _ := s.GetRecentFindings("proj", 10)
	initialLastSeen := findings[0].LastSeen

	if err := s.UpdateFindingLastSeen(fID); err != nil {
		t.Fatalf("UpdateFindingLastSeen failed: %v", err)
	}

	findings, _ = s.GetRecentFindings("proj", 10)
	// last_seen should be >= initial (may be same second in fast test)
	if findings[0].LastSeen.Before(initialLastSeen) {
		t.Errorf("last_seen went backwards: %v < %v", findings[0].LastSeen, initialLastSeen)
	}
}

func TestGetFindingByTitleAndFile(t *testing.T) {
	s := tempStore(t)

	runID, _ := s.RecordRun("proj", 1, 1, 0, "{}")
	s.RecordFinding(runID, "proj", "god_object", "high", "Store too large", "47 methods", "internal/store/store.go", "")

	// Should find the existing finding
	f, err := s.GetFindingByTitleAndFile("proj", "Store too large", "internal/store/store.go")
	if err != nil {
		t.Fatalf("GetFindingByTitleAndFile failed: %v", err)
	}
	if f == nil {
		t.Fatal("expected finding, got nil")
	}
	if f.Title != "Store too large" {
		t.Errorf("title = %q, want %q", f.Title, "Store too large")
	}

	// Should not find with different title
	f, err = s.GetFindingByTitleAndFile("proj", "Different title", "internal/store/store.go")
	if err != nil {
		t.Fatalf("GetFindingByTitleAndFile failed: %v", err)
	}
	if f != nil {
		t.Fatalf("expected nil for different title, got %+v", f)
	}

	// Should not find with different file
	f, err = s.GetFindingByTitleAndFile("proj", "Store too large", "other/file.go")
	if err != nil {
		t.Fatalf("GetFindingByTitleAndFile failed: %v", err)
	}
	if f != nil {
		t.Fatalf("expected nil for different file, got %+v", f)
	}

	// Should not find in different project
	f, err = s.GetFindingByTitleAndFile("other-proj", "Store too large", "internal/store/store.go")
	if err != nil {
		t.Fatalf("GetFindingByTitleAndFile failed: %v", err)
	}
	if f != nil {
		t.Fatalf("expected nil for different project, got %+v", f)
	}
}

func TestGetFindingByTitleAndFileExcludesResolved(t *testing.T) {
	s := tempStore(t)

	runID, _ := s.RecordRun("proj", 1, 1, 0, "{}")
	fID, _ := s.RecordFinding(runID, "proj", "tech_debt", "low", "Old TODO", "stale", "main.go", "")
	s.UpdateFindingStatus(fID, "resolved")

	f, err := s.GetFindingByTitleAndFile("proj", "Old TODO", "main.go")
	if err != nil {
		t.Fatalf("GetFindingByTitleAndFile failed: %v", err)
	}
	if f != nil {
		t.Fatalf("expected nil for resolved finding, got %+v", f)
	}
}

func TestGetTrendingFindings(t *testing.T) {
	s := tempStore(t)

	// Create 3 runs with a persistent finding and one transient finding
	r1, _ := s.RecordRun("proj", 2, 2, 0, "{}")
	s.RecordFinding(r1, "proj", "god_object", "high", "Store too large", "47 methods", "store.go", "")
	s.RecordFinding(r1, "proj", "tech_debt", "low", "One-time issue", "detail", "other.go", "")

	r2, _ := s.RecordRun("proj", 1, 0, 1, "{}")
	s.RecordFinding(r2, "proj", "god_object", "high", "Store too large", "48 methods", "store.go", "")

	r3, _ := s.RecordRun("proj", 1, 0, 0, "{}")
	s.RecordFinding(r3, "proj", "god_object", "high", "Store too large", "49 methods", "store.go", "")

	// minOccurrences=2: should find the persistent finding
	trending, err := s.GetTrendingFindings("proj", 2)
	if err != nil {
		t.Fatalf("GetTrendingFindings failed: %v", err)
	}
	if len(trending) != 1 {
		t.Fatalf("expected 1 trending finding, got %d", len(trending))
	}
	if trending[0].Title != "Store too large" {
		t.Errorf("title = %q, want %q", trending[0].Title, "Store too large")
	}
	// Should be the most recent version (run 3, "49 methods")
	if trending[0].Detail != "49 methods" {
		t.Errorf("detail = %q, want %q (most recent)", trending[0].Detail, "49 methods")
	}

	// minOccurrences=3: should still find it (3 occurrences)
	trending, err = s.GetTrendingFindings("proj", 3)
	if err != nil {
		t.Fatalf("GetTrendingFindings failed: %v", err)
	}
	if len(trending) != 1 {
		t.Fatalf("expected 1 trending finding with min 3, got %d", len(trending))
	}

	// minOccurrences=4: should find nothing
	trending, err = s.GetTrendingFindings("proj", 4)
	if err != nil {
		t.Fatalf("GetTrendingFindings failed: %v", err)
	}
	if len(trending) != 0 {
		t.Fatalf("expected 0 trending findings with min 4, got %d", len(trending))
	}
}

func TestGetTrendingFindingsExcludesResolved(t *testing.T) {
	s := tempStore(t)

	r1, _ := s.RecordRun("proj", 1, 1, 0, "{}")
	f1, _ := s.RecordFinding(r1, "proj", "tech_debt", "medium", "Resolved issue", "was bad", "file.go", "")

	r2, _ := s.RecordRun("proj", 1, 0, 0, "{}")
	s.RecordFinding(r2, "proj", "tech_debt", "medium", "Resolved issue", "still bad", "file.go", "")

	// Mark the first one resolved
	s.UpdateFindingStatus(f1, "resolved")

	// The second one is still open, but only 1 open occurrence → should not trend with min=2
	// Actually the query filters status='open' per finding, so only the second (open) one counts as 1 run
	trending, err := s.GetTrendingFindings("proj", 2)
	if err != nil {
		t.Fatalf("GetTrendingFindings failed: %v", err)
	}
	if len(trending) != 0 {
		t.Fatalf("expected 0 trending (one resolved), got %d", len(trending))
	}
}

func TestGetTrendingFindingsDefaultMinOccurrences(t *testing.T) {
	s := tempStore(t)

	r1, _ := s.RecordRun("proj", 1, 1, 0, "{}")
	s.RecordFinding(r1, "proj", "coverage", "medium", "Low coverage", "23%", "pkg/", "")

	r2, _ := s.RecordRun("proj", 1, 0, 0, "{}")
	s.RecordFinding(r2, "proj", "coverage", "medium", "Low coverage", "22%", "pkg/", "")

	// Default minOccurrences (0 → 2)
	trending, err := s.GetTrendingFindings("proj", 0)
	if err != nil {
		t.Fatalf("GetTrendingFindings failed: %v", err)
	}
	if len(trending) != 1 {
		t.Fatalf("expected 1 trending with default min, got %d", len(trending))
	}
}

func TestGetTrendingFindingsProjectIsolation(t *testing.T) {
	s := tempStore(t)

	rA1, _ := s.RecordRun("proj-a", 1, 1, 0, "{}")
	_, _ = s.RecordFinding(rA1, "proj-a", "tech_debt", "low", "Shared finding", "detail", "a.go", "")
	_, _ = s.RecordFinding(rA1, "proj-a", "coverage", "medium", "Project A only", "detail", "a2.go", "")

	rA2, _ := s.RecordRun("proj-a", 1, 1, 0, "{}")
	_, _ = s.RecordFinding(rA2, "proj-a", "tech_debt", "low", "Shared finding", "detail", "a.go", "")

	rB1, _ := s.RecordRun("proj-b", 1, 1, 0, "{}")
	_, _ = s.RecordFinding(rB1, "proj-b", "tech_debt", "low", "Shared finding", "detail", "a.go", "")

	trendingA, err := s.GetTrendingFindings("proj-a", 2)
	if err != nil {
		t.Fatalf("GetTrendingFindings proj-a failed: %v", err)
	}
	if len(trendingA) != 1 {
		t.Fatalf("expected 1 trending finding for proj-a, got %d", len(trendingA))
	}
	if trendingA[0].Project != "proj-a" {
		t.Errorf("trendingA project = %q, want %q", trendingA[0].Project, "proj-a")
	}

	trendingB, err := s.GetTrendingFindings("proj-b", 1)
	if err != nil {
		t.Fatalf("GetTrendingFindings proj-b failed: %v", err)
	}
	if len(trendingB) != 1 {
		t.Fatalf("expected 1 trending finding for proj-b with min=1, got %d", len(trendingB))
	}
	if trendingB[0].Project != "proj-b" {
		t.Errorf("trendingB project = %q, want %q", trendingB[0].Project, "proj-b")
	}
	if trendingB[0].Title != "Shared finding" {
		t.Errorf("trendingB title = %q, want %q", trendingB[0].Title, "Shared finding")
	}
}

func TestStingrayTablesCreatedOnStartup(t *testing.T) {
	s := tempStore(t)

	// Verify stingray_runs table exists
	var count int
	err := s.DB().QueryRow(`SELECT COUNT(*) FROM pragma_table_info('stingray_runs')`).Scan(&count)
	if err != nil {
		t.Fatalf("pragma_table_info stingray_runs failed: %v", err)
	}
	if count == 0 {
		t.Error("stingray_runs table does not exist")
	}

	// Verify stingray_findings table exists
	err = s.DB().QueryRow(`SELECT COUNT(*) FROM pragma_table_info('stingray_findings')`).Scan(&count)
	if err != nil {
		t.Fatalf("pragma_table_info stingray_findings failed: %v", err)
	}
	if count == 0 {
		t.Error("stingray_findings table does not exist")
	}
}

func TestOpenMigratesLegacyDBWithoutStingrayTables(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy sqlite db failed: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE dispatches (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			bead_id TEXT NOT NULL,
			project TEXT NOT NULL,
			agent_id TEXT NOT NULL,
			provider TEXT NOT NULL,
			tier TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("create legacy dispatches table failed: %v", err)
	}
	db.Close()

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	var tableCount int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM pragma_table_info('stingray_runs')`).Scan(&tableCount); err != nil {
		t.Fatalf("pragma_table_info(stingray_runs) failed: %v", err)
	}
	if tableCount == 0 {
		t.Fatal("stingray_runs table should have been created on startup")
	}

	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM pragma_table_info('stingray_findings')`).Scan(&tableCount); err != nil {
		t.Fatalf("pragma_table_info(stingray_findings) failed: %v", err)
	}
	if tableCount == 0 {
		t.Fatal("stingray_findings table should have been created on startup")
	}

	var indexCount int
	if err := s.DB().QueryRow(`
		SELECT COUNT(*) FROM pragma_index_list('stingray_runs')
		WHERE name IN ('idx_stingray_runs_project', 'idx_stingray_runs_run_at')
	`).Scan(&indexCount); err != nil {
		t.Fatalf("pragma_index_list(stingray_runs) failed: %v", err)
	}
	if indexCount != 2 {
		t.Fatalf("expected 2 stingray_runs indexes, got %d", indexCount)
	}

	if err := s.DB().QueryRow(`
		SELECT COUNT(*) FROM pragma_index_list('stingray_findings')
		WHERE name IN (
			'idx_stingray_findings_run',
			'idx_stingray_findings_project',
			'idx_stingray_findings_status',
			'idx_stingray_findings_category'
		)
	`).Scan(&indexCount); err != nil {
		t.Fatalf("pragma_index_list(stingray_findings) failed: %v", err)
	}
	if indexCount != 4 {
		t.Fatalf("expected 4 stingray_findings indexes, got %d", indexCount)
	}
}

func TestGetRecentFindingsNegativeLimitDefaultsToStandard(t *testing.T) {
	s := tempStore(t)

	runID, _ := s.RecordRun("proj", 0, 0, 0, "{}")
	for i := 0; i < 25; i++ {
		if _, err := s.RecordFinding(runID, "proj", "coverage", "low", "finding", "detail", "", ""); err != nil {
			t.Fatalf("RecordFinding %d failed: %v", i, err)
		}
	}

	findings, err := s.GetRecentFindings("proj", -3)
	if err != nil {
		t.Fatalf("GetRecentFindings failed: %v", err)
	}
	if len(findings) != 20 {
		t.Errorf("expected default limit 20, got %d", len(findings))
	}
}

func TestGetRecentFindingsOrdersByLastSeen(t *testing.T) {
	s := tempStore(t)

	runID, _ := s.RecordRun("proj", 0, 0, 0, "{}")
	oldFindingID, err := s.RecordFinding(runID, "proj", "coverage", "low", "older", "detail", "", "")
	if err != nil {
		t.Fatalf("RecordFinding old failed: %v", err)
	}
	newFindingID, err := s.RecordFinding(runID, "proj", "coverage", "low", "newer", "detail", "", "")
	if err != nil {
		t.Fatalf("RecordFinding new failed: %v", err)
	}

	if _, err := s.DB().Exec(`UPDATE stingray_findings SET last_seen = datetime('now', '-2 minutes') WHERE id = ?`, oldFindingID); err != nil {
		t.Fatalf("force old finding last_seen failed: %v", err)
	}

	findings, err := s.GetRecentFindings("proj", 10)
	if err != nil {
		t.Fatalf("GetRecentFindings failed: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}
	if findings[0].ID != newFindingID {
		t.Errorf("first finding ID = %d, want %d", findings[0].ID, newFindingID)
	}
	if findings[1].ID != oldFindingID {
		t.Errorf("second finding ID = %d, want %d", findings[1].ID, oldFindingID)
	}
}

func TestGetTrendingFindingsReopenAndStatusTransitions(t *testing.T) {
	s := tempStore(t)

	r1, _ := s.RecordRun("proj", 1, 1, 0, "{}")
	openRun1ID, _ := s.RecordFinding(r1, "proj", "tech_debt", "medium", "Unstable API", "run 1", "api.go", "")

	r2, _ := s.RecordRun("proj", 1, 1, 0, "{}")
	openRun2ID, _ := s.RecordFinding(r2, "proj", "tech_debt", "medium", "Unstable API", "run 2", "api.go", "")

	r3, _ := s.RecordRun("proj", 1, 1, 1, "{}")
	resolvedID, _ := s.RecordFinding(r3, "proj", "tech_debt", "medium", "Unstable API", "resolved in run 3", "api.go", "")
	if err := s.UpdateFindingStatus(resolvedID, "resolved"); err != nil {
		t.Fatalf("UpdateFindingStatus resolved failed: %v", err)
	}

	r4, _ := s.RecordRun("proj", 1, 1, 0, "{}")
	reopenID, _ := s.RecordFinding(r4, "proj", "tech_debt", "medium", "Unstable API", "reopened in run 4", "api.go", "")

	trending, err := s.GetTrendingFindings("proj", 2)
	if err != nil {
		t.Fatalf("GetTrendingFindings failed: %v", err)
	}
	if len(trending) != 1 {
		t.Fatalf("expected 1 trending after reopen, got %d", len(trending))
	}
	if trending[0].ID != reopenID {
		t.Fatalf("expected reopened finding ID %d, got %d", reopenID, trending[0].ID)
	}

	if err := s.UpdateFindingStatus(reopenID, "resolved"); err != nil {
		t.Fatalf("UpdateFindingStatus reopen resolved failed: %v", err)
	}

	trending, err = s.GetTrendingFindings("proj", 2)
	if err != nil {
		t.Fatalf("GetTrendingFindings failed: %v", err)
	}
	if len(trending) != 1 {
		t.Fatalf("expected 1 trending after reopen resolved, got %d", len(trending))
	}
	if trending[0].ID != openRun2ID {
		t.Fatalf("expected latest open row ID %d, got %d", openRun2ID, trending[0].ID)
	}

	if err := s.UpdateFindingStatus(openRun1ID, "filed"); err != nil {
		t.Fatalf("UpdateFindingStatus filed failed: %v", err)
	}

	run, err := s.GetFindingByTitleAndFile("proj", "Unstable API", "api.go")
	if err != nil {
		t.Fatalf("GetFindingByTitleAndFile failed: %v", err)
	}
	if run == nil || run.ID != openRun2ID {
		t.Fatalf("expected latest open/filed finding %d, got %+v", openRun2ID, run)
	}
}

func TestUpdateFindingStatusTransitions(t *testing.T) {
	s := tempStore(t)

	runID, _ := s.RecordRun("proj", 1, 1, 0, "{}")
	findingID, err := s.RecordFinding(runID, "proj", "god_object", "high", "Huge struct", "too many fields", "store.go", "")
	if err != nil {
		t.Fatalf("RecordFinding failed: %v", err)
	}

	if err := s.UpdateFindingStatus(findingID, "filed"); err != nil {
		t.Fatalf("UpdateFindingStatus filed failed: %v", err)
	}
	if err := s.UpdateFindingStatus(findingID, "resolved"); err != nil {
		t.Fatalf("UpdateFindingStatus resolved failed: %v", err)
	}
	if err := s.UpdateFindingStatus(findingID, "wont_fix"); err != nil {
		t.Fatalf("UpdateFindingStatus wont_fix failed: %v", err)
	}

	findings, err := s.GetRecentFindings("proj", 10)
	if err != nil {
		t.Fatalf("GetRecentFindings failed: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].Status != "wont_fix" {
		t.Errorf("status = %q, want %q", findings[0].Status, "wont_fix")
	}
	if findings[0].ID != findingID {
		t.Fatalf("expected finding ID %d, got %d", findingID, findings[0].ID)
	}
}

func TestMultipleRunsLatestReturned(t *testing.T) {
	s := tempStore(t)

	s.RecordRun("proj", 5, 5, 0, `{"run":1}`)
	id2, _ := s.RecordRun("proj", 3, 1, 2, `{"run":2}`)

	run, err := s.GetLatestRun("proj")
	if err != nil {
		t.Fatalf("GetLatestRun failed: %v", err)
	}
	if run.ID != id2 {
		t.Errorf("expected latest run ID %d, got %d", id2, run.ID)
	}
	if run.MetricsJSON != `{"run":2}` {
		t.Errorf("metrics_json = %q, want %q", run.MetricsJSON, `{"run":2}`)
	}
}

func TestOpenCreatesStingraySchema(t *testing.T) {
	s := tempStore(t)

	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='stingray_runs'").Scan(&count); err != nil {
		t.Fatalf("query stingray_runs failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("stingray_runs table missing, got count %d", count)
	}

	if err := s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='stingray_findings'").Scan(&count); err != nil {
		t.Fatalf("query stingray_findings failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("stingray_findings table missing, got count %d", count)
	}
}

func TestOpenMigratesLegacyDbForStingraySchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy_stingray.db")

	legacy, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open legacy db failed: %v", err)
	}
	if _, err := legacy.Exec(`CREATE TABLE legacy_stingray_placeholder (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create legacy table failed: %v", err)
	}
	if _, err := legacy.Exec(`DROP TABLE IF EXISTS stingray_findings`); err != nil {
		t.Fatalf("drop legacy stingray_findings failed: %v", err)
	}
	if _, err := legacy.Exec(`DROP TABLE IF EXISTS stingray_runs`); err != nil {
		t.Fatalf("drop legacy stingray_runs failed: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy db failed: %v", err)
	}

	s2, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed on legacy db: %v", err)
	}
	defer s2.Close()

	var count int
	if err := s2.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='stingray_runs'").Scan(&count); err != nil {
		t.Fatalf("query stingray_runs failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("stingray_runs table missing after migration, got count %d", count)
	}
	if err := s2.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='stingray_findings'").Scan(&count); err != nil {
		t.Fatalf("query stingray_findings failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("stingray_findings table missing after migration, got count %d", count)
	}
}

func TestGetRecentFindingsDefaultLimitAndOrder(t *testing.T) {
	s := tempStore(t)

	runID, _ := s.RecordRun("proj", 0, 0, 0, "{}")
	for i := 0; i < 25; i++ {
		title := "finding-" + string('a'+i)
		_, err := s.RecordFinding(runID, "proj", "tech_debt", "low", title, "detail", "", "")
		if err != nil {
			t.Fatalf("RecordFinding %d failed: %v", i, err)
		}
	}

	findingsZero, err := s.GetRecentFindings("proj", 0)
	if err != nil {
		t.Fatalf("GetRecentFindings limit 0 failed: %v", err)
	}
	if len(findingsZero) != 20 {
		t.Fatalf("expected default limit 20 for 0, got %d", len(findingsZero))
	}

	findingsNegative, err := s.GetRecentFindings("proj", -5)
	if err != nil {
		t.Fatalf("GetRecentFindings limit -5 failed: %v", err)
	}
	if len(findingsNegative) != 20 {
		t.Fatalf("expected default limit 20 for negative, got %d", len(findingsNegative))
	}

	findingsLimited, err := s.GetRecentFindings("proj", 5)
	if err != nil {
		t.Fatalf("GetRecentFindings limit 5 failed: %v", err)
	}
	if len(findingsLimited) != 5 {
		t.Fatalf("expected 5 findings, got %d", len(findingsLimited))
	}

	// Latest IDs should be returned first.
	if findingsLimited[0].ID <= findingsLimited[1].ID || findingsLimited[1].ID <= findingsLimited[2].ID {
		t.Fatalf("expected descending IDs in recent findings: got %d, %d, %d", findingsLimited[0].ID, findingsLimited[1].ID, findingsLimited[2].ID)
	}
}

func TestGetTrendingFindingsDefaultMinOccurrencesAndProjectIsolation(t *testing.T) {
	s := tempStore(t)

	r1, _ := s.RecordRun("proj-a", 1, 1, 0, "{}")
	s.RecordFinding(r1, "proj-a", "coverage", "low", "Coverage dip", "coverage dropped", "internal/foo.go", "")

	r2, _ := s.RecordRun("proj-a", 1, 0, 0, "{}")
	s.RecordFinding(r2, "proj-a", "coverage", "low", "Coverage dip", "coverage dropped", "internal/foo.go", "")

	r3, _ := s.RecordRun("proj-b", 1, 1, 0, "{}")
	s.RecordFinding(r3, "proj-b", "coverage", "low", "Coverage dip", "coverage dropped", "internal/foo.go", "")

	trendingA, err := s.GetTrendingFindings("proj-a", 0)
	if err != nil {
		t.Fatalf("GetTrendingFindings for proj-a failed: %v", err)
	}
	if len(trendingA) != 1 {
		t.Fatalf("expected 1 trending for proj-a, got %d", len(trendingA))
	}

	trendingB, err := s.GetTrendingFindings("proj-b", 0)
	if err != nil {
		t.Fatalf("GetTrendingFindings for proj-b failed: %v", err)
	}
	if len(trendingB) != 0 {
		t.Fatalf("expected 0 trending for proj-b, got %d", len(trendingB))
	}
}

func TestGetTrendingFindingsStatusReopenRules(t *testing.T) {
	s := tempStore(t)

	r1, _ := s.RecordRun("proj", 1, 1, 0, "{}")
	findingID1, err := s.RecordFinding(r1, "proj", "doc_drift", "high", "Doc mismatch", "docs outdated", "README.md", "")
	if err != nil {
		t.Fatalf("RecordFinding 1 failed: %v", err)
	}

	r2, _ := s.RecordRun("proj", 1, 0, 0, "{}")
	findingID2, err := s.RecordFinding(r2, "proj", "doc_drift", "high", "Doc mismatch", "docs outdated", "README.md", "")
	if err != nil {
		t.Fatalf("RecordFinding 2 failed: %v", err)
	}

	if err := s.UpdateFindingStatus(findingID2, "resolved"); err != nil {
		t.Fatalf("UpdateFindingStatus failed: %v", err)
	}

	trending, err := s.GetTrendingFindings("proj", 0)
	if err != nil {
		t.Fatalf("GetTrendingFindings failed: %v", err)
	}
	if len(trending) != 0 {
		t.Fatalf("expected 0 trending when one occurrence is resolved, got %d", len(trending))
	}

	if err := s.UpdateFindingStatus(findingID2, "open"); err != nil {
		t.Fatalf("reopen UpdateFindingStatus failed: %v", err)
	}

	trending, err = s.GetTrendingFindings("proj", 0)
	if err != nil {
		t.Fatalf("GetTrendingFindings after reopen failed: %v", err)
	}
	if len(trending) != 1 {
		t.Fatalf("expected 1 trending after reopen, got %d", len(trending))
	}

	if findingID1 == findingID2 {
		t.Fatalf("expected distinct finding IDs for separate runs")
	}
}
