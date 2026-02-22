package graph

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

	statusOpen   = "open"
	statusReady  = "ready"
	statusClosed = "closed"
	defaultType  = "task"
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

const (
	insertTaskSQL = `INSERT INTO tasks (
		id,
		title,
		description,
		status,
		priority,
		"type",
		assignee,
		labels,
		estimate_minutes,
		parent_id,
		acceptance,
		design,
		notes,
		project,
		error_log,
		created_at,
		updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`

	getTaskSQL = `SELECT ` + taskColumns + `
		FROM tasks
		WHERE id = ?;`

	listTasksSQL = `SELECT ` + taskColumns + `
		FROM tasks
		WHERE project = ?`

	readyNodesSQL = `SELECT ` + taskColumns + `
		FROM tasks AS t
		WHERE t.project = ?
		  AND lower(t.status) = ?
		  AND lower(t."type") NOT IN (?, ?)
		  AND NOT EXISTS (
			SELECT 1
			FROM task_edges e
			JOIN tasks dependency ON dependency.id = e.to_task
			WHERE e.from_task = t.id
			  AND lower(dependency.status) != ?
		)
		ORDER BY t.priority ASC, t.estimate_minutes ASC;`

	insertEdgeSQL        = `INSERT OR IGNORE INTO task_edges (from_task, to_task) VALUES (?, ?);`
	deleteEdgeSQL        = `DELETE FROM task_edges WHERE from_task = ? AND to_task = ?;`
	selectTaskProjectSQL = `SELECT project FROM tasks WHERE id = ?;`
	cycleCheckSQL        = `
		WITH RECURSIVE reachable(task_id) AS (
			SELECT to_task FROM task_edges WHERE from_task = ?
			UNION ALL
			SELECT e.to_task
			FROM task_edges e
			INNER JOIN reachable r ON e.from_task = r.task_id
		)
		SELECT 1 FROM reachable WHERE task_id = ? LIMIT 1;`
	dependenciesSQL = `SELECT from_task, to_task FROM task_edges WHERE from_task IN `
)

const (
	maxTaskIDAttempts = 10
)

var updatableColumns = map[string]struct{}{
	"title":            {},
	"description":      {},
	"status":           {},
	"priority":         {},
	"type":             {},
	"assignee":         {},
	"labels":           {},
	"estimate_minutes": {},
	"parent_id":        {},
	"acceptance":       {},
	"design":           {},
	"notes":            {},
	"project":          {},
	"error_log":        {},
}

type rowScanner interface {
	Scan(dest ...any) error
}

type DAG struct {
	db *sql.DB
}

func NewDAG(db *sql.DB) *DAG {
	return &DAG{db: db}
}

func (d *DAG) EnsureSchema(ctx context.Context) error {
	if d == nil || d.db == nil {
		return fmt.Errorf("graph: DAG is not initialized")
	}

	ctx = sanitizeContext(ctx)
	if _, err := execContext(ctx, d.db, pragmaJournalModeWAL); err != nil {
		return fmt.Errorf("set journal mode WAL: %w", err)
	}
	if _, err := execContext(ctx, d.db, pragmaForeignKeysOn); err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
	}
	if _, err := execContext(ctx, d.db, taskTableSchema); err != nil {
		return fmt.Errorf("create tasks table: %w", err)
	}
	if _, err := execContext(ctx, d.db, taskEdgesSchema); err != nil {
		return fmt.Errorf("create task_edges table: %w", err)
	}
	return nil
}

func (d *DAG) generateTaskID(project string) (string, error) {
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
		return statusOpen
	}
	return status
}

func normalizeTaskType(taskType string) string {
	taskType = strings.TrimSpace(strings.ToLower(taskType))
	if taskType == "" {
		return defaultType
	}
	return taskType
}

func (d *DAG) CreateTask(ctx context.Context, task Task) (string, error) {
	if d == nil || d.db == nil {
		return "", fmt.Errorf("graph: DAG is not initialized")
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

	for attempt := 0; attempt < maxTaskIDAttempts; attempt++ {
		id, err := d.generateTaskID(project)
		if err != nil {
			return "", err
		}

		_, err = execContext(ctx, d.db, insertTaskSQL,
			id,
			task.Title,
			task.Description,
			status,
			task.Priority,
			taskType,
			task.Assignee,
			labelsJSON,
			task.EstimateMinutes,
			task.ParentID,
			task.Acceptance,
			task.Design,
			task.Notes,
			project,
			task.ErrorLog,
			now,
			now,
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

func (d *DAG) GetTask(ctx context.Context, id string) (Task, error) {
	if d == nil || d.db == nil {
		return Task{}, fmt.Errorf("graph: DAG is not initialized")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return Task{}, fmt.Errorf("id is required")
	}

	row := queryRowContext(ctx, d.db, getTaskSQL, id)
	task, err := scanTask(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, fmt.Errorf("task %q: not found", id)
		}
		return Task{}, err
	}

	dependencies, err := d.taskDependencies(ctx, []string{task.ID})
	if err != nil {
		return Task{}, fmt.Errorf("load task dependencies: %w", err)
	}
	task.DependsOn = dependencies[task.ID]

	return task, nil
}

func (d *DAG) ListTasks(ctx context.Context, project string, statuses ...string) ([]Task, error) {
	if d == nil || d.db == nil {
		return nil, fmt.Errorf("graph: DAG is not initialized")
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return nil, fmt.Errorf("project is required")
	}

	args := []any{project}
	query := listTasksSQL
	statusFilters := normalizeStatusFilters(statuses)
	if len(statusFilters) > 0 {
		query += fmt.Sprintf(" AND lower(status) IN (%s)", placeholders(len(statusFilters)))
		for _, status := range statusFilters {
			args = append(args, status)
		}
	}

	rows, err := queryContext(ctx, d.db, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	var ids []string
	for rows.Next() {
		task, scanErr := scanTask(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan task: %w", scanErr)
		}
		ids = append(ids, task.ID)
		tasks = append(tasks, task)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("list tasks: %w", rowsErr)
	}

	dependencies, err := d.taskDependencies(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("load task dependencies: %w", err)
	}
	for i := range tasks {
		tasks[i].DependsOn = dependencies[tasks[i].ID]
	}

	return tasks, nil
}

func (d *DAG) UpdateTask(ctx context.Context, id string, fields map[string]any) error {
	if d == nil || d.db == nil {
		return fmt.Errorf("graph: DAG is not initialized")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("id is required")
	}
	if len(fields) == 0 {
		return nil
	}

	type updateField struct {
		column string
		value  any
	}

	assignments := make([]updateField, 0, len(fields))
	unrecognized := make([]string, 0)
	for rawKey, rawValue := range fields {
		key := strings.TrimSpace(strings.ToLower(rawKey))
		if _, ok := updatableColumns[key]; !ok {
			unrecognized = append(unrecognized, rawKey)
			continue
		}

		switch key {
		case "title":
			assignments = append(assignments, updateField{column: "title", value: coerceString(rawValue)})
		case "description":
			assignments = append(assignments, updateField{column: "description", value: coerceString(rawValue)})
		case "status":
			assignments = append(assignments, updateField{column: "status", value: normalizeTaskStatus(coerceString(rawValue))})
		case "priority":
			priority, err := coerceInt(rawValue)
			if err != nil {
				return err
			}
			assignments = append(assignments, updateField{column: "priority", value: priority})
		case "type":
			assignments = append(assignments, updateField{column: "\"type\"", value: normalizeTaskType(coerceString(rawValue))})
		case "assignee":
			assignments = append(assignments, updateField{column: "assignee", value: coerceString(rawValue)})
		case "labels":
			labelsJSON, err := marshalLabelsValue(rawValue)
			if err != nil {
				return fmt.Errorf("labels: %w", err)
			}
			assignments = append(assignments, updateField{column: "labels", value: labelsJSON})
		case "estimate_minutes":
			value, err := coerceInt(rawValue)
			if err != nil {
				return err
			}
			assignments = append(assignments, updateField{column: "estimate_minutes", value: value})
		case "parent_id":
			assignments = append(assignments, updateField{column: "parent_id", value: coerceString(rawValue)})
		case "acceptance":
			assignments = append(assignments, updateField{column: "acceptance", value: coerceString(rawValue)})
		case "design":
			assignments = append(assignments, updateField{column: "design", value: coerceString(rawValue)})
		case "notes":
			assignments = append(assignments, updateField{column: "notes", value: coerceString(rawValue)})
		case "project":
			assignments = append(assignments, updateField{column: "project", value: coerceString(rawValue)})
		case "error_log":
			assignments = append(assignments, updateField{column: "error_log", value: coerceString(rawValue)})
		}
	}
	if len(assignments) == 0 {
		if _, err := d.GetTask(ctx, id); err != nil {
			return err
		}
		return fmt.Errorf("graph: no recognized fields to update")
	}
	if len(unrecognized) > 0 {
		return fmt.Errorf("graph: field %q is not updatable", unrecognized[0])
	}

	sort.Slice(assignments, func(i, j int) bool {
		return assignments[i].column < assignments[j].column
	})

	setClauses := make([]string, len(assignments))
	args := make([]any, 0, len(assignments)+2)
	for i := range assignments {
		setClauses[i] = fmt.Sprintf("%s = ?", assignments[i].column)
		args = append(args, assignments[i].value)
	}
	now := time.Now().UTC()
	args = append(args, now, id)

	query := fmt.Sprintf("UPDATE tasks SET %s, updated_at = ? WHERE id = ?;", strings.Join(setClauses, ", "))
	result, err := execContext(ctx, d.db, query, args...)
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

func (d *DAG) CloseTask(ctx context.Context, id string) error {
	return d.UpdateTask(ctx, id, map[string]any{"status": statusClosed})
}

func (d *DAG) AddEdge(ctx context.Context, from, to string) error {
	if d == nil || d.db == nil {
		return fmt.Errorf("graph: DAG is not initialized")
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
		return fmt.Errorf("graph: self-loop edges are not allowed")
	}

	fromProject, err := d.taskProject(ctx, from)
	if err != nil {
		return err
	}
	toProject, err := d.taskProject(ctx, to)
	if err != nil {
		return err
	}
	if fromProject != toProject {
		return fmt.Errorf("graph: cross-project dependencies are not allowed")
	}
	if cycleErr := d.ensureNoCycle(ctx, from, to); cycleErr != nil {
		return cycleErr
	}

	_, err = execContext(ctx, d.db, insertEdgeSQL, from, to)
	return err
}

func (d *DAG) RemoveEdge(ctx context.Context, from, to string) error {
	if d == nil || d.db == nil {
		return fmt.Errorf("graph: DAG is not initialized")
	}
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from == "" {
		return fmt.Errorf("from task id is required")
	}
	if to == "" {
		return fmt.Errorf("to task id is required")
	}

	_, err := execContext(ctx, d.db, deleteEdgeSQL, from, to)
	return err
}

func (d *DAG) GetReadyNodes(ctx context.Context, project string) ([]Task, error) {
	if d == nil || d.db == nil {
		return nil, fmt.Errorf("graph: DAG is not initialized")
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return nil, fmt.Errorf("project is required")
	}

	rows, err := queryContext(ctx, d.db, readyNodesSQL, project, statusReady, taskTypeEpic, taskTypeWhale, statusClosed)
	if err != nil {
		return nil, fmt.Errorf("get ready nodes: %w", err)
	}
	defer rows.Close()

	var tasks []Task
	var ids []string
	for rows.Next() {
		task, scanErr := scanTask(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan task: %w", scanErr)
		}
		ids = append(ids, task.ID)
		tasks = append(tasks, task)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("get ready nodes: %w", rowsErr)
	}

	dependencies, err := d.taskDependencies(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("load task dependencies: %w", err)
	}
	for i := range tasks {
		tasks[i].DependsOn = dependencies[tasks[i].ID]
	}

	return tasks, nil
}

func (d *DAG) taskProject(ctx context.Context, id string) (string, error) {
	row := queryRowContext(ctx, d.db, selectTaskProjectSQL, id)
	var project string
	if err := row.Scan(&project); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("graph: task %q: not found", id)
		}
		return "", fmt.Errorf("lookup task %q project: %w", id, err)
	}
	return project, nil
}

func (d *DAG) ensureNoCycle(ctx context.Context, from, to string) error {
	var marker int
	err := queryRowContext(ctx, d.db, cycleCheckSQL, to, from).Scan(&marker)
	if err == nil {
		return fmt.Errorf("graph: adding this edge would create a cycle")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("cycle check: %w", err)
	}
	return nil
}

func (d *DAG) taskDependencies(ctx context.Context, taskIDs []string) (map[string][]string, error) {
	dependencies := make(map[string][]string, len(taskIDs))
	if len(taskIDs) == 0 {
		return dependencies, nil
	}

	query := dependenciesSQL + "(" + placeholders(len(taskIDs)) + ")"
	args := make([]any, len(taskIDs))
	for i, id := range taskIDs {
		args[i] = id
	}

	rows, err := queryContext(ctx, d.db, query, args...)
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
		&task.ID,
		&task.Title,
		&task.Description,
		&task.Status,
		&task.Priority,
		&task.Type,
		&task.Assignee,
		&labelsJSON,
		&task.EstimateMinutes,
		&task.ParentID,
		&task.Acceptance,
		&task.Design,
		&task.Notes,
		&task.Project,
		&task.ErrorLog,
		&task.CreatedAt,
		&task.UpdatedAt,
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

func queryContext(ctx context.Context, db *sql.DB, query string, args ...any) (*sql.Rows, error) {
	return db.QueryContext(sanitizeContext(ctx), query, args...)
}

func queryRowContext(ctx context.Context, db *sql.DB, query string, args ...any) *sql.Row {
	return db.QueryRowContext(sanitizeContext(ctx), query, args...)
}

func execContext(ctx context.Context, db *sql.DB, query string, args ...any) (sql.Result, error) {
	return db.ExecContext(sanitizeContext(ctx), query, args...)
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
	case int:
		return v, nil
	case int8:
		return int(v), nil
	case int16:
		return int(v), nil
	case int32:
		return int(v), nil
	case int64:
		return int(v), nil
	case uint:
		return int(v), nil
	case uint8:
		return int(v), nil
	case uint16:
		return int(v), nil
	case uint32:
		return int(v), nil
	case uint64:
		return int(v), nil
	case float32:
		return int(v), nil
	case float64:
		return int(v), nil
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
