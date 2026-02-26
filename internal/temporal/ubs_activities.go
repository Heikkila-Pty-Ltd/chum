package temporal

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"go.temporal.io/sdk/activity"

	"github.com/antigravity-dev/chum/internal/graph"
	"github.com/antigravity-dev/chum/internal/store"
)

// UBSScanRequest contains the parameters for a UBS scan.
type UBSScanRequest struct {
	WorktreePath string `json:"worktree_path"`
	MorselID     string `json:"morsel_id"`
	Project      string `json:"project"`
	Provider     string `json:"provider"`
	Species      string `json:"species"`
	DispatchID   int64  `json:"dispatch_id"`
	Attempt      int    `json:"attempt"`
}

// UBSScanResult contains the results of a UBS scan.
type UBSScanResult struct {
	TotalFindings int  `json:"total_findings"`
	Critical      int  `json:"critical"`
	Warnings      int  `json:"warnings"`
	Info          int  `json:"info"`
	Passed        bool `json:"passed"` // true if no critical findings
}

// ubsJSONOutput represents a single finding from UBS --format=json.
type ubsJSONFinding struct {
	RuleID   string `json:"rule_id"`
	Severity string `json:"severity"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Message  string `json:"message"`
	Language string `json:"language"`
}

// ubsJSONReport is the top-level UBS JSON output.
type ubsJSONReport struct {
	Findings []ubsJSONFinding `json:"findings"`
	Summary  struct {
		Critical int `json:"critical"`
		Warning  int `json:"warning"`
		Info     int `json:"info"`
		Total    int `json:"total"`
	} `json:"summary"`
}

// RunUBSScanActivity executes the Ultimate Bug Scanner against a worktree
// and records all findings in the ubs_findings table.
func (a *Activities) RunUBSScanActivity(ctx context.Context, req UBSScanRequest) (*UBSScanResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(SharkPrefix+" Running UBS scan", "MorselID", req.MorselID, "WorktreePath", req.WorktreePath)

	// Run UBS with JSON output
	cmd := exec.CommandContext(ctx, "ubs", req.WorktreePath, "--format=json")
	output, err := cmd.Output()

	// UBS returns exit code 1 when findings are present — that's expected.
	// Only treat as error if we get no output at all.
	if err != nil && len(output) == 0 {
		logger.Warn(SharkPrefix+" UBS scan failed (non-fatal)", "error", err)
		return &UBSScanResult{Passed: true}, nil
	}

	// Parse the JSON output
	var report ubsJSONReport
	if err := json.Unmarshal(output, &report); err != nil {
		// Try line-by-line JSONL parsing
		findings := parseUBSJSONL(output)
		if len(findings) == 0 {
			logger.Warn(SharkPrefix+" UBS output parse failed (non-fatal)", "error", err)
			return &UBSScanResult{Passed: true}, nil
		}
		report.Findings = findings
		for _, f := range findings {
			switch f.Severity {
			case "critical":
				report.Summary.Critical++
			case "warning":
				report.Summary.Warning++
			default:
				report.Summary.Info++
			}
		}
		report.Summary.Total = len(findings)
	}

	// Record findings in the database
	if a.Store != nil && len(report.Findings) > 0 {
		var storeFindings []store.UBSFinding
		for _, f := range report.Findings {
			storeFindings = append(storeFindings, store.UBSFinding{
				DispatchID: req.DispatchID,
				MorselID:   req.MorselID,
				Project:    req.Project,
				Provider:   req.Provider,
				Species:    req.Species,
				RuleID:     f.RuleID,
				Severity:   f.Severity,
				FilePath:   f.File,
				LineNumber: f.Line,
				Message:    f.Message,
				Language:   f.Language,
				Attempt:    req.Attempt,
			})
		}
		if err := a.Store.RecordUBSFindings(storeFindings); err != nil {
			logger.Warn(SharkPrefix+" Failed to record UBS findings (non-fatal)", "error", err)
		}
	}

	result := &UBSScanResult{
		TotalFindings: report.Summary.Total,
		Critical:      report.Summary.Critical,
		Warnings:      report.Summary.Warning,
		Info:          report.Summary.Info,
		Passed:        report.Summary.Critical == 0,
	}

	logger.Info(SharkPrefix+" UBS scan complete",
		"Total", result.TotalFindings,
		"Critical", result.Critical,
		"Warnings", result.Warnings,
		"Passed", result.Passed,
	)

	return result, nil
}

// GetBugPrimingActivity queries aggregate bug patterns for a provider and species,
// returning a formatted prompt section for shark priming.
func (a *Activities) GetBugPrimingActivity(ctx context.Context, provider, species string) (string, error) {
	logger := activity.GetLogger(ctx)
	if a.Store == nil {
		return "", nil
	}

	var sections []string

	// Provider-level weaknesses
	providerBugs, err := a.Store.GetProviderWeaknesses(provider, 5)
	if err != nil {
		logger.Warn(SharkPrefix+" Failed to get provider weaknesses (non-fatal)", "error", err)
	} else if len(providerBugs) > 0 {
		lines := []string{fmt.Sprintf("⚠️ PROVIDER BUG PROFILE (%s):", provider)}
		lines = append(lines, fmt.Sprintf("Your model commonly produces these bugs:"))
		for i, b := range providerBugs {
			sev := strings.ToUpper(b.Severity)
			lines = append(lines, fmt.Sprintf("  %d. [%s] %s (%d occurrences, %d self-fixed) — %s",
				i+1, sev, b.RuleID, b.Count, b.SelfFixed, b.Message))
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}

	// Species-level recurring bugs
	speciesBugs, err := a.Store.GetSpeciesBugProfile(species, 5)
	if err != nil {
		logger.Warn(SharkPrefix+" Failed to get species bug profile (non-fatal)", "error", err)
	} else if len(speciesBugs) > 0 {
		lines := []string{fmt.Sprintf("🧬 SPECIES BUG HISTORY (%s):", species)}
		lines = append(lines, "Previous organisms of this species commonly produced:")
		for i, b := range speciesBugs {
			sev := strings.ToUpper(b.Severity)
			lines = append(lines, fmt.Sprintf("  %d. [%s] %s (%d occurrences) — %s",
				i+1, sev, b.RuleID, b.Count, b.Message))
		}
		sections = append(sections, strings.Join(lines, "\n"))
	}

	if len(sections) == 0 {
		return "", nil
	}

	return strings.Join(sections, "\n\n"), nil
}

// parseUBSJSONL attempts to parse UBS output as newline-delimited JSON.
func parseUBSJSONL(data []byte) []ubsJSONFinding {
	var findings []ubsJSONFinding
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var f ubsJSONFinding
		if err := json.Unmarshal([]byte(line), &f); err == nil && f.RuleID != "" {
			findings = append(findings, f)
		}
	}
	return findings
}

// ubsBaselineFinding is a grouped finding from a UBS baseline scan.
type ubsBaselineFinding struct {
	Category string // e.g. "Mutex manual Lock/Unlock"
	Severity string // "critical" or "warning"
	Count    int
	Detail   string // raw line from UBS
}

// UBSBaselineScanActivity runs UBS against the project root (not a worktree)
// and creates morsels for any critical/warning categories not already tracked.
// This is the "immune system bootstrap" — UBS scans the trunk, and findings
// that don't already have open tasks become new morsels.
func (a *Activities) UBSBaselineScanActivity(ctx context.Context, project, workDir string) (int, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(RemoraPrefix+" UBS baseline scan starting", "Project", project, "WorkDir", workDir)

	// Run UBS in text mode (text has per-category detail, JSON only has totals)
	cmd := exec.CommandContext(ctx, "ubs", workDir, "--format=text")
	output, err := cmd.CombinedOutput()
	if err != nil && len(output) == 0 {
		logger.Warn(RemoraPrefix+" UBS baseline scan failed (non-fatal)", "error", err)
		return 0, nil
	}

	// Parse text output for critical and warning findings
	findings := parseUBSTextFindings(string(output))
	if len(findings) == 0 {
		logger.Info(RemoraPrefix + " UBS baseline: no critical/warning findings")
		return 0, nil
	}

	logger.Info(RemoraPrefix+" UBS baseline findings", "Categories", len(findings))

	// Get existing open tasks to avoid duplicates
	if a.DAG == nil {
		logger.Warn(RemoraPrefix + " No DAG configured, skipping morsel creation")
		return 0, nil
	}

	existingTasks, listErr := a.DAG.ListTasks(ctx, project)
	if listErr != nil {
		logger.Warn(RemoraPrefix+" Failed to list tasks for dedup", "error", listErr)
		existingTasks = nil
	}

	// Index existing task titles for dedup
	existingTitles := make(map[string]bool)
	for _, t := range existingTasks {
		existingTitles[strings.ToLower(t.Title)] = true
	}

	created := 0
	for _, f := range findings {
		title := fmt.Sprintf("UBS %s: fix %d %s issues", f.Severity, f.Count, f.Category)

		// Skip if a similar task already exists
		if existingTitles[strings.ToLower(title)] {
			continue
		}
		// Also check for partial title match (ubs + category)
		alreadyCovered := false
		for existingTitle := range existingTitles {
			if strings.Contains(existingTitle, "ubs") &&
				strings.Contains(strings.ToLower(existingTitle), strings.ToLower(f.Category)) {
				alreadyCovered = true
				break
			}
		}
		if alreadyCovered {
			continue
		}

		priority := 2
		if f.Severity == "critical" {
			priority = 0
		} else if f.Count >= 10 {
			priority = 1
		}

		estimate := 15
		if f.Count > 10 {
			estimate = 45
		} else if f.Count > 5 {
			estimate = 30
		}

		_, err := a.DAG.CreateTask(ctx, graph.Task{
			Title: title,
			Description: fmt.Sprintf("UBS baseline scan found %d %s-severity issues in category: %s\n\nDetail: %s\n\nFix each instance. Run `ubs %s --format=text` to see exact locations.",
				f.Count, f.Severity, f.Category, f.Detail, workDir),
			Acceptance:      fmt.Sprintf("- All %s issues in category '%s' resolved\n- go build ./... passes\n- UBS scan shows 0 findings in this category", f.Severity, f.Category),
			Type:            "task",
			Priority:        priority,
			EstimateMinutes: estimate,
			Labels:          []string{"ubs-baseline", "ubs-" + f.Severity},
			Project:         project,
		})
		if err != nil {
			logger.Warn(RemoraPrefix+" Failed to create UBS morsel", "category", f.Category, "error", err)
			continue
		}
		logger.Info(RemoraPrefix+" Created UBS morsel", "Title", title, "Priority", priority)
		created++
	}

	logger.Info(RemoraPrefix+" UBS baseline scan complete", "FindingCategories", len(findings), "MorselsCreated", created)
	return created, nil
}

// parseUBSTextFindings extracts critical and warning categories from UBS text output.
// UBS text format uses:
//
//	🔥 CRITICAL (N found)
//	⚠ Warning (N found)
//
// followed by a description line.
func parseUBSTextFindings(text string) []ubsBaselineFinding {
	var findings []ubsBaselineFinding
	lines := strings.Split(text, "\n")

	// Track the current section header (e.g. "Mutex manual Lock/Unlock")
	var currentCategory string

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Detect category headers: lines starting with "•" in UBS text
		if strings.HasPrefix(trimmed, "•") {
			currentCategory = strings.TrimPrefix(trimmed, "• ")
			currentCategory = strings.TrimSpace(currentCategory)
			continue
		}

		// Detect findings
		if strings.Contains(trimmed, "🔥 CRITICAL") || strings.Contains(trimmed, "⚠ Warning") {
			severity := "warning"
			if strings.Contains(trimmed, "CRITICAL") {
				severity = "critical"
			}

			// Extract count: "(N found)"
			count := 1
			if idx := strings.Index(trimmed, "("); idx >= 0 {
				if end := strings.Index(trimmed[idx:], " found)"); end >= 0 {
					countStr := trimmed[idx+1 : idx+end]
					if n, err := fmt.Sscanf(countStr, "%d", &count); n == 0 || err != nil {
						count = 1
					}
				}
			}

			// Get the detail line (next line after severity)
			detail := ""
			if i+1 < len(lines) {
				detail = strings.TrimSpace(lines[i+1])
			}

			category := currentCategory
			if category == "" {
				category = detail
			}
			if category == "" {
				category = "unknown"
			}

			findings = append(findings, ubsBaselineFinding{
				Category: category,
				Severity: severity,
				Count:    count,
				Detail:   detail,
			})
		}
	}
	return findings
}
