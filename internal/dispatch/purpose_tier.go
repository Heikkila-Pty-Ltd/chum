package dispatch

import (
	"strings"

	"github.com/antigravity-dev/chum/internal/config"
)

const (
	ScrumPurposePlanning  = "planning"
	ScrumPurposeReview    = "review"
	ScrumPurposeReporting = "reporting"
)

// PreferredTiersForPurpose returns the preferred tier order for a scrum purpose.
func PreferredTiersForPurpose(purpose string) []string {
	switch strings.ToLower(strings.TrimSpace(purpose)) {
	case ScrumPurposePlanning:
		return []string{"premium", "balanced", "fast"}
	case ScrumPurposeReview:
		return []string{"balanced", "premium", "fast"}
	case ScrumPurposeReporting:
		return []string{"fast", "balanced", "premium"}
	default:
		return []string{"balanced", "fast", "premium"}
	}
}

// SelectProviderForPurpose picks the first configured provider model by purpose tier intent.
// Returns ("","") when no provider can be resolved from configured tier lists.
func SelectProviderForPurpose(cfg *config.Config, purpose string) (model string, tier string) {
	if cfg == nil {
		return "", ""
	}

	for _, candidateTier := range PreferredTiersForPurpose(purpose) {
		var names []string
		switch candidateTier {
		case "fast":
			names = cfg.Tiers.Fast
		case "balanced":
			names = cfg.Tiers.Balanced
		case "premium":
			names = cfg.Tiers.Premium
		default:
			continue
		}

		for _, name := range names {
			p, ok := cfg.Providers[name]
			if !ok {
				continue
			}
			m := strings.TrimSpace(p.Model)
			if m == "" {
				continue
			}
			return m, candidateTier
		}
	}

	return "", ""
}
