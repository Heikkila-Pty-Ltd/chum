// Package api provides a lightweight HTTP API for querying CHUM state.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"go.temporal.io/sdk/client"

	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/graph"
	"github.com/antigravity-dev/chum/internal/store"
)

// Server is the HTTP API server.
type Server struct {
	cfg            *config.Config
	store          *store.Store
	dag            *graph.DAG
	logger         *slog.Logger
	startTime      time.Time
	httpServer     *http.Server
	authMiddleware *AuthMiddleware
}

// NewServer creates a new API server.
func NewServer(cfg *config.Config, s *store.Store, dag *graph.DAG, logger *slog.Logger) (*Server, error) {
	authMiddleware, err := NewAuthMiddleware(&cfg.API.Security, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize auth middleware: %w", err)
	}

	return &Server{
		cfg:            cfg,
		store:          s,
		dag:            dag,
		logger:         logger,
		startTime:      time.Now(),
		authMiddleware: authMiddleware,
	}, nil
}

// Close closes the server and cleans up resources
func (s *Server) Close() error {
	if s.authMiddleware != nil {
		return s.authMiddleware.Close()
	}
	return nil
}

// Start begins listening on the configured bind address. Blocks until context is cancelled.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// Read-only endpoints
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/projects", s.handleProjects)
	mux.HandleFunc("/projects/", s.handleProjectDetail)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/recommendations", s.handleRecommendations)
	mux.HandleFunc("/dispatches/", s.handleDispatchDetail)
	mux.HandleFunc("/safety/blocks", s.handleSafetyBlocks)

	// Temporal workflow endpoints
	mux.HandleFunc("/workflows/start", s.authMiddleware.RequireAuth(s.handleWorkflowStart))
	mux.HandleFunc("/workflows/", s.authMiddleware.RequireAuth(s.routeWorkflows))

	// Planning ceremony endpoints
	mux.HandleFunc("/planning/start", s.authMiddleware.RequireAuth(s.handlePlanningStart))
	mux.HandleFunc("/planning/", s.authMiddleware.RequireAuth(s.routePlanning))

	// Crab decomposition endpoints
	mux.HandleFunc("/crab/decompose", s.authMiddleware.RequireAuth(s.handleCrabDecompose))
	mux.HandleFunc("/crab/", s.authMiddleware.RequireAuth(s.routeCrab))

	// Task CRUD endpoints (DAG)
	mux.HandleFunc("/tasks", s.authMiddleware.RequireAuth(s.routeTasks))
	mux.HandleFunc("/tasks/", s.authMiddleware.RequireAuth(s.routeTasks))

	s.httpServer = &http.Server{
		Addr:        s.cfg.API.Bind,
		Handler:     mux,
		BaseContext: func(_ net.Listener) context.Context { return ctx },
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.httpServer.Shutdown(shutCtx)
	}()

	s.logger.Info("api server starting", "bind", s.cfg.API.Bind)
	err := s.httpServer.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

// temporalClient creates a new Temporal client using the configured host port.
func (s *Server) temporalClient() (client.Client, error) {
	return client.Dial(client.Options{HostPort: s.cfg.General.TemporalHostPort})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
