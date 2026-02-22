package temporal

import (
	"context"

	"go.temporal.io/sdk/activity"

	"github.com/antigravity-dev/chum/internal/store"
)

// EvolveGenomeActivity mutates a species genome based on a DoD outcome.
// Called after every task completion (success or failure).
// On success: approach added to patterns (DNA).
// On failure: approach added to antibodies. Auto-promotes to fossil at 3x.
func (a *Activities) EvolveGenomeActivity(ctx context.Context, species string, doDPassed bool, entry store.GenomeEntry) error {
	logger := activity.GetLogger(ctx)
	if a.Store == nil {
		return nil
	}

	if err := a.Store.EvolveGenome(species, doDPassed, entry); err != nil {
		logger.Warn(OctopusPrefix+" Genome evolution failed (non-fatal)", "species", species, "error", err)
		return nil // non-fatal — genome is enrichment, not critical path
	}

	outcome := "pattern"
	if !doDPassed {
		outcome = "antibody"
	}
	logger.Info(OctopusPrefix+" Genome evolved",
		"Species", species,
		"Outcome", outcome,
		"Pattern", entry.Pattern,
	)

	// Try to spread phages (horizontal gene transfer) on success
	if doDPassed {
		if err := a.Store.SpreadPhages(species); err != nil {
			logger.Warn(OctopusPrefix+" Phage spread failed (non-fatal)", "species", species, "error", err)
		}
	}

	return nil
}

// GetGenomeForPromptActivity retrieves the formatted genome for a species
// to inject into task prompts. Includes phage inheritance from parent species.
func (a *Activities) GetGenomeForPromptActivity(ctx context.Context, species string) (string, error) {
	if a.Store == nil {
		return "", nil
	}
	return a.Store.GetGenomeForPrompt(species)
}
