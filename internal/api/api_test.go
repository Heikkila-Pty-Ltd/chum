package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/store"
)

func setupTestServer(t *testing.T) *Server {
	t.Helper()
	tmpDB := t.TempDir() + "/test.db"
	st, err := store.Open(tmpDB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := &config.Config{
		Projects: map[string]config.Project{
			"test-proj": {Enabled: true, BeadsDir: "/tmp/beads", Workspace: "/tmp/ws", Priority: 1},
		},
		API: config.API{Bind: "127.0.0.1:0"},
		General: config.General{
			TickInterval: config.Duration{Duration: 60 * time.Second},
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	srv, err := NewServer(cfg, st, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return srv
}

func TestHandleStatus(t *testing.T) {
	srv := setupTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	w := httptest.NewRecorder()
	srv.handleStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %s", ct)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if _, ok := resp["uptime_s"]; !ok {
		t.Fatal("missing uptime_s")
	}
}

func TestHandleProjects(t *testing.T) {
	srv := setupTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/projects", nil)
	w := httptest.NewRecorder()
	srv.handleProjects(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp []map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp) != 1 {
		t.Fatalf("expected 1 project, got %d", len(resp))
	}
	if resp[0]["name"] != "test-proj" {
		t.Fatalf("expected test-proj, got %v", resp[0]["name"])
	}
}

func TestHandleProjectDetail(t *testing.T) {
	srv := setupTestServer(t)

	// Existing project
	req := httptest.NewRequest(http.MethodGet, "/projects/test-proj", nil)
	w := httptest.NewRecorder()
	srv.handleProjectDetail(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Missing project
	req = httptest.NewRequest(http.MethodGet, "/projects/nonexistent", nil)
	w = httptest.NewRecorder()
	srv.handleProjectDetail(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandleHealth(t *testing.T) {
	srv := setupTestServer(t)

	// Healthy (no events)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	srv.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["healthy"] != true {
		t.Fatal("expected healthy=true")
	}

	// Insert critical event
	srv.store.RecordHealthEvent("gateway_critical", "test critical")
	req = httptest.NewRequest(http.MethodGet, "/health", nil)
	w = httptest.NewRecorder()
	srv.handleHealth(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestHandleMetrics(t *testing.T) {
	srv := setupTestServer(t)

	_, err := srv.store.RecordDispatch("metric-bead", "test-proj", "test-proj-coder", "claude-sonnet-4", "balanced", 777, "metric-sess", "prompt", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	srv.handleMetrics(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "chum_dispatches_total") {
		t.Fatal("missing chum_dispatches_total metric")
	}
	if !strings.Contains(body, "chum_uptime_seconds") {
		t.Fatal("missing chum_uptime_seconds metric")
	}
}

func TestHandleSafetyBlocks(t *testing.T) {
	srv := setupTestServer(t)

	// Empty — no active blocks.
	req := httptest.NewRequest(http.MethodGet, "/safety/blocks", nil)
	w := httptest.NewRecorder()
	srv.handleSafetyBlocks(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["total"] != float64(0) {
		t.Fatalf("expected total=0, got %v", resp["total"])
	}

	// Create blocks.
	if err := srv.store.SetBlock("proj-a", "churn_block", time.Now().Add(5*time.Minute), "high failure rate"); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetBlock("bead-xyz", "quarantine", time.Now().Add(10*time.Minute), "consecutive failures"); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetBlockWithMetadata("system", "circuit_breaker", time.Now().Add(15*time.Minute), "gateway tripped", map[string]interface{}{"failures": 5}); err != nil {
		t.Fatal(err)
	}

	// Query again.
	req = httptest.NewRequest(http.MethodGet, "/safety/blocks", nil)
	w = httptest.NewRecorder()
	srv.handleSafetyBlocks(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	resp = nil
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["total"] != float64(3) {
		t.Fatalf("expected total=3, got %v", resp["total"])
	}

	countsByType, ok := resp["counts_by_type"].(map[string]any)
	if !ok {
		t.Fatalf("expected counts_by_type map, got %T", resp["counts_by_type"])
	}
	if countsByType["churn_block"] != float64(1) {
		t.Fatalf("expected 1 churn_block, got %v", countsByType["churn_block"])
	}
	if countsByType["quarantine"] != float64(1) {
		t.Fatalf("expected 1 quarantine, got %v", countsByType["quarantine"])
	}
	if countsByType["circuit_breaker"] != float64(1) {
		t.Fatalf("expected 1 circuit_breaker, got %v", countsByType["circuit_breaker"])
	}

	blocks, ok := resp["blocks"].([]any)
	if !ok {
		t.Fatalf("expected blocks array, got %T", resp["blocks"])
	}
	if len(blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(blocks))
	}

	// Method not allowed for POST.
	req = httptest.NewRequest(http.MethodPost, "/safety/blocks", nil)
	w = httptest.NewRecorder()
	srv.handleSafetyBlocks(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 for POST, got %d", w.Code)
	}
}

func TestHandleMetricsSafetyBlocks(t *testing.T) {
	srv := setupTestServer(t)

	// Create safety blocks.
	if err := srv.store.SetBlock("proj-a", "churn_block", time.Now().Add(5*time.Minute), "high failure rate"); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetBlock("bead-xyz", "quarantine", time.Now().Add(10*time.Minute), "consecutive failures"); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	srv.handleMetrics(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "chum_safety_blocks_active") {
		t.Fatal("missing chum_safety_blocks_active metric")
	}
	if !strings.Contains(body, `chum_safety_blocks_active{block_type="churn_block"} 1`) {
		t.Fatalf("missing churn_block metric in:\n%s", body)
	}
	if !strings.Contains(body, `chum_safety_blocks_active{block_type="quarantine"} 1`) {
		t.Fatalf("missing quarantine metric in:\n%s", body)
	}
	if !strings.Contains(body, "chum_safety_blocks_total 2") {
		t.Fatalf("missing chum_safety_blocks_total metric in:\n%s", body)
	}
}

func TestServerStartStop(t *testing.T) {
	srv := setupTestServer(t)
	srv.cfg.API.Bind = "127.0.0.1:0" // random port

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	// Give server a moment to start
	cancel()

	err := <-errCh
	if err != nil {
		t.Fatalf("server error: %v", err)
	}
}
