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

// EscalationChain returns the ordered list of provider names to try for a
// given starting tier: fast → balanced → premium. Each entry is a provider
// key from [providers.*] in config.
func EscalationChain(tiers config.Tiers, startTier string) []string {
	startTier = strings.TrimSpace(strings.ToLower(startTier))
	var chain []string
	seen := make(map[string]bool)

	addUnique := func(agents []string) {
		for _, a := range agents {
			if !seen[a] {
				seen[a] = true
				chain = append(chain, a)
			}
		}
	}

	switch startTier {
	case "fast", "":
		addUnique(tiers.Fast)
		addUnique(tiers.Balanced)
		addUnique(tiers.Premium)
	case "balanced":
		addUnique(tiers.Balanced)
		addUnique(tiers.Premium)
	case "premium":
		addUnique(tiers.Premium)
	}
	if len(chain) == 0 {
		chain = []string{"codex-spark"}
	}
	return chain
}

// ResolveProviderCLI returns the CLI name and model for a provider key.
// Example: "gemini-flash" → ("gemini", "gemini-2.5-flash")
func ResolveProviderCLI(providers map[string]config.Provider, providerKey string) (cli, model string) {
	p, ok := providers[providerKey]
	if !ok {
		return "codex", "" // fallback
	}
	cli = p.CLI
	if cli == "" {
		cli = "codex"
	}
	return cli, p.Model
}

// cliCommand returns an exec.Cmd for a given agent in non-interactive coding mode.
//
// SECURITY: The prompt is NOT included in the argument list. Instead, runCLI
// pipes it via stdin from a temp file. This prevents:
//   - Prompt content leaking into /proc/PID/cmdline
//   - ARG_MAX overflow on long prompts
//   - Any CLI-level argument parsing surprises from untrusted prompt content
func cliCommand(agent, workDir string) *exec.Cmd {
	return cliCommandWithModel(agent, workDir, "")
}

// cliCommandWithModel returns an exec.Cmd for a given agent with an optional model override.
// When model is empty, the CLI uses its default model.
func cliCommandWithModel(agent, workDir, model string) *exec.Cmd {
	var cmd *exec.Cmd
	switch strings.ToLower(agent) {
	case "codex":
		args := []string{"exec", "--full-auto", "--json"}
		if model != "" {
			args = append(args, "-m", model)
		}
		cmd = exec.Command("codex", args...)
	case "gemini":
		args := []string{"-p", "", "--yolo", "-o", "json"}
		if model != "" {
			args = append(args, "--model", model)
		}
		cmd = exec.Command("gemini", args...)
	default: // claude — JSON output gives us token usage
		args := []string{"--print", "--output-format", "json", "--dangerously-skip-permissions"}
		if model != "" {
			args = append(args, "--model", model)
		}
		cmd = exec.Command("claude", args...)
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

// runAgentWithModel executes a CLI agent with a specific model and returns a CLIResult.
func runAgentWithModel(ctx context.Context, agent, model, prompt, workDir string) (CLIResult, error) {
	return runCLI(ctx, agent, prompt, cliCommandWithModel(agent, workDir, model))
}

// runReviewAgent executes a CLI agent in code review mode and returns a CLIResult.
func runReviewAgent(ctx context.Context, agent, prompt, workDir string) (CLIResult, error) {
	return runCLI(ctx, agent, prompt, cliReviewCommand(agent, workDir))
}
