package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"go.temporal.io/sdk/client"

	"github.com/antigravity-dev/chum/internal/temporal"
)

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

	c, err := s.temporalClient()
	if err != nil {
		s.logger.Error("failed to connect to temporal", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to connect to temporal")
		return
	}
	defer c.Close()

	wo := client.StartWorkflowOptions{
		ID:        req.TaskID,
		TaskQueue: temporal.DefaultTaskQueue,
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

	c, err := s.temporalClient()
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

	c, err := s.temporalClient()
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

	c, err := s.temporalClient()
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
