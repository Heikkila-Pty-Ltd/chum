package dispatch

import (
	"testing"
	"time"
)

func TestBackoffDelay_ExponentialGrowth(t *testing.T) {
	base := 2 * time.Minute
	maxDelay := 30 * time.Minute

	tests := []struct {
		retries      int
		wantMinDelay time.Duration // minimum delay (no jitter)
		wantMaxDelay time.Duration // maximum delay (with 10% jitter)
	}{
		{0, 0, 0},                             // No retries = no delay
		{1, base, base + base/10},             // 2m to 2m12s
		{2, base * 2, base*2 + (base*2)/10},   // 4m to 4m24s
		{3, base * 4, base*4 + (base*4)/10},   // 8m to 8m48s
		{5, maxDelay, maxDelay + maxDelay/10}, // 32m would be capped at 30m, then jitter to 33m
	}

	for _, tt := range tests {
		// Run multiple times to account for jitter
		for i := 0; i < 10; i++ {
			got := BackoffDelay(tt.retries, base, maxDelay)

			if tt.retries == 0 {
				if got != 0 {
					t.Errorf("BackoffDelay(%d) = %v, want 0", tt.retries, got)
				}
				continue
			}

			// The delay should be within expected range (accounting for jitter)
			if got < tt.wantMinDelay || got > tt.wantMaxDelay {
				t.Errorf("BackoffDelay(%d) = %v, want between %v and %v",
					tt.retries, got, tt.wantMinDelay, tt.wantMaxDelay)
			}
		}
	}
}

func TestBackoffDelay_CapsAtMaxDelay(t *testing.T) {
	base := 2 * time.Minute
	maxDelay := 30 * time.Minute

	// Test with various high retry counts
	highRetryCounts := []int{5, 10, 20, 100}

	for _, retries := range highRetryCounts {
		for i := 0; i < 10; i++ { // Run multiple times for jitter
			got := BackoffDelay(retries, base, maxDelay)

			// With 10% jitter, max possible is maxDelay * 1.1
			maxPossible := maxDelay + maxDelay/10

			if got > maxPossible {
				t.Errorf("BackoffDelay(%d) = %v, exceeds max of %v",
					retries, got, maxPossible)
			}

			// Should be at least maxDelay (no negative jitter)
			if got < maxDelay {
				t.Errorf("BackoffDelay(%d) = %v, less than max of %v",
					retries, got, maxDelay)
			}
		}
	}
}

func TestBackoffDelay_VerifyExponentialFormula(t *testing.T) {
	base := 2 * time.Minute
	maxDelay := 60 * time.Minute

	// Test that the exponential formula base * 2^(retries-1) is followed
	// for retries that don't hit the cap
	tests := []struct {
		retries  int
		wantBase time.Duration
	}{
		{1, 2 * time.Minute},  // 2^0 = 1, so 2m
		{2, 4 * time.Minute},  // 2^1 = 2, so 4m
		{3, 8 * time.Minute},  // 2^2 = 4, so 8m
		{4, 16 * time.Minute}, // 2^3 = 8, so 16m
		{5, 32 * time.Minute}, // 2^4 = 16, so 32m
	}

	for _, tt := range tests {
		got := BackoffDelay(tt.retries, base, maxDelay)

		// With 10% jitter, the range is [base, base*1.1]
		minExpected := tt.wantBase
		maxExpected := tt.wantBase + tt.wantBase/10

		if got < minExpected || got > maxExpected {
			t.Errorf("BackoffDelay(%d) = %v, want between %v and %v",
				tt.retries, got, minExpected, maxExpected)
		}
	}
}

func TestShouldRetry_TooSoon(t *testing.T) {
	base := 2 * time.Minute
	maxDelay := 30 * time.Minute

	// For retry 1, backoff should be ~2 minutes
	// Test with last attempt 1 minute ago - should be too soon
	lastAttempt := time.Now().Add(-1 * time.Minute)

	if ShouldRetry(lastAttempt, 1, base, maxDelay) {
		t.Error("ShouldRetry should return false when not enough time has passed")
	}
}

func TestShouldRetry_EnoughTimePassed(t *testing.T) {
	base := 2 * time.Minute
	maxDelay := 30 * time.Minute

	// For retry 1, backoff is ~2 minutes (up to 2m12s with jitter)
	// Test with last attempt 3 minutes ago - should be enough
	lastAttempt := time.Now().Add(-3 * time.Minute)

	if !ShouldRetry(lastAttempt, 1, base, maxDelay) {
		t.Error("ShouldRetry should return true when enough time has passed")
	}
}

func TestShouldRetry_ZeroRetries(t *testing.T) {
	base := 2 * time.Minute
	maxDelay := 30 * time.Minute

	// With 0 retries, backoff is 0, so should always retry
	lastAttempt := time.Now().Add(-1 * time.Second)

	if !ShouldRetry(lastAttempt, 0, base, maxDelay) {
		t.Error("ShouldRetry should return true for 0 retries (no backoff required)")
	}
}

func TestShouldRetry_HighRetryCount(t *testing.T) {
	base := 2 * time.Minute
	maxDelay := 30 * time.Minute

	// For retry 10, backoff would be capped at 30m (plus jitter up to 33m)
	// Test with last attempt 35 minutes ago - should be enough
	lastAttempt := time.Now().Add(-35 * time.Minute)

	if !ShouldRetry(lastAttempt, 10, base, maxDelay) {
		t.Error("ShouldRetry should return true when enough time has passed for high retry count")
	}

	// Test with last attempt 20 minutes ago - should be too soon
	lastAttempt = time.Now().Add(-20 * time.Minute)

	if ShouldRetry(lastAttempt, 10, base, maxDelay) {
		t.Error("ShouldRetry should return false when not enough time has passed for high retry count")
	}
}

func TestShouldRetry_BoundaryConditions(t *testing.T) {
	base := 2 * time.Minute
	maxDelay := 30 * time.Minute

	// Test exactly at the boundary (this may be flaky due to jitter, but should mostly work)
	// For retry 2, base delay is 4 minutes (4m to 4m24s with jitter)
	// We'll test with 5 minutes which should definitely be enough
	lastAttempt := time.Now().Add(-5 * time.Minute)

	if !ShouldRetry(lastAttempt, 2, base, maxDelay) {
		t.Error("ShouldRetry should return true when time equals or exceeds backoff delay")
	}
}
