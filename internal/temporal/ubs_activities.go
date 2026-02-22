package temporal

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"go.temporal.io/sdk/activity"

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
