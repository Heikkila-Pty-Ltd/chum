package store

import (
	"testing"
	"time"
)

func TestWasMorselDispatchedRecently(t *testing.T) {
	s := tempStore(t)
	morselID := "test-morsel"
	cooldown := 5 * time.Minute

	// Initially should not be recently dispatched
	recent, err := s.WasMorselDispatchedRecently(morselID, cooldown)
	if err != nil {
		t.Fatal(err)
	}
	if recent {
		t.Error("morsel should not be recently dispatched initially")
	}

	// Record a dispatch
	_, err = s.RecordDispatch(morselID, "test-project", "test-agent", "test-provider", "fast", 123, "", "test prompt", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// Now should be recently dispatched
	recent, err = s.WasMorselDispatchedRecently(morselID, cooldown)
	if err != nil {
		t.Fatal(err)
	}
	if !recent {
		t.Error("morsel should be recently dispatched after recording dispatch")
	}

	// With zero cooldown, should not be recent
	recent, err = s.WasMorselDispatchedRecently(morselID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if recent {
		t.Error("morsel should not be recent with zero cooldown")
	}
}

func TestWasMorselDispatchedRecently_MultipleMorsel(t *testing.T) {
	s := tempStore(t)
	cooldown := 5 * time.Minute

	// Record dispatch for morsel1
	_, err := s.RecordDispatch("morsel1", "test-project", "test-agent", "test-provider", "fast", 123, "", "test prompt", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// morsel1 should be recent
	recent, err := s.WasMorselDispatchedRecently("morsel1", cooldown)
	if err != nil {
		t.Fatal(err)
	}
	if !recent {
		t.Error("morsel1 should be recently dispatched")
	}

	// morsel2 should not be recent
	recent, err = s.WasMorselDispatchedRecently("morsel2", cooldown)
	if err != nil {
		t.Fatal(err)
	}
	if recent {
		t.Error("morsel2 should not be recently dispatched")
	}
}

func TestWasMorselDispatchedRecently_MultipleDispatches(t *testing.T) {
	s := tempStore(t)
	morselID := "test-morsel"
	cooldown := 5 * time.Minute

	// Record multiple dispatches for same morsel
	for i := 0; i < 3; i++ {
		_, err := s.RecordDispatch(morselID, "test-project", "test-agent", "test-provider", "fast", 123+i, "", "test prompt", "", "", "")
		if err != nil {
			t.Fatal(err)
		}
	}

	// Should still be recent (any dispatch within cooldown counts)
	recent, err := s.WasMorselDispatchedRecently(morselID, cooldown)
	if err != nil {
		t.Fatal(err)
	}
	if !recent {
		t.Error("morsel should be recently dispatched with multiple dispatches")
	}
}
