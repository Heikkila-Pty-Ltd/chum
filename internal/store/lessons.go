package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// StoredLesson is a lesson persisted in the lessons table with FTS5 indexing.
type StoredLesson struct {
	ID            int64
	BeadID        string
	Project       string
	Category      string // pattern, antipattern, rule, insight
	Summary       string
	Detail        string
	FilePaths     []string
	Labels        []string
	SemgrepRuleID string
	CreatedAt     time.Time
}

// migrateLessonsTable creates the lessons table and FTS5 virtual table for
// full-text search over extracted lessons. Called from migrate().
func migrateLessonsTable(db *sql.DB) error {
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS lessons (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			bead_id TEXT NOT NULL,
			project TEXT NOT NULL,
			category TEXT NOT NULL,
			summary TEXT NOT NULL,
			detail TEXT NOT NULL,
			file_paths TEXT NOT NULL DEFAULT '[]',
			labels TEXT NOT NULL DEFAULT '',
			semgrep_rule_id TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)
	`); err != nil {
		return fmt.Errorf("create lessons table: %w", err)
	}

	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_lessons_bead ON lessons(bead_id)`); err != nil {
		return fmt.Errorf("create lessons bead index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_lessons_project ON lessons(project)`); err != nil {
		return fmt.Errorf("create lessons project index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_lessons_category ON lessons(category)`); err != nil {
		return fmt.Errorf("create lessons category index: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_lessons_created ON lessons(created_at)`); err != nil {
		return fmt.Errorf("create lessons created_at index: %w", err)
	}

	// FTS5 virtual table for full-text search across lessons.
	// content= sync mode uses triggers to keep FTS in sync with the content table.
	if _, err := db.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS lessons_fts USING fts5(
			summary, detail, file_paths, labels,
			content='lessons',
			content_rowid='id'
		)
	`); err != nil {
		return fmt.Errorf("create lessons_fts virtual table: %w", err)
	}

	// Triggers to keep FTS5 in sync with the content table.
	if _, err := db.Exec(`
		CREATE TRIGGER IF NOT EXISTS lessons_ai AFTER INSERT ON lessons BEGIN
			INSERT INTO lessons_fts(rowid, summary, detail, file_paths, labels)
			VALUES (new.id, new.summary, new.detail, new.file_paths, new.labels);
		END
	`); err != nil {
		return fmt.Errorf("create lessons_ai trigger: %w", err)
	}
	if _, err := db.Exec(`
		CREATE TRIGGER IF NOT EXISTS lessons_ad AFTER DELETE ON lessons BEGIN
			INSERT INTO lessons_fts(lessons_fts, rowid, summary, detail, file_paths, labels)
			VALUES ('delete', old.id, old.summary, old.detail, old.file_paths, old.labels);
		END
	`); err != nil {
		return fmt.Errorf("create lessons_ad trigger: %w", err)
	}
	if _, err := db.Exec(`
		CREATE TRIGGER IF NOT EXISTS lessons_au AFTER UPDATE ON lessons BEGIN
			INSERT INTO lessons_fts(lessons_fts, rowid, summary, detail, file_paths, labels)
			VALUES ('delete', old.id, old.summary, old.detail, old.file_paths, old.labels);
			INSERT INTO lessons_fts(rowid, summary, detail, file_paths, labels)
			VALUES (new.id, new.summary, new.detail, new.file_paths, new.labels);
		END
	`); err != nil {
		return fmt.Errorf("create lessons_au trigger: %w", err)
	}

	return nil
}

// StoreLesson persists a lesson and updates the FTS5 index (via triggers).
func (s *Store) StoreLesson(beadID, project, category, summary, detail string, filePaths []string, labels []string, semgrepRuleID string) (int64, error) {
	filePathsJSON, err := json.Marshal(filePaths)
	if err != nil {
		return 0, fmt.Errorf("marshal file_paths: %w", err)
	}
	labelsStr := strings.Join(labels, ",")

	result, err := s.db.Exec(`
		INSERT INTO lessons (bead_id, project, category, summary, detail, file_paths, labels, semgrep_rule_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, beadID, project, category, summary, detail, string(filePathsJSON), labelsStr, semgrepRuleID)
	if err != nil {
		return 0, fmt.Errorf("insert lesson: %w", err)
	}
	return result.LastInsertId()
}

// SearchLessons performs FTS5 full-text search across lessons, ordered by BM25 relevance.
func (s *Store) SearchLessons(query string, limit int) ([]StoredLesson, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.Query(`
		SELECT l.id, l.bead_id, l.project, l.category, l.summary, l.detail,
		       l.file_paths, l.labels, l.semgrep_rule_id, l.created_at
		FROM lessons l
		JOIN lessons_fts f ON l.id = f.rowid
		WHERE lessons_fts MATCH ?
		ORDER BY bm25(lessons_fts)
		LIMIT ?
	`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("search lessons: %w", err)
	}
	defer rows.Close()
	return scanLessons(rows)
}

// SearchLessonsByFilePath returns lessons whose file_paths overlap with the given paths.
// Builds an FTS5 OR query from the path tokens.
func (s *Store) SearchLessonsByFilePath(filePaths []string, limit int) ([]StoredLesson, error) {
	if len(filePaths) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}

	// Build FTS5 query: OR-join path components.
	// FTS5 tokenizes on non-alphanumeric chars, so we split paths into
	// alpha-numeric tokens and search for those.
	var terms []string
	for _, p := range filePaths {
		// Split on / and .
		for _, part := range strings.FieldsFunc(p, func(r rune) bool {
			return r == '/' || r == '.' || r == '_' || r == '-'
		}) {
			part = strings.TrimSpace(part)
			if part != "" && len(part) > 1 {
				terms = append(terms, part)
			}
		}
	}
	if len(terms) == 0 {
		return nil, nil
	}

	// Deduplicate
	seen := make(map[string]bool)
	var unique []string
	for _, t := range terms {
		if !seen[t] {
			seen[t] = true
			unique = append(unique, t)
		}
	}

	ftsQuery := strings.Join(unique, " OR ")
	return s.SearchLessons(ftsQuery, limit)
}

// GetRecentLessons returns the N most recent lessons for a project.
func (s *Store) GetRecentLessons(project string, limit int) ([]StoredLesson, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.Query(`
		SELECT id, bead_id, project, category, summary, detail,
		       file_paths, labels, semgrep_rule_id, created_at
		FROM lessons
		WHERE project = ?
		ORDER BY created_at DESC
		LIMIT ?
	`, project, limit)
	if err != nil {
		return nil, fmt.Errorf("get recent lessons: %w", err)
	}
	defer rows.Close()
	return scanLessons(rows)
}

// GetLessonsByBead returns all lessons for a specific bead.
func (s *Store) GetLessonsByBead(beadID string) ([]StoredLesson, error) {
	rows, err := s.db.Query(`
		SELECT id, bead_id, project, category, summary, detail,
		       file_paths, labels, semgrep_rule_id, created_at
		FROM lessons
		WHERE bead_id = ?
		ORDER BY created_at DESC
	`, beadID)
	if err != nil {
		return nil, fmt.Errorf("get lessons by bead: %w", err)
	}
	defer rows.Close()
	return scanLessons(rows)
}

// CountLessons returns the total number of lessons, optionally filtered by project.
func (s *Store) CountLessons(project string) (int, error) {
	var count int
	var err error
	if project == "" {
		err = s.db.QueryRow(`SELECT COUNT(*) FROM lessons`).Scan(&count)
	} else {
		err = s.db.QueryRow(`SELECT COUNT(*) FROM lessons WHERE project = ?`, project).Scan(&count)
	}
	if err != nil {
		return 0, fmt.Errorf("count lessons: %w", err)
	}
	return count, nil
}

// scanLessons scans rows into StoredLesson slices, deserializing JSON/CSV fields.
func scanLessons(rows *sql.Rows) ([]StoredLesson, error) {
	var lessons []StoredLesson
	for rows.Next() {
		var l StoredLesson
		var filePathsJSON, labelsStr string
		var createdAt string

		if err := rows.Scan(&l.ID, &l.BeadID, &l.Project, &l.Category,
			&l.Summary, &l.Detail, &filePathsJSON, &labelsStr,
			&l.SemgrepRuleID, &createdAt); err != nil {
			return nil, fmt.Errorf("scan lesson: %w", err)
		}

		// Deserialize file paths from JSON array
		if filePathsJSON != "" && filePathsJSON != "[]" {
			if err := json.Unmarshal([]byte(filePathsJSON), &l.FilePaths); err != nil {
				l.FilePaths = nil // best-effort
			}
		}

		// Split labels from comma-separated
		if labelsStr != "" {
			l.Labels = strings.Split(labelsStr, ",")
		}

		// Parse created_at
		if t, err := time.Parse("2006-01-02 15:04:05", createdAt); err == nil {
			l.CreatedAt = t
		}

		lessons = append(lessons, l)
	}
	return lessons, rows.Err()
}
