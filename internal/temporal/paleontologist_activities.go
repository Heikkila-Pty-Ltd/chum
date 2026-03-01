package temporal

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"

	"github.com/antigravity-dev/chum/internal/store"
)

// ===== PALEONTOLOGIST ACTIVITIES =====

// AnalyzeProviderFitnessActivity queries dispatch outcomes and updates genome
// provider_genes where success rates have shifted significantly.
// Returns the number of genome mutations applied.
func (a *Activities) AnalyzeProviderFitnessActivity(ctx context.Context, req PaleontologistRequest) (int, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(PaleontologistPrefix + " Analyzing provider fitness")

	since := time.Now().UTC().Add(-time.Duration(req.LookbackH) * time.Hour)
	rates, err := a.Store.GetProviderSuccessRates(since)
	if err != nil {
		return 0, fmt.Errorf("get provider success rates: %w", err)
	}

	mutations := 0
	for _, rate := range rates {
		if rate.Successes+rate.Failures < 2 {
			continue // not enough data to be meaningful
		}

		genome, err := a.Store.GetGenome(rate.Species)
		if err != nil || genome == nil {
			continue
		}

		// Check if the provider's fitness has shifted significantly.
		// If success rate < 30% and the genome still prefers this provider, evolve.
		if rate.SuccessRate < 0.3 && rate.Failures >= 3 {
			entry := store.GenomeEntry{
				Pattern:     fmt.Sprintf("Provider %s has %.0f%% success rate on this species", rate.Provider, rate.SuccessRate*100),
				Alternative: "Consider using a different provider for this species type",
				Agent:       "paleontologist",
			}
			if err := a.Store.EvolveGenomeWithCost(rate.Species, false, entry, rate.Provider, rate.AvgCostUSD); err != nil {
				logger.Warn(PaleontologistPrefix+" Failed to evolve genome", "species", rate.Species, "error", err)
			} else {
				mutations++
				logger.Info(PaleontologistPrefix+" Provider fitness mutation applied",
					"Species", rate.Species, "Provider", rate.Provider, "SuccessRate", rate.SuccessRate)
			}
		}
	}

	return mutations, nil
}

// DiscoverAntibodiesActivity finds recurring UBS patterns and creates genome antibodies.
// Returns the number of antibodies created.
func (a *Activities) DiscoverAntibodiesActivity(ctx context.Context, req PaleontologistRequest) (int, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(PaleontologistPrefix + " Discovering antibodies from UBS patterns")

	patterns, err := a.Store.GetRepeatingUBSPatterns(3)
	if err != nil {
		return 0, fmt.Errorf("get repeating UBS patterns: %w", err)
	}

	antibodies := 0
	for _, p := range patterns {
		entry := store.GenomeEntry{
			Pattern:     fmt.Sprintf("UBS %s: %s in %s", p.RuleID, p.Message, p.FilePath),
			Alternative: fmt.Sprintf("Check %s — this pattern has appeared %d times", p.FilePath, p.Count),
			Count:       p.Count,
			Agent:       "paleontologist",
			Files:       []string{p.FilePath},
		}
		if err := a.Store.EvolveGenomeWithCost(p.Species, false, entry, "", 0); err != nil {
			logger.Warn(PaleontologistPrefix+" Failed to create antibody", "species", p.Species, "error", err)
		} else {
			antibodies++
			logger.Info(PaleontologistPrefix+" Antibody created",
				"Species", p.Species, "RuleID", p.RuleID, "Count", p.Count)
		}
	}

	return antibodies, nil
}

// ScanProteinCandidatesActivity finds species ready for proteinisation.
// Returns the number of proteins nominated.
func (a *Activities) ScanProteinCandidatesActivity(ctx context.Context, req PaleontologistRequest) (int, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(PaleontologistPrefix + " Scanning for proteinisation candidates")

	candidates, err := a.Store.GetProteinCandidates(5)
	if err != nil {
		return 0, fmt.Errorf("get protein candidates: %w", err)
	}

	nominated := 0
	for _, c := range candidates {
		if c.HasProtein {
			continue // already proteinised
		}

		logger.Info(PaleontologistPrefix+" Protein candidate found",
			"Species", c.Species, "Successes", c.TotalSuccesses,
			"TopPattern", c.TopPattern, "FittestProvider", c.FittestProvider)
		nominated++
		// Note: actual protein creation requires the CalcifyPatternActivity
		// from the learner workflow. We log the nomination here for the
		// strategic groomer to pick up and action.
	}

	return nominated, nil
}

// AuditSpeciesHealthActivity checks genomes for anomalies and takes action:
// escalating stuck species/hibernators and bootstrapping orphans.
// Returns the number of species audited.
func (a *Activities) AuditSpeciesHealthActivity(ctx context.Context, req PaleontologistRequest) (int, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(PaleontologistPrefix + " Auditing species health")

	audited := 0

	// 1. Check for stale hibernators (hibernating > 24h)
	hibernators, err := a.Store.GetStaleHibernators(24 * time.Hour)
	if err != nil {
		logger.Warn(PaleontologistPrefix+" Failed to get stale hibernators", "error", err)
	} else {
		for _, h := range hibernators {
			audited++
			logger.Info(PaleontologistPrefix+" Stale hibernator detected",
				"Species", h.Species, "Generation", h.Generation,
				"Issue", h.Issue, "Antibodies", h.AntibodyCount, "LastEvolved", h.LastEvolved)

			// Only send Matrix notification once per species per 24h to avoid spam.
			eventType := "stale_hibernator_alerted"
			if a.Sender != nil && !a.Store.HasRecentHealthEvent(eventType, h.Species, 24*time.Hour) {
				targetRoom := a.AdminRoom
				if targetRoom == "" {
					targetRoom = a.DefaultRoom
				}
				msg := fmt.Sprintf("⚠️ **Stale Hibernator Detected**\nSpecies `%s` has been hibernating for >24h. It may need higher-level LLM intervention or manual review.", h.Species)
				_ = a.Sender.SendMessage(ctx, targetRoom, msg)
				_ = a.Store.RecordHealthEvent(eventType, h.Species)
			}
		}
	}

	// 2. Check for stuck species (high antibodies, 0 fossils)
	stuck, err := a.Store.GetStuckSpecies(10) // 10 antibodies threshold
	if err != nil {
		logger.Warn(PaleontologistPrefix+" Failed to get stuck species", "error", err)
	} else {
		for _, s := range stuck {
			audited++
			logger.Info(PaleontologistPrefix+" Stuck species detected",
				"Species", s.Species, "Antibodies", s.AntibodyCount)

			// Only send Matrix notification once per species per 24h to avoid spam.
			eventType := "stuck_species_alerted"
			if a.Sender != nil && !a.Store.HasRecentHealthEvent(eventType, s.Species, 24*time.Hour) {
				targetRoom := a.AdminRoom
				if targetRoom == "" {
					targetRoom = a.DefaultRoom
				}
				msg := fmt.Sprintf("⚠️ **Stuck Species Detected**\nSpecies `%s` has %d antibodies but 0 fossils. The agent keeps failing but cannot consolidate the learnings. Please review the failures.", s.Species, s.AntibodyCount)
				_ = a.Sender.SendMessage(ctx, targetRoom, msg)
				_ = a.Store.RecordHealthEvent(eventType, s.Species)
			}
		}
	}

	// 3. Check for species without genomes (orphans)
	orphans, err := a.Store.GetSpeciesWithoutGenome()
	if err != nil {
		logger.Warn(PaleontologistPrefix+" Failed to get orphan species", "error", err)
	} else {
		for _, species := range orphans {
			audited++
			logger.Info(PaleontologistPrefix+" Bootstrapping empty genome for orphan species", "Species", species)
			if err := a.Store.CreateEmptyGenome(species); err != nil {
				logger.Warn(PaleontologistPrefix+" Failed to bootstrap genome", "species", species, "error", err)
			}
		}
	}

	return audited, nil
}

// AnalyzeCostTrendsActivity compares cost-per-success between current and previous
// time windows to detect cost spikes.
// Returns the number of cost alerts generated.
func (a *Activities) AnalyzeCostTrendsActivity(ctx context.Context, req PaleontologistRequest) (int, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(PaleontologistPrefix + " Analyzing cost trends")

	current, previous, err := a.Store.GetCostTrends(req.LookbackH)
	if err != nil {
		return 0, fmt.Errorf("get cost trends: %w", err)
	}

	// Build map of previous costs for comparison
	prevCosts := make(map[string]float64)
	for _, t := range previous {
		key := t.Provider + "/" + t.Agent
		prevCosts[key] = t.CostPerSuccess
	}

	alerts := 0
	for _, t := range current {
		key := t.Provider + "/" + t.Agent
		prevCost, hasPrev := prevCosts[key]
		if !hasPrev || prevCost <= 0 {
			continue
		}
		// Alert if cost-per-success increased by > 50%
		if t.CostPerSuccess > prevCost*1.5 {
			alerts++
			logger.Warn(PaleontologistPrefix+" Cost spike detected",
				"Provider", t.Provider, "Agent", t.Agent,
				"PrevCostPerSuccess", prevCost, "CurrentCostPerSuccess", t.CostPerSuccess,
				"Increase", fmt.Sprintf("%.0f%%", (t.CostPerSuccess/prevCost-1)*100))
		}
	}

	return alerts, nil
}

// DiscoverRecurringDoDFailuresActivity detects DoD failure patterns that appear
// across multiple dispatches and raises alerts for systemic issues.
// Returns the number of recurring failure patterns detected.
func (a *Activities) DiscoverRecurringDoDFailuresActivity(ctx context.Context, req PaleontologistRequest) (int, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(PaleontologistPrefix + " Discovering recurring DoD failures")

	since := time.Now().UTC().Add(-time.Duration(req.LookbackH) * time.Hour)
	patterns, err := a.Store.GetRecurringDoDFailures(3, since) // threshold: 3+ occurrences
	if err != nil {
		return 0, fmt.Errorf("get recurring DoD failures: %w", err)
	}

	detected := 0
	for _, p := range patterns {
		detected++

		// Truncate failure message for logging
		failureSnippet := p.Failures
		if len(failureSnippet) > 200 {
			failureSnippet = failureSnippet[:200] + "..."
		}

		logger.Warn(PaleontologistPrefix+" RECURRING DOD FAILURE DETECTED",
			"Count", p.Count,
			"Projects", strings.Join(p.Projects, ", "),
			"MorselIDs", strings.Join(p.MorselIDs, ", "),
			"FirstSeen", p.FirstSeenAt,
			"LastSeen", p.LastSeenAt,
			"Failures", failureSnippet)

		// Send Matrix alert for high-frequency failures (5+ occurrences)
		if p.Count >= 5 && a.Sender != nil {
			targetRoom := a.AdminRoom
			if targetRoom == "" {
				targetRoom = a.DefaultRoom
			}

			// Build affected morsels list (max 5 for brevity)
			morselList := p.MorselIDs
			if len(morselList) > 5 {
				morselList = append(morselList[:5], "...")
			}

			msg := fmt.Sprintf(
				"🚨 **SYSTEMIC BUILD FAILURE DETECTED** 🚨\n\n"+
					"**Pattern:** Same DoD failure across **%d morsels** in the last %dh\n\n"+
					"**Affected projects:** %s\n\n"+
					"**Affected morsels:**\n%s\n\n"+
					"**Failure:**\n```\n%s\n```\n\n"+
					"**Action required:** This is a systemic issue, not an individual morsel problem. "+
					"Investigate the root cause (e.g., broken dependency, missing env var, infrastructure issue) "+
					"before dispatching more morsels. Fix the underlying issue to unblock the pipeline.",
				p.Count,
				req.LookbackH,
				strings.Join(p.Projects, ", "),
				"- `"+strings.Join(morselList, "`\n- `")+"`",
				truncate(p.Failures, 500),
			)

			if sendErr := a.Sender.SendMessage(ctx, targetRoom, msg); sendErr != nil {
				logger.Warn(PaleontologistPrefix+" Failed to send recurring failure alert", "error", sendErr)
			} else {
				logger.Info(PaleontologistPrefix+" Recurring failure alert sent to Matrix",
					"count", p.Count, "projects", len(p.Projects))
			}
		}

		// Record health event for visibility in observability tools
		if a.Store != nil {
			details := fmt.Sprintf("Recurring DoD failure (%d occurrences): %s", p.Count, truncate(p.Failures, 200))
			if recErr := a.Store.RecordHealthEvent("recurring_dod_failure", details); recErr != nil {
				logger.Warn(PaleontologistPrefix+" Failed to record health event", "error", recErr)
			}
		}
	}

	return detected, nil
}

// AnalyzeFailureRateTrendsActivity compares current vs previous failure rates
// and maintains a "doomsday clock" that escalates warnings to Hex.
// NOT a hard gate - Hex decides whether to pause based on the clock.
func (a *Activities) AnalyzeFailureRateTrendsActivity(ctx context.Context, req PaleontologistRequest) error {
	logger := activity.GetLogger(ctx)
	logger.Info(PaleontologistPrefix + " Analyzing failure rate trends")

	// Analyze overall failure rate delta (all projects)
	delta, err := a.Store.GetFailureRateDelta("", req.LookbackH)
	if err != nil {
		return fmt.Errorf("get overall failure rate delta: %w", err)
	}

	logger.Info(PaleontologistPrefix+" Failure rate trend",
		"Trend", delta.Trend,
		"CurrentRate", fmt.Sprintf("%.1f%%", delta.CurrentRate),
		"PreviousRate", fmt.Sprintf("%.1f%%", delta.PreviousRate),
		"Delta", fmt.Sprintf("%+.1f%%", delta.Delta),
		"CurrentDispatches", delta.CurrentDispatches,
		"PreviousDispatches", delta.PreviousDispatches)

	// Record health event FIRST (feeds doomsday clock)
	if delta.Trend != "stable" {
		details := fmt.Sprintf("Failure rate %s: %.1f%% → %.1f%% (%+.1f%% points)",
			delta.Trend, delta.PreviousRate, delta.CurrentRate, delta.Delta)
		if recErr := a.Store.RecordHealthEvent("failure_rate_"+delta.Trend, details); recErr != nil {
			logger.Warn(PaleontologistPrefix+" Failed to record health event", "error", recErr)
		}
	}

	// Calculate doomsday clock (system health score)
	healthScore, err := a.Store.GetSystemHealthScore()
	if err != nil {
		logger.Warn(PaleontologistPrefix+" Failed to calculate health score", "error", err)
		healthScore = &store.SystemHealthScore{
			Score:          50,
			AlertLevel:     "yellow",
			MeteorStatus:   "Unknown",
			MeteorDistance: "🌍...........☄️",
		}
	}

	logger.Info(PaleontologistPrefix+" Meteor tracking",
		"Score", healthScore.Score,
		"AlertLevel", healthScore.AlertLevel,
		"MeteorStatus", healthScore.MeteorStatus,
		"DegradationStreak", healthScore.DegradationStreak,
		"ImprovementStreak", healthScore.ImprovementStreak)

	// Send to Hex via Matrix with escalating urgency
	if a.Sender != nil && delta.CurrentDispatches >= 10 {
		targetRoom := a.AdminRoom // Always send to Hex
		if targetRoom == "" {
			targetRoom = a.DefaultRoom
		}

		var emoji, header string
		switch healthScore.AlertLevel {
		case "green":
			emoji = "🌍"
			header = "**ECOSYSTEM THRIVING**"
		case "yellow":
			emoji = "☄️"
			header = "**METEOR DETECTED - Approaching**"
		case "orange":
			emoji = "⚠️"
			header = "**METEOR INCOMING - Impact Risk**"
		case "red":
			if healthScore.Score < 15 {
				emoji = "💥"
				header = "**☠️ EXTINCTION EVENT IN PROGRESS**"
			} else {
				emoji = "🚨"
				header = "**METEOR NEAR IMPACT - Critical**"
			}
		}

		msg := fmt.Sprintf(
			"%s %s — Meteor Tracking Report\n\n"+
				"☄️ **Meteor Status:** %s\n"+
				"📏 **Distance:** `%s`\n"+
				"📊 **Ecosystem Health:** %d/100 (%s)\n"+
				"📉 **Degradation Streak:** %d consecutive impact warnings\n"+
				"📈 **Recovery Streak:** %d consecutive improvements\n\n"+
				"**Current Species Mortality Rate:** %.1f%% (%d extinct / %d organisms)\n"+
				"**Previous Mortality Rate:** %.1f%% (%d extinct / %d organisms)\n"+
				"**Atmospheric Change:** %+.1f%% points (%s)\n\n"+
				"**Paleontologist Assessment for Hex:**\n%s\n\n"+
				"**Analysis Window:** Last %dh vs previous %dh\n"+
				"**Next Scan:** 30 minutes",
			emoji, header,
			healthScore.MeteorStatus,
			healthScore.MeteorDistance,
			healthScore.Score, healthScore.AlertLevel,
			healthScore.DegradationStreak,
			healthScore.ImprovementStreak,
			delta.CurrentRate,
			int(float64(delta.CurrentDispatches)*(delta.CurrentRate/100)),
			delta.CurrentDispatches,
			delta.PreviousRate,
			int(float64(delta.PreviousDispatches)*(delta.PreviousRate/100)),
			delta.PreviousDispatches,
			delta.Delta,
			delta.Trend,
			healthScore.Recommendation,
			req.LookbackH, req.LookbackH,
		)

		if sendErr := a.Sender.SendMessage(ctx, targetRoom, msg); sendErr != nil {
			logger.Warn(PaleontologistPrefix+" Failed to send doomsday clock report to Hex", "error", sendErr)
		} else {
			logger.Info(PaleontologistPrefix+" Meteor tracking report sent to Hex",
				"AlertLevel", healthScore.AlertLevel,
				"MeteorStatus", healthScore.MeteorStatus,
				"Score", healthScore.Score)
		}
	}

	return nil
}

// RecordPaleontologyRunActivity records a paleontologist analysis run in the audit table.
func (a *Activities) RecordPaleontologyRunActivity(ctx context.Context,
	antibodies, genes, proteins, audited, alerts, recurringFailures int, summary string) error {
	return a.Store.RecordPaleontologyRun(store.PaleontologyRunResult{
		AntibodiesDiscovered: antibodies,
		GenesMutated:         genes,
		ProteinsNominated:    proteins,
		SpeciesAudited:       audited,
		CostAlerts:           alerts,
		RecurringFailures:    recurringFailures,
		Summary:              summary,
	})
}
