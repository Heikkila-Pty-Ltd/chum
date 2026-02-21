package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/antigravity-dev/chum/internal/config"
)

// AuthMiddleware provides authentication and authorization for API endpoints
type AuthMiddleware struct {
	config    *config.APISecurity
	logger    *slog.Logger
	auditFile *os.File
}

// NewAuthMiddleware creates a new auth middleware
func NewAuthMiddleware(cfg *config.APISecurity, logger *slog.Logger) (*AuthMiddleware, error) {
	am := &AuthMiddleware{
		config: cfg,
		logger: logger,
	}

	// Open audit log if configured
	if cfg.AuditLog != "" {
		auditPath := config.ExpandHome(cfg.AuditLog)
		f, err := os.OpenFile(auditPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to open audit log %q: %w", auditPath, err)
		}
		am.auditFile = f
	}

	return am, nil
}

// Close closes the audit log file
func (am *AuthMiddleware) Close() error {
	if am.auditFile != nil {
		return am.auditFile.Close()
	}
	return nil
}

// AuditEvent represents an audit log entry
type AuditEvent struct {
	Timestamp  time.Time `json:"timestamp"`
	RemoteAddr string    `json:"remote_addr"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	UserAgent  string    `json:"user_agent,omitempty"`
	Authorized bool      `json:"authorized"`
	Token      string    `json:"token,omitempty"` // Truncated for security
	Error      string    `json:"error,omitempty"`
	StatusCode int       `json:"status_code"`
	Duration   string    `json:"duration"`
}

// logAuditEvent writes an audit event to the log file
func (am *AuthMiddleware) logAuditEvent(event AuditEvent) {
	if am.auditFile == nil {
		return
	}

	data, err := json.Marshal(event)
	if err != nil {
		am.logger.Error("failed to marshal audit event", "error", err)
		return
	}

	if _, err := am.auditFile.Write(append(data, '\n')); err != nil {
		am.logger.Error("failed to write audit event", "error", err)
	}
}

// truncateToken returns first 8 chars of token for audit logging
func truncateToken(token string) string {
	if len(token) <= 8 {
		return strings.Repeat("*", len(token))
	}
	return token[:4] + "****"
}

// isLocalRequest checks if the request comes from a local address
func isLocalRequest(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return false
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}

	// Check for loopback addresses (127.x.x.x and ::1)
	if ip.IsLoopback() {
		return true
	}

	// Check for private addresses (RFC 1918)
	if ip.IsPrivate() {
		return true
	}

	return false
}

// extractToken gets the bearer token from Authorization header
func extractToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}

	parts := strings.Split(auth, " ")
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return ""
	}

	return parts[1]
}

// isValidToken checks if the provided token is in the allowed list
func (am *AuthMiddleware) isValidToken(token string) bool {
	if token == "" {
		return false
	}

	for _, allowedToken := range am.config.AllowedTokens {
		if token == allowedToken {
			return true
		}
	}

	return false
}

// isControlEndpoint checks if this is a control endpoint that modifies system state
func isControlEndpoint(method, path string) bool {
	if method != http.MethodPost {
		return false
	}

	controlPaths := []string{
		"/scheduler/pause",
		"/scheduler/resume",
		"/scheduler/plan/activate",
		"/scheduler/plan/clear",
	}

	for _, controlPath := range controlPaths {
		if path == controlPath {
			return true
		}
	}

	// Check for dispatch control endpoints with patterns
	if strings.HasPrefix(path, "/dispatches/") {
		if strings.HasSuffix(path, "/cancel") || strings.HasSuffix(path, "/retry") {
			return true
		}
	}

	return false
}

// RequireAuth creates middleware that enforces authentication for control endpoints
func (am *AuthMiddleware) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Check if this is a control endpoint
		if !isControlEndpoint(r.Method, r.URL.Path) {
			next(w, r)
			return
		}

		event := AuditEvent{
			Timestamp:  start,
			RemoteAddr: r.RemoteAddr,
			Method:     r.Method,
			Path:       r.URL.Path,
			UserAgent:  r.Header.Get("User-Agent"),
		}

		defer func() {
			event.Duration = time.Since(start).String()
			am.logAuditEvent(event)
		}()

		// Check if auth is enabled
		if !am.config.Enabled {
			// If auth is disabled but require_local_only is set, check for local requests
			if am.config.RequireLocalOnly && !isLocalRequest(r.RemoteAddr) {
				event.Authorized = false
				event.Error = "non-local request rejected (require_local_only=true)"
				event.StatusCode = http.StatusForbidden
				writeError(w, http.StatusForbidden, "Access denied: non-local requests not allowed")
				return
			}

			event.Authorized = true
			next(w, r)
			return
		}

		// Auth is enabled - extract and validate token
		token := extractToken(r)
		event.Token = truncateToken(token)

		if !am.isValidToken(token) {
			event.Authorized = false
			event.Error = "invalid or missing token"
			event.StatusCode = http.StatusUnauthorized

			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "Unauthorized: valid token required")
			return
		}

		event.Authorized = true
		next(w, r)
	}
}
