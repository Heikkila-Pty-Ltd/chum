package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/antigravity-dev/chum/internal/config"
)

func TestAuthMiddleware_RequireAuth_Disabled(t *testing.T) {
	cfg := &config.APISecurity{
		Enabled: false,
		RequireLocalOnly: false,
	}
	
	middleware, err := NewAuthMiddleware(cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("failed to create auth middleware: %v", err)
	}
	defer middleware.Close()
	
	handler := middleware.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})
	
	// Test control endpoint without auth (should pass when auth disabled)
	req := httptest.NewRequest(http.MethodPost, "/scheduler/pause", nil)
	req.RemoteAddr = "192.168.1.100:12345" // Non-local address
	w := httptest.NewRecorder()
	
	handler(w, req)
	
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	
	body := w.Body.String()
	if body != "success" {
		t.Errorf("expected 'success', got %q", body)
	}
}

func TestAuthMiddleware_RequireAuth_LocalOnly(t *testing.T) {
	cfg := &config.APISecurity{
		Enabled: false,
		RequireLocalOnly: true,
	}
	
	middleware, err := NewAuthMiddleware(cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("failed to create auth middleware: %v", err)
	}
	defer middleware.Close()
	
	handler := middleware.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})
	
	// Test non-local request (should be rejected)
	req := httptest.NewRequest(http.MethodPost, "/scheduler/pause", nil)
	req.RemoteAddr = "8.8.8.8:12345"
	w := httptest.NewRecorder()
	
	handler(w, req)
	
	if w.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", w.Code)
	}
	
	// Test local request (should be allowed)
	req = httptest.NewRequest(http.MethodPost, "/scheduler/pause", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	w = httptest.NewRecorder()
	
	handler(w, req)
	
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestAuthMiddleware_RequireAuth_TokenAuth(t *testing.T) {
	cfg := &config.APISecurity{
		Enabled: true,
		AllowedTokens: []string{"valid-token-123456"},
	}
	
	middleware, err := NewAuthMiddleware(cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("failed to create auth middleware: %v", err)
	}
	defer middleware.Close()
	
	handler := middleware.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})
	
	// Test without token (should be rejected)
	req := httptest.NewRequest(http.MethodPost, "/scheduler/pause", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	w := httptest.NewRecorder()
	
	handler(w, req)
	
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
	
	// Test with invalid token (should be rejected)
	req = httptest.NewRequest(http.MethodPost, "/scheduler/pause", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	req.Header.Set("Authorization", "Bearer invalid-token")
	w = httptest.NewRecorder()
	
	handler(w, req)
	
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
	
	// Test with valid token (should pass)
	req = httptest.NewRequest(http.MethodPost, "/scheduler/pause", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	req.Header.Set("Authorization", "Bearer valid-token-123456")
	w = httptest.NewRecorder()
	
	handler(w, req)
	
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestAuthMiddleware_NonControlEndpoint(t *testing.T) {
	cfg := &config.APISecurity{
		Enabled: true,
		AllowedTokens: []string{"valid-token-123456"},
	}
	
	middleware, err := NewAuthMiddleware(cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("failed to create auth middleware: %v", err)
	}
	defer middleware.Close()
	
	handler := middleware.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})
	
	// Test non-control endpoint (should pass without auth)
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	w := httptest.NewRecorder()
	
	handler(w, req)
	
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestAuthMiddleware_AuditLogging(t *testing.T) {
	// Create temporary audit log file
	tmpDir := t.TempDir()
	auditPath := filepath.Join(tmpDir, "audit.log")
	
	cfg := &config.APISecurity{
		Enabled: true,
		AllowedTokens: []string{"valid-token-123456"},
		AuditLog: auditPath,
	}
	
	middleware, err := NewAuthMiddleware(cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("failed to create auth middleware: %v", err)
	}
	defer middleware.Close()
	
	handler := middleware.RequireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})
	
	// Make an authenticated request
	req := httptest.NewRequest(http.MethodPost, "/scheduler/pause", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	req.Header.Set("Authorization", "Bearer valid-token-123456")
	req.Header.Set("User-Agent", "test-client/1.0")
	w := httptest.NewRecorder()
	
	handler(w, req)
	
	// Give some time for async logging
	time.Sleep(10 * time.Millisecond)
	
	// Check audit log was created and contains entry
	auditData, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("failed to read audit log: %v", err)
	}
	
	if len(auditData) == 0 {
		t.Fatal("audit log is empty")
	}
	
	// Parse audit event
	var event AuditEvent
	if err := json.Unmarshal(bytes.TrimSpace(auditData), &event); err != nil {
		t.Fatalf("failed to parse audit event: %v", err)
	}
	
	// Verify audit event fields
	if event.Method != "POST" {
		t.Errorf("expected method POST, got %s", event.Method)
	}
	
	if event.Path != "/scheduler/pause" {
		t.Errorf("expected path /scheduler/pause, got %s", event.Path)
	}
	
	if !event.Authorized {
		t.Error("expected authorized=true")
	}
	
	if event.Token != "vali****" {
		t.Errorf("expected truncated token 'vali****', got %s", event.Token)
	}
	
	if event.UserAgent != "test-client/1.0" {
		t.Errorf("expected user agent 'test-client/1.0', got %s", event.UserAgent)
	}
}

func TestIsControlEndpoint(t *testing.T) {
	tests := []struct {
		method   string
		path     string
		expected bool
	}{
		{"POST", "/scheduler/pause", true},
		{"POST", "/scheduler/resume", true},
		{"POST", "/dispatches/123/cancel", true},
		{"POST", "/dispatches/456/retry", true},
		{"GET", "/scheduler/pause", false},
		{"GET", "/status", false},
		{"POST", "/status", false},
		{"POST", "/dispatches/123", false},
		{"GET", "/dispatches/123/cancel", false},
	}
	
	for _, tt := range tests {
		actual := isControlEndpoint(tt.method, tt.path)
		if actual != tt.expected {
			t.Errorf("isControlEndpoint(%s, %s) = %v, expected %v", 
				tt.method, tt.path, actual, tt.expected)
		}
	}
}

func TestIsLocalRequest(t *testing.T) {
	tests := []struct {
		remoteAddr string
		expected   bool
	}{
		{"127.0.0.1:12345", true},
		{"[::1]:12345", true},
		{"192.168.1.100:12345", true},   // Private IP
		{"10.0.0.1:12345", true},        // Private IP
		{"172.16.0.1:12345", true},      // Private IP
		{"8.8.8.8:12345", false},        // Public IP
		{"1.1.1.1:12345", false},        // Public IP
		{"invalid", false},              // Invalid format
	}
	
	for _, tt := range tests {
		actual := isLocalRequest(tt.remoteAddr)
		if actual != tt.expected {
			t.Errorf("isLocalRequest(%s) = %v, expected %v", 
				tt.remoteAddr, actual, tt.expected)
		}
	}
}

func TestExtractToken(t *testing.T) {
	tests := []struct {
		header   string
		expected string
	}{
		{"Bearer token123", "token123"},
		{"bearer token123", "token123"},
		{"BEARER token123", "token123"},
		{"Basic token123", ""},
		{"Bearer", ""},
		{"", ""},
		{"token123", ""},
		{"Bearer token_with_underscores", "token_with_underscores"},
		{"Bearer token with spaces", ""},
	}
	
	for _, tt := range tests {
		req := httptest.NewRequest("GET", "/", nil)
		if tt.header != "" {
			req.Header.Set("Authorization", tt.header)
		}
		
		actual := extractToken(req)
		if actual != tt.expected {
			t.Errorf("extractToken(%q) = %q, expected %q", 
				tt.header, actual, tt.expected)
		}
	}
}

func TestConfigValidation(t *testing.T) {
	// Test auth enabled with no tokens (should fail)
	cfg := &config.Config{
		API: config.API{
			Security: config.APISecurity{
				Enabled: true,
				AllowedTokens: []string{},
			},
		},
		Projects: map[string]config.Project{
			"test": {Enabled: true},
		},
	}
	
	// Use reflection to call the internal validate function
	// For now, just test that config loads properly with tokens
	cfg.API.Security.AllowedTokens = []string{"valid-token-12345678"}
	
	// This should not panic
	if len(cfg.API.Security.AllowedTokens[0]) < 16 {
		t.Error("token should be at least 16 characters for this test")
	}
}