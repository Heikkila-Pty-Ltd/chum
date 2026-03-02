package temporal

import "fmt"

// iterationBudgetPrompt returns the prompt section that makes agents aware
// of their tool-call iteration budget and instructs them to wrap up gracefully.
//
// At iteration (max - IterationWrapUpMargin), the agent should stop calling
// tools and produce a structured summary of what was accomplished and what remains.
//
// maxIterations is the configured budget (from config or DefaultMaxAgentIterations).
func iterationBudgetPrompt(maxIterations int) string {
	if maxIterations <= 0 {
		maxIterations = DefaultMaxAgentIterations
	}
	wrapUpAt := maxIterations - IterationWrapUpMargin
	if wrapUpAt < 1 {
		wrapUpAt = 1
	}

	return fmt.Sprintf(`ITERATION BUDGET: You have a maximum of %d tool-call iterations for this task. `+
		`Track your iteration count. At iteration %d, STOP calling tools and produce a wrap-up summary with exactly this format:

--- WRAP-UP ---
COMPLETED:
- (list what you accomplished)

REMAINING:
- (list what still needs to be done)

FILES MODIFIED:
- (list files you changed)
---

This is non-negotiable. Do not burn your last iterations on speculative tool calls. `+
		`Use them to verify your work compiles/passes, then wrap up.`, maxIterations, wrapUpAt)
}

// getMaxAgentIterations returns the configured max iterations, falling back to the default.
func getMaxAgentIterations(a *Activities) int {
	if a.CfgMgr != nil {
		if cfg := a.CfgMgr.Get(); cfg != nil && cfg.General.MaxAgentIterations > 0 {
			return cfg.General.MaxAgentIterations
		}
	}
	return DefaultMaxAgentIterations
}
