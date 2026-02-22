package store

import (
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "calcified_test.db"))
	if err != nil {
		t.Fatalf("openTestDB: %v", err)
	}
	return s
}

func TestCalcifiedScriptCRUD(t *testing.T) {
	s := openTestDB(t)
	defer s.Close()

	// Record a new shadow script
	id, err := s.RecordCalcifiedScript("parse_lead_form", "chum", ".cortex/calcified/parse_lead_form.py.shadow", "abc123hash")
	if err != nil {
		t.Fatalf("RecordCalcifiedScript: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero ID")
	}

	// Get shadow script
	shadow, err := s.GetShadowScriptForType("parse_lead_form")
	if err != nil {
		t.Fatalf("GetShadowScriptForType: %v", err)
	}
	if shadow == nil {
		t.Fatal("expected shadow script, got nil")
	}
	if shadow.Status != "shadow" {
		t.Errorf("expected status 'shadow', got %q", shadow.Status)
	}
	if shadow.SHA256 != "abc123hash" {
		t.Errorf("expected SHA256 'abc123hash', got %q", shadow.SHA256)
	}

	// Should not return an active script yet
	active, err := s.GetActiveScriptForType("parse_lead_form")
	if err != nil {
		t.Fatalf("GetActiveScriptForType: %v", err)
	}
	if active != nil {
		t.Fatal("expected no active script, got one")
	}

	// Update shadow counts
	if err := s.UpdateScriptShadowCounts(id, 3, 0); err != nil {
		t.Fatalf("UpdateScriptShadowCounts: %v", err)
	}

	// Promote
	if err := s.PromoteScript(id); err != nil {
		t.Fatalf("PromoteScript: %v", err)
	}
	active, err = s.GetActiveScriptForType("parse_lead_form")
	if err != nil {
		t.Fatalf("GetActiveScriptForType after promote: %v", err)
	}
	if active == nil {
		t.Fatal("expected active script after promotion")
	}
	if active.Status != "active" {
		t.Errorf("expected status 'active', got %q", active.Status)
	}
	if active.ShadowMatches != 3 {
		t.Errorf("expected 3 shadow matches, got %d", active.ShadowMatches)
	}

	// Quarantine
	if err := s.QuarantineScript(id, "format changed"); err != nil {
		t.Fatalf("QuarantineScript: %v", err)
	}
	q, err := s.GetCalcifiedScriptByID(id)
	if err != nil {
		t.Fatalf("GetCalcifiedScriptByID: %v", err)
	}
	if q.Status != "quarantined" {
		t.Errorf("expected status 'quarantined', got %q", q.Status)
	}
	if q.QuarantineReason != "format changed" {
		t.Errorf("expected quarantine reason 'format changed', got %q", q.QuarantineReason)
	}
}

func TestGetConsecutiveSuccessfulDispatches(t *testing.T) {
	s := openTestDB(t)
	defer s.Close()

	// Insert dispatches in order: 3 completed, 1 failed, 2 completed
	inserts := []struct {
		morselID string
		status   string
	}{
		{"parse-001", "completed"},
		{"parse-002", "completed"},
		{"parse-003", "failed"},
		{"parse-004", "completed"},
		{"parse-005", "completed"},
		{"parse-006", "completed"},
	}
	for _, ins := range inserts {
		_, err := s.db.Exec(
			`INSERT INTO dispatches (morsel_id, project, agent_id, provider, tier, prompt, status)
			 VALUES (?, 'testproj', 'codex', 'codex-spark', 'fast', 'test prompt', ?)`,
			ins.morselID, ins.status,
		)
		if err != nil {
			t.Fatalf("insert dispatch: %v", err)
		}
	}

	// Most recent 3 are completed, then a failed — streak should be 3
	count, err := s.GetConsecutiveSuccessfulDispatches("parse", "testproj")
	if err != nil {
		t.Fatalf("GetConsecutiveSuccessfulDispatches: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 consecutive successes, got %d", count)
	}
}

func TestCalcifiedActiveVsShadow(t *testing.T) {
	s := openTestDB(t)
	defer s.Close()

	// Create two scripts for the same type — one shadow, one gets promoted
	id1, _ := s.RecordCalcifiedScript("extract_invoice", "chum", "script1.py.shadow", "hash1")
	id2, _ := s.RecordCalcifiedScript("extract_invoice", "chum", "script2.py.shadow", "hash2")

	// Promote the first one
	s.PromoteScript(id1)

	active, _ := s.GetActiveScriptForType("extract_invoice")
	shadow, _ := s.GetShadowScriptForType("extract_invoice")

	if active == nil {
		t.Fatal("expected active script")
	}
	if active.ID != id1 {
		t.Errorf("expected active script ID %d, got %d", id1, active.ID)
	}
	if shadow == nil {
		t.Fatal("expected shadow script")
	}
	if shadow.ID != id2 {
		t.Errorf("expected shadow script ID %d, got %d", id2, shadow.ID)
	}
}
