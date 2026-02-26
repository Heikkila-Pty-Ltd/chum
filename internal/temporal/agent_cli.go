package temporal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.temporal.io/sdk/activity"

	"github.com/antigravity-dev/chum/internal/config"
)

// filterEnv returns a copy of env with the named variable removed.
func filterEnv(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			out = append(out, e)
		}
	}
	return out
}

// applyEnvOverrides returns a copy of the base environment with the given
// key=value overrides applied. Existing keys are replaced; new keys are appended.
// Values starting with "$" are expanded from the current process environment,
// allowing indirection like GEMINI_API_KEY = "$GEMINI_API_KEY_FREE".
// An empty value (or "" after expansion) REMOVES the variable entirely,
// forcing the child process to fall back to other auth mechanisms (e.g. OAuth).
func applyEnvOverrides(base []string, overrides map[string]string) []string {
	result := make([]string, 0, len(base)+len(overrides))
	replaced := make(map[string]bool, len(overrides))
	for _, e := range base {
		key := e
		if idx := strings.IndexByte(e, '='); idx >= 0 {
			key = e[:idx]
		}
		if val, ok := overrides[key]; ok {
			replaced[key] = true
			val = expandEnvValue(val)
			if val == "" {
				continue // empty = remove the variable entirely
			}
			result = append(result, key+"="+val)
		} else {
			result = append(result, e)
		}
	}
	// Append any overrides not already in base (skip empty values).
	for k, v := range overrides {
		if !replaced[k] {
			v = expandEnvValue(v)
			if v != "" {
				result = append(result, k+"="+v)
			}
		}
	}
	return result
}

// expandEnvValue expands "$VAR_NAME" references to their current value.
func expandEnvValue(val string) string {
	if strings.HasPrefix(val, "$") {
		envKey := val[1:]
		if resolved := os.Getenv(envKey); resolved != "" {
			return resolved
		}
	}
	return val
}

// ErrModelExhausted is returned when a model hits its usage/rate limit.
var ErrModelExhausted = errors.New("model exhausted (rate/usage limit)")

// modelExhaustedPatterns are substrings that indicate rate/usage limits in CLI output.
var modelExhaustedPatterns = []string{
	"usage limit",
	"rate limit",
	"quota exceeded",
	"try again at",
	"rate_limit_exceeded",
	"too many requests",
	"capacity",
}

// IsModelExhausted checks whether CLI output indicates a rate/usage limit.
func IsModelExhausted(output string) bool {
	lower := strings.ToLower(output)
	for _, pattern := range modelExhaustedPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// agentFailureTracker tracks consecutive failures per agent for circuit breaking.
// 3 consecutive failures of the same agent = circuit open, stop dispatching to it.
type agentFailureTracker struct {
	mu       sync.Mutex
	failures map[string]int // agent -> consecutive failure count
}

var globalFailureTracker = &agentFailureTracker{
	failures: make(map[string]int),
}

const agentCircuitBreakerThreshold = 3

// recordFailure increments the consecutive failure count for an agent.
// Returns true if the circuit breaker is now open (>= threshold).
func (t *agentFailureTracker) recordFailure(agent string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failures[agent]++
	return t.failures[agent] >= agentCircuitBreakerThreshold
}

// recordSuccess resets the consecutive failure count for an agent.
func (t *agentFailureTracker) recordSuccess(agent string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.failures, agent)
}

// isCircuitOpen returns true if the agent has hit the failure threshold.
func (t *agentFailureTracker) isCircuitOpen(agent string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.failures[agent] >= agentCircuitBreakerThreshold
}

// consecutiveFailures returns the current failure count for an agent.
func (t *agentFailureTracker) consecutiveFailures(agent string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.failures[agent]
}

// tierRoundRobin stores per-tier atomic counters for round-robin agent selection.
// Each tier (fast, balanced, premium) gets its own counter that monotonically
// increases and wraps around the agent list length.
var tierRoundRobin sync.Map // map[string]*atomic.Uint64

// ResolveTierAgent returns the next agent in the given tier's agent list using
// round-robin selection. Each call advances the counter so consecutive calls
// distribute load evenly across all configured providers in the tier.
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
	if len(agents) == 0 {
		return "codex"
	}
	if len(agents) == 1 {
		return agents[0]
	}
	counter, _ := tierRoundRobin.LoadOrStore(tier, &atomic.Uint64{})
	idx := counter.(*atomic.Uint64).Add(1) - 1 // 0-indexed
	return agents[idx%uint64(len(agents))]
}

// ResetTierRoundRobin clears all round-robin counters. Used in tests to ensure
// deterministic ordering.
func ResetTierRoundRobin() {
	tierRoundRobin.Range(func(key, _ any) bool {
		tierRoundRobin.Delete(key)
		return true
	})
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
		chain = []string{"codex"}
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

// normalizeAgent extracts the canonical CLI name from a provider key.
// For example: "gemini-pro" → "gemini", "codex-spark" → "codex".
// Unknown keys are returned lowercased.
func normalizeAgent(agent string) string {
	lower := strings.ToLower(strings.TrimSpace(agent))
	for _, prefix := range []string{"gemini", "codex", "deepseek", "claude"} {
		if strings.HasPrefix(lower, prefix) {
			return prefix
		}
	}
	return lower
}

// PreflightCLIs validates that CLI binaries exist for all enabled providers.
// Returns a list of warnings for any missing CLIs. Called at worker startup.
func PreflightCLIs(cfg *config.Config, logger interface{ Warn(string, ...any) }) []string {
	if cfg == nil {
		return nil
	}

	var warnings []string
	seen := make(map[string]bool) // dedup by resolved CLI binary

	for name, prov := range cfg.Providers {
		if !prov.IsEnabled() {
			continue
		}

		// Resolve the CLI binary: use dispatch.cli config if available, else provider.CLI, else the provider key
		cliCmd := prov.CLI
		if cliCfg, ok := cfg.Dispatch.CLI[name]; ok && cliCfg.Cmd != "" {
			cliCmd = cliCfg.Cmd
		}
		if cliCmd == "" {
			cliCmd = name
		}

		if seen[cliCmd] {
			continue
		}
		seen[cliCmd] = true

		if _, err := exec.LookPath(cliCmd); err != nil {
			msg := fmt.Sprintf("CLI binary %q not found for provider %q — provider will use hardcoded fallback", cliCmd, name)
			warnings = append(warnings, msg)
			if logger != nil {
				logger.Warn(msg, "provider", name, "cli", cliCmd)
			}
		}
	}

	return warnings
}

// ---------------------------------------------------------------------------
// CLI command builders and runners — Activities methods with package wrappers
// ---------------------------------------------------------------------------
//
// The core logic lives on (a *Activities) methods so production code reads
// config via a.CfgMgr (hot-reloadable). Package-level wrappers delegate to
// a zero-value Activities{} for backward compatibility and testing.
// ---------------------------------------------------------------------------

// cliCommand returns an exec.Cmd for a given agent in non-interactive coding mode.
// Package-level wrapper — delegates to the Activities method.
//
// SECURITY: The prompt is NOT included in the argument list. Instead, runCLI
// pipes it via stdin from a temp file. This prevents:
//   - Prompt content leaking into /proc/PID/cmdline
//   - ARG_MAX overflow on long prompts
//   - Any CLI-level argument parsing surprises from untrusted prompt content
func cliCommand(agent, workDir string) *exec.Cmd {
	return (&Activities{}).cliCommandWithModel(agent, workDir, "")
}

// cliReviewCommand returns an exec.Cmd for a given agent in code review mode.
// Package-level wrapper — delegates to the Activities method.
func cliReviewCommand(agent, workDir string) *exec.Cmd {
	return (&Activities{}).cliReviewCommand(agent, workDir)
}

// ---------------------------------------------------------------------------
// Activities methods — production code uses these directly via DI
// ---------------------------------------------------------------------------

// cliCommandWithModel returns an exec.Cmd for a given agent with an optional model override.
// When model is empty, the CLI uses its default model.
//
// Config-driven: if a.CfgMgr is set, looks up [dispatch.cli.<agent>] first.
// Falls back to hardcoded defaults when CfgMgr is nil or the agent key is missing.
func (a *Activities) cliCommandWithModel(agent, workDir, model string) *exec.Cmd {
	// --- Config-driven path (hot-reloadable) ---
	if a.CfgMgr != nil {
		if cfg := a.CfgMgr.Get(); cfg != nil {
			if cliCfg, ok := cfg.Dispatch.CLI[agent]; ok && cliCfg.Cmd != "" {
				args := append([]string{}, cliCfg.Args...)
				if model != "" && cliCfg.ModelFlag != "" {
					args = append(args, cliCfg.ModelFlag, model)
				}
				cmd := exec.Command(cliCfg.Cmd, args...)
				cmd.Dir = workDir
				// Apply per-provider env overrides (e.g. GEMINI_API_KEY for free-tier).
				if len(cliCfg.Env) > 0 {
					cmd.Env = applyEnvOverrides(os.Environ(), cliCfg.Env)
				}
				return cmd
			}
		}
	}

	// --- Hardcoded fallback (tests, missing config keys) ---
	var cmd *exec.Cmd
	switch normalizeAgent(agent) {
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
	case "deepseek":
		args := []string{"--json"}
		if model != "" {
			args = append(args, "--model", model)
		}
		cmd = exec.Command("deepseek", args...)
	case "claude":
		args := []string{"--dangerously-skip-permissions"}
		if model != "" {
			args = append(args, "--model", model)
		}
		cmd = exec.Command("claude", args...)
	default: // codex fallback
		args := []string{"exec", "--full-auto", "--json"}
		if model != "" {
			args = append(args, "-m", model)
		}
		cmd = exec.Command("codex", args...)
	}
	cmd.Dir = workDir
	return cmd
}

// cliReviewCommand returns an exec.Cmd for a given agent in code review mode.
// Note: `codex review` is for git diff reviews, not structured JSON output.
// We use `codex exec` for both coding and review — the prompt differentiates them.
//
// SECURITY: Same stdin-piped prompt as cliCommand — see that function for details.
func (a *Activities) cliReviewCommand(agent, workDir string) *exec.Cmd {
	var cmd *exec.Cmd
	switch normalizeAgent(agent) {
	case "codex":
		// codex exec for review — same as coding, but the prompt asks for review output
		cmd = exec.Command("codex", "exec", "--full-auto")
	case "gemini":
		cmd = exec.Command("gemini", "-p", "", "--yolo", "-o", "json")
	case "deepseek":
		cmd = exec.Command("deepseek", "--json")
	case "claude":
		cmd = exec.Command("claude", "--dangerously-skip-permissions")
	default: // codex fallback
		cmd = exec.Command("codex", "exec", "--full-auto")
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
func (a *Activities) runCLI(ctx context.Context, agent, prompt string, cmd *exec.Cmd) (CLIResult, error) {
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

	// Strip CLAUDECODE env var so child CLI processes (especially `claude`)
	// don't reject themselves as nested sessions. The chum worker may have
	// inherited this variable from the shell that launched it.
	// Preserve any env overrides already set by cliCommandWithModel (e.g. API key isolation).
	if cmd.Env == nil {
		cmd.Env = filterEnv(os.Environ(), "CLAUDECODE")
	} else {
		cmd.Env = filterEnv(cmd.Env, "CLAUDECODE")
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = promptFile

	// Defensive: ensure the working directory exists before exec.
	// /tmp worktrees can disappear after reboots or cleanup jobs; without
	// this guard the chdir fails and the entire activity errors out.
	if cmd.Dir != "" {
		if _, statErr := os.Stat(cmd.Dir); os.IsNotExist(statErr) {
			activity.GetLogger(ctx).Warn("⚠️ CLI workdir missing — creating it defensively (investigate root cause)",
				"WorkDir", cmd.Dir, "Agent", agent)
			if mkErr := os.MkdirAll(cmd.Dir, 0o755); mkErr != nil {
				promptFile.Close()
				return CLIResult{}, fmt.Errorf("failed to create workdir %s: %w", cmd.Dir, mkErr)
			}
		}
	}

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
				// Wrap with ErrModelExhausted if rate limit detected
				if IsModelExhausted(raw) {
					return result, fmt.Errorf("%s: %w: %w", agent, ErrModelExhausted, err)
				}
				return result, fmt.Errorf("%s exited with error: %w", agent, err)
			}
			return parseAgentOutput(agent, raw), nil
		case <-time.After(5 * time.Second):
			activity.RecordHeartbeat(ctx)
		}
	}
}

// runAgent executes a CLI agent in coding mode and returns a CLIResult.
func (a *Activities) runAgent(ctx context.Context, agent, prompt, workDir string) (CLIResult, error) {
	return a.runCLI(ctx, agent, prompt, a.cliCommandWithModel(agent, workDir, ""))
}

// runAgentWithModel executes a CLI agent with a specific model and returns a CLIResult.
func (a *Activities) runAgentWithModel(ctx context.Context, agent, model, prompt, workDir string) (CLIResult, error) {
	return a.runCLI(ctx, agent, prompt, a.cliCommandWithModel(agent, workDir, model))
}

// runReviewAgent executes a CLI agent in code review mode and returns a CLIResult.
func (a *Activities) runReviewAgent(ctx context.Context, agent, prompt, workDir string) (CLIResult, error) {
	return a.runCLI(ctx, agent, prompt, a.cliReviewCommand(agent, workDir))
}

// runAgentWithFailover tries each agent in the tier's escalation chain.
// On any failure: records health event, sends Matrix alert, tries next agent.
// 3 consecutive failures of the same agent: circuit breaker opens, agent skipped.
//
// This is the primary entry point for activities that want resilient agent execution.
func (a *Activities) runAgentWithFailover(ctx context.Context, tier, prompt, workDir string) (CLIResult, string, error) {
	logger := activity.GetLogger(ctx)
	chain := EscalationChain(a.Tiers, tier)

	var lastErr error
	var lastResult CLIResult

	for i, agent := range chain {
		// Check persisted exhaustion block (survives restarts).
		if a.Store != nil && !globalFailureTracker.isCircuitOpen(agent) {
			if block, blockErr := a.Store.GetBlock("agent", "exhausted:"+agent); blockErr == nil && block != nil && block.BlockedUntil.After(time.Now()) {
				// Re-hydrate the in-memory circuit breaker from the persisted block.
				for j := 0; j < agentCircuitBreakerThreshold; j++ {
					globalFailureTracker.recordFailure(agent)
				}
				logger.Warn("💾 Restored persisted exhaustion block — skipping agent",
					"Agent", agent, "BlockedUntil", block.BlockedUntil.Format(time.RFC3339))
			}
		}

		// Circuit breaker: skip agents that have hit 3 consecutive failures
		if globalFailureTracker.isCircuitOpen(agent) {
			logger.Warn("⚡ Circuit breaker OPEN — skipping agent",
				"Agent", agent,
				"ConsecutiveFailures", globalFailureTracker.consecutiveFailures(agent))
			a.alertAgentFailure(ctx, agent, tier, "circuit_breaker_open",
				fmt.Sprintf("Agent %s has %d consecutive failures — circuit breaker open, skipping",
					agent, globalFailureTracker.consecutiveFailures(agent)))
			continue
		}

		logger.Info("🦈 Trying agent", "Agent", agent, "Tier", tier,
			"Position", fmt.Sprintf("%d/%d", i+1, len(chain)))

		result, err := a.runAgent(ctx, agent, prompt, workDir)
		if err == nil {
			// Success — reset the failure counter
			globalFailureTracker.recordSuccess(agent)
			return result, agent, nil
		}

		// Failure — record it, alert, and try next
		lastErr = err
		lastResult = result

		circuitOpen := globalFailureTracker.recordFailure(agent)
		failCount := globalFailureTracker.consecutiveFailures(agent)

		errKind := "agent_failure"
		if errors.Is(err, ErrModelExhausted) {
			errKind = "model_exhausted"
		}

		detail := fmt.Sprintf("Agent %s failed (tier=%s, attempt=%d/%d, consecutive=%d): %v",
			agent, tier, i+1, len(chain), failCount, err)

		logger.Error("🚨 Agent failure", "Agent", agent, "Tier", tier,
			"ErrorKind", errKind, "ConsecutiveFailures", failCount,
			"CircuitOpen", circuitOpen, "error", err)

		// Record health event + Matrix alert for EVERY failure
		a.alertAgentFailure(ctx, agent, tier, errKind, detail)

		if circuitOpen {
			logger.Error("⚡ CIRCUIT BREAKER TRIPPED — stopping agent",
				"Agent", agent, "ConsecutiveFailures", failCount)
			a.alertAgentFailure(ctx, agent, tier, "circuit_breaker_tripped",
				fmt.Sprintf("🔴 CIRCUIT BREAKER: Agent %s has failed %d times consecutively. Machine stopped for this agent.",
					agent, failCount))

			// Persist exhaustion block so it survives restarts.
			if errors.Is(err, ErrModelExhausted) && a.Store != nil {
				blockDuration := 6 * time.Hour
				//nolint:errcheck // best-effort persistence
				a.Store.SetBlockWithMetadata("agent", "exhausted:"+agent,
					time.Now().Add(blockDuration),
					fmt.Sprintf("model exhausted after %d consecutive failures", failCount),
					map[string]interface{}{"agent": agent, "failures": failCount})
				logger.Warn("💾 Persisted exhaustion block — agent will stay blocked across restarts",
					"Agent", agent, "BlockDuration", blockDuration)
			}
		}
	}

	// All agents exhausted
	if lastErr != nil {
		a.alertAgentFailure(ctx, "ALL", tier, "all_agents_exhausted",
			fmt.Sprintf("🔴 ALL AGENTS EXHAUSTED in tier %s. Chain: %v. Last error: %v",
				tier, chain, lastErr))
		return lastResult, "", fmt.Errorf("all agents in tier %s exhausted: %w", tier, lastErr)
	}
	return CLIResult{}, "", fmt.Errorf("no agents available in tier %s (all circuit-broken)", tier)
}

// alertAgentFailure records a health event AND sends a Matrix notification.
// This ensures agent failures are NEVER silent.
func (a *Activities) alertAgentFailure(ctx context.Context, agent, tier, eventType, detail string) {
	// Record to DB (health_events table) — queryable, persistent
	if a.Store != nil {
		_ = a.Store.RecordHealthEvent(eventType, detail)
	}

	// Send to Matrix — immediately visible
	if a.Sender != nil && a.AdminRoom != "" {
		emoji := "⚠️"
		switch eventType {
		case "circuit_breaker_tripped", "all_agents_exhausted":
			emoji = "🔴"
		case "model_exhausted":
			emoji = "🟡"
		}
		msg := fmt.Sprintf("%s **%s** | `%s` (tier: %s)\n\n%s",
			emoji, eventType, agent, tier, detail)
		_ = a.Sender.SendMessage(ctx, a.AdminRoom, msg)
	}
}
