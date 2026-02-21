package temporal

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"

	"github.com/antigravity-dev/chum/internal/config"
)

// ResolveTierAgent returns the first agent in the given tier's agent list.
// Falls back to "codex" when the tier is unknown or has no agents configured.
func ResolveTierAgent(tiers config.Tiers, tier string) string {
	tier = strings.TrimSpace(strings.ToLower(tier))

	var agents []string
	switch tier {
	case "fast", "":
		agents = tiers.Fast
	case "balanced":
		agents = tiers.Balanced
	case "premium":
		agents = tiers.Premium
	}
	if len(agents) > 0 {
		return agents[0]
	}
	return "codex"
}
// cliCommand returns an exec.Cmd for a given agent in non-interactive coding mode.
// V1: claude, codex, and gemini — all with JSON output for token tracking.
func cliCommand(agent, prompt, workDir string) *exec.Cmd {
	var cmd *exec.Cmd
	switch strings.ToLower(agent) {
	case "codex":
		// codex exec --full-auto --json for JSONL events with token usage
		cmd = exec.Command("codex", "exec", "--full-auto", "--json", prompt)
	case "gemini":
		// gemini CLI: -p for non-interactive, --yolo auto-accept, -o json for stats
		cmd = exec.Command("gemini", "-p", prompt, "--yolo", "-o", "json")
	default: // claude — JSON output gives us token usage
		cmd = exec.Command("claude", "--print", "--output-format", "json", "--dangerously-skip-permissions", prompt)
	}
	cmd.Dir = workDir
	return cmd
}
// cliReviewCommand returns an exec.Cmd for a given agent in code review mode.
// Note: `codex review` is for git diff reviews, not structured JSON output.
// We use `codex exec` for both coding and review — the prompt differentiates them.
func cliReviewCommand(agent, prompt, workDir string) *exec.Cmd {
	var cmd *exec.Cmd
	switch strings.ToLower(agent) {
	case "codex":
		// codex exec for review — same as coding, but the prompt asks for review output
		cmd = exec.Command("codex", "exec", "--full-auto", prompt)
	default: // claude reviews via --print with JSON output for token tracking
		cmd = exec.Command("claude", "--print", "--output-format", "json", "--dangerously-skip-permissions", prompt)
	}
	cmd.Dir = workDir
	return cmd
}
// runCLI executes a CLI command and returns a CLIResult with stdout and token usage.
// For claude agents, parses --output-format json to extract tokens.
// For codex/other agents, returns raw output with zero tokens.
func runCLI(ctx context.Context, agent string, cmd *exec.Cmd) (CLIResult, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return CLIResult{}, fmt.Errorf("failed to start %s: %w", agent, err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	for {
		select {
		case err := <-done:
			raw := strings.TrimSpace(stdout.String())
			if err != nil {
				errOut := strings.TrimSpace(stderr.String())
				if errOut != "" {
					raw += "\n" + errOut
				}
				result := parseAgentOutput(agent, raw)
				return result, fmt.Errorf("%s exited with error: %w", agent, err)
			}
			return parseAgentOutput(agent, raw), nil
		case <-time.After(5 * time.Second):
			activity.RecordHeartbeat(ctx)
		}
	}
}
// runAgent executes a CLI agent in coding mode and returns a CLIResult.
func runAgent(ctx context.Context, agent, prompt, workDir string) (CLIResult, error) {
	return runCLI(ctx, agent, cliCommand(agent, prompt, workDir))
}

// runReviewAgent executes a CLI agent in code review mode and returns a CLIResult.
func runReviewAgent(ctx context.Context, agent, prompt, workDir string) (CLIResult, error) {
	return runCLI(ctx, agent, cliReviewCommand(agent, prompt, workDir))
}
