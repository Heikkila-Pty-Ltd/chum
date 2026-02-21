package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.temporal.io/sdk/client"

	"github.com/antigravity-dev/chum/internal/temporal"
)

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

	c, err := s.temporalClient()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to connect to temporal")
		return
	}
	defer c.Close()

	sessionID := fmt.Sprintf("planning-%s-%d", req.Project, time.Now().Unix())
	wo := client.StartWorkflowOptions{
		ID:        sessionID,
		TaskQueue: temporal.DefaultTaskQueue,
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

	c, err := s.temporalClient()
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

	c, err := s.temporalClient()
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
