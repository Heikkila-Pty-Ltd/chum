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

	candidateTopK := normalizePlanningCandidateTopK(req.CandidateTopK)
	targetItems := candidateTopK + 2 // keep alternatives beyond the shortlist
	if targetItems > maxPlanningCandidateTopK {
		targetItems = maxPlanningCandidateTopK
	}
	if targetItems < 3 {
		targetItems = 3
	}

	prompt := fmt.Sprintf(`You are a Chief Scrum Master analyzing the backlog for project "%s".

Identify the %d highest-impact work items. For each item, explain:
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

Start wide — consider all possible areas of improvement. Then rank by impact.`, req.Project, targetItems)

	agent := ResolveTierAgent(a.Tiers, req.Tier)
	cliResult, err := runAgent(ctx, agent, prompt, req.WorkDir)
	a.recordPlanningLLMCall(
		ctx,
		req,
		"groom_backlog",
		"",
		agent,
		prompt,
		cliResult,
		err,
		"Backlog grooming call completed",
	)
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

	a.recordPlanningTrace(ctx, PlanningTraceRecord{
		SessionID:   req.TraceSessionID,
		Project:     req.Project,
		TaskID:      backlog.Items[0].ID,
		Cycle:       req.TraceCycle,
		Stage:       "groom_backlog",
		EventType:   "backlog_result",
		Actor:       agent,
		SummaryText: backlog.Rationale,
		FullText:    jsonStr,
	})

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
	a.recordPlanningLLMCall(
		ctx,
		req,
		"generate_questions",
		item.ID,
		agent,
		prompt,
		cliResult,
		err,
		"Question generation call completed",
	)
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

	questionsJSON, _ := json.Marshal(questions)
	a.recordPlanningTrace(ctx, PlanningTraceRecord{
		SessionID:   req.TraceSessionID,
		Project:     req.Project,
		TaskID:      item.ID,
		Cycle:       req.TraceCycle,
		Stage:       "generate_questions",
		EventType:   "questions_result",
		Actor:       agent,
		SummaryText: fmt.Sprintf("Generated %d planning questions", len(questions)),
		FullText:    string(questionsJSON),
	})

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
	a.recordPlanningLLMCall(
		ctx,
		req,
		"summarize_plan",
		item.ID,
		agent,
		prompt,
		cliResult,
		err,
		"Plan summarization call completed",
	)
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

	summaryJSON, _ := json.Marshal(summary)
	a.recordPlanningTrace(ctx, PlanningTraceRecord{
		SessionID:   req.TraceSessionID,
		Project:     req.Project,
		TaskID:      item.ID,
		Cycle:       req.TraceCycle,
		Stage:       "summarize_plan",
		EventType:   "plan_summary_result",
		Actor:       agent,
		SummaryText: summary.What,
		FullText:    string(summaryJSON),
	})

	logger.Info("Plan summarized",
		"What", summary.What,
		"Effort", summary.Effort,
	)

	return &summary, nil
}

// robustParseJSON tries multiple strategies to extract and parse JSON from LLM output.
// This is specifically designed to handle Gemini's escaping quirks which cause
// extractJSON to truncate the JSON after sanitization alters the brace depths.
func robustParseJSON(raw string, target interface{}) error {
	// Strategy 1: Standard path — extractJSON → sanitizeLLMJSON → unmarshal
	if jsonStr := extractJSON(raw); jsonStr != "" {
		sanitized := sanitizeLLMJSON(jsonStr)
		if err := json.Unmarshal([]byte(sanitized), target); err == nil {
			return nil
		}
	}

	// Strategy 2: Sanitize FIRST, then extract
	// (fixes cases where backslashes confuse brace matching)
	sanitizedRaw := sanitizeLLMJSON(raw)
	if jsonStr := extractJSON(sanitizedRaw); jsonStr != "" {
		if err := json.Unmarshal([]byte(jsonStr), target); err == nil {
			return nil
		}
	}

	// Strategy 3: Try nuking all invalid backslashes first, then extract
	nuked := nukeInvalidBackslashes(raw)
	if jsonStr := extractJSON(nuked); jsonStr != "" {
		if err := json.Unmarshal([]byte(jsonStr), target); err == nil {
			return nil
		}
	}

	// Strategy 4: Extract from code fences only (bypass brace matching entirely)
	if idx := strings.Index(raw, "```json"); idx >= 0 {
		start := idx + 7
		if end := strings.Index(raw[start:], "```"); end >= 0 {
			fenced := strings.TrimSpace(raw[start : start+end])
			sanitized := sanitizeLLMJSON(fenced)
			if err := json.Unmarshal([]byte(sanitized), target); err == nil {
				return nil
			}
		}
	}

	// Strategy 5: Repair truncated JSON (LLM output cut off at context limit)
	// Try extracting whatever JSON-like content we can find and close unclosed braces.
	if jsonStr := extractJSON(raw); jsonStr != "" {
		sanitized := sanitizeLLMJSON(jsonStr)
		repaired := repairTruncatedJSONArray(sanitized) // works for objects too
		if repaired != sanitized {
			if err := json.Unmarshal([]byte(repaired), target); err == nil {
				return nil
			}
		}
		// Also try extracting first complete object
		if first := extractFirstCompleteJSONObject(sanitized); first != "" {
			if err := json.Unmarshal([]byte(first), target); err == nil {
				return nil
			}
		}
	}

	// All strategies failed — return the most informative error
	jsonStr := extractJSON(raw)
	if jsonStr == "" {
		return fmt.Errorf("no JSON found in output (%d bytes)", len(raw))
	}
	sanitized := sanitizeLLMJSON(jsonStr)
	return json.Unmarshal([]byte(sanitized), target)
}

// robustParseJSONArray is like robustParseJSON but for JSON arrays.
func robustParseJSONArray(raw string, target interface{}) error {
	// Strategy 1: Standard path
	if jsonStr := extractJSONArray(raw); jsonStr != "" {
		sanitized := sanitizeLLMJSON(jsonStr)
		if err := json.Unmarshal([]byte(sanitized), target); err == nil {
			return nil
		}
	}

	// Strategy 2: Sanitize first, then extract
	sanitizedRaw := sanitizeLLMJSON(raw)
	if jsonStr := extractJSONArray(sanitizedRaw); jsonStr != "" {
		if err := json.Unmarshal([]byte(jsonStr), target); err == nil {
			return nil
		}
	}

	// Strategy 3: Nuke backslashes, then extract
	nuked := nukeInvalidBackslashes(raw)
	if jsonStr := extractJSONArray(nuked); jsonStr != "" {
		if err := json.Unmarshal([]byte(jsonStr), target); err == nil {
			return nil
		}
	}

	// All failed
	jsonStr := extractJSONArray(raw)
	if jsonStr == "" {
		return fmt.Errorf("no JSON array found in output (%d bytes)", len(raw))
	}
	return json.Unmarshal([]byte(sanitizeLLMJSON(jsonStr)), target)
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

// sanitizeLLMJSON cleans up common LLM JSON output quirks:
//   - Literal newlines/tabs/carriage-returns inside JSON string values → escaped
//   - Double-escaped sequences (\\n → \n in the JSON sense)
//   - Stray backslashes outside JSON strings (Gemini pattern)
//   - Leading/trailing whitespace
//
// LLMs (especially Gemini) produce JSON with various escaping issues.
// This function applies progressively more aggressive fixes until json.Valid passes.
func sanitizeLLMJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}

	// Fast path — if already valid, return immediately.
	if json.Valid([]byte(raw)) {
		return raw
	}

	// Phase 1: Fix double-escaped sequences that came from CLI piping.
	// Gemini frequently wraps output in extra escape layers.
	cleaned := raw
	if strings.Contains(cleaned, "\\\\") {
		cleaned = fixDoubleEscapes(cleaned)
		if json.Valid([]byte(cleaned)) {
			return cleaned
		}
	}

	// Phase 2: Walk char-by-char, fix control chars inside strings AND
	// stray backslashes outside strings.
	cleaned = fixJSONChars(raw)
	if json.Valid([]byte(cleaned)) {
		return cleaned
	}

	// Phase 3: Try on the double-escaped version too.
	cleaned = fixJSONChars(fixDoubleEscapes(raw))
	if json.Valid([]byte(cleaned)) {
		return cleaned
	}

	// Phase 4: Nuclear option — strip ALL backslashes that aren't valid JSON
	// escape sequences. This is aggressive but catches Gemini's edge cases.
	cleaned = nukeInvalidBackslashes(raw)
	if json.Valid([]byte(cleaned)) {
		return cleaned
	}

	// Give up — return the best effort (Phase 2 result, which at least
	// handles the most common issues).
	return fixJSONChars(raw)
}

// fixDoubleEscapes converts double-escaped sequences to single-escaped.
// Handles: \\n → \n, \\t → \t, \\" → \"
func fixDoubleEscapes(s string) string {
	s = strings.ReplaceAll(s, "\\\\n", "\\n")
	s = strings.ReplaceAll(s, "\\\\t", "\\t")
	s = strings.ReplaceAll(s, "\\\\r", "\\r")
	s = strings.ReplaceAll(s, "\\\\\"", "\\\"")
	return s
}

// fixJSONChars walks char-by-char, fixing:
// - Literal control chars inside strings → proper JSON escapes
// - Stray backslashes outside strings → removed
func fixJSONChars(raw string) string {
	var out strings.Builder
	out.Grow(len(raw))
	inString := false

	for i := 0; i < len(raw); i++ {
		ch := raw[i]

		if ch == '\\' && inString && i+1 < len(raw) {
			next := raw[i+1]
			// Valid JSON escapes: " \ / b f n r t u
			switch next {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't', 'u':
				out.WriteByte(ch)
				out.WriteByte(next)
				i++
				continue
			default:
				// Invalid escape inside string — double-escape the backslash
				out.WriteString("\\\\")
				continue
			}
		}

		if ch == '\\' && !inString {
			// Stray backslash outside a string — skip it entirely.
			// This is Gemini's main issue: putting \n or \ in value positions.
			if i+1 < len(raw) {
				next := raw[i+1]
				// If it's \n, \t, \r outside a string, skip both chars
				// (they're meaningless whitespace in JSON grammar)
				if next == 'n' || next == 't' || next == 'r' {
					i++
					continue
				}
			}
			continue
		}

		if ch == '"' {
			inString = !inString
			out.WriteByte(ch)
			continue
		}

		if inString {
			switch ch {
			case '\n':
				out.WriteString("\\n")
			case '\r':
				out.WriteString("\\r")
			case '\t':
				out.WriteString("\\t")
			default:
				if ch < 0x20 {
					out.WriteString(fmt.Sprintf("\\u%04x", ch))
				} else {
					out.WriteByte(ch)
				}
			}
		} else {
			out.WriteByte(ch)
		}
	}

	return out.String()
}

// nukeInvalidBackslashes removes ALL backslash sequences that aren't valid
// JSON escapes, both inside and outside strings. This is the most aggressive
// sanitizer — only used as a last resort.
func nukeInvalidBackslashes(raw string) string {
	var out strings.Builder
	out.Grow(len(raw))
	inString := false

	for i := 0; i < len(raw); i++ {
		ch := raw[i]

		if ch == '"' && (i == 0 || raw[i-1] != '\\') {
			inString = !inString
			out.WriteByte(ch)
			continue
		}

		if ch == '\\' && i+1 < len(raw) {
			next := raw[i+1]
			if inString {
				switch next {
				case '"', '\\', '/', 'b', 'f', 'n', 'r', 't', 'u':
					out.WriteByte(ch)
					out.WriteByte(next)
					i++
				default:
					// Skip invalid escape entirely
					i++
				}
			} else {
				// Skip backslash outside string
				continue
			}
			continue
		}

		if inString && ch < 0x20 {
			switch ch {
			case '\n':
				out.WriteString("\\n")
			case '\r':
				out.WriteString("\\r")
			case '\t':
				out.WriteString("\\t")
			default:
				out.WriteString(fmt.Sprintf("\\u%04x", ch))
			}
		} else {
			out.WriteByte(ch)
		}
	}
	return out.String()
}
