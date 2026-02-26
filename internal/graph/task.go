package graph

import "time"

type Task struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	Status          string    `json:"status"`
	Priority        int       `json:"priority"`
	Type            string    `json:"issue_type"`
	Assignee        string    `json:"assignee"`
	Labels          []string  `json:"labels"`
	EstimateMinutes int       `json:"estimated_minutes"`
	ParentID        string    `json:"parent_id"`
	Acceptance      string    `json:"acceptance_criteria"`
	Design          string    `json:"design"`
	Notes           string    `json:"notes"`
	DependsOn       []string  `json:"depends_on"`
	Project         string    `json:"project"`
	ErrorLog        string    `json:"error_log,omitempty"` // scent of failed sharks
	Complexity      int       `json:"complexity"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type CrossDep struct {
	Project string `json:"project"`
	TaskID  string `json:"task_id"`
}

const taskTypeEpic = "epic"
const taskTypeWhale = "whale"   // epic-level grouping from crab decomposition
