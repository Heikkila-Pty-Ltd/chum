package temporal

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"

	"github.com/antigravity-dev/chum/internal/graph"
)

// MutateTasksActivity runs a fast LLM to decide what task mutations to apply
// after a task completes, then executes those mutations via the DAG.
//
// Mutations are capped at 5 per cycle to prevent runaway grooming.
func (a *Activities) MutateTasksActivity(ctx context.Context, req TacticalGroomRequest) (*GroomResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(RemoraPrefix+" Tactical groom: analyzing tasks", "TaskID", req.TaskID, "Project", req.Project)

	// Get current task state
	allTasks, err := a.DAG.ListTasks(ctx, req.Project)
	if err != nil {
		logger.Warn(RemoraPrefix+" Can't list tasks, skipping grooming", "error", err)
		return &GroomResult{}, nil
	}

	// Get detail of completed task
	completedTask, showErr := a.DAG.GetTask(ctx, req.TaskID)
	if showErr != nil {
		logger.Warn(RemoraPrefix+" Can't show completed task", "task", req.TaskID, "error", showErr)
	}

	// Build compressed backlog summary for the LLM
	var taskSummary strings.Builder
	openCount := 0
	for i := range allTasks {
		t := &allTasks[i]
		if t.Status == "open" && t.Type != "epic" {
			taskSummary.WriteString(fmt.Sprintf("- [P%d] %s: %s\n", t.Priority, t.ID, t.Title))
			openCount++
			if openCount >= 30 { // cap to keep prompt small
				taskSummary.WriteString(fmt.Sprintf("... and %d more open tasks\n", countOpenTasks(allTasks)-30))
				break
			}
		}
	}

	completedContext := ""
	if showErr == nil {
		completedContext = fmt.Sprintf("COMPLETED TASK: %s - %s\nDescription: %s",
			completedTask.ID, completedTask.Title,
			truncate(completedTask.Description, 500))
	}

	prompt := fmt.Sprintf(`You are a tactical backlog groomer. A task just completed. Analyze the open backlog and suggest mutations.

%s

OPEN TASKS (%d):
%s

Rules:
1. Only suggest mutations that are clearly warranted by the completion
2. Reprioritize if the completed task unblocks or changes context for siblings
3. Add dependencies if you discover implicit blockers
4. Append hints to related tasks using update_notes (e.g. "after %s completed, consider X")
5. Never create vague "refactor" or "cleanup" tasks
6. Maximum 5 mutations per cycle

Respond with ONLY a JSON array of mutations:
[{
  "task_id": "existing-task-id or empty for create",
  "action": "update_priority|add_dependency|update_notes|create|close",
  "priority": 2,
  "notes": "context to append",
  "depends_on_id": "dependency target",
  "title": "new task title (for create)",
  "description": "new task description (for create)",
  "reason": "reason for closing (for close)"
}]

Return empty array [] if no mutations are needed.`,
		completedContext, openCount, taskSummary.String(), req.TaskID)

	agent := ResolveTierAgent(a.Tiers, req.Tier)
	cliResult, err := runAgent(ctx, agent, prompt, req.WorkDir)
	if err != nil {
		logger.Warn(RemoraPrefix+" LLM grooming call failed (non-fatal)", "error", err)
		return &GroomResult{}, nil
	}

	jsonStr := extractJSONArray(cliResult.Output)
	if jsonStr == "" || jsonStr == "[]" {
		return &GroomResult{}, nil
	}

	var mutations []BeadMutation
	if err := json.Unmarshal([]byte(jsonStr), &mutations); err != nil {
		logger.Warn(RemoraPrefix+" Failed to parse mutations JSON", "error", err)
		return &GroomResult{}, nil
	}

	// Cap at 5 mutations per cycle
	if len(mutations) > 5 {
		mutations = mutations[:5]
	}

	result := &GroomResult{}
	for i := range mutations {
		m := &mutations[i]
		if err := a.applyMutation(ctx, req.Project, *m); err != nil {
			result.MutationsFailed++
			result.Details = append(result.Details, fmt.Sprintf("FAILED %s on %s: %v", m.Action, m.TaskID, err))
			logger.Warn(RemoraPrefix+" Mutation failed", "action", m.Action, "task", m.TaskID, "error", err)
		} else {
			result.MutationsApplied++
			result.Details = append(result.Details, fmt.Sprintf("OK %s on %s", m.Action, m.TaskID))
		}
	}

	logger.Info(RemoraPrefix+" Tactical groom complete", "Applied", result.MutationsApplied, "Failed", result.MutationsFailed)
	return result, nil
}

// ApplyStrategicMutationsActivity applies pre-normalized strategic mutations
// directly without re-invoking the LLM. This is the correct path for mutations
// produced by StrategicAnalysisActivity + normalizeStrategicMutations.
func (a *Activities) ApplyStrategicMutationsActivity(ctx context.Context, project string, mutations []BeadMutation) (*GroomResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(RemoraPrefix+" Applying strategic mutations", "count", len(mutations))

	result := &GroomResult{}
	for i := range mutations {
		m := &mutations[i]
		if err := a.applyMutation(ctx, project, *m); err != nil {
			result.MutationsFailed++
			result.Details = append(result.Details, fmt.Sprintf("FAILED %s on %s: %v", m.Action, m.TaskID, err))
			logger.Warn(RemoraPrefix+" Strategic mutation failed", "action", m.Action, "task", m.TaskID, "error", err)
		} else {
			result.MutationsApplied++
			result.Details = append(result.Details, fmt.Sprintf("OK %s on %s", m.Action, m.TaskID))
		}
	}

	logger.Info(RemoraPrefix+" Strategic mutations complete", "Applied", result.MutationsApplied, "Failed", result.MutationsFailed)
	return result, nil
}

// applyMutation executes a single BeadMutation against the DAG.
func (a *Activities) applyMutation(ctx context.Context, project string, m BeadMutation) error {
	switch m.Action {
	case "update_priority":
		if m.Priority == nil {
			return fmt.Errorf("priority required for update_priority")
		}
		return a.DAG.UpdateTask(ctx, m.TaskID, map[string]any{"priority": *m.Priority})

	case "add_dependency":
		if m.DependsOnID == "" {
			return fmt.Errorf("depends_on_id required for add_dependency")
		}
		return a.DAG.AddEdge(ctx, m.TaskID, m.DependsOnID)

	case "update_notes":
		return a.DAG.UpdateTask(ctx, m.TaskID, map[string]any{"notes": m.Notes})

	case "create":
		m.Title = normalizeMutationTitle(m.Title)
		if m.Title == "" {
			return fmt.Errorf("title required for create")
		}

		priority := 2
		if m.Priority != nil {
			priority = *m.Priority
		}
		if isStrategicMutation(m) && m.Deferred {
			priority = 4
		}
		// Only enforce actionability for strategic creates — tactical LLM output
		// does not produce acceptance/design/estimate fields.
		if isStrategicMutation(m) && !isCreateMutationActionable(m) {
			if m.Deferred {
				return nil // no-op for incomplete deferred strategic suggestions
			}
			return fmt.Errorf("strategic create mutation missing acceptance/design/estimate metadata")
		}
		labels := mergeLabels(m.Labels, isStrategicMutation(m), m.Deferred)
		_, err := a.DAG.CreateTask(ctx, graph.Task{
			Title:           m.Title,
			Description:     m.Description,
			Type:            "task",
			Priority:        priority,
			Acceptance:      m.Acceptance,
			Design:          m.Design,
			EstimateMinutes: m.EstimateMinutes,
			Labels:          labels,
			Project:         project,
		})
		return err

	case "close":
		return a.DAG.CloseTask(ctx, m.TaskID)

	default:
		return fmt.Errorf("unknown mutation action: %s", m.Action)
	}
}

func isCreateMutationActionable(m BeadMutation) bool {
	return strings.TrimSpace(m.Title) != "" &&
		strings.TrimSpace(m.Description) != "" &&
		strings.TrimSpace(m.Acceptance) != "" &&
		strings.TrimSpace(m.Design) != "" &&
		m.EstimateMinutes > 0
}

func isStrategicMutation(m BeadMutation) bool {
	return strings.EqualFold(strings.TrimSpace(m.StrategicSource), StrategicMutationSource)
}

func normalizeMutationTitle(raw string) string {
	title := strings.TrimSpace(raw)
	lower := strings.ToLower(title)
	if strings.HasPrefix(lower, "auto:") {
		title = strings.TrimSpace(title[len("auto:"):])
	}
	if title == "" {
		return title
	}
	return title
}

func mergeLabels(labels []string, isStrategic, isDeferred bool) []string {
	if !isStrategic {
		return labels
	}
	out := append([]string{}, labels...)
	out = appendLabelIfMissing(out, StrategicSourceLabel)
	if isDeferred {
		out = appendLabelIfMissing(out, StrategicDeferredLabel)
	}
	return out
}

func appendLabelIfMissing(labels []string, label string) []string {
	for _, existing := range labels {
		if strings.EqualFold(existing, label) {
			return labels
		}
	}
	return append(labels, label)
}

// countOpenTasks returns the number of open, non-epic tasks.
func countOpenTasks(allTasks []graph.Task) int {
	n := 0
	for i := range allTasks {
		if allTasks[i].Status == "open" && allTasks[i].Type != "epic" {
			n++
		}
	}
	return n
}

// GenerateRepoMapActivity generates a compressed codebase map using go list + go doc.
// This gives the strategic groombot structural awareness without reading entire files.
func (a *Activities) GenerateRepoMapActivity(ctx context.Context, req StrategicGroomRequest) (*RepoMap, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(RemoraPrefix+" Generating repo map", "Project", req.Project)

	cmd := exec.CommandContext(ctx, "go", "list", "-json", "./...")
	cmd.Dir = req.WorkDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("go list failed: %w (%s)", err, truncate(string(output), 500))
	}

	repoMap := &RepoMap{GeneratedAt: time.Now().Format(time.RFC3339)}
	decoder := json.NewDecoder(strings.NewReader(string(output)))
	for decoder.More() {
		var pkg struct {
			ImportPath string   `json:"ImportPath"`
			Name       string   `json:"Name"`
			GoFiles    []string `json:"GoFiles"`
			Doc        string   `json:"Doc"`
		}
		if err := decoder.Decode(&pkg); err != nil {
			continue
		}

		// Get exported symbols via go doc (best-effort, quick)
		var exports []string
		docCmd := exec.CommandContext(ctx, "go", "doc", "-short", pkg.ImportPath)
		docCmd.Dir = req.WorkDir
		docOutput, docErr := docCmd.CombinedOutput()
		if docErr == nil {
			for _, line := range strings.Split(string(docOutput), "\n") {
				line = strings.TrimSpace(line)
				if line != "" && (strings.HasPrefix(line, "func ") ||
					strings.HasPrefix(line, "type ") ||
					strings.HasPrefix(line, "var ") ||
					strings.HasPrefix(line, "const ")) {
					exports = append(exports, line)
					if len(exports) >= 20 {
						break
					}
				}
			}
		}

		repoMap.Packages = append(repoMap.Packages, PackageInfo{
			ImportPath: pkg.ImportPath,
			Name:       pkg.Name,
			GoFiles:    pkg.GoFiles,
			DocSummary: firstLine(pkg.Doc),
			Exports:    exports,
		})
		repoMap.TotalFiles += len(pkg.GoFiles)
	}

	logger.Info(RemoraPrefix+" Repo map generated", "Packages", len(repoMap.Packages), "Files", repoMap.TotalFiles)
	return repoMap, nil
}

func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}

// GetBeadStateSummaryActivity returns a compressed text summary of the open backlog.
func (a *Activities) GetBeadStateSummaryActivity(ctx context.Context, req StrategicGroomRequest) (string, error) {
	allTasks, err := a.DAG.ListTasks(ctx, req.Project)
	if err != nil {
		return "", fmt.Errorf("listing tasks: %w", err)
	}

	depGraph := graph.BuildDepGraph(allTasks)
	unblocked := graph.FilterUnblockedOpen(allTasks, depGraph)

	var sb strings.Builder
	openCount, closedCount := 0, 0
	for i := range allTasks {
		if allTasks[i].Status == "open" {
			openCount++
		} else {
			closedCount++
		}
	}

	sb.WriteString(fmt.Sprintf("Total: %d open, %d closed, %d unblocked ready\n\n", openCount, closedCount, len(unblocked)))

	for i := range allTasks {
		t := &allTasks[i]
		if t.Status != "open" || t.Type == "epic" {
			continue
		}
		blocked := ""
		if len(t.DependsOn) > 0 {
			blocked = fmt.Sprintf(" (blocked by: %s)", strings.Join(t.DependsOn, ","))
		}
		sb.WriteString(fmt.Sprintf("[P%d] %s: %s%s\n", t.Priority, t.ID, t.Title, blocked))
	}

	return sb.String(), nil
}

// StrategicAnalysisActivity uses a premium LLM with the repo map + task state
// + recent lessons to produce a strategic analysis.
func (a *Activities) StrategicAnalysisActivity(ctx context.Context, req StrategicGroomRequest, repoMap *RepoMap, taskState string) (*StrategicAnalysis, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(RemoraPrefix+" Strategic analysis", "Project", req.Project)

	// Query recent lessons for context
	var lessonsContext string
	if a.Store != nil {
		lessons, lessonsErr := a.Store.GetRecentLessons(req.Project, 10)
		if lessonsErr != nil {
			logger.Warn(RemoraPrefix+" Failed to get recent lessons", "error", lessonsErr)
		} else if len(lessons) > 0 {
			var lb strings.Builder
			for i := range lessons {
				lb.WriteString(fmt.Sprintf("- [%s] %s (task: %s)\n", lessons[i].Category, lessons[i].Summary, lessons[i].BeadID))
			}
			lessonsContext = "RECENT LESSONS:\n" + lb.String()
		}
	}

	// Compress repo map to string
	var rmSummary strings.Builder
	for _, pkg := range repoMap.Packages {
		rmSummary.WriteString(fmt.Sprintf("pkg %s (%d files): %s\n", pkg.ImportPath, len(pkg.GoFiles), pkg.DocSummary))
		limit := 5
		if limit > len(pkg.Exports) {
			limit = len(pkg.Exports)
		}
		for _, exp := range pkg.Exports[:limit] {
			rmSummary.WriteString(fmt.Sprintf("  %s\n", exp))
		}
	}

	prompt := fmt.Sprintf(`You are a senior engineering strategist performing a daily analysis of project "%s".

REPO STRUCTURE (%d packages, %d files):
%s

OPEN TASKS:
%s

%s

Produce a strategic analysis:
1. What are the TOP 3-5 priorities and why?
2. What RISKS exist (technical debt, blocked tasks, complexity)?
3. What task MUTATIONS would improve the backlog? (reprioritize, create, add deps, close stale)

Mutation contract:
- action=create must be fully actionable with:
  - scoped title (no generic "Auto:" prefixes)
  - description
  - acceptance_criteria
  - design
  - estimate_minutes (minutes, integer > 0)
  - strategic_source: "strategic"
  - deferred: false
- decomposition/meta recommendations must be explicitly deferred:
  - set deferred: true
  - set strategic_source: "strategic"
  - set priority to 4 or omit
  - title can be a short recommendation label
  - do not emit these as immediate executable tasks

Respond with ONLY a JSON object:
{
  "priorities": [{"task_id": "or empty", "title": "...", "rationale": "...", "urgency": "critical|high|medium|low"}],
  "risks": ["risk 1", "risk 2"],
  "observations": ["observation 1"],
  "mutations": [{
    "task_id": "existing-task-id or empty for create",
    "action": "update_priority|add_dependency|update_notes|create|close",
    "priority": 2,
    "reason": "...",
    "notes": "...",
    "depends_on_id": "...",
    "title": "...",
    "description": "...",
    "acceptance_criteria": "...",
    "design": "...",
    "estimate_minutes": 30,
    "strategic_source": "strategic",
    "deferred": true|false,
    "labels": ["source:strategic", "strategy:deferred"]
  }]
}

Be opinionated. Say what matters most and why.`,
		req.Project,
		len(repoMap.Packages), repoMap.TotalFiles,
		truncate(rmSummary.String(), 4000),
		truncate(taskState, 3000),
		lessonsContext,
	)

	// Strategic analysis requires deep reasoning — always use gemini-pro.
	// codex-spark crashes on the large repo-map + task-state prompts.
	agent := "gemini-pro"
	cliResult, err := runAgent(ctx, agent, prompt, req.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("strategic analysis failed: %w", err)
	}

	jsonStr := extractJSON(cliResult.Output)
	if jsonStr == "" {
		return nil, fmt.Errorf("no JSON in strategic analysis output")
	}

	var analysis StrategicAnalysis
	if err := json.Unmarshal([]byte(jsonStr), &analysis); err != nil {
		return nil, fmt.Errorf("failed to parse strategic analysis: %w", err)
	}

	logger.Info(RemoraPrefix+" Strategic analysis complete", "Priorities", len(analysis.Priorities), "Risks", len(analysis.Risks))
	return &analysis, nil
}

// GenerateMorningBriefingActivity writes a morning_briefing.md to the project work dir.
func (a *Activities) GenerateMorningBriefingActivity(ctx context.Context, req StrategicGroomRequest, analysis *StrategicAnalysis) (*MorningBriefing, error) {
	logger := activity.GetLogger(ctx)
	today := time.Now().Format("2006-01-02")

	// Get recent lessons
	var recentLessons []Lesson
	if a.Store != nil {
		stored, storedErr := a.Store.GetRecentLessons(req.Project, 5)
		if storedErr != nil {
			logger.Warn(RemoraPrefix+" Failed to get recent lessons for briefing", "error", storedErr)
		}
		for i := range stored {
			recentLessons = append(recentLessons, Lesson{
				TaskID:   stored[i].BeadID,
				Category: stored[i].Category,
				Summary:  stored[i].Summary,
			})
		}
	}

	briefing := &MorningBriefing{
		Date:          today,
		Project:       req.Project,
		TopPriorities: analysis.Priorities,
		Risks:         analysis.Risks,
		RecentLessons: recentLessons,
	}

	// Render markdown
	var md strings.Builder
	md.WriteString(fmt.Sprintf("# Morning Briefing: %s\n\n", today))
	md.WriteString(fmt.Sprintf("**Project**: %s\n\n", req.Project))

	md.WriteString("## Top Priorities\n\n")
	urgencyMarker := map[string]string{"critical": " [!!!]", "high": " [!!]", "medium": " [!]", "low": ""}
	for i, p := range analysis.Priorities {
		marker := urgencyMarker[p.Urgency]
		beadRef := ""
		if p.TaskID != "" {
			beadRef = fmt.Sprintf(" (`%s`)", p.TaskID)
		}
		md.WriteString(fmt.Sprintf("%d. **%s**%s%s\n   %s\n\n", i+1, p.Title, beadRef, marker, p.Rationale))
	}

	if len(analysis.Risks) > 0 {
		md.WriteString("## Risks\n\n")
		for _, r := range analysis.Risks {
			md.WriteString(fmt.Sprintf("- %s\n", r))
		}
		md.WriteString("\n")
	}

	if len(recentLessons) > 0 {
		md.WriteString("## Recent Lessons Learned\n\n")
		for i := range recentLessons {
			md.WriteString(fmt.Sprintf("- [%s] %s (from %s)\n", recentLessons[i].Category, recentLessons[i].Summary, recentLessons[i].TaskID))
		}
		md.WriteString("\n")
	}

	if len(analysis.Observations) > 0 {
		md.WriteString("## Observations\n\n")
		for _, o := range analysis.Observations {
			md.WriteString(fmt.Sprintf("- %s\n", o))
		}
	}

	briefing.Markdown = md.String()

	// Write to work dir morning_briefing.md
	briefingPath := filepath.Join(req.WorkDir, "morning_briefing.md")
	if err := os.WriteFile(briefingPath, []byte(briefing.Markdown), 0o644); err != nil {
		logger.Error(RemoraPrefix+" Failed to write morning briefing", "path", briefingPath, "error", err)
	} else {
		logger.Info(RemoraPrefix+" Morning briefing written", "path", briefingPath)
	}

	return briefing, nil
}
