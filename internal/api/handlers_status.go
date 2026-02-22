package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

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
		"morsels_dir": proj.MorselsDir,
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
				"morsel_id":     e.MorselID,
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

	// Query lessons store (replaced the legacy recommendation store).
	query := r.URL.Query().Get("q")
	limitStr := r.URL.Query().Get("limit")
	limit := 20
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	var results []map[string]any
	if query != "" {
		lessons, err := s.store.SearchLessons(query, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "search lessons: "+err.Error())
			return
		}
		for _, l := range lessons {
			results = append(results, map[string]any{
				"id":        l.ID,
				"morsel_id":   l.MorselID,
				"project":   l.Project,
				"category":  l.Category,
				"summary":   l.Summary,
				"detail":    l.Detail,
				"files":     l.FilePaths,
				"labels":    l.Labels,
				"created_at": l.CreatedAt.Format(time.RFC3339),
			})
		}
	}
	if results == nil {
		results = []map[string]any{}
	}

	resp := map[string]any{
		"recommendations": results,
		"hours":           hours,
		"count":           len(results),
		"generated_at":    time.Now(),
	}

	writeJSON(w, resp)
}
