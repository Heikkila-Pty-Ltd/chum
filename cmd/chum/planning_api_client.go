package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/antigravity-dev/chum/internal/matrix"
)

type planningAPIClient struct {
	baseURL string
	client  *http.Client
	token   string
}

func newPlanningAPIClient(bindAddr string, token string) (*planningAPIClient, error) {
	baseURL := strings.TrimSpace(bindAddr)
	if baseURL == "" {
		return nil, fmt.Errorf("api bind address is required")
	}
	if !strings.Contains(baseURL, "://") {
		baseURL = "http://" + baseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return &planningAPIClient{
		baseURL: baseURL,
		token:   strings.TrimSpace(token),
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}, nil
}

func (c *planningAPIClient) StartPlanningSession(ctx context.Context, req matrix.PlanningStartRequest) (matrix.PlanningSession, error) {
	payload := map[string]any{
		"project":  strings.TrimSpace(req.Project),
		"work_dir": strings.TrimSpace(req.WorkDir),
	}
	if strings.TrimSpace(req.Agent) != "" {
		payload["agent"] = strings.TrimSpace(req.Agent)
	}
	if strings.TrimSpace(req.Tier) != "" {
		payload["tier"] = strings.TrimSpace(req.Tier)
	}
	if req.CandidateTopK > 0 {
		payload["candidate_top_k"] = req.CandidateTopK
	}

	var resp struct {
		SessionID string `json:"session_id"`
		RunID     string `json:"run_id"`
		Status    string `json:"status"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/planning/start", payload, &resp, map[string]string{
		"X-CHUM-Source": choosePlanningSource(req.Source),
	}); err != nil {
		return matrix.PlanningSession{}, err
	}
	return matrix.PlanningSession{
		SessionID: strings.TrimSpace(resp.SessionID),
		RunID:     strings.TrimSpace(resp.RunID),
		Status:    strings.TrimSpace(resp.Status),
	}, nil
}

func (c *planningAPIClient) SubmitPlanningSignal(ctx context.Context, sessionID, signal, value, source string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}
	signal = strings.TrimSpace(signal)
	var suffix string
	switch signal {
	case "item-selected":
		suffix = "select"
	case "answer":
		suffix = "answer"
	case "greenlight":
		suffix = "greenlight"
	default:
		return fmt.Errorf("unsupported planning signal %q", signal)
	}
	payload := map[string]string{
		"value": strings.TrimSpace(value),
	}
	return c.doJSON(ctx, http.MethodPost, "/planning/"+sessionID+"/"+suffix, payload, nil, map[string]string{
		"X-CHUM-Source": choosePlanningSource(source),
	})
}

func (c *planningAPIClient) GetPlanningPrompt(ctx context.Context, sessionID, source string) (matrix.PlanningPrompt, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return matrix.PlanningPrompt{}, fmt.Errorf("session id is required")
	}
	var resp struct {
		SessionID      string   `json:"session_id"`
		Status         string   `json:"status"`
		Phase          string   `json:"phase"`
		ExpectedSignal string   `json:"expected_signal"`
		Prompt         string   `json:"prompt"`
		Options        []string `json:"options"`
		Recommendation string   `json:"recommendation"`
		Context        string   `json:"context"`
		Cycle          int      `json:"cycle"`
		SelectedItem   *struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"selected_item"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/planning/"+sessionID+"/prompt", nil, &resp, map[string]string{
		"X-CHUM-Source": choosePlanningSource(source),
	}); err != nil {
		return matrix.PlanningPrompt{}, err
	}
	prompt := matrix.PlanningPrompt{
		SessionID:      strings.TrimSpace(resp.SessionID),
		Status:         strings.TrimSpace(resp.Status),
		Phase:          strings.TrimSpace(resp.Phase),
		ExpectedSignal: strings.TrimSpace(resp.ExpectedSignal),
		Prompt:         strings.TrimSpace(resp.Prompt),
		Options:        append([]string(nil), resp.Options...),
		Recommendation: strings.TrimSpace(resp.Recommendation),
		Context:        strings.TrimSpace(resp.Context),
		Cycle:          resp.Cycle,
	}
	if resp.SelectedItem != nil {
		prompt.SelectedItemID = strings.TrimSpace(resp.SelectedItem.ID)
		prompt.SelectedItemTitle = strings.TrimSpace(resp.SelectedItem.Title)
	}
	return prompt, nil
}

func (c *planningAPIClient) GetPlanningStatus(ctx context.Context, sessionID string) (matrix.PlanningStatus, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return matrix.PlanningStatus{}, fmt.Errorf("session id is required")
	}
	var resp struct {
		SessionID string `json:"session_id"`
		RunID     string `json:"run_id"`
		Status    string `json:"status"`
		Note      string `json:"note"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/planning/"+sessionID, nil, &resp, nil); err != nil {
		return matrix.PlanningStatus{}, err
	}
	return matrix.PlanningStatus{
		SessionID: strings.TrimSpace(resp.SessionID),
		RunID:     strings.TrimSpace(resp.RunID),
		Status:    strings.TrimSpace(resp.Status),
		Note:      strings.TrimSpace(resp.Note),
	}, nil
}

func (c *planningAPIClient) StopPlanningSession(ctx context.Context, sessionID, reason, source string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}
	payload := map[string]string{
		"reason": strings.TrimSpace(reason),
	}
	return c.doJSON(ctx, http.MethodPost, "/planning/"+sessionID+"/stop", payload, nil, map[string]string{
		"X-CHUM-Source": choosePlanningSource(source),
	})
}

func choosePlanningSource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return "api-client"
	}
	return source
}

func (c *planningAPIClient) doJSON(
	ctx context.Context,
	method, path string,
	payload any,
	out any,
	extraHeaders map[string]string,
) error {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		body = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(c.token) != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, v := range extraHeaders {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			continue
		}
		req.Header.Set(k, v)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		payloadBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(payloadBytes)))
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
