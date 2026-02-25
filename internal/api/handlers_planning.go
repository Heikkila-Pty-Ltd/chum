package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.temporal.io/sdk/client"

	"github.com/antigravity-dev/chum/internal/store"
	"github.com/antigravity-dev/chum/internal/temporal"
)

const (
	planningPromptTraceLimit = 500
)

type planningPromptResponse struct {
	SessionID      string                      `json:"session_id"`
	RunID          string                      `json:"run_id,omitempty"`
	Status         string                      `json:"status,omitempty"`
	Phase          string                      `json:"phase"`
	ExpectedSignal string                      `json:"expected_signal,omitempty"`
	Prompt         string                      `json:"prompt"`
	Options        []string                    `json:"options,omitempty"`
	Recommendation string                      `json:"recommendation,omitempty"`
	Context        string                      `json:"context,omitempty"`
	Cycle          int                         `json:"cycle,omitempty"`
	SelectedItem   *planningPromptSelectedItem `json:"selected_item,omitempty"`
}

type planningPromptSelectedItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type planningCandidateOption struct {
	ID          string
	Title       string
	Rank        int
	Shortlisted bool
	Recommended bool
}

// POST /planning/start — start an interactive planning session.
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
	if strings.TrimSpace(req.Project) == "" {
		writeError(w, http.StatusBadRequest, "project is required")
		return
	}
	if strings.TrimSpace(req.Agent) == "" {
		req.Agent = "claude"
	}
	if strings.TrimSpace(req.WorkDir) == "" {
		writeError(w, http.StatusBadRequest, "work_dir is required")
		return
	}
	if req.SlowStepThreshold == 0 {
		req.SlowStepThreshold = s.cfg.General.SlowStepThreshold.Duration
	}
	if req.CandidateTopK <= 0 {
		req.CandidateTopK = s.cfg.Dispatch.CostControl.PlanningCandidateTopK
	}
	if req.SignalTimeout <= 0 {
		req.SignalTimeout = s.cfg.Dispatch.CostControl.PlanningSignalTimeout.Duration
	}
	if req.SessionTimeout <= 0 {
		req.SessionTimeout = s.cfg.Dispatch.CostControl.PlanningSessionTimeout.Duration
	}

	c, err := s.temporalClient()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to connect to temporal")
		return
	}
	defer c.Close()

	sessionID := fmt.Sprintf("planning-%s-%d", strings.TrimSpace(req.Project), time.Now().Unix())
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

	source := planningTraceActorFromRequest(r)
	s.recordPlanningControlTrace(
		we.GetID(),
		req.Project,
		"control",
		"control_session_started",
		source,
		"planning session started via control channel",
		fmt.Sprintf(
			"project=%s work_dir=%s agent=%s tier=%s top_k=%d signal_timeout=%s session_timeout=%s",
			req.Project,
			req.WorkDir,
			req.Agent,
			req.Tier,
			req.CandidateTopK,
			req.SignalTimeout,
			req.SessionTimeout,
		),
		map[string]any{
			"source":          source,
			"project":         req.Project,
			"work_dir":        req.WorkDir,
			"agent":           req.Agent,
			"tier":            req.Tier,
			"candidate_top_k": req.CandidateTopK,
			"signal_timeout":  req.SignalTimeout.String(),
			"session_timeout": req.SessionTimeout.String(),
		},
	)

	s.logger.Info("planning session started", "session_id", sessionID)
	writeJSON(w, map[string]any{
		"session_id": we.GetID(),
		"run_id":     we.GetRunID(),
		"status":     "grooming_backlog",
	})
}

// routePlanning routes /planning/{id}/* to the appropriate handler.
func (s *Server) routePlanning(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/planning/")

	switch {
	case strings.HasSuffix(path, "/select"):
		s.handlePlanningSignal(w, r, "item-selected")
		return
	case strings.HasSuffix(path, "/answer"):
		s.handlePlanningSignal(w, r, "answer")
		return
	case strings.HasSuffix(path, "/greenlight"):
		s.handlePlanningSignal(w, r, "greenlight")
		return
	case strings.HasSuffix(path, "/prompt"):
		s.handlePlanningPrompt(w, r)
		return
	case strings.HasSuffix(path, "/stop"):
		s.handlePlanningStop(w, r)
		return
	default:
		// GET /planning/{id} — query planning session status
		s.handlePlanningStatus(w, r)
		return
	}
}

// POST /planning/{id}/select, /answer, /greenlight — send signal to planning workflow.
func (s *Server) handlePlanningSignal(w http.ResponseWriter, r *http.Request, signalName string) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	workflowID := planningWorkflowIDFromPath(r.URL.Path, "/select", "/answer", "/greenlight")
	if strings.TrimSpace(workflowID) == "" {
		writeError(w, http.StatusBadRequest, "session_id required")
		return
	}

	var req struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json - need {\"value\": \"...\"}")
		return
	}

	c, err := s.temporalClient()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to connect to temporal")
		return
	}
	defer c.Close()

	value := strings.TrimSpace(req.Value)
	if err := c.SignalWorkflow(context.Background(), workflowID, "", signalName, value); err != nil {
		s.logger.Error("failed to signal planning workflow", "signal", signalName, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to send signal")
		return
	}

	source := planningTraceActorFromRequest(r)
	s.recordPlanningControlTrace(
		workflowID,
		s.inferPlanningProject(workflowID),
		planningStageForSignal(signalName),
		"control_signal_submitted",
		source,
		fmt.Sprintf("signal %s submitted", signalName),
		fmt.Sprintf("signal=%s value=%s", signalName, value),
		map[string]any{
			"source": source,
			"signal": signalName,
			"value":  value,
		},
	)

	writeJSON(w, map[string]any{
		"session_id": workflowID,
		"signal":     signalName,
		"value":      value,
	})
}

// GET /planning/{id}/prompt — infer and return the next interactive prompt.
func (s *Server) handlePlanningPrompt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	workflowID := planningWorkflowIDFromPath(r.URL.Path, "/prompt")
	if strings.TrimSpace(workflowID) == "" {
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

	events, err := s.store.ListPlanningTraceEvents(workflowID, planningPromptTraceLimit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load planning trace")
		return
	}

	prompt := buildPlanningPromptResponse(workflowID, desc.WorkflowExecutionInfo.Status.String(), events)
	prompt.RunID = desc.WorkflowExecutionInfo.Execution.RunId

	source := planningTraceActorFromRequest(r)
	s.recordPlanningControlTrace(
		workflowID,
		s.inferPlanningProject(workflowID),
		"control_prompt",
		"control_prompt_presented",
		source,
		fmt.Sprintf("prompt presented phase=%s signal=%s", prompt.Phase, prompt.ExpectedSignal),
		fmt.Sprintf("prompt=%s", prompt.Prompt),
		map[string]any{
			"source":          source,
			"phase":           prompt.Phase,
			"expected_signal": prompt.ExpectedSignal,
			"cycle":           prompt.Cycle,
		},
	)

	writeJSON(w, prompt)
}

// POST /planning/{id}/stop — terminate an active planning session.
func (s *Server) handlePlanningStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	workflowID := planningWorkflowIDFromPath(r.URL.Path, "/stop")
	if strings.TrimSpace(workflowID) == "" {
		writeError(w, http.StatusBadRequest, "session_id required")
		return
	}

	var req struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid json request body")
			return
		}
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "stopped by control channel"
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
	status := desc.WorkflowExecutionInfo.Status.String()
	if !strings.EqualFold(status, "Running") {
		writeJSON(w, map[string]any{
			"session_id": workflowID,
			"status":     status,
			"stopped":    false,
			"note":       "workflow is already closed",
		})
		return
	}

	if err := c.TerminateWorkflow(context.Background(), workflowID, "", reason); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to stop planning session")
		return
	}

	source := planningTraceActorFromRequest(r)
	s.recordPlanningControlTrace(
		workflowID,
		s.inferPlanningProject(workflowID),
		"control",
		"control_session_stopped",
		source,
		"planning session stopped via control channel",
		fmt.Sprintf("reason=%s", reason),
		map[string]any{
			"source": source,
			"reason": reason,
		},
	)

	writeJSON(w, map[string]any{
		"session_id": workflowID,
		"status":     "terminated",
		"stopped":    true,
		"reason":     reason,
	})
}

// GET /planning/{id} — query planning session status.
func (s *Server) handlePlanningStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	workflowID := planningWorkflowIDFromPath(r.URL.Path, "/status")
	if strings.TrimSpace(workflowID) == "" {
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
	if info.Status.String() == "Running" {
		resp["note"] = "Use GET /planning/{id}/prompt to fetch the next actionable planning prompt."
	}

	source := planningTraceActorFromRequest(r)
	s.recordPlanningControlTrace(
		workflowID,
		s.inferPlanningProject(workflowID),
		"control_status",
		"control_status_requested",
		source,
		fmt.Sprintf("status requested: %s", info.Status.String()),
		fmt.Sprintf("status=%s run_id=%s", info.Status.String(), info.Execution.RunId),
		map[string]any{
			"source": source,
			"status": info.Status.String(),
		},
	)

	writeJSON(w, resp)
}

func planningWorkflowIDFromPath(path string, suffixes ...string) string {
	id := strings.TrimPrefix(path, "/planning/")
	for _, suffix := range suffixes {
		if strings.TrimSpace(suffix) == "" {
			continue
		}
		id = strings.TrimSuffix(id, suffix)
	}
	return strings.Trim(strings.TrimSpace(id), "/")
}

func planningStageForSignal(signalName string) string {
	switch strings.TrimSpace(signalName) {
	case "item-selected":
		return "selection"
	case "answer":
		return "question_answer"
	case "greenlight":
		return "greenlight"
	default:
		return "control_signal"
	}
}

func planningTraceActorFromRequest(r *http.Request) string {
	if r == nil {
		return "api"
	}
	source := strings.TrimSpace(r.Header.Get("X-CHUM-Source"))
	if source == "" {
		return "api"
	}
	return source
}

func (s *Server) inferPlanningProject(sessionID string) string {
	if s == nil || s.store == nil || strings.TrimSpace(sessionID) == "" {
		return ""
	}
	events, err := s.store.ListPlanningTraceEvents(sessionID, 1)
	if err != nil || len(events) == 0 {
		return ""
	}
	return strings.TrimSpace(events[0].Project)
}

func (s *Server) recordPlanningControlTrace(
	sessionID, project, stage, eventType, actor, summary, fullText string,
	metadata map[string]any,
) {
	if s == nil || s.store == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	eventType = strings.TrimSpace(eventType)
	if sessionID == "" || eventType == "" {
		return
	}
	metaJSON := "{}"
	if metadata != nil {
		if b, err := json.Marshal(metadata); err == nil {
			metaJSON = string(b)
		}
	}
	if err := s.store.RecordPlanningTraceEvent(store.PlanningTraceEvent{
		SessionID:    sessionID,
		Project:      strings.TrimSpace(project),
		Stage:        strings.TrimSpace(stage),
		EventType:    eventType,
		Actor:        strings.TrimSpace(actor),
		SummaryText:  strings.TrimSpace(summary),
		FullText:     fullText,
		MetadataJSON: metaJSON,
	}); err != nil {
		s.logger.Debug("planning control trace skipped (non-fatal)", "error", err)
	}
}

func buildPlanningPromptResponse(sessionID, workflowStatus string, events []store.PlanningTraceEvent) planningPromptResponse {
	resp := planningPromptResponse{
		SessionID: strings.TrimSpace(sessionID),
		Status:    strings.TrimSpace(workflowStatus),
		Phase:     "processing",
		Prompt:    "Planning workflow is running. Fetch again in a moment.",
	}
	if len(events) == 0 {
		return resp
	}

	latestCycle := 0
	for i := range events {
		if events[i].Cycle > latestCycle {
			latestCycle = events[i].Cycle
		}
	}
	resp.Cycle = latestCycle

	if hasPlanningEventType(events, latestCycle, "plan_agreed") {
		resp.Phase = "agreed"
		resp.ExpectedSignal = ""
		resp.Prompt = "Plan is agreed and execution is in progress."
		return resp
	}
	if hasPlanningEventType(events, latestCycle, "planning_signal_timeout") {
		resp.Phase = "timed_out"
		resp.ExpectedSignal = ""
		resp.Prompt = "Planning session timed out waiting for input. Start a new planning session to continue."
		return resp
	}
	if hasPlanningEventType(events, latestCycle, "planning_exhausted") {
		resp.Phase = "exhausted"
		resp.ExpectedSignal = ""
		resp.Prompt = "Planning exhausted without agreement. Start a new planning session."
		return resp
	}

	selectedID, selectedTitle := latestSelectedItem(events, latestCycle)
	if selectedID != "" {
		resp.SelectedItem = &planningPromptSelectedItem{ID: selectedID, Title: selectedTitle}
	}

	questions := latestPlanningQuestions(events, latestCycle, selectedID)
	if len(questions) > 0 {
		answerCount := countPlanningAnswers(events, latestCycle, selectedID)
		if answerCount < len(questions) {
			q := questions[answerCount]
			resp.Phase = "questioning"
			resp.ExpectedSignal = "answer"
			resp.Prompt = strings.TrimSpace(q.Question)
			resp.Options = cloneStrings(q.Options)
			resp.Recommendation = strings.TrimSpace(q.Recommendation)
			resp.Context = strings.TrimSpace(q.Context)
			return resp
		}
		if !hasPlanningEventType(events, latestCycle, "greenlight_decision") && hasSummaryForCycle(events, latestCycle, selectedID) {
			resp.Phase = "greenlight"
			resp.ExpectedSignal = "greenlight"
			resp.Prompt = latestSummaryPrompt(events, latestCycle, selectedID)
			resp.Options = []string{"GO", "REALIGN"}
			resp.Recommendation = "GO if this plan and DoD checks are acceptable; REALIGN to continue another planning cycle."
			return resp
		}
	}

	candidates := latestPlanningCandidates(events, latestCycle)
	if selectedID == "" && len(candidates) > 0 {
		resp.Phase = "selecting"
		resp.ExpectedSignal = "item-selected"
		resp.Prompt = "Select the highest value slice to plan next."
		resp.Options = formatCandidateOptions(candidates)
		resp.Recommendation = "Pick the highest-ranked shortlisted option unless you need a strategic exception."
		return resp
	}

	if strings.EqualFold(resp.Status, "Running") {
		resp.Phase = "processing"
		resp.ExpectedSignal = ""
		resp.Prompt = "Planning is processing the current step."
		return resp
	}

	resp.Phase = "completed"
	resp.ExpectedSignal = ""
	resp.Prompt = "Planning session is closed."
	return resp
}

func hasPlanningEventType(events []store.PlanningTraceEvent, cycle int, eventType string) bool {
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		return false
	}
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Cycle != cycle {
			continue
		}
		if strings.TrimSpace(events[i].EventType) == eventType {
			return true
		}
	}
	return false
}

func latestSelectedItem(events []store.PlanningTraceEvent, cycle int) (string, string) {
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Cycle != cycle || strings.TrimSpace(ev.EventType) != "item_selected" {
			continue
		}
		id := strings.TrimSpace(ev.OptionID)
		if id == "" {
			id = strings.TrimSpace(ev.TaskID)
		}
		title := strings.TrimSpace(ev.SummaryText)
		if title == "" {
			title = strings.TrimSpace(ev.TaskID)
		}
		return id, title
	}
	return "", ""
}

func latestPlanningQuestions(events []store.PlanningTraceEvent, cycle int, selectedID string) []temporal.PlanningQuestion {
	selectedID = strings.TrimSpace(selectedID)
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Cycle != cycle || strings.TrimSpace(ev.EventType) != "questions_result" {
			continue
		}
		if selectedID != "" {
			if strings.TrimSpace(ev.TaskID) != selectedID && strings.TrimSpace(ev.OptionID) != selectedID {
				continue
			}
		}
		payload := strings.TrimSpace(ev.FullText)
		if payload == "" {
			continue
		}
		var questions []temporal.PlanningQuestion
		if err := json.Unmarshal([]byte(payload), &questions); err != nil {
			return nil
		}
		return questions
	}
	return nil
}

func countPlanningAnswers(events []store.PlanningTraceEvent, cycle int, selectedID string) int {
	selectedID = strings.TrimSpace(selectedID)
	count := 0
	for i := range events {
		ev := events[i]
		if ev.Cycle != cycle || strings.TrimSpace(ev.EventType) != "answer_recorded" {
			continue
		}
		if selectedID != "" && strings.TrimSpace(ev.OptionID) != selectedID && strings.TrimSpace(ev.TaskID) != selectedID {
			continue
		}
		count++
	}
	return count
}

func hasSummaryForCycle(events []store.PlanningTraceEvent, cycle int, selectedID string) bool {
	selectedID = strings.TrimSpace(selectedID)
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Cycle != cycle {
			continue
		}
		if selectedID != "" && strings.TrimSpace(ev.TaskID) != selectedID && strings.TrimSpace(ev.OptionID) != selectedID {
			continue
		}
		if strings.TrimSpace(ev.EventType) == "plan_summary_result" {
			return true
		}
		if strings.TrimSpace(ev.Stage) == "summarize_plan" && strings.TrimSpace(ev.EventType) == "tool_result" {
			return true
		}
	}
	return false
}

func latestSummaryPrompt(events []store.PlanningTraceEvent, cycle int, selectedID string) string {
	selectedID = strings.TrimSpace(selectedID)
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Cycle != cycle {
			continue
		}
		if selectedID != "" && strings.TrimSpace(ev.TaskID) != selectedID && strings.TrimSpace(ev.OptionID) != selectedID {
			continue
		}
		if strings.TrimSpace(ev.EventType) == "plan_summary_result" {
			what := strings.TrimSpace(ev.SummaryText)
			if what != "" {
				return "Greenlight this plan: " + what
			}
		}
	}
	return "Greenlight this plan now, or realign for another planning cycle."
}

func latestPlanningCandidates(events []store.PlanningTraceEvent, cycle int) []planningCandidateOption {
	type candidateAccum struct {
		option planningCandidateOption
		seen   bool
	}
	acc := make(map[string]candidateAccum)

	for i := range events {
		ev := events[i]
		if ev.Cycle != cycle {
			continue
		}
		typ := strings.TrimSpace(ev.EventType)
		if typ != "candidate_ranked" && typ != "candidate_pruned" && typ != "candidate_with_implications" {
			continue
		}
		id := strings.TrimSpace(ev.OptionID)
		if id == "" {
			id = strings.TrimSpace(ev.TaskID)
		}
		if id == "" {
			continue
		}

		rec := acc[id]
		rec.option.ID = id
		if title := candidateTitleFromSummary(strings.TrimSpace(ev.SummaryText)); title != "" {
			rec.option.Title = title
		}

		var meta map[string]any
		if strings.TrimSpace(ev.MetadataJSON) != "" && strings.TrimSpace(ev.MetadataJSON) != "{}" {
			_ = json.Unmarshal([]byte(ev.MetadataJSON), &meta)
		}
		if rank := metaInt(meta, "rank"); rank > 0 {
			rec.option.Rank = rank
		}
		if metaBool(meta, "shortlisted") {
			rec.option.Shortlisted = true
		}
		if metaBool(meta, "recommended") {
			rec.option.Recommended = true
		}

		if rec.option.Rank <= 0 {
			rec.option.Rank = extractNumericPrefixRank(strings.TrimSpace(ev.SummaryText))
		}
		if typ == "candidate_ranked" {
			rec.option.Shortlisted = true
		}
		rec.seen = true
		acc[id] = rec
	}

	candidates := make([]planningCandidateOption, 0, len(acc))
	for _, rec := range acc {
		if !rec.seen {
			continue
		}
		if strings.TrimSpace(rec.option.Title) == "" {
			rec.option.Title = rec.option.ID
		}
		candidates = append(candidates, rec.option)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Rank != candidates[j].Rank {
			if candidates[i].Rank == 0 {
				return false
			}
			if candidates[j].Rank == 0 {
				return true
			}
			return candidates[i].Rank < candidates[j].Rank
		}
		return candidates[i].ID < candidates[j].ID
	})
	return candidates
}

func formatCandidateOptions(candidates []planningCandidateOption) []string {
	if len(candidates) == 0 {
		return nil
	}
	hasShortlist := false
	for i := range candidates {
		if candidates[i].Shortlisted {
			hasShortlist = true
			break
		}
	}
	options := make([]string, 0, len(candidates))
	for i := range candidates {
		c := candidates[i]
		if hasShortlist && !c.Shortlisted {
			continue
		}
		label := c.Title
		if c.Rank > 0 {
			label = fmt.Sprintf("#%d %s", c.Rank, label)
		}
		option := fmt.Sprintf("%s (%s)", c.ID, label)
		if c.Recommended {
			option += " [recommended]"
		}
		options = append(options, option)
	}
	if len(options) > 0 {
		return options
	}
	options = make([]string, 0, len(candidates))
	for i := range candidates {
		c := candidates[i]
		options = append(options, fmt.Sprintf("%s (%s)", c.ID, c.Title))
	}
	return options
}

func candidateTitleFromSummary(summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}
	if strings.HasPrefix(summary, "#") {
		parts := strings.SplitN(summary, " ", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(parts[1])
		}
	}
	if idx := strings.LastIndex(summary, "(rank "); idx > 0 {
		return strings.TrimSpace(summary[:idx])
	}
	return summary
}

func extractNumericPrefixRank(summary string) int {
	summary = strings.TrimSpace(summary)
	if !strings.HasPrefix(summary, "#") {
		return 0
	}
	parts := strings.SplitN(strings.TrimPrefix(summary, "#"), " ", 2)
	if len(parts) == 0 {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0
	}
	return n
}

func metaInt(meta map[string]any, key string) int {
	if meta == nil {
		return 0
	}
	v, ok := meta[key]
	if !ok {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(t))
		return n
	default:
		return 0
	}
}

func metaBool(meta map[string]any, key string) bool {
	if meta == nil {
		return false
	}
	v, ok := meta[key]
	if !ok {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(strings.TrimSpace(t), "true")
	default:
		return false
	}
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}
