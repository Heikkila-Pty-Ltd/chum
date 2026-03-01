package temporal

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"go.temporal.io/sdk/activity"
)

// AutoFixResult holds the outcome of an automatic lint-fix pass.
type AutoFixResult struct {
	FilesFixed int      `json:"files_fixed"`
	ToolsRun   []string `json:"tools_run"`
	Output     string   `json:"output"`
}

// AutoFixLintActivity runs deterministic auto-fix tools (gofmt, goimports)
// to clean up common formatting issues before the next agent retry.
// This is cheap and avoids wasting a retry on trivially fixable lint errors.
func (a *Activities) AutoFixLintActivity(ctx context.Context, workDir string) (*AutoFixResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(OrcaPrefix+" Running auto-fix lint", "workDir", workDir)

	result := &AutoFixResult{}
	var allOutput strings.Builder

	// List of auto-fix tools to run. Each is: [command, description].
	// Only run tools that are installed and applicable.
	fixTools := []struct {
		cmd  string
		args []string
		name string
	}{
		{"gofmt", []string{"-w", "."}, "gofmt"},
		{"goimports", []string{"-w", "."}, "goimports"},
	}

	for _, tool := range fixTools {
		path, err := exec.LookPath(tool.cmd)
		if err != nil {
			continue // tool not installed, skip
		}
		_ = path

		cmd := exec.CommandContext(ctx, tool.cmd, tool.args...)
		cmd.Dir = workDir
		out, err := cmd.CombinedOutput()
		allOutput.WriteString(fmt.Sprintf("--- %s ---\n%s\n", tool.name, string(out)))
		if err != nil {
			logger.Warn(OrcaPrefix+" Auto-fix tool failed (non-fatal)", "tool", tool.name, "error", err)
			continue
		}
		result.ToolsRun = append(result.ToolsRun, tool.name)
	}

	// Count files changed by git
	cmd := exec.CommandContext(ctx, "git", "diff", "--name-only")
	cmd.Dir = workDir
	diffOut, err := cmd.Output()
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(diffOut)), "\n")
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				result.FilesFixed++
			}
		}
	}

	// Stage and commit auto-fixes if any
	if result.FilesFixed > 0 {
		stageCmd := exec.CommandContext(ctx, "git", "add", "-A")
		stageCmd.Dir = workDir
		if err := stageCmd.Run(); err != nil {
			logger.Warn(OrcaPrefix+" git add failed during auto-fix", "error", err)
		} else {
			commitCmd := exec.CommandContext(ctx, "git", "commit", "-m", "chore: auto-fix formatting (gofmt/goimports)")
			commitCmd.Dir = workDir
			if err := commitCmd.Run(); err != nil {
				logger.Warn(OrcaPrefix+" git commit failed during auto-fix", "error", err)
			}
		}
	}

	result.Output = allOutput.String()
	return result, nil
}
