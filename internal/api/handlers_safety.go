package api

import (
	"net/http"
	"strings"
	"time"
)

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
