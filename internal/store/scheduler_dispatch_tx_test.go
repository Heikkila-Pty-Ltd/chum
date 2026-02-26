package store

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func inMemoryStore(t *testing.T) *Store {
	t.Helper()

	re := regexp.MustCompile(`[^a-zA-Z0-9_]+`)
	dbName := re.ReplaceAllString(t.Name(), "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_pragma=busy_timeout(5000)", dbName)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		t.Fatalf("create schema: %v", err)
	}
	// Match production Open() order: schema → ensureGenomesTable → migrate.
	s := &Store{db: db}
	if err := s.ensureGenomesTable(); err != nil {
		db.Close()
		t.Fatalf("ensure genomes table: %v", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		t.Fatalf("migrate schema: %v", err)
	}
	t.Cleanup(func() {
		_ = s.Close()
	})
	return s
}

func TestRecordSchedulerDispatch_RollbackAndRetrySafety(t *testing.T) {
	testCases := []struct {
		name      string
		failpoint string
	}{
		{
			name:      "failure before insert",
			failpoint: dispatchPersistFailpointBeforeInsert,
		},
		{
			name:      "failure after partial write",
			failpoint: dispatchPersistFailpointAfterInsert,
		},
		{
			name:      "failure during status update",
			failpoint: dispatchPersistFailpointBeforeStageWrite,
		},
	}

	injectedErr := errors.New("injected db failure")

	for _, tc := range testCases {

		t.Run(tc.name, func(t *testing.T) {
			s := inMemoryStore(t)
			s.SetDispatchPersistHookForTesting(func(point string) error {
				if point == tc.failpoint {
					return injectedErr
				}
				return nil
			})

			_, err := s.RecordSchedulerDispatch(
				"morsel-rollback", "proj", "agent-1", "model-x", "balanced", 1234, "sess-1", "prompt", "/tmp/log", "main", "openclaw", []string{"stage:todo", "team:platform"},
			)
			if err == nil {
				t.Fatalf("expected injected error at %s", tc.failpoint)
			}
			if !strings.Contains(err.Error(), "dispatch persist failpoint") {
				t.Fatalf("expected failpoint error, got: %v", err)
			}

			countAfterFailure := dispatchCountForMorsel(t, s, "morsel-rollback")
			if countAfterFailure != 0 {
				t.Fatalf("expected rollback to leave 0 rows, got %d", countAfterFailure)
			}

			// Retry with failpoint removed must succeed and create exactly one row.
			s.SetDispatchPersistHookForTesting(nil)
			dispatchID, err := s.RecordSchedulerDispatch(
				"morsel-rollback", "proj", "agent-1", "model-x", "balanced", 1234, "sess-1", "prompt", "/tmp/log", "main", "openclaw", []string{"stage:todo", "team:platform"},
			)
			if err != nil {
				t.Fatalf("retry should succeed: %v", err)
			}
			if dispatchID <= 0 {
				t.Fatalf("expected positive dispatch id, got %d", dispatchID)
			}

			countAfterRetry := dispatchCountForMorsel(t, s, "morsel-rollback")
			if countAfterRetry != 1 {
				t.Fatalf("expected exactly 1 row after retry, got %d", countAfterRetry)
			}

			row, err := s.GetDispatchByID(dispatchID)
			if err != nil {
				t.Fatalf("load dispatch after retry: %v", err)
			}
			if row.Stage != "running" {
				t.Fatalf("stage = %q, want running", row.Stage)
			}
			if row.Status != "running" {
				t.Fatalf("status = %q, want running", row.Status)
			}
			if got := strings.Split(row.Labels, ","); len(got) != 2 {
				t.Fatalf("labels not persisted correctly, got %q", row.Labels)
			}
		})
	}
}

func dispatchCountForMorsel(t *testing.T, s *Store, morselID string) int {
	t.Helper()
	var count int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM dispatches WHERE morsel_id = ?`, morselID).Scan(&count); err != nil {
		t.Fatalf("count dispatches for morsel %s: %v", morselID, err)
	}
	return count
}

func TestRecordSchedulerDispatch_NoDuplicatesAfterTransientFailure(t *testing.T) {
	s := inMemoryStore(t)
	injectedErr := errors.New("injected db failure")
	failures := 0
	s.SetDispatchPersistHookForTesting(func(point string) error {
		if point == dispatchPersistFailpointAfterInsert && failures == 0 {
			failures++
			return injectedErr
		}
		return nil
	})

	_, err := s.RecordSchedulerDispatch(
		"morsel-retry", "proj", "agent-2", "model-y", "fast", 5678, "sess-2", "prompt", "/tmp/log2", "", "openclaw", []string{"retry:test"},
	)
	if err == nil {
		t.Fatal("expected first call to fail")
	}

	dispatchID, err := s.RecordSchedulerDispatch(
		"morsel-retry", "proj", "agent-2", "model-y", "fast", 5678, "sess-2", "prompt", "/tmp/log2", "", "openclaw", []string{"retry:test"},
	)
	if err != nil {
		t.Fatalf("second call should succeed: %v", err)
	}

	count := dispatchCountForMorsel(t, s, "morsel-retry")
	if count != 1 {
		t.Fatalf("expected 1 dispatch row after fail+retry, got %d", count)
	}

	if _, err := s.GetDispatchByID(dispatchID); err != nil {
		t.Fatalf("dispatch %d missing after retry: %v", dispatchID, err)
	}
	if failures != 1 {
		t.Fatalf("expected exactly one injected failure, got %d", failures)
	}

	t.Logf("fail+retry succeeded without duplicates (dispatch_id=%d)", dispatchID)
}

func TestRecordSchedulerDispatch_FailpointErrorIncludesLocation(t *testing.T) {
	s := inMemoryStore(t)
	s.SetDispatchPersistHookForTesting(func(point string) error {
		if point == dispatchPersistFailpointBeforeStageWrite {
			return fmt.Errorf("db write blocked")
		}
		return nil
	})

	_, err := s.RecordSchedulerDispatch(
		"morsel-observable", "proj", "agent-3", "model-z", "premium", 777, "sess-3", "prompt", "/tmp/log3", "", "openclaw", nil,
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), dispatchPersistFailpointBeforeStageWrite) {
		t.Fatalf("error should include failpoint %q, got %v", dispatchPersistFailpointBeforeStageWrite, err)
	}
}
