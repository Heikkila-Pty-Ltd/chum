package store

import (
	"testing"
	"time"
)

func TestWasBeadDispatchedRecently(t *testing.T) {
	s := tempStore(t)
	beadID := "test-bead"
	cooldown := 5 * time.Minute

	// Initially should not be recently dispatched
	recent, err := s.WasBeadDispatchedRecently(beadID, cooldown)
	if err != nil {
		t.Fatal(err)
	}
	if recent {
		t.Error("bead should not be recently dispatched initially")
	}

	// Record a dispatch
	_, err = s.RecordDispatch(beadID, "test-project", "test-agent", "test-provider", "fast", 123, "", "test prompt", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Now should be recently dispatched
	recent, err = s.WasBeadDispatchedRecently(beadID, cooldown)
	if err != nil {
		t.Fatal(err)
	}
	if !recent {
		t.Error("bead should be recently dispatched after recording dispatch")
	}

	// With zero cooldown, should not be recent
	recent, err = s.WasBeadDispatchedRecently(beadID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if recent {
		t.Error("bead should not be recent with zero cooldown")
	}
}

func TestWasBeadDispatchedRecently_MultipleBead(t *testing.T) {
	s := tempStore(t)
	cooldown := 5 * time.Minute

	// Record dispatch for bead1
	_, err := s.RecordDispatch("bead1", "test-project", "test-agent", "test-provider", "fast", 123, "", "test prompt", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// bead1 should be recent
	recent, err := s.WasBeadDispatchedRecently("bead1", cooldown)
	if err != nil {
		t.Fatal(err)
	}
	if !recent {
		t.Error("bead1 should be recently dispatched")
	}

	// bead2 should not be recent
	recent, err = s.WasBeadDispatchedRecently("bead2", cooldown)
	if err != nil {
		t.Fatal(err)
	}
	if recent {
		t.Error("bead2 should not be recently dispatched")
	}
}

func TestWasBeadDispatchedRecently_MultipleDispatches(t *testing.T) {
	s := tempStore(t)
	beadID := "test-bead"
	cooldown := 5 * time.Minute

	// Record multiple dispatches for same bead
	for i := 0; i < 3; i++ {
		_, err := s.RecordDispatch(beadID, "test-project", "test-agent", "test-provider", "fast", 123+i, "", "test prompt", "", "", "")
		if err != nil {
			t.Fatal(err)
		}
	}

	// Should still be recent (any dispatch within cooldown counts)
	recent, err := s.WasBeadDispatchedRecently(beadID, cooldown)
	if err != nil {
		t.Fatal(err)
	}
	if !recent {
		t.Error("bead should be recently dispatched with multiple dispatches")
	}
}
