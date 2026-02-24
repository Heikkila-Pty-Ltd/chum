package temporal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.temporal.io/sdk/activity"
)

// FailureTriageActivity analyses a pipeline failure and decides whether to
// retry with specific guidance or rescope the task to turtles/crabs.
//
// It reads the agent's raw output text, combines it with structured failure
// info, and asks a fast LLM to triage. This closes the feedback loop —
// every failure is analysed, not just escalations.
func (a *Activities) FailureTriageActivity(ctx context.Context, req FailureTriageRequest) (*FailureTriageResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(OctopusPrefix+" Triaging failure",
		"TaskID", req.TaskID, "Type", req.FailureType,
		"Attempt", req.Attempt, "MaxRetries", req.MaxRetries)

	// Build context from failure data
	var contextParts []string
	contextParts = append(contextParts,
		fmt.Sprintf("TASK: %s (project: %s)", req.TaskID, req.Project))
	contextParts = append(contextParts,
		fmt.Sprintf("PLAN: %s", req.PlanSummary))
	contextParts = append(contextParts,
		fmt.Sprintf("FAILURE TYPE: %s (attempt %d of %d)", req.FailureType, req.Attempt, req.MaxRetries))

	if len(req.Failures) > 0 {
		contextParts = append(contextParts,
			"STRUCTURED ERRORS:\n"+strings.Join(req.Failures, "\n"))
	}

	if req.AgentOutput != "" {
		contextParts = append(contextParts,
			"AGENT OUTPUT (last portion):\n"+truncate(req.AgentOutput, 4000))
	}

	prompt := fmt.Sprintf(`You are a failure triage system for an AI coding pipeline. A task just failed. Analyse the failure and decide what to do next.

%s

DECISION RULES:
- Choose "retry" if:
  - The error is a fixable code bug (wrong import, missing field, syntax error)
  - The agent made a correctable mistake that specific guidance would fix
  - A DoD check failed but the fix is straightforward (test failure, lint issue)
  - There are retries remaining (attempt %d of %d)

- Choose "rescope" if:
  - The same error has repeated across multiple attempts
  - The task scope is too broad for a single agent session
  - There is a missing prerequisite or infrastructure blocker
  - The error is "command not found" or environment-related (not fixable by the agent)
  - The agent is going in circles or producing empty/minimal output (<1KB)
  - The failure category is "infrastructure" (gateway crash, CLI death, OOM)

Respond with ONLY a JSON object:
{
  "decision": "retry" or "rescope",
  "guidance": "if retry: specific, actionable instruction for the next attempt (e.g. 'Run go test before marking complete', 'Use the store.GetX method instead of raw SQL')",
  "rescope_reason": "if rescope: why this task needs turtle/crab intervention",
  "antibodies": ["1-3 short patterns to remember for future attempts"],
  "category": "infrastructure|logic|scope|complexity"
}`, strings.Join(contextParts, "\n\n"), req.Attempt, req.MaxRetries)

	agent := ResolveTierAgent(a.Tiers, "fast")
	cliResult, err := runAgent(ctx, agent, prompt, req.WorkDir)
	if err != nil {
		logger.Warn(OctopusPrefix+" Failure triage LLM failed (falling back to retry)", "error", err)
		// Fallback: if we can't triage, default to retry
		return &FailureTriageResult{
			Decision: "retry",
			Guidance: "Previous attempt failed. Review the error output carefully before making changes.",
			Category: "unknown",
		}, nil
	}

	jsonStr := extractJSON(cliResult.Output)
	if jsonStr == "" {
		logger.Warn(OctopusPrefix + " Failure triage produced no JSON (falling back to retry)")
		return &FailureTriageResult{
			Decision: "retry",
			Guidance: "Previous attempt failed. Review the error output carefully before making changes.",
			Category: "unknown",
		}, nil
	}

	// Sanitize and parse
	jsonStr = sanitizeLLMJSON(jsonStr)
	var result FailureTriageResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		logger.Warn(OctopusPrefix+" Failure triage JSON parse failed (falling back to retry)", "error", err)
		return &FailureTriageResult{
			Decision: "retry",
			Guidance: "Previous attempt failed. Review the error output carefully before making changes.",
			Category: "unknown",
		}, nil
	}

	// Validate decision
	if result.Decision != "retry" && result.Decision != "rescope" {
		result.Decision = "retry"
	}

	logger.Info(OctopusPrefix+" Failure triaged",
		"Decision", result.Decision,
		"Category", result.Category,
		"Antibodies", len(result.Antibodies))

	return &result, nil
}
