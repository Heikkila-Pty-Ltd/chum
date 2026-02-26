package store

import "time"

// UBSFinding represents a single bug finding from the Ultimate Bug Scanner.
type UBSFinding struct {
	ID         int64     `json:"id"`
	DispatchID int64     `json:"dispatch_id"`
	MorselID   string    `json:"morsel_id"`
	Project    string    `json:"project"`
	Provider   string    `json:"provider"`
	Species    string    `json:"species"`
	RuleID     string    `json:"rule_id"`
	Severity   string    `json:"severity"` // critical, warning, info
	FilePath   string    `json:"file_path"`
	LineNumber int       `json:"line_number"`
	Message    string    `json:"message"`
	Language   string    `json:"language"`
	Attempt    int       `json:"attempt"`
	Fixed      bool      `json:"fixed"`
	CreatedAt  time.Time `json:"created_at"`
}

// BugPattern is an aggregate view of recurring bug patterns.
type BugPattern struct {
	RuleID    string `json:"rule_id"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
	Count     int    `json:"count"`
	SelfFixed int    `json:"self_fixed"`
}

// RecordUBSFindings batch-inserts multiple findings.
func (s *Store) RecordUBSFindings(findings []UBSFinding) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO ubs_findings
		(dispatch_id, morsel_id, project, provider, species, rule_id, severity,
		 file_path, line_number, message, language, attempt, fixed)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, f := range findings {
		if _, err := stmt.Exec(
			f.DispatchID, f.MorselID, f.Project, f.Provider, f.Species,
			f.RuleID, f.Severity, f.FilePath, f.LineNumber, f.Message,
			f.Language, f.Attempt, f.Fixed,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetProviderWeaknesses returns the most common bug patterns for a given provider.
func (s *Store) GetProviderWeaknesses(provider string, limit int) ([]BugPattern, error) {
	rows, err := s.db.Query(`
		SELECT rule_id, severity,
		  COALESCE(MAX(message), '') as message,
		  COUNT(*) as total,
		  SUM(CASE WHEN fixed=1 THEN 1 ELSE 0 END) as self_fixed
		FROM ubs_findings
		WHERE provider = ? AND severity IN ('critical', 'warning')
		GROUP BY rule_id, severity
		ORDER BY total DESC
		LIMIT ?`, provider, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var patterns []BugPattern
	for rows.Next() {
		var p BugPattern
		if err := rows.Scan(&p.RuleID, &p.Severity, &p.Message, &p.Count, &p.SelfFixed); err != nil {
			return nil, err
		}
		patterns = append(patterns, p)
	}
	return patterns, rows.Err()
}

// GetSpeciesBugProfile returns recurring bug patterns for a species.
func (s *Store) GetSpeciesBugProfile(species string, limit int) ([]BugPattern, error) {
	rows, err := s.db.Query(`
		SELECT rule_id, severity,
		  COALESCE(MAX(message), '') as message,
		  COUNT(*) as total,
		  SUM(CASE WHEN fixed=1 THEN 1 ELSE 0 END) as self_fixed
		FROM ubs_findings
		WHERE species = ?
		GROUP BY rule_id, severity
		ORDER BY total DESC
		LIMIT ?`, species, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var patterns []BugPattern
	for rows.Next() {
		var p BugPattern
		if err := rows.Scan(&p.RuleID, &p.Severity, &p.Message, &p.Count, &p.SelfFixed); err != nil {
			return nil, err
		}
		patterns = append(patterns, p)
	}
	return patterns, rows.Err()
}

// MarkFindingsFixed marks findings from a dispatch as fixed.
func (s *Store) MarkFindingsFixed(dispatchID int64) error {
	_, err := s.db.Exec(`UPDATE ubs_findings SET fixed = 1 WHERE dispatch_id = ? AND fixed = 0`, dispatchID)
	return err
}
