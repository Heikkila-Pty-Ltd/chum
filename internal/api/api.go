// Package api provides a lightweight HTTP API for querying CHUM state.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"go.temporal.io/sdk/client"

	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/store"
	"github.com/antigravity-dev/chum/internal/temporal"
)

// Server is the HTTP API server.
type Server struct {
	cfg            *config.Config
	store          *store.Store
	logger         *slog.Logger
	startTime      time.Time
	httpServer     *http.Server
	authMiddleware *AuthMiddleware
}

// NewServer creates a new API server.
func NewServer(cfg *config.Config, s *store.Store, logger *slog.Logger) (*Server, error) {
	authMiddleware, err := NewAuthMiddleware(&cfg.API.Security, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize auth middleware: %w", err)
	}

	return &Server{
		cfg:            cfg,
		store:          s,
		logger:         logger,
		startTime:      time.Now(),
		authMiddleware: authMiddleware,
	}, nil
}

// Close closes the server and cleans up resources
func (s *Server) Close() error {
	if s.authMiddleware != nil {
		return s.authMiddleware.Close()
	}
	return nil
}

// Start begins listening on the configured bind address. Blocks until context is cancelled.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// Read-only endpoints
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/projects", s.handleProjects)
	mux.HandleFunc("/projects/", s.handleProjectDetail)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/recommendations", s.handleRecommendations)
	mux.HandleFunc("/dispatches/", s.handleDispatchDetail)
	mux.HandleFunc("/safety/blocks", s.handleSafetyBlocks)

	// Temporal workflow endpoints
	mux.HandleFunc("/workflows/start", s.authMiddleware.RequireAuth(s.handleWorkflowStart))
	mux.HandleFunc("/workflows/", s.authMiddleware.RequireAuth(s.routeWorkflows))

	// Planning ceremony endpoints
	mux.HandleFunc("/planning/start", s.authMiddleware.RequireAuth(s.handlePlanningStart))
	mux.HandleFunc("/planning/", s.authMiddleware.RequireAuth(s.routePlanning))

	s.httpServer = &http.Server{
		Addr:        s.cfg.API.Bind,
		Handler:     mux,
		BaseContext: func(_ net.Listener) context.Context { return ctx },
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.httpServer.Shutdown(shutCtx)
	}()

	s.logger.Info("api server starting", "bind", s.cfg.API.Bind)
	err := s.httpServer.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// GET /status
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	running, _ := s.store.GetRunningDispatches()

	resp := map[string]any{
		"uptime_s":      time.Since(s.startTime).Seconds(),
		"running_count": len(running),
	}
	writeJSON(w, resp)
}

// GET /projects
func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	type projectInfo struct {
		Name     string `json:"name"`
		Enabled  bool   `json:"enabled"`
		Priority int    `json:"priority"`
	}
	var projects []projectInfo
	for name, proj := range s.cfg.Projects {
		projects = append(projects, projectInfo{
			Name:     name,
			Enabled:  proj.Enabled,
			Priority: proj.Priority,
		})
	}
	writeJSON(w, projects)
}

// GET /projects/{id}
func (s *Server) handleProjectDetail(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/projects/")
	if id == "" {
		s.handleProjects(w, r)
		return
	}

	proj, ok := s.cfg.Projects[id]
	if !ok {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}

	resp := map[string]any{
		"name":      id,
		"enabled":   proj.Enabled,
		"priority":  proj.Priority,
		"workspace": proj.Workspace,
		"beads_dir": proj.BeadsDir,
	}
	writeJSON(w, resp)
}

// GET /health
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	events, err := s.store.GetRecentHealthEvents(1)
	healthy := true
	var recentEvents []map[string]any

	if err == nil {
		for _, e := range events {
			if e.EventType == "gateway_critical" {
				healthy = false
			}
			recentEvents = append(recentEvents, map[string]any{
				"type":        e.EventType,
				"details":     e.Details,
				"dispatch_id": e.DispatchID,
				"bead_id":     e.BeadID,
				"time":        e.CreatedAt.Format(time.RFC3339),
			})
		}
	}

	if !healthy {
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	resp := map[string]any{
		"healthy":       healthy,
		"events_1h":     len(recentEvents),
		"recent_events": recentEvents,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// GET /metrics - Prometheus-compatible text format
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	running, _ := s.store.GetRunningDispatches()

	var b strings.Builder
	db := s.store.DB()

	// --- Dispatch counters ---
	var totalDispatches, totalFailed int
	db.QueryRow(`SELECT COUNT(*) FROM dispatches`).Scan(&totalDispatches)
	db.QueryRow(`SELECT COUNT(*) FROM dispatches WHERE status='failed'`).Scan(&totalFailed)

	fmt.Fprintf(&b, "# HELP chum_dispatches_total Total number of dispatches\n")
	fmt.Fprintf(&b, "# TYPE chum_dispatches_total counter\n")
	fmt.Fprintf(&b, "chum_dispatches_total %d\n", totalDispatches)

	fmt.Fprintf(&b, "# HELP chum_dispatches_failed_total Total number of failed dispatches\n")
	fmt.Fprintf(&b, "# TYPE chum_dispatches_failed_total counter\n")
	fmt.Fprintf(&b, "chum_dispatches_failed_total %d\n", totalFailed)

	fmt.Fprintf(&b, "# HELP chum_dispatches_running Current running dispatches\n")
	fmt.Fprintf(&b, "# TYPE chum_dispatches_running gauge\n")
	fmt.Fprintf(&b, "chum_dispatches_running %d\n", len(running))

	// Running dispatches by stage
	runningByStage, err := s.store.GetRunningDispatchStageCounts()
	if err != nil {
		s.logger.Warn("failed to get dispatch stage counts", "error", err)
	} else {
		fmt.Fprintf(&b, "# HELP chum_dispatches_running_by_stage Current number of running dispatches by stage\n")
		fmt.Fprintf(&b, "# TYPE chum_dispatches_running_by_stage gauge\n")

		stages := make([]string, 0, len(runningByStage))
		for stage := range runningByStage {
			stages = append(stages, stage)
		}
		sort.Strings(stages)
		for _, stage := range stages {
			fmt.Fprintf(&b, "chum_dispatches_running_by_stage{stage=%q} %d\n", stage, runningByStage[stage])
		}
	}

	// --- Token burn by project × agent × type ---
	fmt.Fprintf(&b, "# HELP chum_tokens_total Total tokens consumed by project, agent, and type\n")
	fmt.Fprintf(&b, "# TYPE chum_tokens_total counter\n")

	tokenRows, err := db.Query(`
		SELECT project, agent, 
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0),
			COALESCE(SUM(cache_read_tokens), 0),
			COALESCE(SUM(cache_creation_tokens), 0)
		FROM token_usage GROUP BY project, agent`)
	if err == nil {
		defer tokenRows.Close()
		for tokenRows.Next() {
			var proj, agent string
			var input, output, cacheRead, cacheCreate int64
			if tokenRows.Scan(&proj, &agent, &input, &output, &cacheRead, &cacheCreate) == nil {
				fmt.Fprintf(&b, "chum_tokens_total{project=%q,agent=%q,type=\"input\"} %d\n", proj, agent, input)
				fmt.Fprintf(&b, "chum_tokens_total{project=%q,agent=%q,type=\"output\"} %d\n", proj, agent, output)
				fmt.Fprintf(&b, "chum_tokens_total{project=%q,agent=%q,type=\"cache_read\"} %d\n", proj, agent, cacheRead)
				fmt.Fprintf(&b, "chum_tokens_total{project=%q,agent=%q,type=\"cache_creation\"} %d\n", proj, agent, cacheCreate)
			}
		}
	}

	// --- Cost USD by project × agent ---
	fmt.Fprintf(&b, "# HELP chum_cost_usd_total Estimated USD cost by project and agent\n")
	fmt.Fprintf(&b, "# TYPE chum_cost_usd_total counter\n")

	costRows, err := db.Query(`
		SELECT project, agent, COALESCE(SUM(cost_usd), 0)
		FROM token_usage GROUP BY project, agent`)
	if err == nil {
		defer costRows.Close()
		for costRows.Next() {
			var proj, agent string
			var cost float64
			if costRows.Scan(&proj, &agent, &cost) == nil {
				fmt.Fprintf(&b, "chum_cost_usd_total{project=%q,agent=%q} %.6f\n", proj, agent, cost)
			}
		}
	}

	// --- Token burn by activity (execute, review, plan) ---
	fmt.Fprintf(&b, "# HELP chum_activity_tokens_total Tokens consumed by activity type\n")
	fmt.Fprintf(&b, "# TYPE chum_activity_tokens_total counter\n")

	actRows, err := db.Query(`
		SELECT activity_name,
			COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0)
		FROM token_usage GROUP BY activity_name`)
	if err == nil {
		defer actRows.Close()
		for actRows.Next() {
			var act string
			var input, output int64
			if actRows.Scan(&act, &input, &output) == nil {
				fmt.Fprintf(&b, "chum_activity_tokens_total{activity=%q,type=\"input\"} %d\n", act, input)
				fmt.Fprintf(&b, "chum_activity_tokens_total{activity=%q,type=\"output\"} %d\n", act, output)
			}
		}
	}

	// --- Per-bead cost (top 20 spenders) ---
	fmt.Fprintf(&b, "# HELP chum_bead_cost_usd Per-bead estimated USD cost (top spenders)\n")
	fmt.Fprintf(&b, "# TYPE chum_bead_cost_usd gauge\n")

	beadCostRows, err := db.Query(`
		SELECT bead_id, project, COALESCE(SUM(cost_usd), 0) as total_cost,
			COALESCE(SUM(input_tokens + output_tokens), 0) as total_tokens
		FROM token_usage GROUP BY bead_id ORDER BY total_cost DESC LIMIT 20`)
	if err == nil {
		defer beadCostRows.Close()
		for beadCostRows.Next() {
			var beadID, proj string
			var cost float64
			var tokens int64
			if beadCostRows.Scan(&beadID, &proj, &cost, &tokens) == nil {
				fmt.Fprintf(&b, "chum_bead_cost_usd{bead_id=%q,project=%q} %.6f\n", beadID, proj, cost)
				fmt.Fprintf(&b, "chum_bead_tokens_total{bead_id=%q,project=%q} %d\n", beadID, proj, tokens)
			}
		}
	}

	// --- Workflow health: DoD pass/fail/escalate ---
	fmt.Fprintf(&b, "# HELP chum_dod_results_total DoD check results by outcome\n")
	fmt.Fprintf(&b, "# TYPE chum_dod_results_total counter\n")

	var dodPassed, dodFailed, dodTotal int
	db.QueryRow(`SELECT COUNT(*) FROM dod_results WHERE passed = 1`).Scan(&dodPassed)
	db.QueryRow(`SELECT COUNT(*) FROM dod_results WHERE passed = 0`).Scan(&dodFailed)
	db.QueryRow(`SELECT COUNT(*) FROM dod_results`).Scan(&dodTotal)

	fmt.Fprintf(&b, "chum_dod_results_total{result=\"passed\"} %d\n", dodPassed)
	fmt.Fprintf(&b, "chum_dod_results_total{result=\"failed\"} %d\n", dodFailed)

	if dodTotal > 0 {
		fmt.Fprintf(&b, "# HELP chum_dod_pass_rate DoD pass rate (0-1)\n")
		fmt.Fprintf(&b, "# TYPE chum_dod_pass_rate gauge\n")
		fmt.Fprintf(&b, "chum_dod_pass_rate %.4f\n", float64(dodPassed)/float64(dodTotal))
	}

	// --- Dispatch outcomes by status ---
	fmt.Fprintf(&b, "# HELP chum_dispatch_outcomes_total Dispatch outcomes by status\n")
	fmt.Fprintf(&b, "# TYPE chum_dispatch_outcomes_total counter\n")

	statusRows, err := db.Query(`
		SELECT COALESCE(status, 'unknown'), COUNT(*) FROM dispatches GROUP BY status`)
	if err == nil {
		defer statusRows.Close()
		for statusRows.Next() {
			var status string
			var count int
			if statusRows.Scan(&status, &count) == nil {
				fmt.Fprintf(&b, "chum_dispatch_outcomes_total{status=%q} %d\n", status, count)
			}
		}
	}

	// --- Workflow duration (average by status) ---
	fmt.Fprintf(&b, "# HELP chum_dispatch_duration_seconds_avg Average dispatch duration by status\n")
	fmt.Fprintf(&b, "# TYPE chum_dispatch_duration_seconds_avg gauge\n")

	durRows, err := db.Query(`
		SELECT COALESCE(status, 'unknown'), AVG(duration_s), COUNT(*)
		FROM dispatches WHERE duration_s > 0 GROUP BY status`)
	if err == nil {
		defer durRows.Close()
		for durRows.Next() {
			var status string
			var avgDur float64
			var count int
			if durRows.Scan(&status, &avgDur, &count) == nil {
				fmt.Fprintf(&b, "chum_dispatch_duration_seconds_avg{status=%q} %.2f\n", status, avgDur)
				fmt.Fprintf(&b, "chum_dispatch_duration_seconds_count{status=%q} %d\n", status, count)
			}
		}
	}

	// --- Retry / handoff overhead (beads with multiple dispatches) ---
	fmt.Fprintf(&b, "# HELP chum_bead_retry_overhead Beads with highest dispatch attempts (inefficiency indicator)\n")
	fmt.Fprintf(&b, "# TYPE chum_bead_retry_overhead gauge\n")

	retryRows, err := db.Query(`
		SELECT bead_id, COUNT(*) as attempts FROM dispatches
		GROUP BY bead_id HAVING attempts > 1
		ORDER BY attempts DESC LIMIT 10`)
	if err == nil {
		defer retryRows.Close()
		for retryRows.Next() {
			var beadID string
			var attempts int
			if retryRows.Scan(&beadID, &attempts) == nil {
				fmt.Fprintf(&b, "chum_bead_retry_overhead{bead_id=%q} %d\n", beadID, attempts)
			}
		}
	}

	// --- Safety block metrics ---
	blockCounts, err := s.store.GetBlockCountsByType()
	if err != nil {
		s.logger.Warn("failed to get safety block counts", "error", err)
	} else {
		fmt.Fprintf(&b, "# HELP chum_safety_blocks_active Active safety blocks by type\n")
		fmt.Fprintf(&b, "# TYPE chum_safety_blocks_active gauge\n")

		types := make([]string, 0, len(blockCounts))
		for bt := range blockCounts {
			types = append(types, bt)
		}
		sort.Strings(types)
		for _, bt := range types {
			fmt.Fprintf(&b, "chum_safety_blocks_active{block_type=%q} %d\n", bt, blockCounts[bt])
		}

		var total int
		for _, c := range blockCounts {
			total += c
		}
		fmt.Fprintf(&b, "# HELP chum_safety_blocks_total Total active safety blocks\n")
		fmt.Fprintf(&b, "# TYPE chum_safety_blocks_total gauge\n")
		fmt.Fprintf(&b, "chum_safety_blocks_total %d\n", total)
	}

	// --- Uptime ---
	fmt.Fprintf(&b, "# HELP chum_uptime_seconds Uptime in seconds\n")
	fmt.Fprintf(&b, "# TYPE chum_uptime_seconds gauge\n")
	fmt.Fprintf(&b, "chum_uptime_seconds %.0f\n", time.Since(s.startTime).Seconds())

	w.Write([]byte(b.String()))
}

// GET /safety/blocks — active safety blocks with counts
func (s *Server) handleSafetyBlocks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	blocks, err := s.store.GetActiveBlocks()
	if err != nil {
		s.logger.Error("failed to get active blocks", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to query active blocks")
		return
	}

	type blockResponse struct {
		Scope        string         `json:"scope"`
		BlockType    string         `json:"block_type"`
		BlockedUntil string         `json:"blocked_until"`
		Reason       string         `json:"reason"`
		Metadata     map[string]any `json:"metadata,omitempty"`
		CreatedAt    string         `json:"created_at"`
	}

	countsByType := make(map[string]int)
	var items []blockResponse
	for _, b := range blocks {
		countsByType[b.BlockType]++
		items = append(items, blockResponse{
			Scope:        b.Scope,
			BlockType:    b.BlockType,
			BlockedUntil: b.BlockedUntil.Format(time.RFC3339),
			Reason:       b.Reason,
			Metadata:     b.Metadata,
			CreatedAt:    b.CreatedAt.Format(time.RFC3339),
		})
	}

	resp := map[string]any{
		"total":          len(items),
		"counts_by_type": countsByType,
		"blocks":         items,
	}

	writeJSON(w, resp)
}

// GET /dispatches/{bead_id} — dispatch history for a bead
func (s *Server) handleDispatchDetail(w http.ResponseWriter, r *http.Request) {
	beadID := strings.TrimPrefix(r.URL.Path, "/dispatches/")
	if beadID == "" {
		writeError(w, http.StatusBadRequest, "bead_id required")
		return
	}

	dispatches, err := s.store.GetDispatchesByBead(beadID)
	if err != nil {
		s.logger.Error("failed to query dispatches", "bead_id", beadID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to query dispatches")
		return
	}

	type dispatchResponse struct {
		ID              int64   `json:"id"`
		Agent           string  `json:"agent"`
		Provider        string  `json:"provider"`
		Tier            string  `json:"tier"`
		Status          string  `json:"status"`
		Stage           string  `json:"stage"`
		ExitCode        int     `json:"exit_code"`
		DurationS       float64 `json:"duration_s"`
		DispatchedAt    string  `json:"dispatched_at"`
		SessionName     string  `json:"session_name"`
		OutputTail      string  `json:"output_tail"`
		FailureCategory string  `json:"failure_category,omitempty"`
		FailureSummary  string  `json:"failure_summary,omitempty"`
	}

	var dispatchList []dispatchResponse
	for _, d := range dispatches {
		outputTail, err := s.store.GetOutputTail(d.ID)
		if err != nil {
			outputTail = ""
		}

		dispatchList = append(dispatchList, dispatchResponse{
			ID:              d.ID,
			Agent:           d.AgentID,
			Provider:        d.Provider,
			Tier:            d.Tier,
			Status:          d.Status,
			Stage:           d.Stage,
			ExitCode:        d.ExitCode,
			DurationS:       d.DurationS,
			DispatchedAt:    d.DispatchedAt.Format(time.RFC3339),
			SessionName:     d.SessionName,
			OutputTail:      outputTail,
			FailureCategory: d.FailureCategory,
			FailureSummary:  d.FailureSummary,
		})
	}

	resp := map[string]any{
		"bead_id":    beadID,
		"dispatches": dispatchList,
	}

	writeJSON(w, resp)
}

// GET /recommendations - Returns recent system recommendations
func (s *Server) handleRecommendations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	hoursStr := r.URL.Query().Get("hours")
	hours := 24
	if hoursStr != "" {
		if h, err := fmt.Sscanf(hoursStr, "%d", &hours); err != nil || h == 0 {
			hours = 24
		}
		if hours <= 0 || hours > 168 {
			hours = 24
		}
	}

	// Legacy recommendation store removed — replaced by CHUM lessons store.
	// TODO: wire lessons search via store.SearchLessons when V2 UI is ready.
	resp := map[string]any{
		"recommendations": []any{},
		"hours":           hours,
		"count":           0,
		"generated_at":    time.Now(),
	}

	writeJSON(w, resp)
}

// --- Temporal Workflow Endpoints ---

// POST /workflows/start — submit a task to Temporal
func (s *Server) handleWorkflowStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req temporal.TaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json request body")
		return
	}

	if req.TaskID == "" || req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "task_id and prompt are required")
		return
	}
	if req.Agent == "" {
		req.Agent = "claude"
	}
	if req.WorkDir == "" {
		req.WorkDir = "/tmp/workspace"
	}
	if req.SlowStepThreshold == 0 {
		req.SlowStepThreshold = s.cfg.General.SlowStepThreshold.Duration
	}

	c, err := client.Dial(client.Options{HostPort: "127.0.0.1:7233"})
	if err != nil {
		s.logger.Error("failed to connect to temporal", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to connect to temporal")
		return
	}
	defer c.Close()

	wo := client.StartWorkflowOptions{
		ID:        req.TaskID,
		TaskQueue: "chum-task-queue",
	}

	we, err := c.ExecuteWorkflow(context.Background(), wo, temporal.ChumAgentWorkflow, req)
	if err != nil {
		s.logger.Error("failed to start workflow", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to start workflow")
		return
	}

	s.logger.Info("workflow started", "workflow_id", we.GetID(), "run_id", we.GetRunID())

	writeJSON(w, map[string]any{
		"workflow_id": we.GetID(),
		"run_id":      we.GetRunID(),
		"status":      "started",
	})
}

// routeWorkflows routes /workflows/{id}/* to the appropriate handler
func (s *Server) routeWorkflows(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/workflows/")

	if strings.HasSuffix(path, "/approve") {
		s.handleWorkflowApprove(w, r)
		return
	}
	if strings.HasSuffix(path, "/reject") {
		s.handleWorkflowReject(w, r)
		return
	}

	// GET /workflows/{id} — query workflow status
	s.handleWorkflowStatus(w, r)
}

// POST /workflows/{id}/approve — send human-approval signal
func (s *Server) handleWorkflowApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/workflows/")
	workflowID := strings.TrimSuffix(path, "/approve")

	c, err := client.Dial(client.Options{HostPort: "127.0.0.1:7233"})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to connect to temporal")
		return
	}
	defer c.Close()

	err = c.SignalWorkflow(context.Background(), workflowID, "", "human-approval", "APPROVED")
	if err != nil {
		s.logger.Error("failed to signal workflow", "workflow_id", workflowID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to approve workflow")
		return
	}

	writeJSON(w, map[string]any{"workflow_id": workflowID, "status": "approved"})
}

// POST /workflows/{id}/reject — send rejection signal
func (s *Server) handleWorkflowReject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/workflows/")
	workflowID := strings.TrimSuffix(path, "/reject")

	c, err := client.Dial(client.Options{HostPort: "127.0.0.1:7233"})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to connect to temporal")
		return
	}
	defer c.Close()

	err = c.SignalWorkflow(context.Background(), workflowID, "", "human-approval", "REJECTED")
	if err != nil {
		s.logger.Error("failed to signal workflow", "workflow_id", workflowID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to reject workflow")
		return
	}

	writeJSON(w, map[string]any{"workflow_id": workflowID, "status": "rejected"})
}

// GET /workflows/{id} — query workflow status
func (s *Server) handleWorkflowStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	workflowID := strings.TrimPrefix(r.URL.Path, "/workflows/")
	if workflowID == "" {
		writeError(w, http.StatusBadRequest, "workflow_id required")
		return
	}

	c, err := client.Dial(client.Options{HostPort: "127.0.0.1:7233"})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to connect to temporal")
		return
	}
	defer c.Close()

	desc, err := c.DescribeWorkflowExecution(context.Background(), workflowID, "")
	if err != nil {
		s.logger.Error("failed to describe workflow", "workflow_id", workflowID, "error", err)
		writeError(w, http.StatusNotFound, "workflow not found")
		return
	}

	info := desc.WorkflowExecutionInfo
	resp := map[string]any{
		"workflow_id": info.Execution.WorkflowId,
		"run_id":      info.Execution.RunId,
		"type":        info.Type.Name,
		"status":      info.Status.String(),
		"start_time":  info.StartTime.AsTime().Format(time.RFC3339),
	}

	if info.CloseTime != nil {
		resp["close_time"] = info.CloseTime.AsTime().Format(time.RFC3339)
	}

	writeJSON(w, resp)
}

// --- Planning Ceremony Endpoints ---

// POST /planning/start — start an interactive planning session
func (s *Server) handlePlanningStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req temporal.PlanningRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json request body")
		return
	}
	if req.Project == "" {
		writeError(w, http.StatusBadRequest, "project is required")
		return
	}
	if req.Agent == "" {
		req.Agent = "claude"
	}
	if req.WorkDir == "" {
		writeError(w, http.StatusBadRequest, "work_dir is required")
		return
	}
	if req.SlowStepThreshold == 0 {
		req.SlowStepThreshold = s.cfg.General.SlowStepThreshold.Duration
	}

	c, err := client.Dial(client.Options{HostPort: "127.0.0.1:7233"})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to connect to temporal")
		return
	}
	defer c.Close()

	sessionID := fmt.Sprintf("planning-%s-%d", req.Project, time.Now().Unix())
	wo := client.StartWorkflowOptions{
		ID:        sessionID,
		TaskQueue: "chum-task-queue",
	}

	we, err := c.ExecuteWorkflow(context.Background(), wo, temporal.PlanningCeremonyWorkflow, req)
	if err != nil {
		s.logger.Error("failed to start planning session", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to start planning session")
		return
	}

	s.logger.Info("planning session started", "session_id", sessionID)

	writeJSON(w, map[string]any{
		"session_id": we.GetID(),
		"run_id":     we.GetRunID(),
		"status":     "grooming_backlog",
	})
}

// routePlanning routes /planning/{id}/* to the appropriate handler
func (s *Server) routePlanning(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/planning/")

	if strings.HasSuffix(path, "/select") {
		s.handlePlanningSignal(w, r, "item-selected")
		return
	}
	if strings.HasSuffix(path, "/answer") {
		s.handlePlanningSignal(w, r, "answer")
		return
	}
	if strings.HasSuffix(path, "/greenlight") {
		s.handlePlanningSignal(w, r, "greenlight")
		return
	}

	// GET /planning/{id} — query planning session status
	s.handlePlanningStatus(w, r)
}

// POST /planning/{id}/select, /answer, /greenlight — send signal to planning workflow
func (s *Server) handlePlanningSignal(w http.ResponseWriter, r *http.Request, signalName string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/planning/")
	// Remove the signal suffix to get the workflow ID
	for _, suffix := range []string{"/select", "/answer", "/greenlight"} {
		path = strings.TrimSuffix(path, suffix)
	}
	workflowID := path

	var req struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json — need {\"value\": \"...\"}")
		return
	}

	c, err := client.Dial(client.Options{HostPort: "127.0.0.1:7233"})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to connect to temporal")
		return
	}
	defer c.Close()

	if err := c.SignalWorkflow(context.Background(), workflowID, "", signalName, req.Value); err != nil {
		s.logger.Error("failed to signal planning workflow", "signal", signalName, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to send signal")
		return
	}

	writeJSON(w, map[string]any{
		"session_id": workflowID,
		"signal":     signalName,
		"value":      req.Value,
	})
}

// GET /planning/{id} — query planning session status
func (s *Server) handlePlanningStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	workflowID := strings.TrimPrefix(r.URL.Path, "/planning/")
	if workflowID == "" {
		writeError(w, http.StatusBadRequest, "session_id required")
		return
	}

	c, err := client.Dial(client.Options{HostPort: "127.0.0.1:7233"})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to connect to temporal")
		return
	}
	defer c.Close()

	desc, err := c.DescribeWorkflowExecution(context.Background(), workflowID, "")
	if err != nil {
		writeError(w, http.StatusNotFound, "planning session not found")
		return
	}

	info := desc.WorkflowExecutionInfo
	resp := map[string]any{
		"session_id": info.Execution.WorkflowId,
		"run_id":     info.Execution.RunId,
		"status":     info.Status.String(),
		"start_time": info.StartTime.AsTime().Format(time.RFC3339),
	}

	if info.CloseTime != nil {
		resp["close_time"] = info.CloseTime.AsTime().Format(time.RFC3339)
	}

	// Check for pending signals to infer phase
	if info.Status.String() == "Running" {
		resp["note"] = "Check chum logs for current phase (backlog/selecting/questioning/summarizing/greenlight)"
	}

	writeJSON(w, resp)
}
