// Package temporal implements the core CHUM Temporal workflows and activities.

package temporal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"

	astpkg "github.com/antigravity-dev/chum/internal/ast"
	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/git"
	"github.com/antigravity-dev/chum/internal/graph"
	"github.com/antigravity-dev/chum/internal/matrix"
	"github.com/antigravity-dev/chum/internal/store"
)

// Activities holds dependencies for Temporal activity methods.
type Activities struct {
	Store       *store.Store
	Tiers       config.Tiers
	CfgMgr      config.ConfigManager // hot-reloadable config for CLI dispatch
	DAG         *graph.DAG
	AST         *astpkg.Parser // tree-sitter Go parser for codebase context injection
	Sender      matrix.Sender  // Matrix notification sender (nil = disabled)
	DefaultRoom string         // Matrix room ID for standard notifications
	AdminRoom   string         // Matrix room ID for critical escalations (DM)
	TurtleRoom  string         // Matrix room for turtle deliberation (3-agent conversation)
}

// WorktreeDir returns the worktree directory path for a task/explosion pair.
// Uses os.TempDir() so paths are portable across systems with different $TMPDIR.
func WorktreeDir(taskID, explosionID string) string {
	if explosionID != "" {
		return filepath.Join(os.TempDir(), fmt.Sprintf("chum-wt-%s-%s", taskID, explosionID))
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("chum-wt-%s", taskID))
}

// StructuredPlanActivity generates a structured plan from a task prompt.
// The plan is gated — it must pass Validate() to enter the coding engine.
func (a *Activities) StructuredPlanActivity(ctx context.Context, req TaskRequest) (*StructuredPlan, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(SharkPrefix+" Generating structured plan", "Agent", req.Agent, "TaskID", req.TaskID)

	// Inject species genome — the accumulated evolutionary memory.
	// The genome IS the real product; the plan is just metabolism.
	var genomeContext string
	if a.Store != nil {
		species := classifySpecies(req.TaskID, req.Prompt, nil)
		if genome, err := a.Store.GetGenomeForPrompt(species); err == nil && genome != "" {
			genomeContext = "\n" + genome + "\n"
			logger.Info(SharkPrefix+" Genome injected into planning prompt", "Species", species)
		}
	}

	semgrepContext := loadSemgrepContext(req.WorkDir)
	if semgrepContext != "" {
		logger.Info(SharkPrefix + " Semgrep gates injected into planning prompt")
	}

	// Inject previous failures — the scent of death.
	// A new shark is born with the memory of the one that came before.
	var failureContext string
	if len(req.PreviousErrors) > 0 {
		var validErrors []string
		for _, errStr := range req.PreviousErrors {
			if strings.TrimSpace(errStr) != "" {
				validErrors = append(validErrors, errStr)
			}
		}
		if len(validErrors) > 0 {
			failureContext = "\nPREVIOUS FAILURES (avoid these mistakes):\n---\n" + strings.Join(validErrors, "\n---\n") + "\n"
			logger.Info(SharkPrefix+" Previous failures injected into planning prompt", "Count", len(validErrors))
		}
	}

	// Build AST codebase context — gives the planner structural awareness.
	codebaseContext := a.buildCodebaseContext(ctx, req.WorkDir)
	var codebaseSection string
	if codebaseContext != "" {
		codebaseSection = "\nCODEBASE STRUCTURE:\n" + codebaseContext + "\n"
		logger.Info(SharkPrefix+" Codebase context injected into planning prompt", "Bytes", len(codebaseContext))
	}

	prompt := fmt.Sprintf(`You are a senior engineering planner. Analyze this task and produce a structured execution plan.

TASK: %s
%s%s%s%s
OUTPUT FORMAT: You MUST respond with ONLY a JSON object (no markdown, no commentary) with this exact structure:
{
  "summary": "one-line summary of the task",
  "steps": [{"description": "what to do", "file": "which file", "rationale": "why"}],
  "files_to_modify": ["file1.go", "file2.go"],
  "acceptance_criteria": ["criterion 1", "criterion 2"],
  "estimated_complexity": "low|medium|high",
  "risk_assessment": "what could go wrong"
}

Be thorough. Planning space is cheap — implementation is expensive.`, req.Prompt, genomeContext, semgrepContext, failureContext, codebaseSection)

	cliResult, err := a.runAgent(ctx, req.Agent, prompt, req.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("plan generation failed: %w", err)
	}

	logger.Info(SharkPrefix+" Plan generation token usage",
		"InputTokens", cliResult.Tokens.InputTokens,
		"OutputTokens", cliResult.Tokens.OutputTokens,
		"CacheReadTokens", cliResult.Tokens.CacheReadTokens,
		"CacheCreationTokens", cliResult.Tokens.CacheCreationTokens,
		"CostUSD", cliResult.Tokens.CostUSD,
	)

	// Extract JSON from the output (agent might wrap it in markdown)
	jsonStr := extractJSON(cliResult.Output)
	if jsonStr == "" {
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("agent did not produce valid JSON plan. Output:\n%s", truncate(cliResult.Output, 500)),
			"PLAN_NO_JSON", nil)
	}

	plan, err := flexUnmarshalPlan(jsonStr)
	if err != nil {
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("failed to parse plan JSON: %v\nRaw: %s", err, truncate(jsonStr, 500)),
			"PLAN_PARSE_FAILED", err)
	}
	plan.TokenUsage = cliResult.Tokens

	// Gate: validate plan before it enters the coding engine
	if issues := plan.Validate(); len(issues) > 0 {
		logger.Warn(SharkPrefix+" Plan validation failed — orca killing shark",
			"TaskID", req.TaskID,
			"Agent", req.Agent,
			"RawJSON", truncate(jsonStr, 1000),
			"Issues", issues)
		// Non-retryable: the agent's JSON schema is wrong, retrying won't fix it.
		return nil, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("plan failed quality gate (orca kill):\n- %s\nRaw JSON: %s", strings.Join(issues, "\n- "), truncate(jsonStr, 500)),
			"PLAN_QUALITY_GATE", nil)
	}

	logger.Info(SharkPrefix+" Plan generated and validated",
		"Summary", plan.Summary,
		"Steps", len(plan.Steps),
		"Files", len(plan.FilesToModify),
		"Criteria", len(plan.AcceptanceCriteria),
	)

	return plan, nil
}

// ExecuteActivity runs the primary coding agent to implement the plan.
func (a *Activities) ExecuteActivity(ctx context.Context, plan StructuredPlan, req TaskRequest) (*ExecutionResult, error) {
	logger := activity.GetLogger(ctx)
	agent := req.Agent
	logger.Info(SharkPrefix+" Executing plan", "Agent", agent, "TaskID", req.TaskID)

	// Build a detailed execution prompt from the structured plan
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("TASK: %s\n\n", plan.Summary))
	sb.WriteString("PLAN:\n")
	for i, step := range plan.Steps {
		sb.WriteString(fmt.Sprintf("%d. [%s] %s\n   Rationale: %s\n", i+1, step.File, step.Description, step.Rationale))
	}
	sb.WriteString(fmt.Sprintf("\nFILES TO MODIFY: %s\n", strings.Join(plan.FilesToModify, ", ")))
	sb.WriteString("\nACCEPTANCE CRITERIA:\n")
	for _, c := range plan.AcceptanceCriteria {
		sb.WriteString(fmt.Sprintf("- %s\n", c))
	}

	// Inject targeted AST context — full source for files we're about to modify,
	// signatures-only for the rest. This gives the executing agent deep understanding
	// of the code it needs to change without overwhelming with the whole codebase.
	if targetedCtx := a.buildTargetedCodebaseContext(ctx, req.WorkDir, plan.FilesToModify); targetedCtx != "" {
		sb.WriteString("\nCODEBASE STRUCTURE:\n")
		sb.WriteString(targetedCtx)
		sb.WriteByte('\n')
	}

	if len(plan.PreviousErrors) > 0 {
		sb.WriteString(fmt.Sprintf("\nPREVIOUS ERRORS TO FIX:\n%s\n", strings.Join(plan.PreviousErrors, "\n")))
		sb.WriteString("\nCRITICAL: You are fixing a build/test failure or review rejection. Fix ONLY the specific errors reported. DO NOT restructure the code, do not engage in large refactors, and DO NOT create new files. Only modify existing files to make the errors pass.\n")
	}

	// --- Learner feedback loop: inject recent lessons and DoD patterns ---
	if a.Store != nil {
		// Recent project-level lessons (rules, patterns, antipatterns)
		lessons, err := a.Store.GetRecentLessons(req.Project, 5)
		if err == nil && len(lessons) > 0 {
			sb.WriteString("\nLESSONS FROM PAST FAILURES (apply these):\n")
			for i := range lessons {
				sb.WriteString(fmt.Sprintf("- [%s] %s\n", lessons[i].Category, lessons[i].Summary))
			}
		}

		// File-specific lessons (if plan has target files)
		if len(plan.FilesToModify) > 0 {
			fileLessons, searchErr := a.Store.SearchLessonsByFilePath(plan.FilesToModify, 3)
			if searchErr == nil && len(fileLessons) > 0 {
				sb.WriteString("\nFILE-SPECIFIC LESSONS (for files you're modifying):\n")
				for i := range fileLessons {
					sb.WriteString(fmt.Sprintf("- %s: %s\n", strings.Join(fileLessons[i].FilePaths, ", "), fileLessons[i].Summary))
				}
			}
		}

		// Recent DoD failures for this project (so the agent knows what checks will run)
		var recentFailures []string
		rows, err := a.Store.DB().Query(`
			SELECT DISTINCT failures FROM dod_results
			WHERE project = ? AND passed = 0 AND failures != ''
			ORDER BY checked_at DESC LIMIT 3`, req.Project)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var f string
				if rows.Scan(&f) == nil && f != "" {
					recentFailures = append(recentFailures, f)
				}
			}
		}
		if len(recentFailures) > 0 {
			sb.WriteString("\nRECENT DOD FAILURES IN THIS PROJECT (avoid repeating):\n")
			for _, f := range recentFailures {
				sb.WriteString(fmt.Sprintf("- %s\n", f))
			}
		}
	}

	sb.WriteString("\nImplement this plan now. Make all necessary code changes.")

	cliResult, err := a.runAgent(ctx, agent, sb.String(), req.WorkDir)

	// Auto-stage any untracked or modified files so the review agent can see them
	// and so DoD checks see the same committed state.
	addCmd := exec.CommandContext(ctx, "git", "add", ".")
	addCmd.Dir = req.WorkDir
	if addErr := addCmd.Run(); addErr != nil {
		logger.Warn(SharkPrefix+" Failed to auto-stage files", "error", addErr)
	}

	exitCode := 0
	if err != nil {
		exitCode = 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		// Don't fail the activity — we want to proceed to review even on non-zero exit
		logger.Warn(SharkPrefix+" Agent exited with error", "error", err)
	}

	logger.Info(SharkPrefix+" Execution token usage",
		"InputTokens", cliResult.Tokens.InputTokens,
		"OutputTokens", cliResult.Tokens.OutputTokens,
		"CostUSD", cliResult.Tokens.CostUSD,
	)

	return &ExecutionResult{
		ExitCode: exitCode,
		Output:   cliResult.Output,
		Agent:    agent,
		Tokens:   cliResult.Tokens,
	}, nil
}

// CodeReviewActivity runs a DIFFERENT agent to review the implementation.
// Claude reviews codex's work, codex reviews claude's. Cross-pollination catches blind spots.
func (a *Activities) CodeReviewActivity(ctx context.Context, plan StructuredPlan, execResult ExecutionResult, req TaskRequest) (*ReviewResult, error) {
	logger := activity.GetLogger(ctx)

	reviewer := req.Reviewer
	if reviewer == "" {
		reviewer = DefaultReviewer(execResult.Agent)
	}

	logger.Info(SharkPrefix+" Code review", "Reviewer", reviewer, "Author", execResult.Agent, "TaskID", req.TaskID)

	prompt := fmt.Sprintf(`You are a senior code reviewer. Another AI agent (%s) implemented the following plan.
Review their work against the acceptance criteria.

PLAN SUMMARY: %s

ACCEPTANCE CRITERIA:
%s

AGENT OUTPUT:
%s

Review the implementation. Respond with ONLY a JSON object:
{
  "approved": true/false,
  "issues": ["issue 1", "issue 2"],
  "suggestions": ["suggestion 1"]
}

Be rigorous. Quality enterprise-grade code only. Flag any: missing error handling, untested paths, race conditions, security issues.`,
		execResult.Agent,
		plan.Summary,
		formatCriteria(plan.AcceptanceCriteria),
		truncate(execResult.Output, 3000),
	)

	cliResult, err := a.runReviewAgent(ctx, reviewer, prompt, req.WorkDir)
	if err != nil {
		// Review failure is not fatal — log and approve with warning
		logger.Warn(SharkPrefix+" Review agent error, defaulting to approved with warning", "error", err)
		return &ReviewResult{
			Approved:      true,
			Issues:        []string{"Review agent failed: " + err.Error()},
			ReviewerAgent: reviewer,
			ReviewOutput:  cliResult.Output,
			Tokens:        cliResult.Tokens,
		}, nil
	}

	logger.Info(SharkPrefix+" Review token usage",
		"InputTokens", cliResult.Tokens.InputTokens,
		"OutputTokens", cliResult.Tokens.OutputTokens,
		"CostUSD", cliResult.Tokens.CostUSD,
	)

	jsonStr := extractJSON(cliResult.Output)
	if jsonStr == "" {
		// Can't parse review — approve with warning
		return &ReviewResult{
			Approved:      true,
			Issues:        []string{"Review output was not valid JSON"},
			ReviewerAgent: reviewer,
			ReviewOutput:  cliResult.Output,
			Tokens:        cliResult.Tokens,
		}, nil
	}

	result := parseReviewJSON(jsonStr, reviewer, cliResult)
	return &result, nil
}

// parseReviewJSON attempts to unmarshal review JSON. On failure, returns an
// approved review with a warning issue — graceful degradation so review
// infrastructure errors never block the pipeline.
func parseReviewJSON(jsonStr, reviewer string, cliResult CLIResult) ReviewResult {
	var result ReviewResult
	if err := robustParseJSON(jsonStr, &result); err != nil {
		return ReviewResult{
			Approved:      true,
			Issues:        []string{"Failed to parse review JSON: " + err.Error()},
			ReviewerAgent: reviewer,
			ReviewOutput:  cliResult.Output,
			Tokens:        cliResult.Tokens,
		}
	}
	result.ReviewerAgent = reviewer
	result.ReviewOutput = cliResult.Output
	result.Tokens = cliResult.Tokens
	return result
}

// DoDVerifyActivity runs DoD checks (compile, test, lint) using git.RunPostMergeChecks.
// Uses cheap agent resources — no smart model needed to run tests.
func (a *Activities) DoDVerifyActivity(ctx context.Context, req TaskRequest) (*DoDResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(OrcaPrefix+" Running DoD checks", "TaskID", req.TaskID, "Checks", len(req.DoDChecks))

	checks := req.DoDChecks
	if len(checks) == 0 {
		// Default DoD: at minimum, the code must compile
		checks = []string{"go build ./..."}
	}

	// --- Preflight: validate worktree integrity before running checks ---
	// Catch corrupted/cleaned worktrees early instead of wasting retries.
	if _, statErr := os.Stat(filepath.Join(req.WorkDir, ".git")); statErr == nil {
		// Worktree git metadata exists; continue.
	} else {
		return &DoDResult{
			Passed: false,
			Failures: []string{fmt.Sprintf(
				"WORKTREE BROKEN: .git directory missing in %s. "+
					"The worktree was likely deleted by the janitor while this workflow was still running "+
					"(cross-project cleanup race). This is an infrastructure failure, not a code issue. "+
					"Do NOT retry in this worktree — it must be recreated from scratch via SetupWorktreeActivity.",
				req.WorkDir)},
		}, nil
	}

	// For npm-based checks, verify package.json exists before attempting build.
	for _, check := range checks {
		if strings.Contains(check, "npm ") {
			if _, statErr := os.Stat(filepath.Join(req.WorkDir, "package.json")); statErr == nil {
				break // only need to check once
			}
			return &DoDResult{
				Passed: false,
				Failures: []string{fmt.Sprintf(
					"WORKTREE BROKEN: package.json missing in %s (required for DoD check: %s). "+
						"The worktree source files are gone — only build artifacts may remain. "+
						"This is an infrastructure failure, not a code issue. "+
						"Do NOT retry in this worktree — it must be recreated.",
					req.WorkDir, check)},
			}, nil
		}
	}

	gitResult, err := git.RunPostMergeChecks(req.WorkDir, checks)
	if err != nil {
		return nil, fmt.Errorf("DoD check execution failed: %w", err)
	}

	result := &DoDResult{
		Passed:   gitResult.Passed,
		Failures: gitResult.Failures,
	}

	for _, c := range gitResult.Checks {
		result.Checks = append(result.Checks, CheckResult{
			Command:    c.Command,
			ExitCode:   c.ExitCode,
			Output:     c.Output,
			Passed:     c.Passed,
			DurationMs: c.Duration.Milliseconds(),
		})
	}

	logger.Info(OrcaPrefix+" DoD result", "Passed", result.Passed, "Checks", len(result.Checks), "Failures", len(result.Failures))
	return result, nil
}

// --- helpers ---

// buildCodebaseContext produces an AST-based codebase overview for the planner.
// Falls back to a simple file listing if tree-sitter is unavailable or fails.
// Output is capped at 8000 characters to keep prompt size reasonable.
func (a *Activities) buildCodebaseContext(ctx context.Context, workDir string) string {
	const maxContextBytes = 8000
	if a.AST != nil && workDir != "" {
		files, err := a.AST.ParseDir(ctx, workDir)
		if err == nil && len(files) > 0 {
			summary := astpkg.Summarize(files)
			if len(summary) > maxContextBytes {
				summary = summary[:maxContextBytes] + "\n... (truncated)"
			}
			return summary
		}
	}
	return fallbackFileList(ctx, workDir)
}

// buildTargetedCodebaseContext produces context with full source for files the
// agent is about to modify and signatures-only for surrounding files.
// Falls back to buildCodebaseContext if target files can't be resolved.
func (a *Activities) buildTargetedCodebaseContext(ctx context.Context, workDir string, targetPaths []string) string {
	const maxContextBytes = 12000 // can be larger since it's targeted
	if a.AST == nil || workDir == "" || len(targetPaths) == 0 {
		return a.buildCodebaseContext(ctx, workDir)
	}
	allFiles, err := a.AST.ParseDir(ctx, workDir)
	if err != nil || len(allFiles) == 0 {
		return a.buildCodebaseContext(ctx, workDir)
	}
	targetFiles := a.AST.ParseFiles(ctx, workDir, targetPaths)
	if len(targetFiles) == 0 {
		return astpkg.Summarize(allFiles)
	}
	summary := astpkg.SummarizeTargeted(allFiles, targetFiles)
	if len(summary) > maxContextBytes {
		summary = summary[:maxContextBytes] + "\n... (truncated)"
	}
	return summary
}

// fallbackFileList is the original file-listing approach used when AST parsing
// is unavailable or fails.
func fallbackFileList(ctx context.Context, workDir string) string {
	if workDir == "" {
		return ""
	}
	var sections []string

	cmd := exec.CommandContext(ctx, "go", "list", "./...")
	cmd.Dir = workDir
	if out, err := cmd.Output(); err == nil && len(out) > 0 {
		sections = append(sections, "Go packages:\n"+string(out))
	}

	cmd = exec.CommandContext(ctx, "find", ".", "-type", "f",
		"-not", "-path", "./.git/*",
		"-not", "-path", "./vendor/*",
		"-not", "-path", "./node_modules/*",
		"-not", "-name", "*.sum",
	)
	cmd.Dir = workDir
	if out, err := cmd.Output(); err == nil && len(out) > 0 {
		tree := string(out)
		if len(tree) > 4000 {
			tree = tree[:4000] + "\n... (truncated)"
		}
		sections = append(sections, "Files:\n"+tree)
	}

	if len(sections) == 0 {
		return ""
	}
	return strings.Join(sections, "\n")
}

// extractJSON finds the first JSON object in text (handles markdown code fences).
// Applies sanitizeJSON to fix common LLM output issues (invalid escapes, trailing commas).
func extractJSON(text string) string {
	var raw string

	// Try to find JSON between code fences first
	if idx := strings.Index(text, "```json"); idx >= 0 {
		start := idx + 7
		if end := strings.Index(text[start:], "```"); end >= 0 {
			raw = strings.TrimSpace(text[start : start+end])
		}
	}
	if raw == "" {
		if idx := strings.Index(text, "```"); idx >= 0 {
			start := idx + 3
			// Skip optional language tag on same line
			if nl := strings.Index(text[start:], "\n"); nl >= 0 {
				start += nl + 1
			}
			if end := strings.Index(text[start:], "```"); end >= 0 {
				candidate := strings.TrimSpace(text[start : start+end])
				if candidate != "" && candidate[0] == '{' {
					raw = candidate
				}
			}
		}
	}

	if raw == "" {
		// Try to find raw JSON object
		start := strings.Index(text, "{")
		if start < 0 {
			return ""
		}
		// Find matching closing brace (skip braces inside strings)
		depth := 0
		inString := false
		escaped := false
		for i := start; i < len(text); i++ {
			ch := text[i]
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' && inString {
				escaped = true
				continue
			}
			if ch == '"' {
				inString = !inString
				continue
			}
			if inString {
				continue
			}
			switch ch {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					raw = text[start : i+1]
					break
				}
			}
			if raw != "" {
				break
			}
		}
	}

	if raw == "" {
		return ""
	}
	return sanitizeJSON(raw)
}

// sanitizeJSON fixes common issues in LLM-generated JSON that cause
// json.Unmarshal to fail:
//  1. Invalid backslash escapes (\n literal outside strings → \\n)
//  2. Trailing commas before } or ] (common LLM mistake)
//  3. Unescaped control characters in string values
func sanitizeJSON(s string) string {
	// First, try to parse as-is — fast path for valid JSON.
	if json.Valid([]byte(s)) {
		return s
	}

	// Walk through the string and fix issues inside JSON string values.
	var buf strings.Builder
	buf.Grow(len(s))
	inString := false
	i := 0
	for i < len(s) {
		ch := s[i]
		if !inString {
			if ch == '"' {
				inString = true
			}
			buf.WriteByte(ch)
			i++
			continue
		}
		// Inside a JSON string value
		if ch == '"' {
			// End of string
			inString = false
			buf.WriteByte(ch)
			i++
			continue
		}
		if ch == '\\' && i+1 < len(s) {
			next := s[i+1]
			// Valid JSON escapes: " \ / b f n r t u
			switch next {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't', 'u':
				buf.WriteByte(ch)
				buf.WriteByte(next)
				i += 2
				continue
			default:
				// Invalid escape — double-escape the backslash
				buf.WriteString("\\\\")
				i++
				continue
			}
		}
		// Control characters (0x00-0x1F) must be escaped in JSON strings
		if ch < 0x20 {
			switch ch {
			case '\n':
				buf.WriteString("\\n")
			case '\r':
				buf.WriteString("\\r")
			case '\t':
				buf.WriteString("\\t")
			default:
				buf.WriteString(fmt.Sprintf("\\u%04x", ch))
			}
			i++
			continue
		}
		buf.WriteByte(ch)
		i++
	}

	result := buf.String()

	// Fix trailing commas: ,} → } and ,] → ]
	result = trailingCommaRe.ReplaceAllString(result, "$1")

	return result
}

var trailingCommaRe = regexp.MustCompile(`,\s*([}\]])`)

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// truncateRunes truncates a string to maxRunes runes, avoiding mid-character
// splits on multi-byte UTF-8 content (e.g. health event details with non-ASCII).
func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}

// flexUnmarshalPlan tries to parse agent output into a StructuredPlan.
// Handles multiple agent output formats:
//  1. Direct JSON: {"summary":"...", "steps":[...]} — codex does this
//  2. Gemini envelope: {"session_id":"...", "response":"{\"summary\":\"...\"}"} —
//     gemini wraps the plan in a session envelope where "response" is a JSON *string*
//  3. camelCase keys: {"filesToModify":[...]} — normalize to snake_case
func flexUnmarshalPlan(jsonStr string) (*StructuredPlan, error) {
	var plan StructuredPlan
	if err := robustParseJSON(jsonStr, &plan); err != nil {
		return nil, err
	}

	// If standard unmarshal populated fields, we're done (codex path).
	if plan.Summary != "" || len(plan.Steps) > 0 || len(plan.AcceptanceCriteria) > 0 || len(plan.FilesToModify) > 0 {
		return &plan, nil
	}

	// Standard unmarshal gave us empty fields. Check for gemini envelope.
	var envelope struct {
		SessionID string `json:"session_id"`
		Response  string `json:"response"`
	}
	if err := robustParseJSON(jsonStr, &envelope); err == nil && envelope.Response != "" {
		// Gemini wraps plan as a JSON string inside "response".
		// The response value is the actual plan JSON.
		var innerPlan StructuredPlan
		if err := robustParseJSON(envelope.Response, &innerPlan); err == nil {
			if innerPlan.Summary != "" || len(innerPlan.Steps) > 0 {
				return &innerPlan, nil
			}
		}
		// Inner parse failed or still empty — try key normalization on inner JSON
		if normalized, err := normalizeJSONKeys(envelope.Response); err == nil {
			var innerPlan2 StructuredPlan
			if err := robustParseJSON(string(normalized), &innerPlan2); err == nil {
				if innerPlan2.Summary != "" || len(innerPlan2.Steps) > 0 {
					return &innerPlan2, nil
				}
			}
		}
	}

	// Last resort: try key normalization on the original JSON.
	if normalized, err := normalizeJSONKeys(jsonStr); err == nil {
		var plan2 StructuredPlan
		if err := robustParseJSON(string(normalized), &plan2); err == nil {
			return &plan2, nil
		}
	}

	return &plan, nil // return the empty plan — Validate() will catch it
}

// normalizeJSONKeys converts camelCase keys to snake_case for known plan fields.
func normalizeJSONKeys(jsonStr string) ([]byte, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return nil, err
	}

	keyMap := map[string]string{
		"filesToModify":       "files_to_modify",
		"acceptanceCriteria":  "acceptance_criteria",
		"estimatedComplexity": "estimated_complexity",
		"riskAssessment":      "risk_assessment",
		"previousErrors":      "previous_errors",
	}

	normalized := make(map[string]json.RawMessage, len(raw))
	for k, v := range raw {
		if mapped, ok := keyMap[k]; ok {
			normalized[mapped] = v
		} else {
			normalized[k] = v
		}
	}

	return json.Marshal(normalized)
}

// loadSemgrepContext reads .semgrep/*.yml rules from the workspace and formats
// them for prompt injection. The organism sees the environment it must survive in.
// A system optimizing for survival doesn't set organisms up for failure.
func loadSemgrepContext(workDir string) string {
	semgrepDir := filepath.Join(workDir, ".semgrep")
	entries, err := os.ReadDir(semgrepDir)
	if err != nil || len(entries) == 0 {
		return ""
	}

	rules := make([]string, 0, len(entries))
	totalLen := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".yml" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(semgrepDir, entry.Name()))
		if err != nil {
			continue
		}
		rule := string(data)
		if totalLen+len(rule) > 2000 {
			break // cap context to avoid prompt bloat
		}
		rules = append(rules, rule)
		totalLen += len(rule)
	}

	if len(rules) == 0 {
		return ""
	}

	result := "\nENFORCED CODE RULES (your output will be scanned by these — plan accordingly):\n"
	for _, r := range rules {
		result += "---\n" + r + "\n"
	}
	return result
}

func formatCriteria(criteria []string) string {
	var sb strings.Builder
	for _, c := range criteria {
		sb.WriteString(fmt.Sprintf("- %s\n", c))
	}
	return sb.String()
}
