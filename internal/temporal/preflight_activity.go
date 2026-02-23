package temporal

import (
	"context"
	"fmt"
	"strings"

	"go.temporal.io/sdk/activity"

	"github.com/antigravity-dev/chum/internal/graph"
)

// PreflightFailureRequest is sent when the workspace fails its build check
// before the agent even starts coding. This files a morsel for crab review.
type PreflightFailureRequest struct {
	TaskID   string        `json:"task_id"`
	Project  string        `json:"project"`
	WorkDir  string        `json:"work_dir"`
	Failures []string      `json:"failures"`
	Checks   []CheckResult `json:"checks"`
}

// FilePreflightFailureActivity creates a fix-it morsel in the DAG when a
// workspace fails its pre-flight build check. The morsel is labelled for
// crab decomposition review so it gets triaged back into the chum bucket.
func (a *Activities) FilePreflightFailureActivity(ctx context.Context, req PreflightFailureRequest) error {
	logger := activity.GetLogger(ctx)
	logger.Error(CrabPrefix+" Filing pre-flight failure morsel",
		"TaskID", req.TaskID, "Project", req.Project, "Failures", len(req.Failures))

	if a.DAG == nil {
		logger.Warn(CrabPrefix + " No DAG configured, skipping preflight morsel filing")
		return nil
	}

	// Build a diagnostic description from the check output
	var desc strings.Builder
	desc.WriteString(fmt.Sprintf("Pre-flight build check failed before task `%s` could start.\n\n", req.TaskID))
	desc.WriteString("## Build Errors\n\n")
	for _, c := range req.Checks {
		if !c.Passed && c.Output != "" {
			desc.WriteString(fmt.Sprintf("```\n$ %s (exit %d)\n%s\n```\n\n", c.Command, c.ExitCode, c.Output))
		}
	}
	desc.WriteString("Fix the build errors so queued tasks can proceed.")

	title := fmt.Sprintf("Fix broken build blocking %s", req.TaskID)

	_, err := a.DAG.CreateTask(ctx, graph.Task{
		Title:       title,
		Description: desc.String(),
		Type:        "task",
		Priority:    1, // high priority — blocks other work
		Acceptance:  "All DoD checks pass (npm run build or equivalent)",
		Design:      "Fix the specific build errors listed in the description.",
		Labels:      []string{"crab:preflight-failure", "auto-filed"},
		Project:     req.Project,
	})
	if err != nil {
		logger.Error(CrabPrefix+" Failed to file preflight morsel", "error", err)
		return err
	}

	logger.Info(CrabPrefix+" Pre-flight failure morsel filed", "Title", title, "Project", req.Project)
	return nil
}
