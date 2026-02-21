// Package learner analyzes dispatch history to surface insights about
// model performance, bead quality, prompt quality, and task sizing.
// All models start equal — we learn purely from data.
package learner

import (
	"database/sql"
	"fmt"
	"time"
)

// LearnerReport is the output of an analysis cycle.
type LearnerReport struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Window      string         `json:"window"`          // e.g. "last 7 days"
	TotalTasks  int            `json:"total_tasks"`
	ModelStats  []ModelStat    `json:"model_stats"`
	Sizing      SizingAnalysis `json:"sizing"`
	Patterns    []Pattern      `json:"patterns"`
	Recommendations []string   `json:"recommendations"`
}

// ModelStat tracks per-model performance metrics.
type ModelStat struct {
	Agent       string  `json:"agent"`
	Provider    string  `json:"provider"`
	Tasks       int     `json:"tasks"`
	Passed      int     `json:"passed"`
	Failed      int     `json:"failed"`
	PassRate    float64 `json:"pass_rate"`    // 0.0 - 1.0
	AvgDuration float64 `json:"avg_duration"` // seconds
	AvgCost     float64 `json:"avg_cost"`     // USD
	Handoffs    int     `json:"handoffs"`     // how many times review bounced
}

// SizingAnalysis identifies task sizing patterns.
type SizingAnalysis struct {
	AvgDuration      float64 `json:"avg_duration"`
	ShortTaskPassRate float64 `json:"short_task_pass_rate"` // < 2 min
	MedTaskPassRate  float64 `json:"med_task_pass_rate"`   // 2-10 min
	LongTaskPassRate float64 `json:"long_task_pass_rate"`  // > 10 min
	Insight          string  `json:"insight"`
}

// Pattern is a detected recurring issue.
type Pattern struct {
	Type        string `json:"type"`        // model_failure, sizing, prompt, dod
	Description string `json:"description"` // human-readable
	Frequency   int    `json:"frequency"`   // how many times seen
	Severity    string `json:"severity"`    // low, medium, high
}

// LogEntry is a timestamped learner observation, persisted for audit trail.
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Category  string    `json:"category"` // analysis, recommendation, pattern, assignment
	Message   string    `json:"message"`
}

// Analyze queries dispatch history and produces a LearnerReport.
// All models start equal — no hardcoded biases.
func Analyze(db *sql.DB) (*LearnerReport, []LogEntry, error) {
	report := &LearnerReport{
		GeneratedAt: time.Now(),
		Window:      "all time",
	}
	var log []LogEntry

	logf := func(cat, msg string, args ...interface{}) {
		entry := LogEntry{
			Timestamp: time.Now(),
			Category:  cat,
			Message:   fmt.Sprintf(msg, args...),
		}
		log = append(log, entry)
	}

	logf("analysis", "Starting learner analysis cycle")

	// --- Model Stats ---
	modelStats, err := queryModelStats(db)
	if err != nil {
		logf("error", "Failed to query model stats: %v", err)
		return report, log, err
	}
	report.ModelStats = modelStats
	report.TotalTasks = 0
	for _, ms := range modelStats {
		report.TotalTasks += ms.Tasks
		logf("analysis", "Model %s: %d tasks, %.0f%% pass rate, avg %.1fs, $%.4f avg cost",
			ms.Agent, ms.Tasks, ms.PassRate*100, ms.AvgDuration, ms.AvgCost)
	}

	// --- Sizing Analysis ---
	sizing, err := querySizingAnalysis(db)
	if err != nil {
		logf("error", "Failed to query sizing analysis: %v", err)
	} else {
		report.Sizing = *sizing
		logf("analysis", "Sizing: short=%.0f%% med=%.0f%% long=%.0f%% pass rate",
			sizing.ShortTaskPassRate*100, sizing.MedTaskPassRate*100, sizing.LongTaskPassRate*100)
	}

	// --- Pattern Detection ---
	patterns, err := detectPatterns(db)
	if err != nil {
		logf("error", "Failed to detect patterns: %v", err)
	} else {
		report.Patterns = patterns
		for _, p := range patterns {
			logf("pattern", "[%s] %s (seen %dx, severity: %s)", p.Type, p.Description, p.Frequency, p.Severity)
		}
	}

	// --- Recommendations ---
	recs := generateRecommendations(report)
	report.Recommendations = recs
	for _, r := range recs {
		logf("recommendation", "%s", r)
	}

	logf("analysis", "Analysis complete: %d tasks, %d patterns, %d recommendations",
		report.TotalTasks, len(report.Patterns), len(report.Recommendations))

	return report, log, nil
}

// queryModelStats aggregates per-agent performance from dispatches + dod_results.
func queryModelStats(db *sql.DB) ([]ModelStat, error) {
	rows, err := db.Query(`
		SELECT
			d.agent_id,
			d.provider,
			COUNT(*) as tasks,
			SUM(CASE WHEN d.status = 'completed' THEN 1 ELSE 0 END) as passed,
			SUM(CASE WHEN d.status != 'completed' THEN 1 ELSE 0 END) as failed,
			AVG(d.duration_s) as avg_duration,
			AVG(d.cost_usd) as avg_cost
		FROM dispatches d
		WHERE d.backend = 'temporal'
		GROUP BY d.agent_id, d.provider
		ORDER BY tasks DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query model stats: %w", err)
	}
	defer rows.Close()

	var stats []ModelStat
	for rows.Next() {
		var ms ModelStat
		if err := rows.Scan(&ms.Agent, &ms.Provider, &ms.Tasks, &ms.Passed, &ms.Failed, &ms.AvgDuration, &ms.AvgCost); err != nil {
			return nil, fmt.Errorf("scan model stat: %w", err)
		}
		if ms.Tasks > 0 {
			ms.PassRate = float64(ms.Passed) / float64(ms.Tasks)
		}
		stats = append(stats, ms)
	}
	return stats, nil
}

// querySizingAnalysis correlates task duration with success rate.
func querySizingAnalysis(db *sql.DB) (*SizingAnalysis, error) {
	sa := &SizingAnalysis{}

	// Average duration
	if err := db.QueryRow(`SELECT COALESCE(AVG(duration_s), 0) FROM dispatches WHERE backend = 'temporal'`).Scan(&sa.AvgDuration); err != nil {
		return sa, fmt.Errorf("query avg duration: %w", err)
	}

	// Short tasks (< 120s)
	var shortTotal, shortPassed int
	if err := db.QueryRow(`SELECT COUNT(*), SUM(CASE WHEN status='completed' THEN 1 ELSE 0 END) FROM dispatches WHERE backend='temporal' AND duration_s > 0 AND duration_s < 120`).Scan(&shortTotal, &shortPassed); err != nil {
		return sa, fmt.Errorf("query short tasks: %w", err)
	}
	if shortTotal > 0 {
		sa.ShortTaskPassRate = float64(shortPassed) / float64(shortTotal)
	}

	// Medium tasks (120-600s)
	var medTotal, medPassed int
	if err := db.QueryRow(`SELECT COUNT(*), SUM(CASE WHEN status='completed' THEN 1 ELSE 0 END) FROM dispatches WHERE backend='temporal' AND duration_s >= 120 AND duration_s < 600`).Scan(&medTotal, &medPassed); err != nil {
		return sa, fmt.Errorf("query medium tasks: %w", err)
	}
	if medTotal > 0 {
		sa.MedTaskPassRate = float64(medPassed) / float64(medTotal)
	}

	// Long tasks (> 600s)
	var longTotal, longPassed int
	if err := db.QueryRow(`SELECT COUNT(*), SUM(CASE WHEN status='completed' THEN 1 ELSE 0 END) FROM dispatches WHERE backend='temporal' AND duration_s >= 600`).Scan(&longTotal, &longPassed); err != nil {
		return sa, fmt.Errorf("query long tasks: %w", err)
	}
	if longTotal > 0 {
		sa.LongTaskPassRate = float64(longPassed) / float64(longTotal)
	}

	// Generate insight
	if longTotal > 2 && sa.LongTaskPassRate < 0.5 {
		sa.Insight = fmt.Sprintf("Long tasks (>10min) have %.0f%% pass rate — consider breaking into smaller pieces", sa.LongTaskPassRate*100)
	} else if shortTotal > 2 && sa.ShortTaskPassRate > 0.9 {
		sa.Insight = "Short tasks perform well — good task decomposition"
	} else {
		sa.Insight = "Insufficient data for sizing insights"
	}

	return sa, nil
}

// detectPatterns finds recurring failure patterns.
func detectPatterns(db *sql.DB) ([]Pattern, error) {
	var patterns []Pattern

	// Pattern: repeated DoD failures by agent
	rows, err := db.Query(`
		SELECT d.agent_id, COUNT(*) as fail_count
		FROM dispatches d
		JOIN dod_results dr ON d.id = dr.dispatch_id
		WHERE dr.passed = 0 AND d.backend = 'temporal'
		GROUP BY d.agent_id
		HAVING fail_count >= 2
	`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var agent string
			var count int
			rows.Scan(&agent, &count)
			patterns = append(patterns, Pattern{
				Type:        "model_failure",
				Description: fmt.Sprintf("%s has %d DoD failures — check if tasks match this model's strengths", agent, count),
				Frequency:   count,
				Severity:    severityFromCount(count),
			})
		}
	}

	// Pattern: escalations
	var escalations int
	if err := db.QueryRow(`SELECT COUNT(*) FROM health_events WHERE event_type = 'escalation_required'`).Scan(&escalations); err != nil {
		escalations = 0
	}
	if escalations > 0 {
		patterns = append(patterns, Pattern{
			Type:        "escalation",
			Description: fmt.Sprintf("%d tasks escalated (exhausted all retries) — beads may be too complex or poorly scoped", escalations),
			Frequency:   escalations,
			Severity:    severityFromCount(escalations),
		})
	}

	// Pattern: high handoff count
	var highHandoffs int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dispatches WHERE backend='temporal' AND retries >= 2`).Scan(&highHandoffs); err != nil {
		highHandoffs = 0
	}
	if highHandoffs > 0 {
		patterns = append(patterns, Pattern{
			Type:        "review_churn",
			Description: fmt.Sprintf("%d tasks needed 2+ review handoffs — prompts may need more specificity", highHandoffs),
			Frequency:   highHandoffs,
			Severity:    "medium",
		})
	}

	return patterns, nil
}

// generateRecommendations produces actionable suggestions from the report.
func generateRecommendations(report *LearnerReport) []string {
	var recs []string

	if report.TotalTasks < 5 {
		recs = append(recs, "Insufficient data (< 5 tasks) — models are treated equally. Run more tasks to build performance data.")
		return recs
	}

	// Find best/worst model
	var best, worst *ModelStat
	for i := range report.ModelStats {
		ms := &report.ModelStats[i]
		if ms.Tasks < 2 {
			continue
		}
		if best == nil || ms.PassRate > best.PassRate {
			best = ms
		}
		if worst == nil || ms.PassRate < worst.PassRate {
			worst = ms
		}
	}

	if best != nil && worst != nil && best.Agent != worst.Agent {
		recs = append(recs, fmt.Sprintf("Prefer %s (%.0f%% pass) over %s (%.0f%% pass) for similar tasks",
			best.Agent, best.PassRate*100, worst.Agent, worst.PassRate*100))
	}

	// Cost efficiency
	if best != nil && best.AvgCost > 0.01 {
		for _, ms := range report.ModelStats {
			if ms.Agent != best.Agent && ms.PassRate >= 0.8 && ms.AvgCost < best.AvgCost*0.5 {
				recs = append(recs, fmt.Sprintf("%s is %.0f%% cheaper than %s with similar pass rate — consider for cost-sensitive tasks",
					ms.Agent, (1-ms.AvgCost/best.AvgCost)*100, best.Agent))
			}
		}
	}

	// Sizing
	if report.Sizing.LongTaskPassRate < 0.5 && report.Sizing.ShortTaskPassRate > 0.8 {
		recs = append(recs, "Break large tasks into smaller pieces — short tasks pass at 2x the rate of long tasks")
	}

	return recs
}

func severityFromCount(count int) string {
	if count >= 5 {
		return "high"
	}
	if count >= 3 {
		return "medium"
	}
	return "low"
}
