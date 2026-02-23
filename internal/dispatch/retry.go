package dispatch

import (
	"math"
	"math/rand"
	"strings"
	"time"
)

// RetryPolicy controls how a dispatch should be retried.
type RetryPolicy struct {
	MaxRetries    int
	InitialDelay  time.Duration
	BackoffFactor float64
	MaxDelay      time.Duration
	EscalateAfter int
}

// DefaultPolicy returns a sane default retry policy for stuck dispatch recovery.
func DefaultPolicy() RetryPolicy {
	return RetryPolicy{
		MaxRetries:    3,
		InitialDelay:  5 * time.Minute,
		BackoffFactor: 2.0,
		MaxDelay:      30 * time.Minute,
		EscalateAfter: 2,
	}
}

// NextRetry calculates the next delay, target tier, and whether to retry.
// attempt is the current retry count for this dispatch.
func (p RetryPolicy) NextRetry(attempt int, currentTier string) (delay time.Duration, tier string, shouldRetry bool) {
	attempt = maxInt(0, attempt)
	tier = normalizeTier(currentTier)

	if p.MaxRetries <= attempt {
		return 0, tier, false
	}

	delay = backoffDelayWithFactor(attempt+1, p.InitialDelay, p.MaxDelay, p.BackoffFactor)
	if shouldEscalateTier(p.EscalateAfter, attempt) {
		tier = escalateTier(tier)
	}

	return delay, tier, true
}

func shouldEscalateTier(escalateAfter, attempt int) bool {
	return escalateAfter > 0 && attempt > 0 && attempt%escalateAfter == 0
}

func normalizeTier(tier string) string {
	return strings.ToLower(strings.TrimSpace(tier))
}

func escalateTier(tier string) string {
	switch tier {
	case "fast":
		return "balanced"
	case "balanced":
		return "premium"
	default:
		return tier
	}
}

// backoffDelayWithFactor returns duration * factor^(retries-1) capped at maxDelay with jitter.
func backoffDelayWithFactor(retries int, base, maxDelay time.Duration, factor float64) time.Duration {
	if retries <= 0 || base <= 0 {
		return 0
	}
	if factor < 1.0 {
		factor = 1.0
	}

	backoff := float64(base) * math.Pow(factor, float64(retries-1))
	if math.IsNaN(backoff) || math.IsInf(backoff, 0) {
		if maxDelay > 0 {
			backoff = float64(maxDelay)
		} else {
			backoff = float64(base)
		}
	}
	if maxDelay > 0 && backoff > float64(maxDelay) {
		backoff = float64(maxDelay)
	}
	if backoff < float64(base) {
		backoff = float64(base)
	}

	jitter := 1.0 + (rand.Float64() * 0.1)
	return time.Duration(backoff * jitter)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
