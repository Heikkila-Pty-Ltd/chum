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

// MutateTasksActivity runs after a morsel lands. Instead of asking an LLM for
// vague backlog opinions, it:
//  1. Finds open tasks related to the landed morsel (file-path + text matching)
//  2. For each hit, asks a cheap LLM: "how did this landed task affect this item?"
//  3. Applies concrete mutations (update_notes, close) based on LLM verdict
//
// Mutations are capped at 10 per cycle. LLM calls are capped at 10 hits.
func (a *Activities) MutateTasksActivity(ctx context.Context, req TacticalGroomRequest) (*GroomResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(RemoraPrefix+" Tactical groom: searching for related tasks",
		"TaskID", req.TaskID, "Project", req.Project, "FilesChanged", len(req.FilesChanged))

	if a.DAG == nil {
		logger.Warn(RemoraPrefix + " No DAG configured, skipping grooming")
		return &GroomResult{}, nil
	}

	// Get all open tasks for this project.
	allTasks, err := a.DAG.ListTasks(ctx, req.Project)
	if err != nil {
		logger.Warn(RemoraPrefix+" Can't list tasks, skipping grooming", "error", err)
		return &GroomResult{}, nil
	}

	// Get detail of the completed task for context.
	completedTask, showErr := a.DAG.GetTask(ctx, req.TaskID)
	if showErr != nil {
		logger.Warn(RemoraPrefix+" Can't find completed task", "task", req.TaskID, "error", showErr)
	}

	completedTitle := req.TaskTitle
	if completedTitle == "" && showErr == nil {
		completedTitle = completedTask.Title
	}

	// Step 1: Find related open tasks (deterministic, no LLM).
	related := findRelatedTasks(req, allTasks, completedTitle)
	if len(related) == 0 {
		logger.Info(RemoraPrefix+" No related tasks found, skipping LLM checks", "OpenTasks", countOpenTasks(allTasks))
		return &GroomResult{}, nil
	}

	logger.Info(RemoraPrefix+" Found related tasks", "Hits", len(related))

	// Step 2: Per-hit LLM impact check (cheap, ~500 tokens each).
	agent := ResolveTierAgent(a.Tiers, req.Tier)
	result := &GroomResult{}

	filesChangedStr := strings.Join(req.FilesChanged, ", ")
	if len(filesChangedStr) > 500 {
		filesChangedStr = filesChangedStr[:500] + "..."
	}

	for i := range related {
		hit := &related[i]
		prompt := fmt.Sprintf(`A task just completed in project "%s".

COMPLETED TASK: "%s" (ID: %s)
Files changed: %s
Summary: %s

OPEN BACKLOG ITEM: "%s" (ID: %s)
Description: %s
Acceptance criteria: %s

How does the completed task affect this backlog item?
Consider: Is work now partially done? Is it obsolete? Does context need updating?

Respond with ONLY a JSON object:
{"action": "update_notes|close|unchanged", "reason": "one-line explanation", "updated_notes": "context to append (only for update_notes)"}`,
			req.Project,
			completedTitle, req.TaskID,
			filesChangedStr,
			truncate(req.DiffSummary, 300),
			hit.Title, hit.ID,
			truncate(hit.Description, 400),
			truncate(hit.Acceptance, 200),
		)

		cliResult, runErr := runAgent(ctx, agent, prompt, req.WorkDir)
		if runErr != nil {
			logger.Warn(RemoraPrefix+" LLM check failed for task", "task", hit.ID, "error", runErr)
			continue
		}

		jsonStr := extractJSON(cliResult.Output)
		if jsonStr == "" {
			logger.Warn(RemoraPrefix+" No JSON in LLM response for task", "task", hit.ID)
			continue
		}

		var verdict struct {
			Action       string `json:"action"`
			Reason       string `json:"reason"`
			UpdatedNotes string `json:"updated_notes"`
		}
		if parseErr := json.Unmarshal([]byte(jsonStr), &verdict); parseErr != nil {
			logger.Warn(RemoraPrefix+" Failed to parse verdict JSON", "task", hit.ID, "error", parseErr)
			continue
		}

		switch verdict.Action {
		case "update_notes":
			note := fmt.Sprintf("[remora] After %s completed: %s", req.TaskID, verdict.Reason)
			if verdict.UpdatedNotes != "" {
				note = fmt.Sprintf("[remora] After %s completed: %s", req.TaskID, verdict.UpdatedNotes)
			}
			if err := a.applyMutation(ctx, req.Project, MorselMutation{
				TaskID: hit.ID,
				Action: "update_notes",
				Notes:  note,
			}); err != nil {
				result.MutationsFailed++
				result.Details = append(result.Details, fmt.Sprintf("FAILED update_notes on %s: %v", hit.ID, err))
			} else {
				result.MutationsApplied++
				result.Details = append(result.Details, fmt.Sprintf("OK update_notes on %s: %s", hit.ID, verdict.Reason))
				logger.Info(RemoraPrefix+" Updated task notes", "task", hit.ID, "reason", verdict.Reason)
			}

		case "close":
			if err := a.applyMutation(ctx, req.Project, MorselMutation{
				TaskID: hit.ID,
				Action: "close",
				Reason: fmt.Sprintf("[remora] Closed after %s completed: %s", req.TaskID, verdict.Reason),
			}); err != nil {
				result.MutationsFailed++
				result.Details = append(result.Details, fmt.Sprintf("FAILED close on %s: %v", hit.ID, err))
			} else {
				result.MutationsApplied++
				result.Details = append(result.Details, fmt.Sprintf("OK close on %s: %s", hit.ID, verdict.Reason))
				logger.Info(RemoraPrefix+" Closed obsolete task", "task", hit.ID, "reason", verdict.Reason)
			}

		case "unchanged":
			logger.Debug(RemoraPrefix+" Task unchanged", "task", hit.ID, "reason", verdict.Reason)

		default:
			logger.Warn(RemoraPrefix+" Unknown action from LLM", "task", hit.ID, "action", verdict.Action)
		}
	}

	// Step 3: Record health event for observability.
	if a.Store != nil && (result.MutationsApplied > 0 || result.MutationsFailed > 0) {
		details := fmt.Sprintf("After %s landed: checked %d related tasks, applied %d mutations, %d failed. %s",
			req.TaskID, len(related), result.MutationsApplied, result.MutationsFailed,
			strings.Join(result.Details, "; "))
		if recErr := a.Store.RecordHealthEvent("remora_groom", details); recErr != nil {
			logger.Warn(RemoraPrefix+" Failed to record health event", "error", recErr)
		}
	}

	logger.Info(RemoraPrefix+" Tactical groom complete",
		"Related", len(related), "Applied", result.MutationsApplied, "Failed", result.MutationsFailed)
	return result, nil
}

// findRelatedTasks returns open tasks that are likely affected by the landed morsel.
// Uses file-path overlap and text matching — no LLM, purely deterministic.
// Returns at most 10 hits, sorted by relevance score.
func findRelatedTasks(req TacticalGroomRequest, allTasks []graph.Task, completedTitle string) []graph.Task {
	type scored struct {
		task  graph.Task
		score int
	}
	var candidates []scored

	completedTitleLower := strings.ToLower(completedTitle)
	// Extract meaningful words from the completed task title (3+ chars)
	titleWords := extractKeywords(completedTitleLower)

	for i := range allTasks {
		t := &allTasks[i]
		// Skip non-open, container types, and the completed task itself.
		if t.Status != "open" && t.Status != "ready" {
			continue
		}
		if t.Type == "epic" || t.Type == "whale" {
			continue
		}
		if t.ID == req.TaskID {
			continue
		}

		score := 0

		// File-path matching: check if the task mentions any changed file.
		taskText := strings.ToLower(t.Description + " " + t.Acceptance + " " + t.Design + " " + t.Notes)
		for _, file := range req.FilesChanged {
			if taskMentionsFile(taskText, file) {
				score += 3 // strong signal
			}
		}

		// Text matching: check if title keywords appear in the task.
		for _, word := range titleWords {
			if strings.Contains(taskText, word) {
				score++ // weak signal, but compound hits add up
			}
		}

		// Check if the task title contains words from the completed title.
		hitTitleLower := strings.ToLower(t.Title)
		for _, word := range titleWords {
			if strings.Contains(hitTitleLower, word) {
				score += 2 // moderate signal — titles overlap
			}
		}

		// Dependency match: task depends on or is downstream of the completed task.
		for _, dep := range t.DependsOn {
			if dep == req.TaskID {
				score += 5 // very strong signal
			}
		}

		if score > 0 {
			candidates = append(candidates, scored{task: *t, score: score})
		}
	}

	// Sort by score descending, cap at 10.
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].score > candidates[i].score {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}

	limit := 10
	if len(candidates) < limit {
		limit = len(candidates)
	}

	result := make([]graph.Task, limit)
	for i := 0; i < limit; i++ {
		result[i] = candidates[i].task
	}
	return result
}

// taskMentionsFile checks if the task text references a file path.
// Matches on basename (e.g. "store.go") or relative path fragments.
func taskMentionsFile(taskText, filePath string) bool {
	filePath = strings.ToLower(filePath)
	// Check full path
	if strings.Contains(taskText, filePath) {
		return true
	}
	// Check basename (e.g. "store.go")
	parts := strings.Split(filePath, "/")
	if len(parts) > 0 {
		basename := parts[len(parts)-1]
		if len(basename) > 3 && strings.Contains(taskText, basename) {
			return true
		}
	}
	// Check parent/basename (e.g. "store/store.go")
	if len(parts) > 1 {
		short := strings.Join(parts[len(parts)-2:], "/")
		if strings.Contains(taskText, short) {
			return true
		}
	}
	return false
}

// extractKeywords returns meaningful lowercase words (3+ chars) from text.
// Filters out common stop words.
func extractKeywords(text string) []string {
	stopWords := map[string]bool{
		"the": true, "and": true, "for": true, "with": true, "from": true,
		"that": true, "this": true, "will": true, "into": true, "when": true,
		"add": true, "fix": true, "update": true, "implement": true,
	}
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_')
	})
	var keywords []string
	seen := make(map[string]bool)
	for _, w := range words {
		if len(w) >= 3 && !stopWords[w] && !seen[w] {
			keywords = append(keywords, w)
			seen[w] = true
		}
	}
	return keywords
}

// ApplyStrategicMutationsActivity applies pre-normalized strategic mutations
// directly without re-invoking the LLM. This is the correct path for mutations
// produced by StrategicAnalysisActivity + normalizeStrategicMutations.
func (a *Activities) ApplyStrategicMutationsActivity(ctx context.Context, project string, mutations []MorselMutation) (*GroomResult, error) {
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

// applyMutation executes a single MorselMutation against the DAG.
func (a *Activities) applyMutation(ctx context.Context, project string, m MorselMutation) error {
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

func isCreateMutationActionable(m MorselMutation) bool {
	return strings.TrimSpace(m.Title) != "" &&
		strings.TrimSpace(m.Description) != "" &&
		strings.TrimSpace(m.Acceptance) != "" &&
		strings.TrimSpace(m.Design) != "" &&
		m.EstimateMinutes > 0
}

func isStrategicMutation(m MorselMutation) bool {
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

// GetMorselStateSummaryActivity returns a compressed text summary of the open backlog.
func (a *Activities) GetMorselStateSummaryActivity(ctx context.Context, req StrategicGroomRequest) (string, error) {
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
				lb.WriteString(fmt.Sprintf("- [%s] %s (task: %s)\n", lessons[i].Category, lessons[i].Summary, lessons[i].MorselID))
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

	agent := ResolveTierAgent(a.Tiers, req.Tier)
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
				TaskID:   stored[i].MorselID,
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
		morselRef := ""
		if p.TaskID != "" {
			morselRef = fmt.Sprintf(" (`%s`)", p.TaskID)
		}
		md.WriteString(fmt.Sprintf("%d. **%s**%s%s\n   %s\n\n", i+1, p.Title, morselRef, marker, p.Rationale))
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
