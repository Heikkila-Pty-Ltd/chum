package dispatch

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/store"
)

func tempStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open failed: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func testProviders() map[string]config.Provider {
	return map[string]config.Provider{
		"cerebras":     {Tier: "fast", Authed: false, Model: "llama"},
		"groq":         {Tier: "fast", Authed: false, Model: "llama"},
		"claude-max20": {Tier: "balanced", Authed: true, Model: "claude"},
		"google-pro":   {Tier: "premium", Authed: true, Model: "gemini"},
	}
}

func testTiers() config.Tiers {
	return config.Tiers{
		Fast:     []string{"cerebras", "groq"},
		Balanced: []string{"claude-max20"},
		Premium:  []string{"google-pro"},
	}
}

func TestCanDispatchAuthed_UnderCap(t *testing.T) {
	s := tempStore(t)
	rl := NewRateLimiter(s, config.RateLimits{Window5hCap: 20, WeeklyCap: 200, WeeklyHeadroomPct: 80})

	ok, reason := rl.CanDispatchAuthed()
	if !ok {
		t.Errorf("should be allowed: %s", reason)
	}
}

func TestCanDispatchAuthed_5hCapReached(t *testing.T) {
	s := tempStore(t)
	rl := NewRateLimiter(s, config.RateLimits{Window5hCap: 3, WeeklyCap: 200, WeeklyHeadroomPct: 80})

	for i := 0; i < 3; i++ {
		if _, err := s.RecordProviderUsage("claude", "agent", "bead"); err != nil {
			t.Fatalf("record provider usage: %v", err)
		}
	}

	ok, _ := rl.CanDispatchAuthed()
	if ok {
		t.Error("should be blocked by 5h cap")
	}
}

func TestCanDispatchAuthed_WeeklyCapReached(t *testing.T) {
	s := tempStore(t)
	rl := NewRateLimiter(s, config.RateLimits{Window5hCap: 100, WeeklyCap: 5, WeeklyHeadroomPct: 80})

	for i := 0; i < 5; i++ {
		if _, err := s.RecordProviderUsage("claude", "agent", "bead"); err != nil {
			t.Fatalf("record provider usage: %v", err)
		}
	}

	ok, _ := rl.CanDispatchAuthed()
	if ok {
		t.Error("should be blocked by weekly cap")
	}
}

func TestHeadroomWarning(t *testing.T) {
	s := tempStore(t)
	rl := NewRateLimiter(s, config.RateLimits{Window5hCap: 100, WeeklyCap: 10, WeeklyHeadroomPct: 80})

	// 8 out of 10 = 80% -> should trigger
	for i := 0; i < 8; i++ {
		if _, err := s.RecordProviderUsage("claude", "agent", "bead"); err != nil {
			t.Fatalf("record provider usage: %v", err)
		}
	}

	if !rl.IsInHeadroomWarning() {
		t.Error("should be in headroom warning at 80%")
	}

	pct := rl.WeeklyUsagePct()
	if pct != 80.0 {
		t.Errorf("WeeklyUsagePct = %f, want 80.0", pct)
	}
}

func TestPickProvider_FastTier(t *testing.T) {
	s := tempStore(t)
	rl := NewRateLimiter(s, config.RateLimits{Window5hCap: 0, WeeklyCap: 0, WeeklyHeadroomPct: 80})

	// Even with zero caps (authed blocked), fast tier should work (free providers)
	p := rl.PickProvider("fast", testProviders(), testTiers())
	if p == nil {
		t.Fatal("should return a free-tier provider")
	}
	if p.Authed {
		t.Error("fast tier should return free provider")
	}
}

func TestPickProvider_AuthedBlocked(t *testing.T) {
	s := tempStore(t)
	// Set caps to 0 to block all authed
	rl := NewRateLimiter(s, config.RateLimits{Window5hCap: 0, WeeklyCap: 0, WeeklyHeadroomPct: 80})

	p := rl.PickProvider("balanced", testProviders(), testTiers())
	if p != nil {
		t.Error("should return nil when authed is blocked")
	}
}

func TestPickProvider_AuthedAllowed(t *testing.T) {
	s := tempStore(t)
	rl := NewRateLimiter(s, config.RateLimits{Window5hCap: 20, WeeklyCap: 200, WeeklyHeadroomPct: 80})

	p := rl.PickProvider("balanced", testProviders(), testTiers())
	if p == nil {
		t.Fatal("should return an authed provider")
	}
	if !p.Authed {
		t.Error("balanced tier should return authed provider")
	}
}

func TestPickProvider_ParallelDispatchAttempts(t *testing.T) {
	s := tempStore(t)
	rl := NewRateLimiter(s, config.RateLimits{Window5hCap: 2, WeeklyCap: 2, WeeklyHeadroomPct: 80})

	var wg sync.WaitGroup
	type result struct {
		allowed bool
	}
	results := make(chan result, 2)

	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()

			p := rl.PickProvider("balanced", testProviders(), testTiers())
			if p == nil {
				results <- result{allowed: false}
				return
			}

			_, err := rl.RecordAuthedDispatch(p.Model, "agent", fmt.Sprintf("bead-%d", i))
			if err != nil {
				results <- result{allowed: false}
				return
			}

			results <- result{allowed: true}
		}()
	}

	wg.Wait()
	close(results)

	passed := 0
	for r := range results {
		if r.allowed {
			passed++
		}
	}

	if passed != 1 {
		t.Fatalf("expected exactly 1 dispatch attempt to be allowed, got %d", passed)
	}

	count, err := s.CountAuthedUsage5h()
	if err != nil {
		t.Fatalf("failed to read authed usage: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 recorded dispatch, got %d", count)
	}
}

func TestDowngradeTier(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"premium", "balanced"},
		{"balanced", "fast"},
		{"fast", ""},
	}
	for _, tt := range tests {
		got := DowngradeTier(tt.in)
		if got != tt.want {
			t.Errorf("DowngradeTier(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPickAndReserveProviderFromCandidates_FreeProvider(t *testing.T) {
	s := tempStore(t)
	rl := NewRateLimiter(s, config.RateLimits{Window5hCap: 0, WeeklyCap: 0, WeeklyHeadroomPct: 80})

	candidates := []string{"cerebras", "groq"}
	p, name, usageID, cleanup, err := rl.PickAndReserveProviderFromCandidates(candidates, testProviders(), nil, "agent", "bead")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("should return a free provider")
	}
	if p.Authed {
		t.Error("should return non-authed provider")
	}
	if name != "cerebras" && name != "groq" {
		t.Errorf("unexpected provider name: %s", name)
	}
	if usageID != 0 {
		t.Errorf("free provider should have usageID=0, got %d", usageID)
	}
	if cleanup != nil {
		t.Error("free provider should not have cleanup function")
	}
}

func TestPickAndReserveProviderFromCandidates_AuthedWithReservation(t *testing.T) {
	s := tempStore(t)
	rl := NewRateLimiter(s, config.RateLimits{Window5hCap: 20, WeeklyCap: 200, WeeklyHeadroomPct: 80})

	candidates := []string{"claude-max20"}
	p, name, usageID, cleanup, err := rl.PickAndReserveProviderFromCandidates(candidates, testProviders(), nil, "agent", "bead")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("should return an authed provider")
	}
	if !p.Authed {
		t.Error("should return authed provider")
	}
	if name != "claude-max20" {
		t.Errorf("unexpected provider name: %s", name)
	}
	if usageID == 0 {
		t.Error("authed provider should have non-zero usageID")
	}
	if cleanup == nil {
		t.Fatal("authed provider must have cleanup function")
	}

	// Verify reservation was recorded
	count, _ := s.CountAuthedUsage5h()
	if count != 1 {
		t.Errorf("expected 1 usage recorded, got %d", count)
	}

	// Call cleanup and verify rollback
	cleanup()
	count, _ = s.CountAuthedUsage5h()
	if count != 0 {
		t.Errorf("expected 0 usage after cleanup, got %d", count)
	}
}

func TestPickAndReserveProviderFromCandidates_ExcludeModel(t *testing.T) {
	s := tempStore(t)
	rl := NewRateLimiter(s, config.RateLimits{Window5hCap: 20, WeeklyCap: 200, WeeklyHeadroomPct: 80})

	// Exclude claude model
	excludeModels := map[string]bool{"claude": true}
	candidates := []string{"claude-max20", "google-pro"}

	p, name, _, cleanup, err := rl.PickAndReserveProviderFromCandidates(candidates, testProviders(), excludeModels, "agent", "bead")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("should return google-pro provider")
	}
	if name != "google-pro" {
		t.Errorf("expected google-pro, got %s", name)
	}
	if p.Model != "gemini" {
		t.Errorf("expected gemini model, got %s", p.Model)
	}
	if cleanup != nil {
		cleanup()
	}
}

func TestPickAndReserveProviderFromCandidates_RateLimitExceeded(t *testing.T) {
	s := tempStore(t)
	// Cap of 3 allows 2 successful reservations (double-check happens after insert)
	rl := NewRateLimiter(s, config.RateLimits{Window5hCap: 3, WeeklyCap: 200, WeeklyHeadroomPct: 80})

	candidates := []string{"claude-max20"}

	// First reservation should succeed (count: 0 -> 1)
	p1, _, _, cleanup1, err := rl.PickAndReserveProviderFromCandidates(candidates, testProviders(), nil, "agent1", "bead1")
	if err != nil {
		t.Fatalf("first reservation failed: %v", err)
	}
	if p1 == nil {
		t.Fatal("first reservation should succeed")
	}
	defer cleanup1()

	// Second reservation should succeed (count: 1 -> 2)
	p2, _, _, cleanup2, err := rl.PickAndReserveProviderFromCandidates(candidates, testProviders(), nil, "agent2", "bead2")
	if err != nil {
		t.Fatalf("second reservation failed: %v", err)
	}
	if p2 == nil {
		t.Fatal("second reservation should succeed")
	}
	defer cleanup2()

	// Third reservation should succeed (count: 2 -> 3, then double-check passes since 3 < 3 is false but 3 >= 3 is true, blocks)
	// Actually with cap=3, the third one will fail because after insert count=3, and 3 >= 3 blocks
	// So we can only get 2 successful with cap=3
	p3, name3, usageID3, cleanup3, err := rl.PickAndReserveProviderFromCandidates(candidates, testProviders(), nil, "agent3", "bead3")
	if err == nil {
		t.Error("third reservation should fail due to rate limit")
		if cleanup3 != nil {
			cleanup3()
		}
	}
	if p3 != nil {
		t.Error("should return nil when rate limited")
	}
	if name3 != "" {
		t.Error("should return empty name when rate limited")
	}
	if usageID3 != 0 {
		t.Error("should return zero usageID when rate limited")
	}
}

func TestPickAndReserveProviderFromCandidates_EmptyCandidates(t *testing.T) {
	s := tempStore(t)
	rl := NewRateLimiter(s, config.RateLimits{Window5hCap: 20, WeeklyCap: 200, WeeklyHeadroomPct: 80})

	candidates := []string{}
	p, name, usageID, cleanup, err := rl.PickAndReserveProviderFromCandidates(candidates, testProviders(), nil, "agent", "bead")
	if err != nil {
		t.Errorf("should not error on empty candidates: %v", err)
	}
	if p != nil {
		t.Error("should return nil for empty candidates")
	}
	if name != "" {
		t.Error("should return empty name for empty candidates")
	}
	if usageID != 0 {
		t.Error("should return zero usageID for empty candidates")
	}
	if cleanup != nil {
		t.Error("should not return cleanup for empty candidates")
	}
}
