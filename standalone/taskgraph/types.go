// Package taskgraph provides a SQLite-backed directed acyclic graph of tasks
// with sophisticated dependency management and automatic status transitions.
package taskgraph

import "time"

// Task represents a unit of work in the task graph.
type Task struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	Status          string    `json:"status"`
	Priority        int       `json:"priority"`
	Type            string    `json:"type"`
	Assignee        string    `json:"assignee"`
	Labels          []string  `json:"labels"`
	EstimateMinutes int       `json:"estimate_minutes"`
	ParentID        string    `json:"parent_id"`
	Acceptance      string    `json:"acceptance"`
	Design          string    `json:"design"`
	Notes           string    `json:"notes"`
	DependsOn       []string  `json:"depends_on"`
	Project         string    `json:"project"`
	ErrorLog        string    `json:"error_log,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Status constants for task lifecycle management.
const (
	StatusOpen      = "open"
	StatusReady     = "ready"
	StatusClosed    = "closed"
	StatusCompleted = "completed"
	StatusEscalated = "escalated"
	StatusPlanFailed = "plan_failed"
	StatusCanceled  = "canceled"
	StatusDone      = "done"
)

// Type constants for task categorization.
const (
	TypeTask  = "task"
	TypeEpic  = "epic"
	TypeWhale = "whale"
)