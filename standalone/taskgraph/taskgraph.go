package taskgraph

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // register sqlite3 driver
)

const (
	pragmaJournalModeWAL = `PRAGMA journal_mode = WAL;`
	pragmaForeignKeysOn  = `PRAGMA foreign_keys = ON;`
	maxTaskIDAttempts    = 10
)

const (
	taskColumns = `id, title, description, status, priority, "type", assignee, labels, estimate_minutes, parent_id, acceptance, design, notes, project, error_log, created_at, updated_at`
)

const (
	taskTableSchema = `CREATE TABLE IF NOT EXISTS tasks (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL DEFAULT '',
		description TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'open',
		priority INTEGER NOT NULL DEFAULT 0,
		"type" TEXT NOT NULL DEFAULT 'task',
		assignee TEXT NOT NULL DEFAULT '',
		labels TEXT NOT NULL DEFAULT '[]',
		estimate_minutes INTEGER NOT NULL DEFAULT 0,
		parent_id TEXT NOT NULL DEFAULT '',
		acceptance TEXT NOT NULL DEFAULT '',
		design TEXT NOT NULL DEFAULT '',
		notes TEXT NOT NULL DEFAULT '',
		project TEXT NOT NULL,
		error_log TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);`

	taskEdgesSchema = `CREATE TABLE IF NOT EXISTS task_edges (
		from_task TEXT NOT NULL,
		to_task TEXT NOT NULL,
		PRIMARY KEY (from_task, to_task),
		FOREIGN KEY (from_task) REFERENCES tasks(id) ON DELETE CASCADE,
		FOREIGN KEY (to_task) REFERENCES tasks(id) ON DELETE CASCADE
	);`
)

var updatableColumns = map[string]struct{}{
	"title": {}, "description": {}, "status": {}, "priority": {},
	"type": {}, "assignee": {}, "labels": {}, "estimate_minutes": {},
	"parent_id": {}, "acceptance": {}, "design": {}, "notes": {},
	"project": {}, "error_log": {},
}

var terminalStatuses = map[string]struct{}{
	StatusClosed: {}, StatusCompleted: {}, StatusEscalated: {},
	StatusPlanFailed: {}, StatusCanceled: {}, StatusDone: {},
}

// TaskGraph manages a SQLite-backed directed acyclic graph of tasks.
type TaskGraph struct {
	db *sql.DB
}

// New creates a TaskGraph with the given database connection.
func New(db *sql.DB) *TaskGraph {
	return &TaskGraph{db: db}
}

// Open creates a new TaskGraph with a SQLite connection to the given path.
func Open(path string) (*TaskGraph, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	
	tg := New(db)
	if err := tg.EnsureSchema(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("ensure schema: %w", err)
	}
	
	return tg, nil
}

// Close closes the database connection.
func (tg *TaskGraph) Close() error {
	if tg.db != nil {
		return tg.db.Close()
	}
	return nil
}

// EnsureSchema creates the required tables and indexes.
func (tg *TaskGraph) EnsureSchema(ctx context.Context) error {
	if tg.db == nil {
		return fmt.Errorf("taskgraph: not initialized")
	}

	ctx = sanitizeContext(ctx)
	
	if _, err := tg.db.ExecContext(ctx, pragmaJournalModeWAL); err != nil {
		return fmt.Errorf("set journal mode WAL: %w", err)
	}
	if _, err := tg.db.ExecContext(ctx, pragmaForeignKeysOn); err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
	}
	if _, err := tg.db.ExecContext(ctx, taskTableSchema); err != nil {
		return fmt.Errorf("create tasks table: %w", err)
	}
	if _, err := tg.db.ExecContext(ctx, taskEdgesSchema); err != nil {
		return fmt.Errorf("create task_edges table: %w", err)
	}
	
	return nil
}

// CreateTask inserts a new task and returns its generated ID.
func (tg *TaskGraph) CreateTask(ctx context.Context, task Task) (string, error) {
	if tg.db == nil {
		return "", fmt.Errorf("taskgraph: not initialized")
	}
	
	project := strings.TrimSpace(task.Project)
	if project == "" {
		return "", fmt.Errorf("project is required")
	}

	labelsJSON, err := marshalLabels(task.Labels)
	if err != nil {
		return "", fmt.Errorf("marshal labels: %w", err)
	}

	status := normalizeTaskStatus(task.Status)
	taskType := normalizeTaskType(task.Type)
	now := time.Now().UTC()

	const insertTaskSQL = `INSERT INTO tasks (
		id, title, description, status, priority, "type", assignee,
		labels, estimate_minutes, parent_id, acceptance, design,
		notes, project, error_log, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`

	for attempt := 0; attempt < maxTaskIDAttempts; attempt++ {
		id, err := generateTaskID(project)
		if err != nil {
			return "", err
		}

		_, err = tg.db.ExecContext(ctx, insertTaskSQL,
			id, task.Title, task.Description, status, task.Priority,
			taskType, task.Assignee, labelsJSON, task.EstimateMinutes,
			task.ParentID, task.Acceptance, task.Design, task.Notes,
			project, task.ErrorLog, now, now,
		)
		if err == nil {
			return id, nil
		}
		if !isUniqueTaskIDError(err) {
			return "", fmt.Errorf("create task: %w", err)
		}
	}

	return "", fmt.Errorf("create task: exceeded maximum id generation attempts (%d)", maxTaskIDAttempts)
}

// GetTask retrieves a task by ID, including its dependencies.
func (tg *TaskGraph) GetTask(ctx context.Context, id string) (Task, error) {
	if tg.db == nil {
		return Task{}, fmt.Errorf("taskgraph: not initialized")
	}
	
	id = strings.TrimSpace(id)
	if id == "" {
		return Task{}, fmt.Errorf("id is required")
	}

	const getTaskSQL = `SELECT ` + taskColumns + ` FROM tasks WHERE id = ?;`
	row := tg.db.QueryRowContext(ctx, getTaskSQL, id)
	task, err := scanTask(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, fmt.Errorf("task %q: not found", id)
		}
		return Task{}, err
	}

	dependencies, err := tg.taskDependencies(ctx, []string{task.ID})
	if err != nil {
		return Task{}, fmt.Errorf("load task dependencies: %w", err)
	}
	task.DependsOn = dependencies[task.ID]

	return task, nil
}

// ListTasks returns tasks for a project, optionally filtering by statuses.
func (tg *TaskGraph) ListTasks(ctx context.Context, project string, statuses ...string) ([]Task, error) {
	if tg.db == nil {
		return nil, fmt.Errorf("taskgraph: not initialized")
	}
	
	project = strings.TrimSpace(project)
	if project == "" {
		return nil, fmt.Errorf("project is required")
	}

	args := []any{project}
	query := `SELECT ` + taskColumns + ` FROM tasks WHERE project = ?`
	statusFilters := normalizeStatusFilters(statuses)
	if len(statusFilters) > 0 {
		query += fmt.Sprintf(" AND lower(status) IN (%s)", placeholders(len(statusFilters)))
		for _, status := range statusFilters {
			args = append(args, status)
		}
	}

	rows, err := tg.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	var ids []string
	for rows.Next() {
		task, scanErr := scanTaskRows(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan task: %w", scanErr)
		}
		ids = append(ids, task.ID)
		tasks = append(tasks, task)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("list tasks: %w", rowsErr)
	}

	dependencies, err := tg.taskDependencies(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("load task dependencies: %w", err)
	}
	for i := range tasks {
		tasks[i].DependsOn = dependencies[tasks[i].ID]
	}

	return tasks, nil
}

// UpdateTask updates specific fields on a task with validation.
func (tg *TaskGraph) UpdateTask(ctx context.Context, id string, fields map[string]any) error {
	if tg.db == nil {
		return fmt.Errorf("taskgraph: not initialized")
	}
	
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("id is required")
	}
	if len(fields) == 0 {
		return nil
	}

	assignments := make([]taskUpdateField, 0, len(fields))
	var unrecognized []string
	
	for rawKey, rawValue := range fields {
		key := strings.TrimSpace(strings.ToLower(rawKey))
		if _, ok := updatableColumns[key]; !ok {
			unrecognized = append(unrecognized, rawKey)
			continue
		}

		switch key {
		case "title", "description", "assignee", "parent_id", "acceptance", "design", "notes", "project", "error_log":
			assignments = append(assignments, taskUpdateField{column: key, value: coerceString(rawValue)})
		case "status":
			assignments = append(assignments, taskUpdateField{column: "status", value: normalizeTaskStatus(coerceString(rawValue))})
		case "type":
			assignments = append(assignments, taskUpdateField{column: `"type"`, value: normalizeTaskType(coerceString(rawValue))})
		case "priority", "estimate_minutes":
			value, err := coerceInt(rawValue)
			if err != nil {
				return err
			}
			assignments = append(assignments, taskUpdateField{column: key, value: value})
		case "labels":
			labelsJSON, err := marshalLabelsValue(rawValue)
			if err != nil {
				return fmt.Errorf("labels: %w", err)
			}
			assignments = append(assignments, taskUpdateField{column: "labels", value: labelsJSON})
		}
	}
	
	if len(assignments) == 0 {
		if _, err := tg.GetTask(ctx, id); err != nil {
			return err
		}
		return fmt.Errorf("taskgraph: no recognized fields to update")
	}
	if len(unrecognized) > 0 {
		return fmt.Errorf("taskgraph: field %q is not updatable", unrecognized[0])
	}

	sort.Slice(assignments, func(i, j int) bool {
		return assignments[i].column < assignments[j].column
	})

	if transitioningToReady(assignments) {
		if err := tg.ensureReadyTransitionUnblocked(ctx, id); err != nil {
			return err
		}
	}

	setClauses := make([]string, len(assignments))
	args := make([]any, 0, len(assignments)+2)
	for i := range assignments {
		setClauses[i] = fmt.Sprintf("%s = ?", assignments[i].column)
		args = append(args, assignments[i].value)
	}
	now := time.Now().UTC()
	args = append(args, now, id)

	query := fmt.Sprintf("UPDATE tasks SET %s, updated_at = ? WHERE id = ?;", strings.Join(setClauses, ", "))
	result, err := tg.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("update task: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update task: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("task %q: not found", id)
	}

	return nil
}

// CloseTask sets a task's status to closed.
func (tg *TaskGraph) CloseTask(ctx context.Context, id string) error {
	return tg.UpdateTask(ctx, id, map[string]any{"status": StatusClosed})
}

// AddEdge creates a dependency relationship between two tasks.
func (tg *TaskGraph) AddEdge(ctx context.Context, from, to string) error {
	if tg.db == nil {
		return fmt.Errorf("taskgraph: not initialized")
	}
	
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from == "" {
		return fmt.Errorf("from task id is required")
	}
	if to == "" {
		return fmt.Errorf("to task id is required")
	}
	if from == to {
		return fmt.Errorf("taskgraph: self-loop edges are not allowed")
	}

	fromProject, err := tg.taskProject(ctx, from)
	if err != nil {
		return err
	}
	toProject, err := tg.taskProject(ctx, to)
	if err != nil {
		return err
	}
	if fromProject != toProject {
		return fmt.Errorf("taskgraph: cross-project dependencies are not allowed")
	}
	if cycleErr := tg.ensureNoCycle(ctx, from, to); cycleErr != nil {
		return cycleErr
	}

	const insertEdgeSQL = `INSERT OR IGNORE INTO task_edges (from_task, to_task) VALUES (?, ?);`
	_, err = tg.db.ExecContext(ctx, insertEdgeSQL, from, to)
	return err
}

// RemoveEdge removes a dependency relationship between two tasks.
func (tg *TaskGraph) RemoveEdge(ctx context.Context, from, to string) error {
	if tg.db == nil {
		return fmt.Errorf("taskgraph: not initialized")
	}
	
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from == "" {
		return fmt.Errorf("from task id is required")
	}
	if to == "" {
		return fmt.Errorf("to task id is required")
	}

	const deleteEdgeSQL = `DELETE FROM task_edges WHERE from_task = ? AND to_task = ?;`
	_, err := tg.db.ExecContext(ctx, deleteEdgeSQL, from, to)
	return err
}

// GetReadyNodes returns tasks that are ready to execute (status=ready, all deps complete).
func (tg *TaskGraph) GetReadyNodes(ctx context.Context, project string) ([]Task, error) {
	if tg.db == nil {
		return nil, fmt.Errorf("taskgraph: not initialized")
	}
	
	project = strings.TrimSpace(project)
	if project == "" {
		return nil, fmt.Errorf("project is required")
	}

	const readyNodesSQL = `SELECT ` + taskColumns + ` FROM tasks AS t
		WHERE t.project = ? AND lower(t.status) = ?
		AND lower(t."type") NOT IN (?, ?)
		AND NOT EXISTS (
			SELECT 1 FROM task_edges e
			JOIN tasks dependency ON dependency.id = e.to_task
			WHERE e.from_task = t.id
			AND lower(dependency.status) NOT IN (?, ?, ?, ?, ?, ?)
		)
		ORDER BY t.priority ASC, t.estimate_minutes ASC;`

	rows, err := tg.db.QueryContext(ctx, readyNodesSQL, project, StatusReady,
		TypeEpic, TypeWhale, StatusClosed, StatusCompleted, StatusEscalated,
		StatusPlanFailed, StatusCanceled, StatusDone)
	if err != nil {
		return nil, fmt.Errorf("get ready nodes: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	var ids []string
	for rows.Next() {
		task, scanErr := scanTaskRows(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan task: %w", scanErr)
		}
		ids = append(ids, task.ID)
		tasks = append(tasks, task)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("get ready nodes: %w", rowsErr)
	}

	dependencies, err := tg.taskDependencies(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("load task dependencies: %w", err)
	}
	for i := range tasks {
		tasks[i].DependsOn = dependencies[tasks[i].ID]
	}

	return tasks, nil
}

// GetDependents returns task IDs that directly depend on the given task.
func (tg *TaskGraph) GetDependents(ctx context.Context, taskID string) ([]string, error) {
	if tg.db == nil {
		return nil, fmt.Errorf("taskgraph: not initialized")
	}
	
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, fmt.Errorf("task id is required")
	}

	const getDependentsSQL = `SELECT from_task FROM task_edges WHERE to_task = ? ORDER BY from_task`
	rows, err := tg.db.QueryContext(ctx, getDependentsSQL, taskID)
	if err != nil {
		return nil, fmt.Errorf("get dependents: %w", err)
	}
	defer rows.Close()

	var dependents []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan dependent: %w", err)
		}
		dependents = append(dependents, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get dependents: %w", err)
	}
	return dependents, nil
}

// AutoUnblockDependents checks tasks depending on completedTaskID and promotes
// any that have all dependencies completed from "open" to "ready".
func (tg *TaskGraph) AutoUnblockDependents(ctx context.Context, completedTaskID string) ([]string, error) {
	dependents, err := tg.GetDependents(ctx, completedTaskID)
	if err != nil {
		return nil, err
	}

	promoted := make([]string, 0, len(dependents))
	for _, depID := range dependents {
		task, err := tg.GetTask(ctx, depID)
		if err != nil {
			continue // best-effort
		}
		if normalizeTaskStatus(task.Status) != StatusOpen {
			continue // only promote open -> ready
		}

		// Check if ALL dependencies are now terminal
		allDone := true
		for _, reqID := range task.DependsOn {
			reqTask, getErr := tg.GetTask(ctx, reqID)
			if getErr != nil {
				allDone = false
				break
			}
			if !isTerminalStatus(reqTask.Status) {
				allDone = false
				break
			}
		}
		if !allDone {
			continue
		}

		if updateErr := tg.UpdateTask(ctx, depID, map[string]any{"status": StatusReady}); updateErr != nil {
			continue // best-effort
		}
		promoted = append(promoted, depID)
	}
	return promoted, nil
}

// Helper types and functions

type taskUpdateField struct {
	column string
	value  any
}

type rowScanner interface {
	Scan(dest ...any) error
}

func generateTaskID(project string) (string, error) {
	project = strings.TrimSpace(project)
	if project == "" {
		return "", fmt.Errorf("project is required")
	}
	const maxSuffix = int64(0x1000000) // 16^6
	n, err := rand.Int(rand.Reader, big.NewInt(maxSuffix))
	if err != nil {
		return "", fmt.Errorf("generate task ID: %w", err)
	}
	return fmt.Sprintf("%s-%06x", project, n), nil
}

func sanitizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func normalizeTaskStatus(status string) string {
	status = strings.TrimSpace(strings.ToLower(status))
	if status == "" {
		return StatusOpen
	}
	return status
}

func normalizeTaskType(taskType string) string {
	taskType = strings.TrimSpace(strings.ToLower(taskType))
	if taskType == "" {
		return TypeTask
	}
	return taskType
}

func isTerminalStatus(status string) bool {
	_, ok := terminalStatuses[normalizeTaskStatus(status)]
	return ok
}

func transitioningToReady(assignments []taskUpdateField) bool {
	for i := range assignments {
		if assignments[i].column != "status" {
			continue
		}
		status, ok := assignments[i].value.(string)
		if !ok {
			return false
		}
		return normalizeTaskStatus(status) == StatusReady
	}
	return false
}

func (tg *TaskGraph) ensureReadyTransitionUnblocked(ctx context.Context, id string) error {
	const readyTransitionCheckSQL = `SELECT e.to_task, lower(coalesce(dependency.status, ''))
		FROM task_edges e
		LEFT JOIN tasks dependency ON dependency.id = e.to_task
		WHERE e.from_task = ?
		AND (dependency.id IS NULL OR lower(dependency.status) NOT IN (?, ?, ?, ?, ?, ?))
		ORDER BY e.to_task ASC LIMIT 1;`

	var dependencyID, dependencyStatus string
	err := tg.db.QueryRowContext(ctx, readyTransitionCheckSQL, id,
		StatusClosed, StatusCompleted, StatusEscalated, StatusPlanFailed, StatusCanceled, StatusDone).
		Scan(&dependencyID, &dependencyStatus)
	if err == nil {
		if dependencyStatus == "" {
			dependencyStatus = "missing"
		}
		return fmt.Errorf("cannot set task %q to ready: unresolved dependency %q has status %q", id, dependencyID, dependencyStatus)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return fmt.Errorf("check ready transition dependencies: %w", err)
}

func (tg *TaskGraph) taskProject(ctx context.Context, id string) (string, error) {
	const selectTaskProjectSQL = `SELECT project FROM tasks WHERE id = ?;`
	row := tg.db.QueryRowContext(ctx, selectTaskProjectSQL, id)
	var project string
	if err := row.Scan(&project); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("taskgraph: task %q: not found", id)
		}
		return "", fmt.Errorf("lookup task %q project: %w", id, err)
	}
	return project, nil
}

func (tg *TaskGraph) ensureNoCycle(ctx context.Context, from, to string) error {
	const cycleCheckSQL = `
		WITH RECURSIVE reachable(task_id) AS (
			SELECT to_task FROM task_edges WHERE from_task = ?
			UNION ALL
			SELECT e.to_task
			FROM task_edges e
			INNER JOIN reachable r ON e.from_task = r.task_id
		)
		SELECT 1 FROM reachable WHERE task_id = ? LIMIT 1;`

	var marker int
	err := tg.db.QueryRowContext(ctx, cycleCheckSQL, to, from).Scan(&marker)
	if err == nil {
		return fmt.Errorf("taskgraph: adding this edge would create a cycle")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("cycle check: %w", err)
	}
	return nil
}

func (tg *TaskGraph) taskDependencies(ctx context.Context, taskIDs []string) (map[string][]string, error) {
	dependencies := make(map[string][]string, len(taskIDs))
	if len(taskIDs) == 0 {
		return dependencies, nil
	}

	const dependenciesSQL = `SELECT from_task, to_task FROM task_edges WHERE from_task IN `
	query := dependenciesSQL + "(" + placeholders(len(taskIDs)) + ")"
	args := make([]any, len(taskIDs))
	for i, id := range taskIDs {
		args[i] = id
	}

	rows, err := tg.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query dependencies: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var from, to string
		if err := rows.Scan(&from, &to); err != nil {
			return nil, fmt.Errorf("scan dependency: %w", err)
		}
		dependencies[from] = append(dependencies[from], to)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query dependencies: %w", err)
	}

	return dependencies, nil
}

func scanTask(scanner rowScanner) (Task, error) {
	var task Task
	var labelsJSON string

	if err := scanner.Scan(
		&task.ID, &task.Title, &task.Description, &task.Status, &task.Priority,
		&task.Type, &task.Assignee, &labelsJSON, &task.EstimateMinutes,
		&task.ParentID, &task.Acceptance, &task.Design, &task.Notes,
		&task.Project, &task.ErrorLog, &task.CreatedAt, &task.UpdatedAt,
	); err != nil {
		return Task{}, err
	}

	labels, err := unmarshalLabels(labelsJSON)
	if err != nil {
		return Task{}, err
	}
	task.Labels = labels

	return task, nil
}

func scanTaskRows(scanner *sql.Rows) (Task, error) {
	return scanTask(scanner)
}

func placeholders(count int) string {
	if count == 0 {
		return ""
	}
	values := make([]string, count)
	for i := range values {
		values[i] = "?"
	}
	return strings.Join(values, ", ")
}

func normalizeStatusFilters(raw []string) []string {
	seen := make(map[string]struct{}, len(raw))
	for _, status := range raw {
		normalized := normalizeTaskStatus(status)
		if normalized == "" {
			continue
		}
		seen[normalized] = struct{}{}
	}

	out := make([]string, 0, len(seen))
	for status := range seen {
		out = append(out, status)
	}
	sort.Strings(out)
	return out
}

func marshalLabels(labels []string) (string, error) {
	if labels == nil {
		labels = []string{}
	}
	b, err := json.Marshal(labels)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func unmarshalLabels(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return []string{}, nil
	}
	var labels []string
	if err := json.Unmarshal([]byte(raw), &labels); err != nil {
		return nil, fmt.Errorf("invalid labels JSON: %w", err)
	}
	if labels == nil {
		return []string{}, nil
	}
	return labels, nil
}

func marshalLabelsValue(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return marshalLabels(nil)
	case []string:
		return marshalLabels(typed)
	case []any:
		labels := make([]string, len(typed))
		for i, labelValue := range typed {
			s, ok := labelValue.(string)
			if !ok {
				return "", fmt.Errorf("label value must be string at index %d", i)
			}
			labels[i] = s
		}
		return marshalLabels(labels)
	case string:
		var labels []string
		if err := json.Unmarshal([]byte(typed), &labels); err != nil {
			return "", fmt.Errorf("labels must be JSON array: %w", err)
		}
		return marshalLabels(labels)
	default:
		return "", fmt.Errorf("labels must be []string or JSON encoded string")
	}
}

func coerceString(value any) string {
	return fmt.Sprintf("%v", value)
}

func coerceInt(value any) (int, error) {
	switch v := value.(type) {
	case int, int8, int16, int32, int64:
		return int(v.(int64)), nil
	case uint, uint8, uint16, uint32, uint64:
		return int(v.(uint64)), nil
	case float32, float64:
		return int(v.(float64)), nil
	default:
		return 0, fmt.Errorf("value is not an integer: %T", value)
	}
}

func isUniqueTaskIDError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "unique constraint failed") && strings.Contains(text, "tasks.id")
}