package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLessonsStoreAndSearch(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	defer os.Remove(dbPath)

	// Store a lesson
	id, err := st.StoreLesson(
		"chum-abc", "chum", "antipattern",
		"Always check error before using defer",
		"When calling os.Open, the error must be checked before deferring Close, otherwise a nil file causes a panic.",
		[]string{"internal/store/store.go", "internal/config/config.go"},
		[]string{"error-handling", "defer"},
		"",
	)
	if err != nil {
		t.Fatalf("StoreLesson: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive ID, got %d", id)
	}

	// Store a second lesson
	_, err = st.StoreLesson(
		"chum-def", "chum", "pattern",
		"Use context.WithTimeout for all external calls",
		"All CLI subprocess calls should use context.WithTimeout to prevent hung processes.",
		[]string{"internal/temporal/activities.go"},
		[]string{"timeout", "subprocess"},
		"",
	)
	if err != nil {
		t.Fatalf("StoreLesson 2: %v", err)
	}

	// Count
	count, err := st.CountLessons("chum")
	if err != nil {
		t.Fatalf("CountLessons: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 lessons, got %d", count)
	}

	// FTS5 search
	results, err := st.SearchLessons("error handling defer", 10)
	if err != nil {
		t.Fatalf("SearchLessons: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 FTS5 result")
	}
	if results[0].Summary != "Always check error before using defer" {
		t.Fatalf("unexpected top result: %s", results[0].Summary)
	}

	// Search by file path
	results, err = st.SearchLessonsByFilePath([]string{"internal/store/store.go"}, 10)
	if err != nil {
		t.Fatalf("SearchLessonsByFilePath: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least 1 result for file path search")
	}

	// Get by morsel
	results, err = st.GetLessonsByMorsel("chum-abc")
	if err != nil {
		t.Fatalf("GetLessonsByMorsel: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 lesson for chum-abc, got %d", len(results))
	}
	if len(results[0].FilePaths) != 2 {
		t.Fatalf("expected 2 file paths, got %d", len(results[0].FilePaths))
	}
	if len(results[0].Labels) != 2 {
		t.Fatalf("expected 2 labels, got %d", len(results[0].Labels))
	}

	// Get recent
	results, err = st.GetRecentLessons("chum", 5)
	if err != nil {
		t.Fatalf("GetRecentLessons: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 recent lessons, got %d", len(results))
	}
}

func TestSanitizeFTS5Query(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"uppercase operators preserved", "scope too-large OR underestimated OR missing-deps",
			`"scope" "too-large" OR "underestimated" OR "missing-deps"`},
		{"simple terms quoted", "error handling defer",
			`"error" "handling" "defer"`},
		{"AND NOT preserved", "foo AND bar NOT baz",
			`"foo" AND "bar" NOT "baz"`},
		{"already quoted stripped and re-quoted", `"hello" world`,
			`"hello" "world"`},
		{"empty string", "", ""},
		{"single term", "large", `"large"`},
		{"lowercase not is quoted", "not found",
			`"not" "found"`},
		{"lowercase or is quoted", "this or that",
			`"this" "or" "that"`},
		{"lowercase and is quoted", "plan and execute",
			`"plan" "and" "execute"`},
		{"mixed case Not is quoted", "Not applicable",
			`"Not" "applicable"`},
		{"parentheses stripped", "fix error(s) in store",
			`"fix" "errors" "in" "store"`},
		{"asterisk stripped", "store*.go",
			`"store.go"`},
		{"braces and caret stripped", "{foo} ^bar",
			`"foo" "bar"`},
		{"colon stripped", "file:path",
			`"filepath"`},
		{"all-special-chars becomes empty and is dropped", "(***)",
			``},
		{"mixed special with operators", "fix OR error(s) AND test*",
			`"fix" OR "errors" AND "test"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeFTS5Query(tt.input)
			if got != tt.expected {
				t.Errorf("sanitizeFTS5Query(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
