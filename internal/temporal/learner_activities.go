package temporal

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"
)

// sanitizeForFilename converts a summary to a safe filename component.
func sanitizeForFilename(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		if r == ' ' || r == '_' {
			return '-'
		}
		return -1
	}, s)
	if len(s) > 40 {
		s = s[:40]
	}
	return strings.Trim(s, "-")
}

// ExtractLessonsActivity uses a fast LLM to analyze the completed morsel's diff,
// DoD results, and review feedback to extract reusable lessons.
func (a *Activities) ExtractLessonsActivity(ctx context.Context, req LearnerRequest) ([]Lesson, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(OctopusPrefix+" Extracting lessons", "TaskID", req.TaskID, "Tier", req.Tier)

	// Build context from the morsel's journey
	var contextParts []string
	if req.DiffSummary != "" {
		contextParts = append(contextParts, "DIFF:\n"+truncate(req.DiffSummary, 4000))
	}
	if req.DoDFailures != "" {
		contextParts = append(contextParts, "DOD FAILURES:\n"+req.DoDFailures)
	}
	if len(req.FilesChanged) > 0 {
		contextParts = append(contextParts, "FILES CHANGED:\n"+strings.Join(req.FilesChanged, "\n"))
	}
	if len(req.PreviousErrors) > 0 {
		contextParts = append(contextParts, "REVIEW/HANDOFF JOURNEY:\n"+strings.Join(req.PreviousErrors, "\n"))
	}

	// Query existing lessons to avoid duplication
	var existingContext string
	if a.Store != nil && len(req.FilesChanged) > 0 {
		existing, existingErr := a.Store.SearchLessonsByFilePath(req.FilesChanged, 5)
		if existingErr != nil {
			logger.Warn(OctopusPrefix+" Failed to search existing lessons", "error", existingErr)
		} else if len(existing) > 0 {
			var summaries []string
			for i := range existing {
				summaries = append(summaries, fmt.Sprintf("- [%s] %s", existing[i].Category, existing[i].Summary))
			}
			existingContext = "EXISTING LESSONS (do NOT duplicate):\n" + strings.Join(summaries, "\n")
		}
	}

	// Build failure-aware extraction prompt
	var modeInstructions string
	if req.DoDPassed {
		modeInstructions = `This morsel SUCCEEDED. Extract reusable patterns:
- What approach worked well and should be replicated?
- What coding patterns or architectural decisions were effective?
- What conventions should be documented for future tasks?`
	} else {
		modeInstructions = `This morsel FAILED. Extract ANTIBODIES — defensive knowledge for successor tasks:
- What was the ROOT CAUSE of the failure?
- Which files or code areas are RISKY and need extra care?
- What defensive patterns should successor tasks carry?
- What errors should the next attempt anticipate and guard against?
- Was the failure a timeout, test failure, lint error, or logic bug?
Prioritize actionable antibodies over general observations.`
	}

	prompt := fmt.Sprintf(`You are a code quality analyst performing evolutionary analysis. A morsel (work item) just %s.

MORSEL: %s (project: %s, agent: %s)
DOD PASSED: %v

%s

%s

%s

Extract 1-5 lessons. Each lesson must be:
- Specific and actionable (not generic advice)
- Tied to concrete file paths or patterns when possible
- Categorized: "pattern" (good practice), "antipattern" (mistake to avoid), "rule" (enforceable), "insight" (observation)

Respond with ONLY a JSON array:
[{
  "category": "pattern|antipattern|rule|insight",
  "summary": "one-line summary",
  "detail": "full explanation with specific code/file references",
  "file_paths": ["affected/file1.go"],
  "labels": ["error-handling", "testing"]
}]

If there are no meaningful lessons, return an empty array [].`,
		map[bool]string{true: "completed successfully", false: "FAILED — extract antibodies"}[req.DoDPassed],
		req.TaskID, req.Project, req.Agent, req.DoDPassed,
		modeInstructions,
		strings.Join(contextParts, "\n\n"),
		existingContext,
	)

	agent := ResolveTierAgent(a.Tiers, req.Tier)
	cliResult, err := runAgent(ctx, agent, prompt, req.WorkDir)
	if err != nil {
		logger.Warn(OctopusPrefix+" Lesson extraction LLM failed", "error", err)
		return nil, nil // non-fatal
	}

	jsonStr := extractJSONArray(cliResult.Output)
	if jsonStr == "" || jsonStr == "[]" {
		return nil, nil
	}

	// Sanitize LLM JSON output — LLMs frequently emit raw backslashes
	// (e.g. file paths like "internal\temporal") that break json.Unmarshal.
	jsonStr = sanitizeLLMJSON(jsonStr)

	var lessons []Lesson
	if err := json.Unmarshal([]byte(jsonStr), &lessons); err != nil {
		logger.Warn(OctopusPrefix+" Primary JSON parse failed, attempting repair", "error", err)

		// Strategy 1: Close unclosed braces/brackets (truncated output)
		repaired := repairTruncatedJSONArray(jsonStr)
		if repaired != jsonStr {
			if err2 := json.Unmarshal([]byte(repaired), &lessons); err2 == nil {
				logger.Info(OctopusPrefix+" Recovered lessons from truncated JSON", "Count", len(lessons))
				goto stamped
			}
		}

		// Strategy 2: Extract just the first complete JSON object from the array
		if first := extractFirstCompleteJSONObject(jsonStr); first != "" {
			wrapped := "[" + first + "]"
			if err3 := json.Unmarshal([]byte(wrapped), &lessons); err3 == nil {
				logger.Info(OctopusPrefix+" Recovered 1 lesson from partial JSON array")
				goto stamped
			}
		}

		logger.Warn(OctopusPrefix+" All JSON repair strategies failed", "error", err)
		return nil, nil
	}
stamped:

	// Stamp morsel/project on each lesson
	for i := range lessons {
		lessons[i].TaskID = req.TaskID
		lessons[i].Project = req.Project
	}

	logger.Info(OctopusPrefix+" Lessons extracted", "Count", len(lessons))
	return lessons, nil
}

// StoreLessonActivity persists lessons to SQLite FTS5.
// Idempotent: checks for duplicate task_id + summary before inserting.
func (a *Activities) StoreLessonActivity(ctx context.Context, lessons []Lesson) error {
	logger := activity.GetLogger(ctx)
	if a.Store == nil {
		logger.Warn(OctopusPrefix + " No store configured, skipping lesson storage")
		return nil
	}

	stored := 0
	for i := range lessons {
		lesson := &lessons[i]
		// Idempotency: check if this exact lesson already exists
		existing, existingErr := a.Store.GetLessonsByMorsel(lesson.TaskID)
		if existingErr != nil {
			logger.Warn(OctopusPrefix+" Failed to check existing lessons", "morsel", lesson.TaskID, "error", existingErr)
		}
		isDuplicate := false
		for j := range existing {
			if existing[j].Summary == lesson.Summary {
				isDuplicate = true
				break
			}
		}
		if isDuplicate {
			continue
		}

		_, err := a.Store.StoreLesson(
			lesson.TaskID, lesson.Project, lesson.Category,
			lesson.Summary, lesson.Detail,
			lesson.FilePaths, lesson.Labels,
			lesson.SemgrepRuleID,
		)
		if err != nil {
			logger.Error(OctopusPrefix+" Failed to store lesson", "error", err)
			continue // best-effort
		}
		stored++
	}

	logger.Info(OctopusPrefix+" Lessons stored", "Stored", stored, "Total", len(lessons))
	return nil
}

// GenerateSemgrepRuleActivity examines lessons of category "rule" or "antipattern"
// and generates Semgrep YAML rule files. Writes to .semgrep/ directory.
// The factory grows its own immune system.
func (a *Activities) GenerateSemgrepRuleActivity(ctx context.Context, req LearnerRequest, lessons []Lesson) ([]SemgrepRule, error) {
	logger := activity.GetLogger(ctx)

	// Filter to enforceable lessons
	var enforceable []Lesson
	for i := range lessons {
		if lessons[i].Category == "rule" || lessons[i].Category == "antipattern" {
			enforceable = append(enforceable, lessons[i])
		}
	}
	if len(enforceable) == 0 {
		return nil, nil
	}

	rules := make([]SemgrepRule, 0, len(enforceable))
	for i := range enforceable {
		lesson := &enforceable[i]
		prompt := fmt.Sprintf(`You are a Semgrep rule author. Generate a Semgrep rule for this code pattern:

LESSON: %s
DETAIL: %s
FILES: %s
LANGUAGE: go

Generate a Semgrep YAML rule. The rule must:
1. Use pattern or pattern-either syntax
2. Have a clear, actionable message
3. Target Go code specifically
4. Have severity "WARNING" for antipatterns, "ERROR" for rules

Respond with ONLY the raw YAML content (no markdown fences):
rules:
  - id: chum-<descriptive-slug>
    patterns:
      - pattern: ...
    message: |
      ...
    languages: [go]
    severity: WARNING`,
			lesson.Summary, truncate(lesson.Detail, 1000),
			strings.Join(lesson.FilePaths, ", "),
		)

		cliResult, err := runAgent(ctx, ResolveTierAgent(a.Tiers, req.Tier), prompt, req.WorkDir)
		if err != nil {
			logger.Warn(OctopusPrefix+" Semgrep rule generation failed", "lesson", lesson.Summary, "error", err)
			continue
		}

		output := strings.TrimSpace(cliResult.Output)
		if !strings.Contains(output, "rules:") {
			logger.Warn(OctopusPrefix+" Generated output doesn't look like Semgrep YAML", "lesson", lesson.Summary)
			continue
		}

		// Strip markdown fences if present
		if strings.HasPrefix(output, "```") {
			lines := strings.Split(output, "\n")
			if len(lines) > 2 {
				output = strings.Join(lines[1:len(lines)-1], "\n")
			}
		}

		ruleID := fmt.Sprintf("chum-%s-%d", sanitizeForFilename(lesson.Summary), time.Now().Unix())
		fileName := ruleID + ".yaml"

		// Write to .semgrep/ directory
		semgrepDir := filepath.Join(req.WorkDir, ".semgrep")
		if mkdirErr := os.MkdirAll(semgrepDir, 0o755); mkdirErr != nil {
			logger.Error(OctopusPrefix+" Failed to create semgrep dir", "path", semgrepDir, "error", mkdirErr)
			continue
		}
		rulePath := filepath.Join(semgrepDir, fileName)

		if err := os.WriteFile(rulePath, []byte(output), 0o644); err != nil {
			logger.Error(OctopusPrefix+" Failed to write semgrep rule", "path", rulePath, "error", err)
			continue
		}

		rules = append(rules, SemgrepRule{
			RuleID:   ruleID,
			FileName: fileName,
			Content:  output,
			Category: lesson.Category,
		})

		logger.Info(OctopusPrefix+" Semgrep rule generated", "RuleID", ruleID, "Path", rulePath)
	}

	return rules, nil
}


// SynthesizeCLAUDEmdActivity reads ALL accumulated lessons from the knowledge base,
// deduplicates and groups by category, and writes a CLAUDE.md file to the project root.
// Both Claude CLI and Codex CLI auto-read CLAUDE.md, closing the long-term memory loop.
//
// This is the "institutional memory" — not just what failed last time, but what the
// project has learned over hundreds of dispatches.
func (a *Activities) SynthesizeCLAUDEmdActivity(ctx context.Context, req LearnerRequest) error {
	logger := activity.GetLogger(ctx)

	if a.Store == nil {
		logger.Warn(OctopusPrefix + " No store configured, skipping CLAUDE.md synthesis")
		return nil
	}

	// Read ALL lessons for this project (not just recent)
	allLessons, err := a.Store.GetRecentLessons(req.Project, 100)
	if err != nil {
		logger.Warn(OctopusPrefix+" Failed to read lessons for CLAUDE.md", "error", err)
		return nil // non-fatal
	}
	if len(allLessons) == 0 {
		logger.Info(OctopusPrefix + " No lessons to synthesize, skipping CLAUDE.md")
		return nil
	}

	// --- Deduplicate and count frequency ---
	type lessonKey struct {
		Category string
		Summary  string
	}
	type weightedLesson struct {
		Category string
		Summary  string
		Detail   string
		Count    int
	}

	freq := make(map[lessonKey]*weightedLesson)
	for i := range allLessons {
		key := lessonKey{allLessons[i].Category, allLessons[i].Summary}
		if wl, ok := freq[key]; ok {
			wl.Count++
		} else {
			freq[key] = &weightedLesson{
				Category: allLessons[i].Category,
				Summary:  allLessons[i].Summary,
				Detail:   allLessons[i].Detail,
				Count:    1,
			}
		}
	}

	// Sort by frequency (most common first)
	sorted := make([]*weightedLesson, 0, len(freq))
	for _, wl := range freq {
		sorted = append(sorted, wl)
	}
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].Count > sorted[i].Count {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	// --- Read recent DoD failure patterns ---
	var dodPatterns []string
	rows, err := a.Store.DB().Query(`
		SELECT failures, COUNT(*) as cnt FROM dod_results
		WHERE project = ? AND passed = 0 AND failures != ''
		GROUP BY failures ORDER BY cnt DESC LIMIT 5`, req.Project)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var f string
			var cnt int
			if rows.Scan(&f, &cnt) == nil {
				dodPatterns = append(dodPatterns, fmt.Sprintf("%s (×%d)", f, cnt))
			}
		}
	}

	// --- Build CLAUDE.md ---
	var md strings.Builder
	md.WriteString("# Project Rules — Auto-generated by CHUM Learner\n\n")
	md.WriteString("> This file is continuously updated after each task completion.\n")
	md.WriteString("> It captures accumulated project wisdom from automated code generation and review.\n")
	md.WriteString(fmt.Sprintf("> **%d lessons** from **%d observations** across this project.\n\n", len(sorted), len(allLessons)))

	// Group by category with priority ordering
	categoryOrder := []string{"rule", "antipattern", "pattern", "insight"}
	categoryHeaders := map[string]string{
		"rule":        "## Rules (Enforced)\n\nThese MUST be followed. Violations will cause DoD failure.\n\n",
		"antipattern": "## Anti-patterns (Avoid)\n\nThese patterns have caused failures before.\n\n",
		"pattern":     "## Good Patterns (Follow)\n\nThese approaches have been verified to work.\n\n",
		"insight":     "## Insights\n\nObservations from project history.\n\n",
	}

	for _, cat := range categoryOrder {
		var catLessons []*weightedLesson
		for _, wl := range sorted {
			if wl.Category == cat {
				catLessons = append(catLessons, wl)
			}
		}
		if len(catLessons) == 0 {
			continue
		}

		md.WriteString(categoryHeaders[cat])
		for _, wl := range catLessons {
			if wl.Count > 1 {
				md.WriteString(fmt.Sprintf("- **%s** (seen %d×)\n", wl.Summary, wl.Count))
			} else {
				md.WriteString(fmt.Sprintf("- %s\n", wl.Summary))
			}
		}
		md.WriteString("\n")
	}

	// DoD patterns section
	if len(dodPatterns) > 0 {
		md.WriteString("## Common DoD Failures\n\n")
		md.WriteString("These checks frequently fail. Address them proactively:\n\n")
		for _, p := range dodPatterns {
			md.WriteString(fmt.Sprintf("- %s\n", p))
		}
		md.WriteString("\n")
	}

	// DoD command reminder
	md.WriteString("## Definition of Done\n\n")
	md.WriteString("Every change must pass: `go build ./... && go vet ./... && golangci-lint run --timeout=5m`\n\n")
	md.WriteString("Run these locally before considering the task complete.\n")

	// Write to project root
	claudePath := filepath.Join(req.WorkDir, "CLAUDE.md")
	if err := os.WriteFile(claudePath, []byte(md.String()), 0o644); err != nil {
		logger.Error(OctopusPrefix+" Failed to write CLAUDE.md", "path", claudePath, "error", err)
		return nil // non-fatal
	}

	logger.Info(OctopusPrefix+" CLAUDE.md synthesized",
		"Path", claudePath,
		"Lessons", len(sorted),
		"Observations", len(allLessons),
		"DoDPatterns", len(dodPatterns),
	)
	return nil
}

// CalcifyPatternActivity detects whether the just-completed morsel's type has
// been solved successfully enough times to warrant a deterministic replacement
// script. If so, it generates a .shadow script via a premium LLM.
//
// This is Step 5 of the learner pipeline — the stochastic→deterministic migration.
// The LLM is expensive and non-deterministic. Every repeated pattern that can be
// replaced by a script is pure margin.
func (a *Activities) CalcifyPatternActivity(ctx context.Context, req LearnerRequest) (bool, error) {
	logger := activity.GetLogger(ctx)

	// Only calcify successful completions
	if !req.DoDPassed {
		return false, nil
	}
	if a.Store == nil {
		return false, nil
	}

	// Derive morsel type from task ID prefix (e.g., "parse-lead-form-001" → "parse")
	morselType := req.TaskID
	if idx := strings.Index(morselType, "-"); idx > 0 {
		morselType = morselType[:idx]
	}

	// Skip if a script already exists for this type (active or shadow)
	active, err := a.Store.GetActiveScriptForType(morselType)
	if err != nil {
		logger.Warn(OctopusPrefix+" Failed to read active calcified script", "type", morselType, "error", err)
	} else if active != nil {
		logger.Info(OctopusPrefix+" Active script already exists, skipping calcification", "type", morselType)
		return false, nil
	}

	shadow, err := a.Store.GetShadowScriptForType(morselType)
	if err != nil {
		logger.Warn(OctopusPrefix+" Failed to read shadow calcified script", "type", morselType, "error", err)
	} else if shadow != nil {
		logger.Info(OctopusPrefix+" Shadow script already exists, skipping calcification", "type", morselType)
		return false, nil
	}

	// Count consecutive successes for this morsel type
	streak, err := a.Store.GetConsecutiveSuccessfulDispatches(morselType, req.Project)
	if err != nil {
		logger.Warn(OctopusPrefix+" Failed to count successes", "type", morselType, "error", err)
		return false, nil
	}

	// Apply risk-weighted threshold
	// Fetch labels for this morsel from the most recent dispatch
	var labels string
	scanErr := a.Store.DB().QueryRowContext(ctx,
		`SELECT labels FROM dispatches WHERE morsel_id LIKE ? AND project = ? ORDER BY id DESC LIMIT 1`,
		morselType+"%", req.Project,
	).Scan(&labels)
	if scanErr != nil && scanErr != sql.ErrNoRows {
		logger.Warn(OctopusPrefix+" Failed to fetch labels for calcification", "type", morselType, "error", scanErr)
	}

	var labelList []string
	if labels != "" {
		labelList = strings.Split(labels, ",")
	}

	threshold := a.calcifierThreshold(labelList)
	if streak < threshold {
		logger.Info(OctopusPrefix+" Not enough consecutive successes for calcification",
			"type", morselType, "streak", streak, "threshold", threshold)
		return false, nil
	}

	logger.Info(OctopusPrefix+" Calcification threshold reached!",
		"type", morselType, "streak", streak, "threshold", threshold)

	// Gather recent successful prompts/outputs for context
	rows, err := a.Store.DB().QueryContext(ctx,
		`SELECT prompt FROM dispatches
		 WHERE morsel_id LIKE ? AND project = ? AND status = 'completed'
		 ORDER BY id DESC LIMIT 10`,
		morselType+"%", req.Project,
	)
	if err != nil {
		return false, fmt.Errorf("gather dispatch history: %w", err)
	}
	defer rows.Close()

	var prompts []string
	for rows.Next() {
		var p string
		scanErr := rows.Scan(&p)
		if scanErr != nil {
			logger.Warn(OctopusPrefix+" Failed to scan dispatch prompt", "type", morselType, "error", scanErr)
			continue
		}
		if p != "" {
			prompts = append(prompts, p)
		}
	}
	if err = rows.Err(); err != nil {
		return false, fmt.Errorf("scan dispatch history: %w", err)
	}
	if len(prompts) == 0 {
		return false, nil
	}

	// Build compilation prompt and dispatch to premium model
	compilationPrompt := buildCompilationPrompt(morselType, prompts)
	agent := ResolveTierAgent(a.Tiers, "premium")
	cliResult, err := runAgent(ctx, agent, compilationPrompt, req.WorkDir)
	if err != nil {
		logger.Warn(OctopusPrefix+" Calcification LLM failed", "error", err)
		return false, nil // non-fatal
	}

	// Extract and validate script content
	scriptContent := extractScriptContent(cliResult.Output)
	if scriptContent == "" {
		logger.Warn(OctopusPrefix + " LLM did not produce a valid script")
		return false, nil
	}

	// Write the shadow script
	_, ext := detectScriptLanguage(scriptContent)
	calcifiedDir := filepath.Join(req.WorkDir, ".cortex", "calcified")
	err = os.MkdirAll(calcifiedDir, 0o755)
	if err != nil {
		return false, fmt.Errorf("create calcified dir: %w", err)
	}

	scriptName := fmt.Sprintf("%s.%s.shadow", sanitizeForFilename(morselType), ext)
	scriptPath := filepath.Join(calcifiedDir, scriptName)

	err = os.WriteFile(scriptPath, []byte(scriptContent), 0o755)
	if err != nil {
		return false, fmt.Errorf("write shadow script: %w", err)
	}

	// Compute SHA-256 provenance hash
	hash, err := hashFile(scriptPath)
	if err != nil {
		return false, fmt.Errorf("hash script: %w", err)
	}

	// Record in store
	_, err = a.Store.RecordCalcifiedScript(morselType, req.Project, scriptPath, hash)
	if err != nil {
		return false, fmt.Errorf("record script: %w", err)
	}

	logger.Info(OctopusPrefix+" Pattern calcified into shadow script",
		"type", morselType, "path", scriptPath, "sha256", hash[:12])
	return true, nil
}

// CommitAndPushLearnerOutputsActivity commits and pushes the CLAUDE.md, .semgrep rules,
// and .cortex/calcified scripts to the base repository so the learning is persistent.
func (a *Activities) CommitAndPushLearnerOutputsActivity(ctx context.Context, workDir string, taskID string) error {
	logger := activity.GetLogger(ctx)
	logger.Info(OctopusPrefix+" Committing and pushing learner outputs", "WorkDir", workDir)

	// Add files generated by the learner
	addCmd := exec.CommandContext(ctx, "git", "add", "CLAUDE.md", ".semgrep/", ".cortex/calcified/")
	addCmd.Dir = workDir
	if err := addCmd.Run(); err != nil {
		logger.Warn(OctopusPrefix+" git add failed (no files or error)", "error", err)
		return nil // nothing to commit
	}

	// Check if there are actually staged changes
	statusCmd := exec.CommandContext(ctx, "git", "diff", "--staged", "--quiet")
	statusCmd.Dir = workDir
	if err := statusCmd.Run(); err == nil {
		// git diff --quiet returns 0 if NO changes
		logger.Info(OctopusPrefix + " No new learner outputs to commit")
		return nil
	}

	// Commit
	commitMsg := fmt.Sprintf("chore: Octopus learning updates from task %s\n\n- Updated CLAUDE.md memory\n- Updated .semgrep rules\n- Calcified patterns (if any)", taskID)
	commitCmd := exec.CommandContext(ctx, "git", "commit", "-m", commitMsg, "--no-verify")
	commitCmd.Dir = workDir
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit failed: %w\n%s", err, string(out))
	}

	// Push
	pushCmd := exec.CommandContext(ctx, "git", "push", "origin", "HEAD")
	pushCmd.Dir = workDir
	if out, err := pushCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git push origin HEAD failed: %w\n%s", err, string(out))
	}

	logger.Info(OctopusPrefix + " Learner outputs committed and pushed successfully")
	return nil
}

