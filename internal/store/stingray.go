package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// StingrayRun represents a single Stingray code health audit run.
type StingrayRun struct {
	ID               int64
	Project          string
	RunAt            time.Time
	FindingsTotal    int
	FindingsNew      int
	FindingsResolved int
	MetricsJSON      string
}

// StingrayFinding represents a single finding from a Stingray run.
type StingrayFinding struct {
	ID        int64
	RunID     int64
	Project   string
	Category  string // god_object, tech_debt, dep_health, coverage, structure, oss_opportunity, coupling, doc_drift
	Severity  string // high, medium, low
	Title     string
	Detail    string
	FilePath  string
	Evidence  string
	BeadID    string
	Status    string // open, filed, resolved, wont_fix
	FirstSeen time.Time
	LastSeen  time.Time
}

const (
	defaultStingrayRecentLimit      = 20
	defaultStingrayTrendingMinRuns  = 2
	stingrayFindingStatusOpen       = "open"
	stingrayFindingStatusFiled      = "filed"
	stingrayFindingStatusResolved   = "resolved"
	stingrayFindingStatusWontFix    = "wont_fix"
	stingrayFindingSeverityHigh     = "high"
	stingrayFindingSeverityMedium   = "medium"
	stingrayFindingSeverityLow      = "low"
	stingrayFindingCategoryGodObject = "god_object"
	stingrayFindingCategoryTechDebt  = "tech_debt"
	stingrayFindingCategoryDepHealth = "dep_health"
	stingrayFindingCategoryCoverage = "coverage"
	stingrayFindingCategoryStructure = "structure"
	stingrayFindingCategoryOSSRisk   = "oss_opportunity"
	stingrayFindingCategoryCoupling  = "coupling"
	stingrayFindingCategoryDocDrift = "doc_drift"
)

var validStingrayFindingStatuses = map[string]struct{}{
	stingrayFindingStatusOpen:     {},
	stingrayFindingStatusFiled:    {},
	stingrayFindingStatusResolved: {},
	stingrayFindingStatusWontFix:  {},
}

var validStingrayFindingSeverities = map[string]struct{}{
	stingrayFindingSeverityHigh:   {},
	stingrayFindingSeverityMedium: {},
	stingrayFindingSeverityLow:    {},
}

var validStingrayFindingCategories = map[string]struct{}{
	stingrayFindingCategoryGodObject:  {},
	stingrayFindingCategoryTechDebt:   {},
	stingrayFindingCategoryDepHealth:  {},
	stingrayFindingCategoryCoverage:   {},
	stingrayFindingCategoryStructure:  {},
	stingrayFindingCategoryOSSRisk:    {},
	stingrayFindingCategoryCoupling:   {},
	stingrayFindingCategoryDocDrift:   {},
}

// RecordRun inserts a new Stingray run and returns its ID.
func (s *Store) RecordRun(project string, findingsTotal, findingsNew, findingsResolved int, metricsJSON string) (int64, error) {
	project = strings.TrimSpace(project)
	if project == "" {
		return 0, fmt.Errorf("store: record stingray run: project is required")
	}
	metricsJSON = strings.TrimSpace(metricsJSON)
	if metricsJSON == "" {
		metricsJSON = "{}"
	}
	if findingsTotal < 0 {
		findingsTotal = 0
	}
	if findingsNew < 0 {
		findingsNew = 0
	}
	if findingsResolved < 0 {
		findingsResolved = 0
	}
	res, err := s.db.Exec(`
		INSERT INTO stingray_runs (project, findings_total, findings_new, findings_resolved, metrics_json)
		VALUES (?, ?, ?, ?, ?)
	`, project, findingsTotal, findingsNew, findingsResolved, metricsJSON)
	if err != nil {
		return 0, fmt.Errorf("store: record stingray run: %w", err)
	}
	runID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: record stingray run: get insert id: %w", err)
	}
	return runID, nil
}

// RecordFinding inserts a new Stingray finding and returns its ID.
func (s *Store) RecordFinding(runID int64, project, category, severity, title, detail, filePath, evidence string) (int64, error) {
	project = strings.TrimSpace(project)
	category = strings.TrimSpace(category)
	severity = strings.TrimSpace(severity)
	title = strings.TrimSpace(title)
	detail = strings.TrimSpace(detail)
	filePath = strings.TrimSpace(filePath)
	evidence = strings.TrimSpace(evidence)
	if project == "" {
		return 0, fmt.Errorf("store: record stingray finding: project is required")
	}
	if runID <= 0 {
		return 0, fmt.Errorf("store: record stingray finding: run ID must be > 0")
	}
	var runProject string
	if err := s.db.QueryRow(`SELECT project FROM stingray_runs WHERE id = ?`, runID).Scan(&runProject); err != nil {
		if err == sql.ErrNoRows {
			return 0, fmt.Errorf("store: record stingray finding: run %d not found", runID)
		}
		return 0, fmt.Errorf("store: record stingray finding: lookup run %d: %w", runID, err)
	}
	if runProject != project {
		return 0, fmt.Errorf("store: record stingray finding: run %d belongs to project %q", runID, runProject)
	}
	if category == "" {
		return 0, fmt.Errorf("store: record stingray finding: category is required")
	}
	if _, ok := validStingrayFindingCategories[category]; !ok {
		return 0, fmt.Errorf("store: record stingray finding: invalid category %q", category)
	}
	if severity == "" {
		return 0, fmt.Errorf("store: record stingray finding: severity is required")
	}
	if _, ok := validStingrayFindingSeverities[severity]; !ok {
		return 0, fmt.Errorf("store: record stingray finding: invalid severity %q", severity)
	}
	if title == "" {
		return 0, fmt.Errorf("store: record stingray finding: title is required")
	}
	if detail == "" {
		return 0, fmt.Errorf("store: record stingray finding: detail is required")
	}
	res, err := s.db.Exec(`
		INSERT INTO stingray_findings (run_id, project, category, severity, title, detail, file_path, evidence, bead_id, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', ?)
	`, runID, project, category, severity, title, detail, filePath, evidence, stingrayFindingStatusOpen)
	if err != nil {
		return 0, fmt.Errorf("store: record stingray finding: %w", err)
	}
	findingID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: record stingray finding: get insert id: %w", err)
	}
	return findingID, nil
}

// GetRecentFindings returns the most recent findings for a project, ordered by last_seen descending.
func (s *Store) GetRecentFindings(project string, limit int) ([]StingrayFinding, error) {
	project = strings.TrimSpace(project)
	if project == "" {
		return []StingrayFinding{}, nil
	}
	if limit <= 0 {
		limit = defaultStingrayRecentLimit
	}
	rows, err := s.db.Query(`
		SELECT id, run_id, project, category, severity, title, detail,
		       file_path, evidence, bead_id, status, first_seen, last_seen
		FROM stingray_findings
		WHERE project = ?
		ORDER BY last_seen DESC, id DESC
		LIMIT ?
	`, project, limit)
	if err != nil {
		return nil, fmt.Errorf("store: get recent stingray findings: %w", err)
	}
	defer rows.Close()
	return scanStingrayFindings(rows)
}

// GetTrendingFindings returns open findings that have appeared in at least minOccurrences runs,
// indicating persistent issues. Measured by counting distinct run_ids for findings with the
// same title and file_path.
func (s *Store) GetTrendingFindings(project string, minOccurrences int) ([]StingrayFinding, error) {
	project = strings.TrimSpace(project)
	if project == "" {
		return []StingrayFinding{}, nil
	}
	if minOccurrences <= 0 {
		minOccurrences = defaultStingrayTrendingMinRuns
	}
	// Find persistent findings: same title+file_path appearing across multiple runs.
	// We group by (title, file_path) and return the most recent row for each group.
	rows, err := s.db.Query(`
		WITH active_findings AS (
			SELECT title, file_path
			FROM stingray_findings
			WHERE project = ? AND status IN (?, ?)
			GROUP BY title, file_path
			HAVING COUNT(DISTINCT run_id) >= ?
		),
		ranked_findings AS (
			SELECT
				f.id,
				f.run_id,
				f.project,
				f.category,
				f.severity,
				f.title,
				f.detail,
				f.file_path,
				f.evidence,
				f.bead_id,
				f.status,
				f.first_seen,
				f.last_seen,
				ROW_NUMBER() OVER (
					PARTITION BY f.title, f.file_path
					ORDER BY f.last_seen DESC, f.id DESC
				) AS rn
			FROM stingray_findings f
			INNER JOIN active_findings af ON af.title = f.title AND af.file_path = f.file_path
			WHERE f.project = ? AND f.status IN (?, ?)
		)
		SELECT id, run_id, project, category, severity, title, detail,
		       file_path, evidence, bead_id, status, first_seen, last_seen
		FROM ranked_findings
		WHERE rn = 1
		ORDER BY last_seen DESC, id DESC
	`, project, stingrayFindingStatusOpen, stingrayFindingStatusFiled, minOccurrences, project, stingrayFindingStatusOpen, stingrayFindingStatusFiled)
	if err != nil {
		return nil, fmt.Errorf("store: get trending stingray findings: %w", err)
	}
	defer rows.Close()
	return scanStingrayFindings(rows)
}

// UpdateFindingStatus updates the status of a finding (open, filed, resolved, wont_fix).
func (s *Store) UpdateFindingStatus(id int64, status string) error {
	status = strings.TrimSpace(status)
	if status == "" {
		return fmt.Errorf("store: update stingray finding status: status is required")
	}
	if _, ok := validStingrayFindingStatuses[status]; !ok {
		return fmt.Errorf("store: update stingray finding status: invalid status %q", status)
	}
	_, err := s.db.Exec(`UPDATE stingray_findings SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return fmt.Errorf("store: update stingray finding status: %w", err)
	}
	return nil
}

// UpdateFindingBeadID links a finding to a filed bead.
func (s *Store) UpdateFindingBeadID(id int64, beadID string) error {
	beadID = strings.TrimSpace(beadID)
	_, err := s.db.Exec(`UPDATE stingray_findings SET bead_id = ?, status = 'filed' WHERE id = ?`, beadID, id)
	if err != nil {
		return fmt.Errorf("store: update stingray finding bead_id: %w", err)
	}
	return nil
}

// UpdateFindingLastSeen bumps last_seen to now for a finding that reappears.
func (s *Store) UpdateFindingLastSeen(id int64) error {
	_, err := s.db.Exec(`
		UPDATE stingray_findings
		SET last_seen = CASE
			WHEN datetime(last_seen) >= datetime('now') THEN datetime(last_seen, '+1 second')
			ELSE datetime('now')
		END
		WHERE id = ?
	`, id)
	if err != nil {
		return fmt.Errorf("store: update stingray finding last_seen: %w", err)
	}
	return nil
}

// GetFindingByTitleAndFile looks up an existing finding by title+file_path for deduplication.
func (s *Store) GetFindingByTitleAndFile(project, title, filePath string) (*StingrayFinding, error) {
	var f StingrayFinding
	var firstSeen, lastSeen string
	err := s.db.QueryRow(`
		SELECT id, run_id, project, category, severity, title, detail,
		       file_path, evidence, bead_id, status, first_seen, last_seen
		FROM stingray_findings
		WHERE project = ? AND title = ? AND file_path = ? AND status IN ('open', 'filed')
		ORDER BY last_seen DESC
		LIMIT 1
	`, project, title, filePath).Scan(
		&f.ID, &f.RunID, &f.Project, &f.Category, &f.Severity, &f.Title, &f.Detail,
		&f.FilePath, &f.Evidence, &f.BeadID, &f.Status, &firstSeen, &lastSeen,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: get stingray finding by title+file: %w", err)
	}
	if t, err := time.Parse("2006-01-02 15:04:05", firstSeen); err == nil {
		f.FirstSeen = t
	}
	if t, err := time.Parse("2006-01-02 15:04:05", lastSeen); err == nil {
		f.LastSeen = t
	}
	return &f, nil
}

// GetLatestRun returns the most recent Stingray run for a project.
func (s *Store) GetLatestRun(project string) (*StingrayRun, error) {
	var r StingrayRun
	var runAt string
	err := s.db.QueryRow(`
		SELECT id, project, run_at, findings_total, findings_new, findings_resolved, metrics_json
		FROM stingray_runs
		WHERE project = ?
		ORDER BY id DESC
		LIMIT 1
	`, project).Scan(
		&r.ID, &r.Project, &runAt, &r.FindingsTotal, &r.FindingsNew, &r.FindingsResolved, &r.MetricsJSON,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: get latest stingray run: %w", err)
	}
	if t, err := time.Parse("2006-01-02 15:04:05", runAt); err == nil {
		r.RunAt = t
	}
	return &r, nil
}

// scanStingrayFindings scans rows into StingrayFinding slices.
func scanStingrayFindings(rows *sql.Rows) ([]StingrayFinding, error) {
	var findings []StingrayFinding
	for rows.Next() {
		var f StingrayFinding
		var firstSeen, lastSeen string
		if err := rows.Scan(
			&f.ID, &f.RunID, &f.Project, &f.Category, &f.Severity, &f.Title, &f.Detail,
			&f.FilePath, &f.Evidence, &f.BeadID, &f.Status, &firstSeen, &lastSeen,
		); err != nil {
			return nil, fmt.Errorf("scan stingray finding: %w", err)
		}
		if t, err := time.Parse("2006-01-02 15:04:05", firstSeen); err == nil {
			f.FirstSeen = t
		}
		if t, err := time.Parse("2006-01-02 15:04:05", lastSeen); err == nil {
			f.LastSeen = t
		}
		findings = append(findings, f)
	}
	return findings, rows.Err()
}
