package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.temporal.io/sdk/client"

	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/graph"
	"github.com/antigravity-dev/chum/internal/temporal"
)

// --- Task API request/response types ---

// CreateTaskRequest is the JSON body for POST /tasks.
type CreateTaskRequest struct {
	ID              string   `json:"id,omitempty"`
	Title           string   `json:"title"`
	Description     string   `json:"description,omitempty"`
	Status          string   `json:"status,omitempty"`
	Priority        int      `json:"priority"`
	Type            string   `json:"type,omitempty"`
	Labels          []string `json:"labels,omitempty"`
	EstimateMinutes int      `json:"estimate_minutes,omitempty"`
	ParentID        string   `json:"parent_id,omitempty"`
	Acceptance      string   `json:"acceptance_criteria,omitempty"`
	Design          string   `json:"design,omitempty"`
	Notes           string   `json:"notes,omitempty"`
	DependsOn       []string `json:"depends_on,omitempty"`
	Project         string   `json:"project"`
}

// POST /tasks — create a new task in the DAG
func (s *Server) handleTaskCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.dag == nil {
		writeError(w, http.StatusServiceUnavailable, "DAG not initialized")
		return
	}

	var req CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}

	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	if req.Project == "" {
		writeError(w, http.StatusBadRequest, "project is required")
		return
	}

	// Validate project exists in config
	proj, ok := s.cfg.Projects[req.Project]
	if !ok || !proj.Enabled {
		writeError(w, http.StatusBadRequest, "project not found or not enabled: "+req.Project)
		return
	}

	// Defaults
	if req.Status == "" {
		req.Status = "ready"
	}
	if req.Type == "" {
		req.Type = "task"
	}

	task := graph.Task{
		Title:           req.Title,
		Description:     req.Description,
		Status:          req.Status,
		Priority:        req.Priority,
		Type:            req.Type,
		Labels:          req.Labels,
		EstimateMinutes: req.EstimateMinutes,
		ParentID:        req.ParentID,
		Acceptance:      req.Acceptance,
		Design:          req.Design,
		Notes:           req.Notes,
		Project:         req.Project,
	}

	// If caller supplies an explicit ID, use UpdateTask to upsert; otherwise generate.
	ctx := r.Context()
	id, err := s.dag.CreateTask(ctx, task)
	if err != nil {
		s.logger.Error("failed to create task", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create task: "+err.Error())
		return
	}

	// Add dependency edges
	for _, dep := range req.DependsOn {
		dep = strings.TrimSpace(dep)
		if dep == "" {
			continue
		}
		if edgeErr := s.dag.AddEdge(ctx, id, dep); edgeErr != nil {
			s.logger.Warn("failed to add dependency edge", "from", id, "to", dep, "error", edgeErr)
		}
	}

	s.logger.Info("task created via API", "id", id, "project", req.Project, "priority", req.Priority)

	// Fire async crab review to validate sizing and dependencies
	s.triggerCrabReview(req.Project, id, req.Title, req.Description)

	w.WriteHeader(http.StatusCreated)
	writeJSON(w, map[string]any{
		"id":      id,
		"status":  req.Status,
		"project": req.Project,
	})
}

// GET /tasks?project=<name>&status=<status> — list tasks
func (s *Server) handleTaskList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.dag == nil {
		writeError(w, http.StatusServiceUnavailable, "DAG not initialized")
		return
	}

	project := r.URL.Query().Get("project")
	if project == "" {
		writeError(w, http.StatusBadRequest, "project query parameter is required")
		return
	}

	status := r.URL.Query().Get("status")
	var statuses []string
	if status != "" {
		statuses = strings.Split(status, ",")
	}

	tasks, err := s.dag.ListTasks(r.Context(), project, statuses...)
	if err != nil {
		s.logger.Error("failed to list tasks", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list tasks: "+err.Error())
		return
	}

	writeJSON(w, map[string]any{
		"project": project,
		"count":   len(tasks),
		"tasks":   tasks,
	})
}

// GET /tasks/{id} — get a single task
func (s *Server) handleTaskGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.dag == nil {
		writeError(w, http.StatusServiceUnavailable, "DAG not initialized")
		return
	}

	taskID := strings.TrimPrefix(r.URL.Path, "/tasks/")
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "task id required")
		return
	}

	task, err := s.dag.GetTask(r.Context(), taskID)
	if err != nil {
		writeError(w, http.StatusNotFound, "task not found: "+taskID)
		return
	}

	writeJSON(w, task)
}

// routeTasks dispatches /tasks and /tasks/* requests.
func (s *Server) routeTasks(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/tasks")

	// POST /tasks — create
	if r.Method == http.MethodPost && (path == "" || path == "/") {
		s.handleTaskCreate(w, r)
		return
	}

	// GET /tasks?project=... — list
	if r.Method == http.MethodGet && (path == "" || path == "/") {
		s.handleTaskList(w, r)
		return
	}

	// GET /tasks/{id} — single task
	if r.Method == http.MethodGet && len(path) > 1 {
		s.handleTaskGet(w, r)
		return
	}

	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

// triggerCrabReview fires an async crab decomposition workflow to review
// the newly-created task's sizing, dependencies and acceptance criteria.
func (s *Server) triggerCrabReview(project, taskID, title, description string) {
	proj, ok := s.cfg.Projects[project]
	if !ok {
		return
	}
	workDir := config.ExpandHome(strings.TrimSpace(proj.Workspace))

	// Build a minimal plan markdown for the crab to review.
	planMD := fmt.Sprintf("# Review Task: %s\n\nTask ID: %s\nProject: %s\n\n%s\n\nPlease review this task for appropriate sizing, dependencies, and acceptance criteria.",
		title, taskID, project, description)

	req := temporal.CrabDecompositionRequest{
		PlanID:             fmt.Sprintf("review-%s-%d", taskID, time.Now().Unix()),
		Project:            project,
		WorkDir:            workDir,
		PlanMarkdown:       planMD,
		Tier:               "fast",
		RequireHumanReview: false,
	}

	c, err := s.temporalClient()
	if err != nil {
		s.logger.Warn("crab review: failed to connect to temporal", "task", taskID, "error", err)
		return
	}
	defer c.Close()

	wo := client.StartWorkflowOptions{
		ID:        fmt.Sprintf("crab-review-%s-%d", taskID, time.Now().Unix()),
		TaskQueue: temporal.DefaultTaskQueue,
	}

	we, err := c.ExecuteWorkflow(context.Background(), wo, temporal.CrabDecompositionWorkflow, req)
	if err != nil {
		s.logger.Warn("crab review: failed to start workflow", "task", taskID, "error", err)
		return
	}

	s.logger.Info("🦀 crab review triggered for new task", "task", taskID, "workflow_id", we.GetID())
}
