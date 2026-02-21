package temporal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.temporal.io/sdk/activity"
)

// PlanningAgents is the team that contributes perspectives during planning.
// Plan space is cheap — get multiple viewpoints before committing to implementation.
var PlanningAgents = []string{"claude", "codex", "gemini"}

// GroomBacklogActivity has the chief analyze the project and identify
// the highest-impact work items. Consults multiple agents for diverse perspectives.
func (a *Activities) GroomBacklogActivity(ctx context.Context, req PlanningRequest) (*BacklogPresentation, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Grooming backlog", "Project", req.Project, "Agent", req.Agent)

	prompt := fmt.Sprintf(`You are a Chief Scrum Master analyzing the backlog for project "%s".

Identify the 3-5 highest-impact work items. For each item, explain:
- WHY it matters (business impact)
- How much EFFORT it requires (low/medium/high)
- Whether you RECOMMEND it as the next focus

Present your strongest recommendation first. Be opinionated — say what you think and why.

Respond with ONLY a JSON object:
{
  "items": [
    {
      "id": "short-slug",
      "title": "one-line title",
      "impact": "why this matters",
      "effort": "low|medium|high",
      "recommended": true,
      "rationale": "why you recommend this (or why not)"
    }
  ],
  "rationale": "Overall: here's what we think the priority should be and why"
}

Start wide — consider all possible areas of improvement. Then rank by impact.`, req.Project)

	agent := ResolveTierAgent(a.Tiers, req.Tier)
	cliResult, err := runAgent(ctx, agent, prompt, req.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("backlog grooming failed: %w", err)
	}

	jsonStr := extractJSON(cliResult.Output)
	if jsonStr == "" {
		return nil, fmt.Errorf("chief did not produce valid JSON backlog. Output:\n%s", truncate(cliResult.Output, 500))
	}

	var backlog BacklogPresentation
	if err := json.Unmarshal([]byte(jsonStr), &backlog); err != nil {
		return nil, fmt.Errorf("failed to parse backlog JSON: %w\nRaw: %s", err, truncate(jsonStr, 500))
	}

	if len(backlog.Items) == 0 {
		return nil, fmt.Errorf("chief produced empty backlog")
	}

	logger.Info("Backlog groomed",
		"Items", len(backlog.Items),
		"TopPick", backlog.Items[0].Title,
	)

	return &backlog, nil
}

// GenerateQuestionsActivity generates clarifying questions for the selected item.
// Questions are sequential — each one builds on knowledge from the previous.
// Consults the planning agent team for diverse perspectives.
func (a *Activities) GenerateQuestionsActivity(ctx context.Context, req PlanningRequest, item BacklogItem) ([]PlanningQuestion, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Generating planning questions", "Item", item.Title)

	prompt := fmt.Sprintf(`You are a senior engineering planner preparing to implement: "%s"

Context: %s
Impact: %s
Effort: %s

Generate 3-5 clarifying questions that MUST be answered before implementation starts.
Each question should:
1. Present clear options (A, B, C)
2. Include your recommendation and WHY
3. Consider tradeoffs (build vs buy, speed vs quality, etc.)

Start wide (architectural choices, approach) then narrow (implementation details).

Respond with ONLY a JSON array:
[
  {
    "question": "the question",
    "options": ["Option A: description", "Option B: description", "Option C: description"],
    "recommendation": "We recommend A because..."
  }
]

Think carefully. These questions prevent wasted tokens and wrong assumptions.`,
		item.Title,
		item.Rationale,
		item.Impact,
		item.Effort,
	)

	agent := ResolveTierAgent(a.Tiers, req.Tier)
	cliResult, err := runAgent(ctx, agent, prompt, req.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("question generation failed: %w", err)
	}

	jsonStr := extractJSONArray(cliResult.Output)
	if jsonStr == "" {
		return nil, fmt.Errorf("agent did not produce valid JSON questions. Output:\n%s", truncate(cliResult.Output, 500))
	}

	var questions []PlanningQuestion
	if err := json.Unmarshal([]byte(jsonStr), &questions); err != nil {
		return nil, fmt.Errorf("failed to parse questions JSON: %w", err)
	}

	if len(questions) == 0 {
		return nil, fmt.Errorf("no questions generated")
	}

	// Cap at 5 questions — keep planning focused
	if len(questions) > 5 {
		questions = questions[:5]
	}

	logger.Info("Questions generated", "Count", len(questions))
	return questions, nil
}

// SummarizePlanActivity produces the final summary: what/why/effort.
// The human reviews this before giving the greenlight.
func (a *Activities) SummarizePlanActivity(ctx context.Context, req PlanningRequest, item BacklogItem, answers map[string]string) (*PlanSummary, error) {
	logger := activity.GetLogger(ctx)
	logger.Info("Summarizing plan", "Item", item.Title)

	// Build context from Q&A
	var qaContext strings.Builder
	for k, v := range answers {
		qaContext.WriteString(fmt.Sprintf("Q%s answer: %s\n", k, v))
	}

	prompt := fmt.Sprintf(`You are a senior engineering planner. Based on the planning discussion, produce a final implementation summary.

SELECTED WORK ITEM: %s
Impact: %s
Effort: %s

PLANNING DECISIONS:
%s

Produce a clear, actionable summary. Respond with ONLY a JSON object:
{
  "what": "Clear description of what we're building — specific, no ambiguity",
  "why": "Business justification — why this matters NOW",
  "effort": "Estimated effort (e.g. '2-3 hours', '1 day', '2-3 days')",
  "risks": ["risk 1", "risk 2"],
  "dod_checks": ["command to verify success 1", "command 2"]
}

Be specific. The implementation team needs to know EXACTLY what to build.`,
		item.Title,
		item.Impact,
		item.Effort,
		qaContext.String(),
	)

	agent := ResolveTierAgent(a.Tiers, req.Tier)
	cliResult, err := runAgent(ctx, agent, prompt, req.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("plan summary failed: %w", err)
	}

	jsonStr := extractJSON(cliResult.Output)
	if jsonStr == "" {
		return nil, fmt.Errorf("agent did not produce valid JSON summary. Output:\n%s", truncate(cliResult.Output, 500))
	}

	var summary PlanSummary
	if err := json.Unmarshal([]byte(jsonStr), &summary); err != nil {
		return nil, fmt.Errorf("failed to parse summary JSON: %w", err)
	}

	logger.Info("Plan summarized",
		"What", summary.What,
		"Effort", summary.Effort,
	)

	return &summary, nil
}

// extractJSONArray finds the first JSON array in text.
func extractJSONArray(text string) string {
	// Try code fences first
	if idx := strings.Index(text, "```json"); idx >= 0 {
		start := idx + 7
		if end := strings.Index(text[start:], "```"); end >= 0 {
			return strings.TrimSpace(text[start : start+end])
		}
	}

	// Find raw array
	start := strings.Index(text, "[")
	if start < 0 {
		return ""
	}
	depth := 0
	for i := start; i < len(text); i++ {
		switch text[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return text[start : i+1]
			}
		}
	}
	return ""
}
