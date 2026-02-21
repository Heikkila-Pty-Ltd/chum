package learner

import (
	"database/sql"
	"math"
	"testing"

	_ "modernc.org/sqlite"
)

// createTestDB opens an in-memory SQLite database and creates only the tables
// the learner queries need: dispatches, dod_results, health_events.
func createTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	schema := `
	CREATE TABLE dispatches (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		bead_id TEXT NOT NULL,
		project TEXT NOT NULL,
		agent_id TEXT NOT NULL,
		provider TEXT NOT NULL,
		tier TEXT NOT NULL DEFAULT '',
		pid INTEGER NOT NULL DEFAULT 0,
		session_name TEXT NOT NULL DEFAULT '',
		stage TEXT NOT NULL DEFAULT 'dispatched',
		labels TEXT NOT NULL DEFAULT '',
		prompt TEXT NOT NULL DEFAULT '',
		dispatched_at DATETIME NOT NULL DEFAULT (datetime('now')),
		completed_at DATETIME,
		next_retry_at DATETIME,
		status TEXT NOT NULL DEFAULT 'running',
		exit_code INTEGER NOT NULL DEFAULT 0,
		duration_s REAL NOT NULL DEFAULT 0,
		retries INTEGER NOT NULL DEFAULT 0,
		escalated_from_tier TEXT NOT NULL DEFAULT '',
		pr_url TEXT NOT NULL DEFAULT '',
		pr_number INTEGER NOT NULL DEFAULT 0,
		input_tokens INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		cost_usd REAL NOT NULL DEFAULT 0,
		backend TEXT NOT NULL DEFAULT '',
		failure_category TEXT NOT NULL DEFAULT '',
		failure_summary TEXT NOT NULL DEFAULT '',
		log_path TEXT NOT NULL DEFAULT '',
		branch TEXT NOT NULL DEFAULT ''
	);

	CREATE TABLE dod_results (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		dispatch_id INTEGER NOT NULL REFERENCES dispatches(id),
		bead_id TEXT NOT NULL,
		project TEXT NOT NULL,
		checked_at DATETIME NOT NULL DEFAULT (datetime('now')),
		passed BOOLEAN NOT NULL DEFAULT 0,
		failures TEXT NOT NULL DEFAULT '',
		check_results TEXT NOT NULL DEFAULT ''
	);

	CREATE TABLE health_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		event_type TEXT NOT NULL,
		details TEXT NOT NULL DEFAULT '',
		dispatch_id INTEGER NOT NULL DEFAULT 0,
		bead_id TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL DEFAULT (datetime('now'))
	);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return db
}

// insertDispatch is a helper that inserts a dispatch row and returns its id.
func insertDispatch(t *testing.T, db *sql.DB, agent, provider, status string, durationS, costUSD float64, retries int) int64 {
	t.Helper()
	res, err := db.Exec(
		`INSERT INTO dispatches (bead_id, project, agent_id, provider, status, duration_s, cost_usd, retries, backend)
		 VALUES ('bead-1', 'proj', ?, ?, ?, ?, ?, ?, 'temporal')`,
		agent, provider, status, durationS, costUSD, retries,
	)
	if err != nil {
		t.Fatalf("insert dispatch: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func floatEq(a, b, epsilon float64) bool {
	return math.Abs(a-b) < epsilon
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestAnalyze_EmptyDB(t *testing.T) {
	db := createTestDB(t)

	report, logs, err := Analyze(db)
	if err != nil {
		t.Fatalf("Analyze on empty db: %v", err)
	}
	if report.TotalTasks != 0 {
		t.Errorf("TotalTasks = %d, want 0", report.TotalTasks)
	}
	if len(report.ModelStats) != 0 {
		t.Errorf("ModelStats = %d entries, want 0", len(report.ModelStats))
	}
	if len(logs) == 0 {
		t.Error("expected at least one log entry")
	}
}

func TestAnalyze_WithModelStats(t *testing.T) {
	db := createTestDB(t)

	// Agent A (providerX): 3 completed, 1 failed
	for i := 0; i < 3; i++ {
		insertDispatch(t, db, "agentA", "providerX", "completed", 60, 0.05, 0)
	}
	insertDispatch(t, db, "agentA", "providerX", "failed", 90, 0.08, 1)

	// Agent B (providerY): 2 completed
	for i := 0; i < 2; i++ {
		insertDispatch(t, db, "agentB", "providerY", "completed", 120, 0.10, 0)
	}

	// Non-temporal dispatch (should be excluded)
	_, err := db.Exec(
		`INSERT INTO dispatches (bead_id, project, agent_id, provider, status, duration_s, cost_usd, retries, backend)
		 VALUES ('bead-x', 'proj', 'agentC', 'providerZ', 'completed', 30, 0.01, 0, 'local')`)
	if err != nil {
		t.Fatalf("insert non-temporal dispatch: %v", err)
	}

	report, _, err := Analyze(db)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	if report.TotalTasks != 6 {
		t.Errorf("TotalTasks = %d, want 6", report.TotalTasks)
	}

	// Find stats by agent
	statsByAgent := make(map[string]ModelStat)
	for _, ms := range report.ModelStats {
		statsByAgent[ms.Agent] = ms
	}

	a, ok := statsByAgent["agentA"]
	if !ok {
		t.Fatal("missing stats for agentA")
	}
	if a.Tasks != 4 {
		t.Errorf("agentA Tasks = %d, want 4", a.Tasks)
	}
	if a.Passed != 3 {
		t.Errorf("agentA Passed = %d, want 3", a.Passed)
	}
	if a.Failed != 1 {
		t.Errorf("agentA Failed = %d, want 1", a.Failed)
	}
	if !floatEq(a.PassRate, 0.75, 0.01) {
		t.Errorf("agentA PassRate = %.4f, want 0.75", a.PassRate)
	}

	b, ok := statsByAgent["agentB"]
	if !ok {
		t.Fatal("missing stats for agentB")
	}
	if b.Tasks != 2 {
		t.Errorf("agentB Tasks = %d, want 2", b.Tasks)
	}
	if !floatEq(b.PassRate, 1.0, 0.01) {
		t.Errorf("agentB PassRate = %.4f, want 1.0", b.PassRate)
	}
	if !floatEq(b.AvgCost, 0.10, 0.001) {
		t.Errorf("agentB AvgCost = %.4f, want 0.10", b.AvgCost)
	}

	if _, exists := statsByAgent["agentC"]; exists {
		t.Error("agentC (non-temporal) should not appear in ModelStats")
	}
}

func TestQuerySizingAnalysis(t *testing.T) {
	db := createTestDB(t)

	// Short tasks (<120s): 4 passed, 1 failed => 80% pass rate
	for i := 0; i < 4; i++ {
		insertDispatch(t, db, "a", "p", "completed", 60, 0.01, 0)
	}
	insertDispatch(t, db, "a", "p", "failed", 90, 0.01, 0)

	// Medium tasks (120-600s): 2 passed, 1 failed => 66.7% pass rate
	for i := 0; i < 2; i++ {
		insertDispatch(t, db, "a", "p", "completed", 300, 0.02, 0)
	}
	insertDispatch(t, db, "a", "p", "failed", 400, 0.02, 0)

	// Long tasks (>=600s): 1 passed, 3 failed => 25% pass rate
	insertDispatch(t, db, "a", "p", "completed", 700, 0.05, 0)
	for i := 0; i < 3; i++ {
		insertDispatch(t, db, "a", "p", "failed", 800, 0.05, 0)
	}

	sizing, err := querySizingAnalysis(db)
	if err != nil {
		t.Fatalf("querySizingAnalysis: %v", err)
	}

	if !floatEq(sizing.ShortTaskPassRate, 0.80, 0.01) {
		t.Errorf("ShortTaskPassRate = %.4f, want 0.80", sizing.ShortTaskPassRate)
	}
	if !floatEq(sizing.MedTaskPassRate, 2.0/3.0, 0.01) {
		t.Errorf("MedTaskPassRate = %.4f, want ~0.667", sizing.MedTaskPassRate)
	}
	if !floatEq(sizing.LongTaskPassRate, 0.25, 0.01) {
		t.Errorf("LongTaskPassRate = %.4f, want 0.25", sizing.LongTaskPassRate)
	}

	// Long task pass rate < 50% with > 2 long tasks => insight about breaking into smaller pieces
	if sizing.Insight == "" {
		t.Error("expected non-empty insight for low long-task pass rate")
	}
	wantSubstr := "Long tasks"
	if len(sizing.Insight) < len(wantSubstr) {
		t.Errorf("insight too short: %q", sizing.Insight)
	}
}

func TestDetectPatterns(t *testing.T) {
	db := createTestDB(t)

	// Create dispatches for agentFail with DoD failures
	for i := 0; i < 4; i++ {
		id := insertDispatch(t, db, "agentFail", "prov", "failed", 100, 0.01, 0)
		_, err := db.Exec(
			`INSERT INTO dod_results (dispatch_id, bead_id, project, passed) VALUES (?, 'bead-1', 'proj', 0)`,
			id,
		)
		if err != nil {
			t.Fatalf("insert dod_result: %v", err)
		}
	}

	// Create a dispatch for agentOK with a passing DoD (should not trigger pattern)
	id := insertDispatch(t, db, "agentOK", "prov", "completed", 50, 0.01, 0)
	_, err := db.Exec(
		`INSERT INTO dod_results (dispatch_id, bead_id, project, passed) VALUES (?, 'bead-2', 'proj', 1)`,
		id,
	)
	if err != nil {
		t.Fatalf("insert dod_result: %v", err)
	}

	patterns, err := detectPatterns(db)
	if err != nil {
		t.Fatalf("detectPatterns: %v", err)
	}

	// Find the model_failure pattern for agentFail
	var found bool
	for _, p := range patterns {
		if p.Type == "model_failure" {
			found = true
			if p.Frequency != 4 {
				t.Errorf("model_failure frequency = %d, want 4", p.Frequency)
			}
			// 4 failures => count >= 3 => "medium"
			if p.Severity != "medium" {
				t.Errorf("model_failure severity = %q, want %q", p.Severity, "medium")
			}
		}
	}
	if !found {
		t.Error("expected model_failure pattern for agentFail")
	}
}

func TestDetectPatterns_HighSeverity(t *testing.T) {
	db := createTestDB(t)

	// 6 DoD failures => severity "high"
	for i := 0; i < 6; i++ {
		id := insertDispatch(t, db, "badAgent", "prov", "failed", 100, 0.01, 0)
		_, err := db.Exec(
			`INSERT INTO dod_results (dispatch_id, bead_id, project, passed) VALUES (?, 'bead-1', 'proj', 0)`,
			id,
		)
		if err != nil {
			t.Fatalf("insert dod_result: %v", err)
		}
	}

	patterns, err := detectPatterns(db)
	if err != nil {
		t.Fatalf("detectPatterns: %v", err)
	}

	for _, p := range patterns {
		if p.Type == "model_failure" {
			if p.Severity != "high" {
				t.Errorf("severity = %q, want %q for count 6", p.Severity, "high")
			}
			return
		}
	}
	t.Error("expected model_failure pattern")
}

func TestDetectPatterns_Escalation(t *testing.T) {
	db := createTestDB(t)

	_, err := db.Exec(`INSERT INTO health_events (event_type, details) VALUES ('escalation_required', 'test')`)
	if err != nil {
		t.Fatalf("insert health_event: %v", err)
	}

	patterns, err := detectPatterns(db)
	if err != nil {
		t.Fatalf("detectPatterns: %v", err)
	}

	var found bool
	for _, p := range patterns {
		if p.Type == "escalation" {
			found = true
			if p.Frequency != 1 {
				t.Errorf("escalation frequency = %d, want 1", p.Frequency)
			}
		}
	}
	if !found {
		t.Error("expected escalation pattern")
	}
}

func TestDetectPatterns_ReviewChurn(t *testing.T) {
	db := createTestDB(t)

	// Insert dispatches with retries >= 2
	for i := 0; i < 3; i++ {
		insertDispatch(t, db, "agent", "prov", "completed", 100, 0.01, 2)
	}

	patterns, err := detectPatterns(db)
	if err != nil {
		t.Fatalf("detectPatterns: %v", err)
	}

	var found bool
	for _, p := range patterns {
		if p.Type == "review_churn" {
			found = true
			if p.Frequency != 3 {
				t.Errorf("review_churn frequency = %d, want 3", p.Frequency)
			}
		}
	}
	if !found {
		t.Error("expected review_churn pattern")
	}
}

func TestGenerateRecommendations_InsufficientData(t *testing.T) {
	report := &LearnerReport{TotalTasks: 3}
	recs := generateRecommendations(report)

	if len(recs) != 1 {
		t.Fatalf("expected 1 recommendation, got %d", len(recs))
	}
	if recs[0] == "" {
		t.Error("expected non-empty insufficient data message")
	}
}

func TestGenerateRecommendations_BestWorstModel(t *testing.T) {
	report := &LearnerReport{
		TotalTasks: 10,
		ModelStats: []ModelStat{
			{Agent: "good-model", Tasks: 5, PassRate: 0.90, AvgCost: 0.005},
			{Agent: "bad-model", Tasks: 5, PassRate: 0.40, AvgCost: 0.005},
		},
	}
	recs := generateRecommendations(report)

	var foundBestWorst bool
	for _, r := range recs {
		if len(r) > 0 {
			// Check that the recommendation mentions preferring good-model over bad-model
			if contains(r, "good-model") && contains(r, "bad-model") {
				foundBestWorst = true
			}
		}
	}
	if !foundBestWorst {
		t.Errorf("expected best/worst model recommendation mentioning both agents, got: %v", recs)
	}
}

func TestGenerateRecommendations_CostEfficiency(t *testing.T) {
	report := &LearnerReport{
		TotalTasks: 10,
		ModelStats: []ModelStat{
			{Agent: "expensive", Tasks: 5, PassRate: 0.95, AvgCost: 0.10},
			{Agent: "cheap", Tasks: 5, PassRate: 0.85, AvgCost: 0.03},
		},
	}
	recs := generateRecommendations(report)

	var foundCost bool
	for _, r := range recs {
		if contains(r, "cheap") && contains(r, "cheaper") {
			foundCost = true
		}
	}
	if !foundCost {
		t.Errorf("expected cost efficiency recommendation, got: %v", recs)
	}
}

func TestGenerateRecommendations_Sizing(t *testing.T) {
	report := &LearnerReport{
		TotalTasks: 10,
		ModelStats: []ModelStat{
			{Agent: "m", Tasks: 10, PassRate: 0.80, AvgCost: 0.01},
		},
		Sizing: SizingAnalysis{
			ShortTaskPassRate: 0.90,
			LongTaskPassRate:  0.30,
		},
	}
	recs := generateRecommendations(report)

	var foundSizing bool
	for _, r := range recs {
		if contains(r, "Break large tasks") || contains(r, "short tasks") {
			foundSizing = true
		}
	}
	if !foundSizing {
		t.Errorf("expected sizing recommendation, got: %v", recs)
	}
}

// contains checks if substr is in s.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
