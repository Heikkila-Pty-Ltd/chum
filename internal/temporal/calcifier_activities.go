package temporal

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.temporal.io/sdk/activity"
)

// CalcificationCandidate represents a morsel type eligible for calcification.
type CalcificationCandidate struct {
	MorselType  string   `json:"morsel_type"`
	Project     string   `json:"project"`
	DispatchIDs []int64  `json:"dispatch_ids"`
	Prompts     []string `json:"prompts"`
	Outputs     []string `json:"outputs"`
	Labels      []string `json:"labels"`
}

// CalcifiedScriptResult is the output of the CompileCalcifiedScript activity.
type CalcifiedScriptResult struct {
	ScriptPath string `json:"script_path"`
	SHA256     string `json:"sha256"`
	Language   string `json:"language"` // "python", "bash", "go"
	StoreID    int64  `json:"store_id"`
}

// DetectCalcificationCandidatesActivity scans dispatch history for morsel types
// that have been solved by the LLM enough consecutive times to warrant calcification.
// It applies risk-weighted thresholds — risky morsel types need more successes.
func (a *Activities) DetectCalcificationCandidatesActivity(ctx context.Context, project string) ([]CalcificationCandidate, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(OctopusPrefix + " Detecting calcification candidates")

	if a.Store == nil {
		return nil, fmt.Errorf("store not available")
	}

	// Query distinct morsel types from recent completions.
	// We use the morsel_id prefix as a "type" — e.g., "parse_lead_form", "extract_invoice".
	rows, err := a.Store.DB().QueryContext(ctx,
		`SELECT DISTINCT
			CASE
				WHEN INSTR(morsel_id, '-') > 0 THEN SUBSTR(morsel_id, 1, INSTR(morsel_id, '-') - 1)
				ELSE morsel_id
			END AS morsel_type,
			labels
		 FROM dispatches
		 WHERE project = ? AND status = 'completed'
		 ORDER BY dispatched_at DESC
		 LIMIT 200`,
		project,
	)
	if err != nil {
		return nil, fmt.Errorf("query morsel types: %w", err)
	}
	defer rows.Close()

	// Deduplicate types
	seen := map[string][]string{} // morselType -> labels
	for rows.Next() {
		var morselType, labels string
		if err := rows.Scan(&morselType, &labels); err != nil {
			continue
		}
		if _, ok := seen[morselType]; !ok {
			var labelList []string
			if labels != "" {
				labelList = strings.Split(labels, ",")
			}
			seen[morselType] = labelList
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var candidates []CalcificationCandidate
	for morselType, labels := range seen {
		// Check if script already exists (active or shadow)
		active, _ := a.Store.GetActiveScriptForType(morselType)
		if active != nil {
			continue
		}
		shadow, _ := a.Store.GetShadowScriptForType(morselType)
		if shadow != nil {
			continue
		}

		// Count consecutive successes
		streak, err := a.Store.GetConsecutiveSuccessfulDispatches(morselType, project)
		if err != nil {
			logger.Warn(OctopusPrefix+" Failed to count successes", "type", morselType, "error", err)
			continue
		}

		// Apply risk-weighted threshold
		threshold := a.calcifierThreshold(labels)
		if streak < threshold {
			continue
		}

		logger.Info(OctopusPrefix+" Calcification candidate found",
			"type", morselType, "streak", streak, "threshold", threshold)

		candidates = append(candidates, CalcificationCandidate{
			MorselType: morselType,
			Project:    project,
			Labels:     labels,
		})
	}

	return candidates, nil
}

// calcifierThreshold computes the effective threshold given morsel labels.
func (a *Activities) calcifierThreshold(labels []string) int {
	baseThreshold := 10
	riskMultiplier := 3
	riskyLabels := []string{"risk:high", "security", "migration", "breaking-change", "database"}

	for _, l := range labels {
		for _, r := range riskyLabels {
			if l == r {
				return baseThreshold * riskMultiplier
			}
		}
	}
	return baseThreshold
}

// CompileCalcifiedScriptActivity gathers successful dispatch prompt/output pairs
// and dispatches a premium model to write a deterministic replacement script.
// The script is saved with a .shadow extension for validation before promotion.
func (a *Activities) CompileCalcifiedScriptActivity(ctx context.Context, candidate CalcificationCandidate) (*CalcifiedScriptResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(OctopusPrefix+" Compiling calcified script", "type", candidate.MorselType)

	if a.Store == nil {
		return nil, fmt.Errorf("store not available")
	}

	// Gather recent successful dispatches for this morsel type
	rows, err := a.Store.DB().QueryContext(ctx,
		`SELECT id, prompt FROM dispatches
		 WHERE morsel_id LIKE ? AND project = ? AND status = 'completed'
		 ORDER BY dispatched_at DESC LIMIT 10`,
		candidate.MorselType+"%", candidate.Project,
	)
	if err != nil {
		return nil, fmt.Errorf("gather dispatches: %w", err)
	}
	defer rows.Close()

	var prompts []string
	var dispatchIDs []int64
	for rows.Next() {
		var id int64
		var prompt string
		if err := rows.Scan(&id, &prompt); err != nil {
			continue
		}
		dispatchIDs = append(dispatchIDs, id)
		prompts = append(prompts, prompt)
	}

	if len(prompts) == 0 {
		return nil, fmt.Errorf("no dispatches found for type %s", candidate.MorselType)
	}

	// Build the meta-prompt for script generation
	compilationPrompt := buildCompilationPrompt(candidate.MorselType, prompts)

	// Dispatch to premium model to generate the deterministic script
	cliResult, err := a.runAgentWithModel(ctx, "gemini", "gemini-2.5-pro", compilationPrompt, "")
	if err != nil {
		return nil, fmt.Errorf("compile script via LLM: %w (output: %s)", err, truncate(cliResult.Output, 500))
	}

	// Extract script content from LLM output
	scriptContent := extractScriptContent(cliResult.Output)
	if scriptContent == "" {
		return nil, fmt.Errorf("LLM did not produce a valid script")
	}

	// Determine language and extension
	lang, ext := detectScriptLanguage(scriptContent)

	// Write to .cortex/calcified/ with .shadow extension
	scriptName := fmt.Sprintf("%s.%s.shadow", candidate.MorselType, ext)
	calcifiedDir := ".cortex/calcified"
	scriptPath := filepath.Join(calcifiedDir, scriptName)

	if err := os.MkdirAll(calcifiedDir, 0o755); err != nil {
		return nil, fmt.Errorf("create calcified dir: %w", err)
	}

	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0o755); err != nil {
		return nil, fmt.Errorf("write script: %w", err)
	}

	// Compute SHA-256 for provenance
	hash, err := hashFile(scriptPath)
	if err != nil {
		return nil, fmt.Errorf("hash script: %w", err)
	}

	// Record in store
	storeID, err := a.Store.RecordCalcifiedScript(candidate.MorselType, candidate.Project, scriptPath, hash)
	if err != nil {
		return nil, fmt.Errorf("record script in store: %w", err)
	}

	logger.Info(OctopusPrefix+" Script compiled and saved as shadow",
		"path", scriptPath, "sha256", hash[:12], "storeID", storeID)

	return &CalcifiedScriptResult{
		ScriptPath: scriptPath,
		SHA256:     hash,
		Language:   lang,
		StoreID:    storeID,
	}, nil
}

// PromoteCalcifiedScriptActivity removes the .shadow extension and marks the script active.
func (a *Activities) PromoteCalcifiedScriptActivity(ctx context.Context, scriptID int64) error {
	logger := activity.GetLogger(ctx)

	script, err := a.Store.GetCalcifiedScriptByID(scriptID)
	if err != nil || script == nil {
		return fmt.Errorf("script %d not found: %w", scriptID, err)
	}

	// Remove .shadow extension on disk
	newPath := strings.TrimSuffix(script.ScriptPath, ".shadow")
	if err := os.Rename(script.ScriptPath, newPath); err != nil {
		return fmt.Errorf("rename shadow script: %w", err)
	}

	// Update store
	if err := a.Store.PromoteScript(scriptID); err != nil {
		return fmt.Errorf("promote in store: %w", err)
	}

	logger.Info(OctopusPrefix+" Script promoted to active duty",
		"type", script.MorselType, "path", newPath)
	return nil
}

// QuarantineAndRewireActivity quarantines a broken script and falls back to LLM.
func (a *Activities) QuarantineAndRewireActivity(ctx context.Context, scriptID int64, reason string) error {
	logger := activity.GetLogger(ctx)

	script, err := a.Store.GetCalcifiedScriptByID(scriptID)
	if err != nil || script == nil {
		return fmt.Errorf("script %d not found: %w", scriptID, err)
	}

	// Rename to .quarantined on disk
	quarantinedPath := script.ScriptPath + ".quarantined"
	if err := os.Rename(script.ScriptPath, quarantinedPath); err != nil {
		logger.Warn(OctopusPrefix+" Failed to rename script for quarantine", "error", err)
		// Continue — store update is more critical
	}

	// Update store
	if err := a.Store.QuarantineScript(scriptID, reason); err != nil {
		return fmt.Errorf("quarantine in store: %w", err)
	}

	logger.Info(OctopusPrefix+" Script quarantined",
		"type", script.MorselType, "reason", reason)
	return nil
}

// --- helpers ---

func buildCompilationPrompt(morselType string, prompts []string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`You are a script compilation engine. Your task is to write a deterministic script that replaces an LLM call.

The morsel type is: %s

Below are %d examples of prompts that were successfully processed by an LLM. Analyze the common pattern across all inputs and write a single script (Python preferred, Bash acceptable) that can deterministically transform the input (received via stdin) into the expected output format.

Requirements:
- Read all input from stdin
- Write output to stdout
- Exit 0 on success, non-zero on failure
- Include a shebang line (#!/usr/bin/env python3 or #!/usr/bin/env bash)
- Handle edge cases gracefully
- No external dependencies beyond standard library

`, morselType, len(prompts)))

	for i, p := range prompts {
		b.WriteString(fmt.Sprintf("--- Example %d ---\n%s\n\n", i+1, truncate(p, 2000)))
	}

	b.WriteString("Output ONLY the script content, no explanations or markdown fences.")
	return b.String()
}

func extractScriptContent(output string) string {
	// Try to extract from markdown code fence first
	if idx := strings.Index(output, "```"); idx >= 0 {
		// Find the end of the opening fence line
		start := strings.Index(output[idx:], "\n")
		if start < 0 {
			return ""
		}
		start += idx + 1
		end := strings.Index(output[start:], "```")
		if end >= 0 {
			return strings.TrimSpace(output[start : start+end])
		}
	}
	// Otherwise, look for shebang
	if idx := strings.Index(output, "#!"); idx >= 0 {
		return strings.TrimSpace(output[idx:])
	}
	return strings.TrimSpace(output)
}

func detectScriptLanguage(content string) (lang, ext string) {
	first := content
	if idx := strings.Index(content, "\n"); idx > 0 {
		first = content[:idx]
	}

	switch {
	case strings.Contains(first, "python"):
		return "python", "py"
	case strings.Contains(first, "bash") || strings.Contains(first, "/bin/sh"):
		return "bash", "sh"
	default:
		return "python", "py"
	}
}

// marshalJSON is a safe JSON marshal helper.
func marshalJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
