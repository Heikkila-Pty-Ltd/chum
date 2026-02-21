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

// POST /crab/decompose — start a crab decomposition workflow
func (s *Server) handleCrabDecompose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req temporal.CrabDecompositionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json request body")
		return
	}
	if req.Project == "" {
		writeError(w, http.StatusBadRequest, "project is required")
		return
	}
	if req.PlanMarkdown == "" {
		writeError(w, http.StatusBadRequest, "plan_markdown is required")
		return
	}
	if req.WorkDir == "" {
		writeError(w, http.StatusBadRequest, "work_dir is required")
		return
	}
	if req.PlanID == "" {
		req.PlanID = fmt.Sprintf("plan-%s-%d", req.Project, time.Now().Unix())
	}

	c, err := s.temporalClient()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to connect to temporal")
		return
	}
	defer c.Close()

	sessionID := fmt.Sprintf("crab-%s-%d", req.Project, time.Now().Unix())
	wo := client.StartWorkflowOptions{
		ID:        sessionID,
		TaskQueue: temporal.DefaultTaskQueue,
	}

	we, err := c.ExecuteWorkflow(context.Background(), wo, temporal.CrabDecompositionWorkflow, req)
	if err != nil {
		s.logger.Error("failed to start crab decomposition", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to start crab decomposition")
		return
	}

	s.logger.Info("crab decomposition started", "session_id", sessionID, "plan_id", req.PlanID)

	writeJSON(w, map[string]any{
		"session_id": we.GetID(),
		"run_id":     we.GetRunID(),
		"plan_id":    req.PlanID,
		"status":     "parsing",
	})
}

// routeCrab routes /crab/{id}/* to the appropriate handler.
func (s *Server) routeCrab(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/crab/")

	if strings.HasSuffix(path, "/clarify") {
		s.handleCrabSignal(w, r, "crab-clarification")
		return
	}
	if strings.HasSuffix(path, "/review") {
		s.handleCrabSignal(w, r, "crab-review")
		return
	}

	// GET /crab/{id} — query decomposition status
	s.handleCrabStatus(w, r)
}

// POST /crab/{id}/clarify, /review — send signal to crab workflow
func (s *Server) handleCrabSignal(w http.ResponseWriter, r *http.Request, signalName string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/crab/")
	for _, suffix := range []string{"/clarify", "/review"} {
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
		s.logger.Error("failed to signal crab workflow", "signal", signalName, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to send signal")
		return
	}

	writeJSON(w, map[string]any{
		"session_id": workflowID,
		"signal":     signalName,
		"value":      req.Value,
	})
}

// GET /crab/{id} — query crab decomposition status
func (s *Server) handleCrabStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	workflowID := strings.TrimPrefix(r.URL.Path, "/crab/")
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
		writeError(w, http.StatusNotFound, "crab session not found")
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

	if info.Status.String() == "Running" {
		resp["note"] = "Check chum logs for current phase (parse/clarify/decompose/scope/size/review/emit)"
	}

	writeJSON(w, resp)
}
