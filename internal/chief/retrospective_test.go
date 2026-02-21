package chief

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/dispatch"
	"github.com/antigravity-dev/chum/internal/graph"

	_ "modernc.org/sqlite"
)

type retrospectiveTestDispatcher struct {
	dispatchFn func(ctx context.Context, agent, prompt, provider, thinkingLevel, workDir string) (int, error)
}

func (d *retrospectiveTestDispatcher) Dispatch(ctx context.Context, agent, prompt, provider, thinkingLevel, workDir string) (int, error) {
	if d.dispatchFn == nil {
		return 1, nil
	}
	return d.dispatchFn(ctx, agent, prompt, provider, thinkingLevel, workDir)
}

func (d *retrospectiveTestDispatcher) IsAlive(handle int) bool { return false }

func (d *retrospectiveTestDispatcher) Kill(handle int) error { return nil }

func (d *retrospectiveTestDispatcher) GetHandleType() string { return "pid" }

func (d *retrospectiveTestDispatcher) GetSessionName(handle int) string { return "" }

func (d *retrospectiveTestDispatcher) GetProcessState(handle int) dispatch.ProcessState {
	return dispatch.ProcessState{}
}

func newTestRetroDAG(t *testing.T) *graph.DAG {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	dag := graph.NewDAG(db)
	if err := dag.EnsureSchema(t.Context()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return dag
}

func TestRecordRetrospectiveResultsReturnsAggregatedErrors(t *testing.T) {
	cfg := &config.Config{
		Chief: config.Chief{
			MatrixRoom: "!coord:example.org",
			AgentID:    "chief-sm",
		},
		Tiers: config.Tiers{
			Fast: []string{"fast-provider"},
		},
		Projects: map[string]config.Project{
			"chum": {
				Enabled:   true,
				Workspace: t.TempDir(),
			},
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	dispatcher := &retrospectiveTestDispatcher{
		dispatchFn: func(ctx context.Context, agent, prompt, provider, thinkingLevel, workDir string) (int, error) {
			return 0, errors.New("matrix down")
		},
	}

	dag := newTestRetroDAG(t)
	rr := NewRetrospectiveRecorder(cfg, nil, dag, dispatcher, logger)

	output := `
## Action Items
- [P1] Fix incident response playbook | project:chum | owner:ops | why:gaps
- [P2] Improve dependency SLAs | project:chum | owner:chief | why:blocking
`

	err := rr.RecordRetrospectiveResults(t.Context(), "ceremony-overall-1", output)
	if err == nil {
		t.Fatal("expected aggregated error, got nil")
	}
	if !strings.Contains(err.Error(), "dispatch retrospective matrix summary") {
		t.Fatalf("expected matrix dispatch error in returned error, got %v", err)
	}
}

func TestRecordRetrospectiveResultsDispatchesAndCreatesActionItems(t *testing.T) {
	cfg := &config.Config{
		Chief: config.Chief{
			MatrixRoom: "!coord:example.org",
			AgentID:    "chief-sm",
		},
		Tiers: config.Tiers{
			Fast: []string{"fast-provider"},
		},
		Projects: map[string]config.Project{
			"chum": {
				Enabled:   true,
				Workspace: t.TempDir(),
			},
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	dispatchCalled := false
	dispatcher := &retrospectiveTestDispatcher{
		dispatchFn: func(ctx context.Context, agent, prompt, provider, thinkingLevel, workDir string) (int, error) {
			dispatchCalled = true
			if agent != "chief-sm" {
				t.Fatalf("expected chief-sm agent, got %s", agent)
			}
			if provider != "fast-provider" {
				t.Fatalf("expected fast-provider, got %s", provider)
			}
			if !strings.Contains(prompt, "coordination update") {
				t.Fatalf("expected retrospective matrix prompt, got %s", prompt)
			}
			return 99, nil
		},
	}

	dag := newTestRetroDAG(t)
	rr := NewRetrospectiveRecorder(cfg, nil, dag, dispatcher, logger)

	output := `
## Action Items
- [P0] Stabilize matrix bridge retries | project:chum | owner:ops | why:delivery failures
`

	if err := rr.RecordRetrospectiveResults(t.Context(), "ceremony-overall-2", output); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !dispatchCalled {
		t.Fatal("expected matrix dispatch to be called")
	}

	// Verify the task was created in the DAG.
	tasks, err := dag.ListTasks(t.Context(), "chum")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task created, got %d", len(tasks))
	}
	if tasks[0].Title != "Stabilize matrix bridge retries" {
		t.Fatalf("expected title 'Stabilize matrix bridge retries', got %q", tasks[0].Title)
	}
}
