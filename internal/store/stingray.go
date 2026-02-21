package store

import (
	"database/sql"
	"fmt"
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

// RecordRun inserts a new Stingray run and returns its ID.
func (s *Store) RecordRun(project string, findingsTotal, findingsNew, findingsResolved int, metricsJSON string) (int64, error) {
	if metricsJSON == "" {
		metricsJSON = "{}"
	}
	res, err := s.db.Exec(`
		INSERT INTO stingray_runs (project, findings_total, findings_new, findings_resolved, metrics_json)
		VALUES (?, ?, ?, ?, ?)
	`, project, findingsTotal, findingsNew, findingsResolved, metricsJSON)
	if err != nil {
		return 0, fmt.Errorf("store: record stingray run: %w", err)
	}
	return res.LastInsertId()
}

// RecordFinding inserts a new Stingray finding and returns its ID.
func (s *Store) RecordFinding(runID int64, project, category, severity, title, detail, filePath, evidence string) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO stingray_findings (run_id, project, category, severity, title, detail, file_path, evidence)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, runID, project, category, severity, title, detail, filePath, evidence)
	if err != nil {
		return 0, fmt.Errorf("store: record stingray finding: %w", err)
	}
	return res.LastInsertId()
}

// GetRecentFindings returns the most recent findings for a project, ordered by last_seen descending.
func (s *Store) GetRecentFindings(project string, limit int) ([]StingrayFinding, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT id, run_id, project, category, severity, title, detail,
		       file_path, evidence, bead_id, status, first_seen, last_seen
		FROM stingray_findings
		WHERE project = ?
		ORDER BY last_seen DESC
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
	if minOccurrences <= 0 {
		minOccurrences = 2
	}
	// Find persistent findings: same title+file_path appearing across multiple runs.
	// We group by (title, file_path) and return the most recent finding row for each group.
	rows, err := s.db.Query(`
		SELECT f.id, f.run_id, f.project, f.category, f.severity, f.title, f.detail,
		       f.file_path, f.evidence, f.bead_id, f.status, f.first_seen, f.last_seen
		FROM stingray_findings f
		INNER JOIN (
			SELECT title, file_path, MAX(id) AS max_id, COUNT(DISTINCT run_id) AS run_count
			FROM stingray_findings
			WHERE project = ? AND status = 'open'
			GROUP BY title, file_path
			HAVING run_count >= ?
		) grouped ON f.id = grouped.max_id
		ORDER BY f.last_seen DESC
	`, project, minOccurrences)
	if err != nil {
		return nil, fmt.Errorf("store: get trending stingray findings: %w", err)
	}
	defer rows.Close()
	return scanStingrayFindings(rows)
}

// UpdateFindingStatus updates the status of a finding (open, filed, resolved, wont_fix).
func (s *Store) UpdateFindingStatus(id int64, status string) error {
	_, err := s.db.Exec(`UPDATE stingray_findings SET status = ? WHERE id = ?`, status, id)
	if err != nil {
		return fmt.Errorf("store: update stingray finding status: %w", err)
	}
	return nil
}

// UpdateFindingBeadID links a finding to a filed bead.
func (s *Store) UpdateFindingBeadID(id int64, beadID string) error {
	_, err := s.db.Exec(`UPDATE stingray_findings SET bead_id = ?, status = 'filed' WHERE id = ?`, beadID, id)
	if err != nil {
		return fmt.Errorf("store: update stingray finding bead_id: %w", err)
	}
	return nil
}

// UpdateFindingLastSeen bumps last_seen to now for a finding that reappears.
func (s *Store) UpdateFindingLastSeen(id int64) error {
	_, err := s.db.Exec(`UPDATE stingray_findings SET last_seen = datetime('now') WHERE id = ?`, id)
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
