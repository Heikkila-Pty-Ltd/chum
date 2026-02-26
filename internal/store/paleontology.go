package store

import (
	"encoding/json"
	"fmt"
	"time"
)

// ProviderSuccessRate holds aggregated success/failure data for a provider+species pair.
type ProviderSuccessRate struct {
	Provider    string
	Species     string
	Successes   int
	Failures    int
	TotalCost   float64
	AvgCostUSD  float64
	SuccessRate float64
}

// RepeatingUBSPattern holds a UBS pattern that appears across multiple dispatches.
type RepeatingUBSPattern struct {
	RuleID    string
	Species   string
	Severity  string
	Message   string
	FilePath  string
	Count     int
	Providers []string // which providers triggered this pattern
}

// ProteinCandidate holds a species that may be ready for proteinisation.
type ProteinCandidate struct {
	Species            string
	ConsecutiveSuccess int
	TotalSuccesses     int
	TopPattern         string
	FittestProvider    string
	AvgCostUSD         float64
	HasProtein         bool // already has a protein
}

// SpeciesHealthReport holds anomaly data for a species genome.
type SpeciesHealthReport struct {
	Species       string
	Generation    int
	Successes     int
	Failures      int
	AntibodyCount int
	FossilCount   int
	PatternCount  int
	Hibernating   bool
	LastEvolved   *time.Time
	Issue         string // description of the anomaly
}

// CostTrend holds cost-per-success data for a time window.
type CostTrend struct {
	Provider       string
	Agent          string
	WindowStart    time.Time
	WindowEnd      time.Time
	TotalCost      float64
	TotalSuccesses int
	CostPerSuccess float64
}

// RecurringDoDFailure holds a DoD failure pattern that appears across multiple dispatches.
type RecurringDoDFailure struct {
	Failures    string   // the failure text
	Count       int      // how many times this exact failure appeared
	Projects    []string // which projects are affected
	MorselIDs   []string // which morsels failed with this error
	FirstSeenAt string   // when this failure first appeared
	LastSeenAt  string   // when this failure last appeared
}

// FailureRateTrend holds DoD failure rate metrics for a time window.
type FailureRateTrend struct {
	Project         string
	WindowStart     time.Time
	WindowEnd       time.Time
	TotalDispatches int
	DoDPassed       int
	DoDFailed       int
	FailureRate     float64 // percentage (0-100)
}

// FailureRateDelta compares failure rates between two time windows.
type FailureRateDelta struct {
	Project            string
	CurrentRate        float64
	PreviousRate       float64
	Delta              float64 // positive = getting worse, negative = improving
	CurrentDispatches  int
	PreviousDispatches int
	Trend              string // "improving", "degrading", "stable"
}

// SystemHealthScore represents the meteor/extinction event risk state.
type SystemHealthScore struct {
	Score             int       // 0-100 (0 = impact, 100 = distant)
	DegradationStreak int       // consecutive degrading periods
	ImprovementStreak int       // consecutive improving periods
	LastTrendChange   time.Time // when trend last changed
	MeteorStatus      string    // "Distant", "Approaching", "Incoming", "IMPACT"
	MeteorDistance    string    // visual representation
	AlertLevel        string    // "green", "yellow", "orange", "red"
	Recommendation    string    // what Hex should consider
}

// PaleontologyRunResult holds the summary of a paleontologist analysis run.
type PaleontologyRunResult struct {
	AntibodiesDiscovered int
	GenesMutated         int
	ProteinsNominated    int
	SpeciesAudited       int
	CostAlerts           int
	RecurringFailures    int
	Summary              string
}

// GetProviderSuccessRates returns aggregated success/failure rates by provider+species
// for dispatches since the given time.
func (s *Store) GetProviderSuccessRates(since time.Time) ([]ProviderSuccessRate, error) {
	rows, err := s.db.Query(`
		SELECT
			d.provider,
			COALESCE(d.morsel_id, '') as species,
			SUM(CASE WHEN d.status = 'completed' THEN 1 ELSE 0 END) as successes,
			SUM(CASE WHEN d.status != 'completed' AND d.status != 'running' THEN 1 ELSE 0 END) as failures,
			SUM(d.cost_usd) as total_cost,
			AVG(d.cost_usd) as avg_cost
		FROM dispatches d
		WHERE d.dispatched_at >= ?
		GROUP BY d.provider, d.morsel_id
		HAVING (successes + failures) > 0
		ORDER BY failures DESC, successes DESC
	`, since.Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, fmt.Errorf("query provider success rates: %w", err)
	}
	defer rows.Close()

	var results []ProviderSuccessRate
	for rows.Next() {
		var r ProviderSuccessRate
		if err := rows.Scan(&r.Provider, &r.Species, &r.Successes, &r.Failures, &r.TotalCost, &r.AvgCostUSD); err != nil {
			return nil, fmt.Errorf("scan provider success rate: %w", err)
		}
		total := r.Successes + r.Failures
		if total > 0 {
			r.SuccessRate = float64(r.Successes) / float64(total)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// GetRepeatingUBSPatterns finds UBS patterns that appear across multiple dispatches.
func (s *Store) GetRepeatingUBSPatterns(minCount int) ([]RepeatingUBSPattern, error) {
	rows, err := s.db.Query(`
		SELECT
			rule_id,
			species,
			severity,
			message,
			file_path,
			COUNT(*) as cnt,
			GROUP_CONCAT(DISTINCT provider) as providers
		FROM ubs_findings
		WHERE fixed = 0
		GROUP BY rule_id, species, file_path
		HAVING cnt >= ?
		ORDER BY cnt DESC
		LIMIT 50
	`, minCount)
	if err != nil {
		return nil, fmt.Errorf("query repeating UBS patterns: %w", err)
	}
	defer rows.Close()

	var results []RepeatingUBSPattern
	for rows.Next() {
		var r RepeatingUBSPattern
		var providers string
		if err := rows.Scan(&r.RuleID, &r.Species, &r.Severity, &r.Message, &r.FilePath, &r.Count, &providers); err != nil {
			return nil, fmt.Errorf("scan UBS pattern: %w", err)
		}
		if providers != "" {
			r.Providers = append(r.Providers, splitCSV(providers)...)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// GetProteinCandidates finds species with high consecutive success that may be
// ready for proteinisation.
func (s *Store) GetProteinCandidates(minSuccesses int) ([]ProteinCandidate, error) {
	rows, err := s.db.Query(`
		SELECT
			g.species,
			g.successes,
			g.patterns,
			g.provider_genes,
			COALESCE(p.id, '') as protein_id
		FROM genomes g
		LEFT JOIN proteins p ON p.id = g.species
		WHERE g.successes >= ?
		  AND g.hibernating = 0
		ORDER BY g.successes DESC
		LIMIT 20
	`, minSuccesses)
	if err != nil {
		return nil, fmt.Errorf("query protein candidates: %w", err)
	}
	defer rows.Close()

	var results []ProteinCandidate
	for rows.Next() {
		var c ProteinCandidate
		var patternsJSON, providerGenesJSON, proteinID string
		if err := rows.Scan(&c.Species, &c.TotalSuccesses, &patternsJSON, &providerGenesJSON, &proteinID); err != nil {
			return nil, fmt.Errorf("scan protein candidate: %w", err)
		}
		c.HasProtein = proteinID != ""
		c.ConsecutiveSuccess = c.TotalSuccesses // approximate; exact consecutive tracking would need dispatch history

		// Extract top pattern
		var patterns []GenomeEntry
		if err := parseJSON(patternsJSON, &patterns); err == nil && len(patterns) > 0 {
			c.TopPattern = patterns[0].Pattern
		}

		// Extract fittest provider
		var genes []ProviderGene
		if err := parseJSON(providerGenesJSON, &genes); err == nil && len(genes) > 0 {
			best := genes[0]
			for _, g := range genes[1:] {
				if g.Fitness() > best.Fitness() {
					best = g
				}
			}
			c.FittestProvider = best.Provider
			if best.Successes > 0 {
				c.AvgCostUSD = best.TotalCost / float64(best.Successes)
			}
		}
		results = append(results, c)
	}
	return results, rows.Err()
}

// GetSpeciesWithoutGenome finds morsel_ids from dispatches that have no genome entry.
func (s *Store) GetSpeciesWithoutGenome() ([]string, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT d.morsel_id
		FROM dispatches d
		LEFT JOIN genomes g ON g.species = d.morsel_id
		WHERE g.species IS NULL
		  AND d.status IN ('completed', 'escalated')
		ORDER BY d.dispatched_at DESC
		LIMIT 20
	`)
	if err != nil {
		return nil, fmt.Errorf("query species without genome: %w", err)
	}
	defer rows.Close()

	var species []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, fmt.Errorf("scan species: %w", err)
		}
		species = append(species, s)
	}
	return species, rows.Err()
}

// GetCostTrends compares cost-per-success between two time windows.
func (s *Store) GetCostTrends(windowHours int) (current []CostTrend, previous []CostTrend, err error) {
	now := time.Now().UTC()
	windowDur := time.Duration(windowHours) * time.Hour
	currentStart := now.Add(-windowDur)
	previousStart := now.Add(-2 * windowDur)

	query := func(start, end time.Time) ([]CostTrend, error) {
		rows, err := s.db.Query(`
			SELECT
				provider,
				agent_id,
				SUM(cost_usd) as total_cost,
				SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END) as successes
			FROM dispatches
			WHERE dispatched_at >= ? AND dispatched_at < ?
			GROUP BY provider, agent_id
			HAVING successes > 0
		`, start.Format("2006-01-02 15:04:05"), end.Format("2006-01-02 15:04:05"))
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		var trends []CostTrend
		for rows.Next() {
			var t CostTrend
			if err := rows.Scan(&t.Provider, &t.Agent, &t.TotalCost, &t.TotalSuccesses); err != nil {
				return nil, err
			}
			t.WindowStart = start
			t.WindowEnd = end
			if t.TotalSuccesses > 0 {
				t.CostPerSuccess = t.TotalCost / float64(t.TotalSuccesses)
			}
			trends = append(trends, t)
		}
		return trends, rows.Err()
	}

	current, err = query(currentStart, now)
	if err != nil {
		return nil, nil, fmt.Errorf("query current window: %w", err)
	}
	previous, err = query(previousStart, currentStart)
	if err != nil {
		return nil, nil, fmt.Errorf("query previous window: %w", err)
	}
	return current, previous, nil
}

// GetStaleHibernators returns species that have been hibernating for longer than the given duration.
func (s *Store) GetStaleHibernators(olderThan time.Duration) ([]SpeciesHealthReport, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	rows, err := s.db.Query(`
		SELECT species, generation, successes, failures, patterns, antibodies, fossils, last_evolved
		FROM genomes
		WHERE hibernating = 1 AND last_evolved < ?
		ORDER BY last_evolved ASC
		LIMIT 20
	`, cutoff.Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, fmt.Errorf("query stale hibernators: %w", err)
	}
	defer rows.Close()

	var results []SpeciesHealthReport
	for rows.Next() {
		var r SpeciesHealthReport
		var patternsJSON, antibodiesJSON, fossilsJSON string
		var lastEvolved *string
		if err := rows.Scan(&r.Species, &r.Generation, &r.Successes, &r.Failures,
			&patternsJSON, &antibodiesJSON, &fossilsJSON, &lastEvolved); err != nil {
			return nil, fmt.Errorf("scan hibernator: %w", err)
		}
		r.Hibernating = true
		r.Issue = "Stale hibernator — may be ready to retry"

		var patterns, antibodies, fossils []GenomeEntry
		_ = parseJSON(patternsJSON, &patterns)
		_ = parseJSON(antibodiesJSON, &antibodies)
		_ = parseJSON(fossilsJSON, &fossils)
		r.PatternCount = len(patterns)
		r.AntibodyCount = len(antibodies)
		r.FossilCount = len(fossils)

		if lastEvolved != nil {
			if t, err := time.Parse("2006-01-02 15:04:05", *lastEvolved); err == nil {
				r.LastEvolved = &t
			}
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// GetStuckSpecies returns species that have a high number of antibodies but zero fossils.
// This indicates the system keeps failing but isn't consolidating the failures into extinct approaches.
func (s *Store) GetStuckSpecies(minAntibodies int) ([]SpeciesHealthReport, error) {
	rows, err := s.db.Query(`
		SELECT species, generation, successes, failures, patterns, antibodies, fossils, last_evolved
		FROM genomes
		WHERE hibernating = 0
	`)
	if err != nil {
		return nil, fmt.Errorf("query stuck species: %w", err)
	}
	defer rows.Close()

	var results []SpeciesHealthReport
	for rows.Next() {
		var r SpeciesHealthReport
		var patternsJSON, antibodiesJSON, fossilsJSON string
		var lastEvolved *string
		if err := rows.Scan(&r.Species, &r.Generation, &r.Successes, &r.Failures,
			&patternsJSON, &antibodiesJSON, &fossilsJSON, &lastEvolved); err != nil {
			return nil, fmt.Errorf("scan stuck species: %w", err)
		}

		var antibodies, fossils []GenomeEntry
		_ = parseJSON(antibodiesJSON, &antibodies)
		_ = parseJSON(fossilsJSON, &fossils)

		if len(fossils) == 0 && len(antibodies) >= minAntibodies {
			r.Issue = "Stuck species — high antibodies but no fossils"
			r.AntibodyCount = len(antibodies)
			r.FossilCount = 0
			if lastEvolved != nil {
				if t, err := time.Parse("2006-01-02 15:04:05", *lastEvolved); err == nil {
					r.LastEvolved = &t
				}
			}
			results = append(results, r)
		}
	}
	return results, rows.Err()
}

// CreateEmptyGenome bootstraps a genome for a species that doesn't have one.
func (s *Store) CreateEmptyGenome(species string) error {
	_, err := s.db.Exec(`
		INSERT INTO genomes (species, parent_species, patterns, antibodies, fossils, provider_genes, generation, successes, failures, total_cost_usd, hibernating, last_evolved)
		VALUES (?, '', '[]', '[]', '[]', '[]', 0, 0, 0, 0, 0, datetime('now'))
		ON CONFLICT(species) DO NOTHING
	`, species)
	if err != nil {
		return fmt.Errorf("create empty genome: %w", err)
	}
	return nil
}

// GetRecurringDoDFailures finds DoD failure patterns that appear across multiple dispatches.
func (s *Store) GetRecurringDoDFailures(minCount int, since time.Time) ([]RecurringDoDFailure, error) {
	rows, err := s.db.Query(`
		SELECT
			failures,
			COUNT(*) as cnt,
			GROUP_CONCAT(DISTINCT project) as projects,
			GROUP_CONCAT(DISTINCT morsel_id) as morsels,
			MIN(checked_at) as first_seen,
			MAX(checked_at) as last_seen
		FROM dod_results
		WHERE passed = 0
		  AND failures != ''
		  AND checked_at >= ?
		GROUP BY failures
		HAVING cnt >= ?
		ORDER BY cnt DESC, last_seen DESC
		LIMIT 20
	`, since.Format("2006-01-02 15:04:05"), minCount)
	if err != nil {
		return nil, fmt.Errorf("query recurring DoD failures: %w", err)
	}
	defer rows.Close()

	var results []RecurringDoDFailure
	for rows.Next() {
		var r RecurringDoDFailure
		var projects, morsels string
		if err := rows.Scan(&r.Failures, &r.Count, &projects, &morsels, &r.FirstSeenAt, &r.LastSeenAt); err != nil {
			return nil, fmt.Errorf("scan recurring DoD failure: %w", err)
		}
		if projects != "" {
			r.Projects = append(r.Projects, splitCSV(projects)...)
		}
		if morsels != "" {
			r.MorselIDs = append(r.MorselIDs, splitCSV(morsels)...)
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// GetFailureRateTrend calculates DoD failure rate for a time window.
func (s *Store) GetFailureRateTrend(project string, windowStart, windowEnd time.Time) (*FailureRateTrend, error) {
	var trend FailureRateTrend
	trend.Project = project
	trend.WindowStart = windowStart
	trend.WindowEnd = windowEnd

	query := `
		SELECT
			COUNT(*) as total,
			COALESCE(SUM(CASE WHEN passed = 1 THEN 1 ELSE 0 END), 0) as passed,
			COALESCE(SUM(CASE WHEN passed = 0 THEN 1 ELSE 0 END), 0) as failed
		FROM dod_results
		WHERE checked_at >= ? AND checked_at < ?`

	args := []interface{}{
		windowStart.Format("2006-01-02 15:04:05"),
		windowEnd.Format("2006-01-02 15:04:05"),
	}

	if project != "" {
		query += ` AND project = ?`
		args = append(args, project)
	}

	if err := s.db.QueryRow(query, args...).Scan(&trend.TotalDispatches, &trend.DoDPassed, &trend.DoDFailed); err != nil {
		return nil, fmt.Errorf("query failure rate trend: %w", err)
	}

	if trend.TotalDispatches > 0 {
		trend.FailureRate = (float64(trend.DoDFailed) / float64(trend.TotalDispatches)) * 100
	}

	return &trend, nil
}

// GetFailureRateDelta compares failure rates between current and previous windows.
func (s *Store) GetFailureRateDelta(project string, windowHours int) (*FailureRateDelta, error) {
	now := time.Now().UTC()
	windowDur := time.Duration(windowHours) * time.Hour

	currentStart := now.Add(-windowDur)
	previousStart := now.Add(-2 * windowDur)

	current, err := s.GetFailureRateTrend(project, currentStart, now)
	if err != nil {
		return nil, fmt.Errorf("get current failure rate: %w", err)
	}

	previous, err := s.GetFailureRateTrend(project, previousStart, currentStart)
	if err != nil {
		return nil, fmt.Errorf("get previous failure rate: %w", err)
	}

	delta := &FailureRateDelta{
		Project:            project,
		CurrentRate:        current.FailureRate,
		PreviousRate:       previous.FailureRate,
		Delta:              current.FailureRate - previous.FailureRate,
		CurrentDispatches:  current.TotalDispatches,
		PreviousDispatches: previous.TotalDispatches,
	}

	// Classify trend
	switch {
	case delta.Delta < -5.0:
		delta.Trend = "improving"
	case delta.Delta > 5.0:
		delta.Trend = "degrading"
	default:
		delta.Trend = "stable"
	}

	return delta, nil
}

// GetFailureRateHistory returns failure rates for multiple time windows (for charting).
func (s *Store) GetFailureRateHistory(project string, windows int, windowHours int) ([]FailureRateTrend, error) {
	now := time.Now().UTC()
	windowDur := time.Duration(windowHours) * time.Hour

	var trends []FailureRateTrend
	for i := 0; i < windows; i++ {
		windowEnd := now.Add(-time.Duration(i) * windowDur)
		windowStart := windowEnd.Add(-windowDur)

		trend, err := s.GetFailureRateTrend(project, windowStart, windowEnd)
		if err != nil {
			return nil, fmt.Errorf("get failure rate window %d: %w", i, err)
		}
		trends = append(trends, *trend)
	}

	return trends, nil
}

// GetSystemHealthScore calculates the doomsday clock state from recent health events.
func (s *Store) GetSystemHealthScore() (*SystemHealthScore, error) {
	// Query last 10 health events related to failure rate trends
	rows, err := s.db.Query(`
		SELECT event_type, created_at
		FROM health_events
		WHERE event_type IN ('failure_rate_improving', 'failure_rate_degrading', 'failure_rate_stable')
		ORDER BY created_at DESC
		LIMIT 10
	`)
	if err != nil {
		return nil, fmt.Errorf("query health events: %w", err)
	}
	defer rows.Close()

	var events []struct {
		eventType string
		createdAt time.Time
	}
	for rows.Next() {
		var e struct {
			eventType string
			createdAt time.Time
		}
		if err := rows.Scan(&e.eventType, &e.createdAt); err != nil {
			return nil, fmt.Errorf("scan health event: %w", err)
		}
		events = append(events, e)
	}

	score := &SystemHealthScore{
		Score:          100,
		AlertLevel:     "green",
		MeteorStatus:   "Distant",
		MeteorDistance: "🌍........................................☄️",
	}

	if len(events) == 0 {
		score.Recommendation = "Baseline - no trend data yet"
		return score, nil
	}

	// Count consecutive degrading/improving from most recent
	for i := range events {
		if events[i].eventType == "failure_rate_degrading" {
			score.DegradationStreak++
		} else {
			break
		}
	}

	for i := range events {
		if events[i].eventType == "failure_rate_improving" {
			score.ImprovementStreak++
		} else {
			break
		}
	}

	if len(events) > 0 {
		score.LastTrendChange = events[0].createdAt
	}

	// Calculate score (0-100) based on streaks
	// Each degrading period: -15 points
	// Each improving period: +10 points (but cap at 100)
	score.Score = 100 - (score.DegradationStreak * 15)
	if score.Score < 0 {
		score.Score = 0
	}
	if score.ImprovementStreak > 0 {
		score.Score = 100 // improving = back to healthy
	}

	// Set meteor status and alert level
	switch {
	case score.Score >= 85:
		score.AlertLevel = "green"
		score.MeteorStatus = "Distant"
		score.MeteorDistance = "🌍........................................☄️"
		score.Recommendation = "Ecosystem thriving - evolution continues normally"
	case score.Score >= 70:
		score.AlertLevel = "yellow"
		score.MeteorStatus = "Approaching"
		score.MeteorDistance = "🌍............................☄️"
		score.Recommendation = "Meteor detected - monitor atmospheric conditions closely"
	case score.Score >= 40:
		score.AlertLevel = "orange"
		score.MeteorStatus = "Incoming"
		score.MeteorDistance = "🌍..............☄️"
		score.Recommendation = "⚠️ Meteor incoming - species should prepare for impact. Consider sheltering low-priority organisms."
	case score.Score >= 15:
		score.AlertLevel = "red"
		score.MeteorStatus = "Near Impact"
		score.MeteorDistance = "🌍....☄️"
		score.Recommendation = "🚨 IMMINENT IMPACT: Extinction risk critical - pause new spawns, investigate atmospheric anomaly"
	default:
		score.AlertLevel = "red"
		score.MeteorStatus = "💥 EXTINCTION EVENT"
		score.MeteorDistance = "🌍💥"
		score.Recommendation = "☠️ EXTINCTION EVENT IN PROGRESS: Stop spawning organisms until ecosystem stabilizes"
	}

	return score, nil
}

// RecordPaleontologyRun saves a summary of a paleontologist analysis run.
func (s *Store) RecordPaleontologyRun(result PaleontologyRunResult) error {
	_, err := s.db.Exec(`
		INSERT INTO paleontology_runs (antibodies_discovered, genes_mutated, proteins_nominated, species_audited, cost_alerts, recurring_failures, summary)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, result.AntibodiesDiscovered, result.GenesMutated, result.ProteinsNominated,
		result.SpeciesAudited, result.CostAlerts, result.RecurringFailures, result.Summary)
	if err != nil {
		return fmt.Errorf("record paleontology run: %w", err)
	}
	return nil
}

// --- helpers ---

func splitCSV(s string) []string {
	var result []string
	for _, part := range splitOnComma(s) {
		trimmed := trimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func splitOnComma(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func trimSpace(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	j := len(s)
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t') {
		j--
	}
	return s[i:j]
}

func parseJSON(data string, v interface{}) error {
	if data == "" || data == "[]" || data == "{}" {
		return nil
	}
	return json.Unmarshal([]byte(data), v)
}
