package store

import (
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
