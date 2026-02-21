package store

import (
	"path/filepath"
	"testing"
	"time"
)

func tempSafetyStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "safety.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSetGetRemoveBlock(t *testing.T) {
	s := tempSafetyStore(t)

	if err := s.SetBlock("scope-a", "type-a", time.Now().Add(time.Minute), "temporary hold"); err != nil {
		t.Fatalf("SetBlock failed: %v", err)
	}

	block, err := s.GetBlock("scope-a", "type-a")
	if err != nil {
		t.Fatalf("GetBlock failed: %v", err)
	}
	if block == nil {
		t.Fatal("expected block, got nil")
	}
	if block.Scope != "scope-a" || block.BlockType != "type-a" || block.Reason != "temporary hold" {
		t.Fatalf("unexpected block metadata: %#v", block)
	}
	if block.BlockedUntil.IsZero() {
		t.Fatal("expected blocked until to be set")
	}

	if err := s.RemoveBlock("scope-a", "type-a"); err != nil {
		t.Fatalf("RemoveBlock failed: %v", err)
	}
	block, err = s.GetBlock("scope-a", "type-a")
	if err != nil {
		t.Fatalf("GetBlock after remove failed: %v", err)
	}
	if block != nil {
		t.Fatalf("expected block to be removed, got %#v", block)
	}
}

func TestSetBlockWithMetadataRoundTrip(t *testing.T) {
	s := tempSafetyStore(t)

	metadata := map[string]interface{}{
		"attempts": 3,
		"tag":      "circuit",
	}
	if err := s.SetBlockWithMetadata("system", "gateway", time.Now().Add(10*time.Minute), "gateway circuit", metadata); err != nil {
		t.Fatalf("SetBlockWithMetadata failed: %v", err)
	}

	block, err := s.GetBlock("system", "gateway")
	if err != nil {
		t.Fatalf("GetBlock failed: %v", err)
	}
	if block == nil {
		t.Fatal("expected block, got nil")
	}
	if got := block.Metadata["tag"]; got != "circuit" {
		t.Fatalf("metadata tag = %v, want circuit", got)
	}
	if _, ok := block.Metadata["attempts"]; !ok {
		t.Fatalf("expected attempts metadata, got %#v", block.Metadata)
	}
}

func TestGetMissingBlockReturnsNil(t *testing.T) {
	s := tempSafetyStore(t)
	block, err := s.GetBlock("missing", "type")
	if err != nil {
		t.Fatalf("GetBlock failed: %v", err)
	}
	if block != nil {
		t.Fatalf("expected nil block for missing key, got %#v", block)
	}
}

func TestSafetyMetrics(t *testing.T) {
	s := tempSafetyStore(t)

	// No blocks yet — counts should be empty.
	counts, err := s.GetBlockCountsByType()
	if err != nil {
		t.Fatalf("GetBlockCountsByType (empty): %v", err)
	}
	if len(counts) != 0 {
		t.Fatalf("expected empty counts, got %v", counts)
	}

	active, err := s.GetActiveBlocks()
	if err != nil {
		t.Fatalf("GetActiveBlocks (empty): %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("expected no active blocks, got %d", len(active))
	}

	// Create blocks of different types.
	if err := s.SetBlock("proj-a", "churn_block", time.Now().Add(5*time.Minute), "high failure rate"); err != nil {
		t.Fatalf("SetBlock churn: %v", err)
	}
	if err := s.SetBlock("proj-b", "churn_block", time.Now().Add(5*time.Minute), "high failure rate b"); err != nil {
		t.Fatalf("SetBlock churn 2: %v", err)
	}
	if err := s.SetBlock("bead-xyz", "quarantine", time.Now().Add(10*time.Minute), "consecutive failures"); err != nil {
		t.Fatalf("SetBlock quarantine: %v", err)
	}
	if err := s.SetBlockWithMetadata("system", "circuit_breaker", time.Now().Add(15*time.Minute), "gateway tripped", map[string]interface{}{
		"failures": 5,
	}); err != nil {
		t.Fatalf("SetBlock circuit_breaker: %v", err)
	}

	// Also insert an expired block — should NOT appear in active.
	if err := s.SetBlock("old-scope", "expired_type", time.Now().Add(-time.Minute), "already expired"); err != nil {
		t.Fatalf("SetBlock expired: %v", err)
	}

	// Verify counts by type.
	counts, err = s.GetBlockCountsByType()
	if err != nil {
		t.Fatalf("GetBlockCountsByType: %v", err)
	}
	if counts["churn_block"] != 2 {
		t.Fatalf("expected 2 churn_block, got %d", counts["churn_block"])
	}
	if counts["quarantine"] != 1 {
		t.Fatalf("expected 1 quarantine, got %d", counts["quarantine"])
	}
	if counts["circuit_breaker"] != 1 {
		t.Fatalf("expected 1 circuit_breaker, got %d", counts["circuit_breaker"])
	}
	if _, exists := counts["expired_type"]; exists {
		t.Fatal("expired block should not appear in counts")
	}

	// Verify active blocks list.
	active, err = s.GetActiveBlocks()
	if err != nil {
		t.Fatalf("GetActiveBlocks: %v", err)
	}
	if len(active) != 4 {
		t.Fatalf("expected 4 active blocks, got %d", len(active))
	}

	// Verify circuit breaker metadata round-trips.
	var foundCB bool
	for _, b := range active {
		if b.BlockType == "circuit_breaker" {
			foundCB = true
			if b.Metadata["failures"] == nil {
				t.Fatal("expected circuit_breaker metadata to include failures")
			}
		}
	}
	if !foundCB {
		t.Fatal("circuit_breaker block not found in active blocks")
	}

	// Remove a block and verify counts decrease.
	if err := s.RemoveBlock("proj-a", "churn_block"); err != nil {
		t.Fatalf("RemoveBlock: %v", err)
	}
	counts, err = s.GetBlockCountsByType()
	if err != nil {
		t.Fatalf("GetBlockCountsByType after remove: %v", err)
	}
	if counts["churn_block"] != 1 {
		t.Fatalf("expected 1 churn_block after removal, got %d", counts["churn_block"])
	}
}

func TestBeadValidatingRoundTrip(t *testing.T) {
	s := tempSafetyStore(t)
	beadID := "bead-validate"

	if err := s.SetBeadValidating(beadID, time.Now().Add(2*time.Minute)); err != nil {
		t.Fatalf("SetBeadValidating failed: %v", err)
	}

	validating, err := s.IsBeadValidating(beadID)
	if err != nil {
		t.Fatalf("IsBeadValidating failed: %v", err)
	}
	if !validating {
		t.Fatalf("expected bead to be validating")
	}

	if err := s.ClearBeadValidating(beadID); err != nil {
		t.Fatalf("ClearBeadValidating failed: %v", err)
	}

	validating, err = s.IsBeadValidating(beadID)
	if err != nil {
		t.Fatalf("IsBeadValidating after clear failed: %v", err)
	}
	if validating {
		t.Fatalf("expected bead to be not validating after clear")
	}
}
