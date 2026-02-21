package temporal

import (
	"bytes"
	"context"
	"fmt"
	"os"
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
//
// SECURITY: The prompt is NOT included in the argument list. Instead, runCLI
// pipes it via stdin from a temp file. This prevents:
//   - Prompt content leaking into /proc/PID/cmdline
//   - ARG_MAX overflow on long prompts
//   - Any CLI-level argument parsing surprises from untrusted prompt content
func cliCommand(agent, workDir string) *exec.Cmd {
	var cmd *exec.Cmd
	switch strings.ToLower(agent) {
	case "codex":
		// codex exec reads the prompt from stdin in --full-auto mode.
		cmd = exec.Command("codex", "exec", "--full-auto", "--json")
	case "gemini":
		// gemini CLI: -p "" enters headless mode, --yolo auto-accept, -o json for stats.
		// Gemini appends stdin to the -p value, so the actual prompt arrives via stdin.
		cmd = exec.Command("gemini", "-p", "", "--yolo", "-o", "json")
	default: // claude — JSON output gives us token usage
		// claude --print reads from stdin when no positional prompt is given.
		cmd = exec.Command("claude", "--print", "--output-format", "json", "--dangerously-skip-permissions")
	}
	cmd.Dir = workDir
	return cmd
}

// cliReviewCommand returns an exec.Cmd for a given agent in code review mode.
// Note: `codex review` is for git diff reviews, not structured JSON output.
// We use `codex exec` for both coding and review — the prompt differentiates them.
//
// SECURITY: Same stdin-piped prompt as cliCommand — see that function for details.
func cliReviewCommand(agent, workDir string) *exec.Cmd {
	var cmd *exec.Cmd
	switch strings.ToLower(agent) {
	case "codex":
		// codex exec for review — same as coding, but the prompt asks for review output
		cmd = exec.Command("codex", "exec", "--full-auto")
	default: // claude reviews via --print with JSON output for token tracking
		cmd = exec.Command("claude", "--print", "--output-format", "json", "--dangerously-skip-permissions")
	}
	cmd.Dir = workDir
	return cmd
}

// runCLI executes a CLI command, piping the prompt via stdin, and returns a
// CLIResult with stdout and token usage.
//
// SECURITY: The prompt is written to a temp file and piped as stdin to keep it
// out of process argument lists (/proc/PID/cmdline) and avoid ARG_MAX limits.
// The temp file is removed on return.
func runCLI(ctx context.Context, agent, prompt string, cmd *exec.Cmd) (CLIResult, error) {
	// Write prompt to temp file, then pipe as stdin.
	promptFile, err := os.CreateTemp("", "chum-prompt-*.txt")
	if err != nil {
		return CLIResult{}, fmt.Errorf("create prompt temp file: %w", err)
	}
	defer os.Remove(promptFile.Name())

	if _, err := promptFile.WriteString(prompt); err != nil {
		promptFile.Close()
		return CLIResult{}, fmt.Errorf("write prompt temp file: %w", err)
	}
	if _, err := promptFile.Seek(0, 0); err != nil {
		promptFile.Close()
		return CLIResult{}, fmt.Errorf("seek prompt temp file: %w", err)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = promptFile

	if err := cmd.Start(); err != nil {
		promptFile.Close()
		return CLIResult{}, fmt.Errorf("failed to start %s: %w", agent, err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	for {
		select {
		case err := <-done:
			promptFile.Close()
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
	return runCLI(ctx, agent, prompt, cliCommand(agent, workDir))
}

// runReviewAgent executes a CLI agent in code review mode and returns a CLIResult.
func runReviewAgent(ctx context.Context, agent, prompt, workDir string) (CLIResult, error) {
	return runCLI(ctx, agent, prompt, cliReviewCommand(agent, workDir))
}
