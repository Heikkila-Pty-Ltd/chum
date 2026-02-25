package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// TiebreakRequest is the JSON body for POST /tiebreak.
type TiebreakRequest struct {
	WorkflowID string `json:"workflow_id"`
	Decision   string `json:"decision"` // "proceed" or "reject"
}

// handleTiebreak sends a turtle-tiebreak signal to an in-flight turtle workflow,
// unblocking its human tiebreak wait.
func (s *Server) handleTiebreak(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req TiebreakRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json: "+err.Error())
		return
	}

	req.WorkflowID = strings.TrimSpace(req.WorkflowID)
	req.Decision = strings.TrimSpace(strings.ToLower(req.Decision))

	if req.WorkflowID == "" {
		writeError(w, http.StatusBadRequest, "workflow_id is required")
		return
	}
	if req.Decision != "proceed" && req.Decision != "reject" {
		writeError(w, http.StatusBadRequest, "decision must be 'proceed' or 'reject'")
		return
	}

	tc, err := s.temporalClient()
	if err != nil {
		s.logger.Error("tiebreak: failed to connect to temporal", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to connect to temporal")
		return
	}
	defer tc.Close()

	if err := tc.SignalWorkflow(r.Context(), req.WorkflowID, "", "turtle-tiebreak", req.Decision); err != nil {
		s.logger.Error("tiebreak: signal failed", "workflow_id", req.WorkflowID, "error", err)
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("signal failed: %s", err.Error()))
		return
	}

	s.logger.Info("🐢 tiebreak signal sent", "workflow_id", req.WorkflowID, "decision", req.Decision)
	writeJSON(w, map[string]string{
		"status":      "ok",
		"workflow_id": req.WorkflowID,
		"decision":    req.Decision,
	})
}
