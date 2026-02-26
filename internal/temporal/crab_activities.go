package temporal

import (
	"context"
	"fmt"
	"strings"

	"go.temporal.io/sdk/activity"

	"github.com/antigravity-dev/chum/internal/graph"
)

// ParsePlanActivity performs deterministic markdown parsing of the plan.
// No LLM involved — pure string processing.
func (a *Activities) ParsePlanActivity(ctx context.Context, req CrabDecompositionRequest) (*ParsedPlan, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(CrabPrefix+" Parsing plan", "PlanID", req.PlanID)

	plan, err := ParseMarkdownPlan(req.PlanMarkdown)
	if err != nil {
		return nil, fmt.Errorf("parse plan: %w", err)
	}

	logger.Info(CrabPrefix+" Plan parsed",
		"Title", plan.Title,
		"ScopeItems", len(plan.ScopeItems),
		"AcceptanceCriteria", len(plan.AcceptanceCriteria),
	)

	return plan, nil
}

// ClarifyGapsActivity performs 3-tier clarification to resolve ambiguities
// in the plan before decomposition.
//
// Tier 1 — Self-answer: query lessons DB and existing morsels for context.
// Tier 2 — Ask Chief: use LLM to resolve remaining gaps.
// Tier 3 — Escalate: flag questions that need human input.
func (a *Activities) ClarifyGapsActivity(ctx context.Context, req CrabDecompositionRequest, plan ParsedPlan) (*ClarificationResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(CrabPrefix+" Clarifying gaps", "PlanID", req.PlanID, "ScopeItems", len(plan.ScopeItems))

	result := &ClarificationResult{}

	// --- Tier 1: Self-answer from lessons DB and existing morsels ---
	activity.RecordHeartbeat(ctx, "tier-1-self-answer")

	var existingMorselsSummary strings.Builder
	if a.DAG != nil {
		allTasks, err := a.DAG.ListTasks(ctx, req.Project)
		if err != nil {
			logger.Warn(CrabPrefix+" Failed to list tasks for overlap check", "error", err)
		} else {
			for i := range allTasks {
				t := &allTasks[i]
				if t.Status == "open" {
					existingMorselsSummary.WriteString(fmt.Sprintf("- [%s] %s: %s\n", t.Type, t.ID, t.Title))
				}
			}
		}
	}

	var unresolvedItems []ScopeItem
	for _, item := range plan.ScopeItems {
		if item.Completed {
			continue
		}

		resolved := false
		if a.Store != nil {
			lessons, err := a.Store.SearchLessons(item.Description, 5)
			if err != nil {
				logger.Warn(CrabPrefix+" Lessons search failed for scope item", "index", item.Index, "error", err)
			} else if len(lessons) > 0 {
				var answerParts []string
				for i := range lessons {
					answerParts = append(answerParts, fmt.Sprintf("[%s] %s", lessons[i].Category, lessons[i].Summary))
				}
				result.Resolved = append(result.Resolved, ClarificationEntry{
					Question: fmt.Sprintf("Context for scope item: %s", item.Description),
					Answer:   strings.Join(answerParts, "; "),
					Source:   "lessons_db",
				})
				resolved = true
			}
		}

		if !resolved {
			unresolvedItems = append(unresolvedItems, item)
		}
	}

	logger.Info(CrabPrefix+" Tier 1 complete",
		"Resolved", len(result.Resolved),
		"Unresolved", len(unresolvedItems),
	)

	// If everything is resolved, we're done.
	if len(unresolvedItems) == 0 {
		return result, nil
	}

	// --- Tier 2: Ask Chief LLM to resolve remaining gaps ---
	activity.RecordHeartbeat(ctx, "tier-2-ask-chief")

	var ambiguityList strings.Builder
	for _, item := range unresolvedItems {
		ambiguityList.WriteString(fmt.Sprintf("- %s\n", item.Description))
	}

	var resolvedContext strings.Builder
	for _, entry := range result.Resolved {
		resolvedContext.WriteString(fmt.Sprintf("- Q: %s -> A: %s (source: %s)\n", entry.Question, entry.Answer, entry.Source))
	}

	prompt := fmt.Sprintf(`You are a senior engineering planner clarifying a decomposition plan.

PLAN: %s
CONTEXT: %s

ALREADY RESOLVED:
%s

EXISTING MORSELS IN PROJECT:
%s

UNRESOLVED SCOPE ITEMS (need clarification):
%s

For each unresolved item, either:
1. Provide a clear answer based on context, resolved items, and existing morsels
2. If you cannot answer, respond with "NEEDS_HUMAN: <specific question>"

Respond with ONLY a JSON array:
[
  {
    "scope_item": "the scope item description",
    "answer": "your clarification or NEEDS_HUMAN: <question>",
    "source": "chief_llm"
  }
]`,
		plan.Title,
		truncate(plan.Context, 2000),
		resolvedContext.String(),
		truncate(existingMorselsSummary.String(), 2000),
		ambiguityList.String(),
	)

	cliResult, _, err := a.runAgentWithFailover(ctx, req.Tier, prompt, req.WorkDir)
	if err != nil {
		logger.Warn(CrabPrefix+" Chief LLM clarification failed", "error", err)
		// Fall through to tier 3 with all unresolved items.
	} else {
		result.Tokens = cliResult.Tokens

		jsonStr := extractJSONArray(cliResult.Output)
		if jsonStr != "" {
			var chiefAnswers []struct {
				ScopeItem string `json:"scope_item"`
				Answer    string `json:"answer"`
				Source    string `json:"source"`
			}
			if parseErr := robustParseJSONArray(jsonStr, &chiefAnswers); parseErr != nil {
				logger.Warn(CrabPrefix+" Failed to parse chief clarification JSON", "error", parseErr)
			} else {
				var stillUnresolved []ScopeItem
				answeredSet := make(map[string]bool)

				for _, ca := range chiefAnswers {
					if strings.HasPrefix(ca.Answer, "NEEDS_HUMAN:") {
						// Will be handled in tier 3.
						continue
					}
					result.Resolved = append(result.Resolved, ClarificationEntry{
						Question: fmt.Sprintf("Context for scope item: %s", ca.ScopeItem),
						Answer:   ca.Answer,
						Source:   "chief_llm",
					})
					answeredSet[ca.ScopeItem] = true
				}

				for _, item := range unresolvedItems {
					if !answeredSet[item.Description] {
						stillUnresolved = append(stillUnresolved, item)
					}
				}
				unresolvedItems = stillUnresolved
			}
		}
	}

	logger.Info(CrabPrefix+" Tier 2 complete", "StillUnresolved", len(unresolvedItems))

	// --- Tier 3: Escalate to human ---
	if len(unresolvedItems) > 0 {
		result.NeedsHumanInput = true
		for _, item := range unresolvedItems {
			result.HumanQuestions = append(result.HumanQuestions,
				fmt.Sprintf("Please clarify scope item: %s", item.Description))
		}
		logger.Info(CrabPrefix+" Tier 3: escalating to human", "Questions", len(result.HumanQuestions))
	}

	return result, nil
}

// DecomposeActivity uses an LLM to decompose the parsed plan into candidate
// whales and morsels. This is the core creative decomposition step.
func (a *Activities) DecomposeActivity(ctx context.Context, req CrabDecompositionRequest, plan ParsedPlan, clarifications ClarificationResult) ([]CandidateWhale, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(CrabPrefix+" Decomposing plan", "PlanID", req.PlanID, "Title", plan.Title)

	activity.RecordHeartbeat(ctx, "building-decomposition-prompt")

	// Build scope items summary.
	var scopeList strings.Builder
	for _, item := range plan.ScopeItems {
		status := "[ ]"
		if item.Completed {
			status = "[x]"
		}
		scopeList.WriteString(fmt.Sprintf("- %s %s\n", status, item.Description))
	}

	// Build clarification context.
	var clarificationContext strings.Builder
	for _, entry := range clarifications.Resolved {
		clarificationContext.WriteString(fmt.Sprintf("- Q: %s\n  A: %s (source: %s)\n", entry.Question, entry.Answer, entry.Source))
	}
	if clarifications.HumanAnswers != "" {
		clarificationContext.WriteString(fmt.Sprintf("\nHuman clarifications:\n%s\n", clarifications.HumanAnswers))
	}

	// Build existing morsels summary.
	var existingMorsels strings.Builder
	if a.DAG != nil {
		allTasks, err := a.DAG.ListTasks(ctx, req.Project)
		if err != nil {
			logger.Warn(CrabPrefix+" Failed to list existing tasks", "error", err)
		} else {
			openCount := 0
			for i := range allTasks {
				t := &allTasks[i]
				if t.Status == "open" {
					existingMorsels.WriteString(fmt.Sprintf("- [%s|P%d] %s: %s\n", t.Type, t.Priority, t.ID, t.Title))
					openCount++
					if openCount >= 30 {
						existingMorsels.WriteString("... and more open tasks\n")
						break
					}
				}
			}
		}
	}

	// Build acceptance criteria.
	var acList strings.Builder
	for _, ac := range plan.AcceptanceCriteria {
		acList.WriteString(fmt.Sprintf("- %s\n", ac))
	}

	// Build out-of-scope.
	var oosList strings.Builder
	for _, oos := range plan.OutOfScope {
		oosList.WriteString(fmt.Sprintf("- %s\n", oos))
	}

	prompt := fmt.Sprintf(`You are a senior engineering decomposer. Break this plan into whales (epic-level groupings) and morsels (bite-sized executable units).

PLAN: %s
CONTEXT: %s

SCOPE ITEMS:
%s

ACCEPTANCE CRITERIA:
%s

OUT OF SCOPE:
%s

CLARIFICATIONS:
%s

EXISTING MORSELS IN PROJECT:
%s

Rules:
1. Each whale maps to one or more scope items
2. Each morsel must be independently executable by a single agent in one session
3. Morsels should be 15-120 minutes of work
4. Include file hints where possible
5. Specify dependencies between morsels (by index)
6. Do NOT duplicate work already covered by existing morsels
7. STRUCTURAL/FEATURE SPLIT: If a morsel touches >5 files, split it into TWO morsels:
   a. A "structural" morsel: zero behavior change (rename, move, re-signature, add unused fields). All existing tests must pass unchanged.
   b. A "feature" morsel: wires the new behavior, adds new tests. Depends on the structural morsel.
   This pattern applies to: DI refactors, schema migrations, config additions, interface extractions, package moves, observability wiring.

Respond with ONLY a JSON array of whales:
[
  {
    "index": 0,
    "title": "whale title",
    "description": "what this whale covers",
    "acceptance_criteria": "how to verify the whale is done",
    "parent_scope_item": 0,
    "morsels": [
      {
        "index": 0,
        "title": "morsel title",
        "description": "specific work to do",
        "acceptance_criteria": "how to verify",
        "design_hints": "approach and key considerations",
        "file_hints": ["path/to/file.go"],
        "depends_on_indices": []
      }
    ]
  }
]`,
		plan.Title,
		truncate(plan.Context, 2000),
		scopeList.String(),
		acList.String(),
		oosList.String(),
		clarificationContext.String(),
		truncate(existingMorsels.String(), 2000),
	)

	activity.RecordHeartbeat(ctx, "calling-llm-decompose")

	cliResult, _, err := a.runAgentWithFailover(ctx, req.Tier, prompt, req.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("decomposition LLM call failed: %w", err)
	}

	jsonStr := extractJSONArray(cliResult.Output)
	if jsonStr == "" {
		return nil, fmt.Errorf("decomposition did not produce valid JSON. Output:\n%s", truncate(cliResult.Output, 500))
	}

	sanitized := sanitizeLLMJSON(jsonStr)
	var whales []CandidateWhale
	if err := robustParseJSONArray(sanitized, &whales); err != nil {
		return nil, fmt.Errorf("failed to parse decomposition JSON: %w\nRaw: %s", err, truncate(sanitized, 500))
	}

	if len(whales) == 0 {
		return nil, fmt.Errorf("decomposition produced zero whales")
	}

	// Cap total morsels to prevent runaway output.
	totalMorsels := 0
	for i := range whales {
		totalMorsels += len(whales[i].Morsels)
	}
	if totalMorsels > 50 {
		logger.Warn(CrabPrefix+" Decomposition produced excessive morsels, capping",
			"Total", totalMorsels, "Cap", 50)
		remaining := 50
		for i := range whales {
			if remaining <= 0 {
				whales[i].Morsels = nil
				continue
			}
			if len(whales[i].Morsels) > remaining {
				whales[i].Morsels = whales[i].Morsels[:remaining]
			}
			remaining -= len(whales[i].Morsels)
		}
	}

	logger.Info(CrabPrefix+" Decomposition complete",
		"Whales", len(whales),
		"TotalMorsels", totalMorsels,
	)

	return whales, nil
}

// ScopeMorselsActivity reviews the scope of each morsel using lessons DB
// context and an LLM, splitting oversized morsels where appropriate.
func (a *Activities) ScopeMorselsActivity(ctx context.Context, req CrabDecompositionRequest, whales []CandidateWhale) ([]CandidateWhale, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(CrabPrefix+" Scoping morsels", "Whales", len(whales))

	// Query lessons DB for scope-related observations.
	var lessonsContext strings.Builder
	if a.Store != nil {
		lessons, err := a.Store.SearchLessons("scope too-large OR underestimated OR missing-deps", 10)
		if err != nil {
			logger.Warn(CrabPrefix+" Failed to search scope lessons", "error", err)
		} else if len(lessons) > 0 {
			lessonsContext.WriteString("HISTORICAL SCOPE LESSONS:\n")
			for i := range lessons {
				lessonsContext.WriteString(fmt.Sprintf("- [%s] %s\n", lessons[i].Category, lessons[i].Summary))
			}
		}
	}

	// Build morsel summary for review.
	var morselSummary strings.Builder
	for _, whale := range whales {
		morselSummary.WriteString(fmt.Sprintf("\nWhale %d: %s\n", whale.Index, whale.Title))
		for _, morsel := range whale.Morsels {
			morselSummary.WriteString(fmt.Sprintf("  Morsel %d: %s\n    Desc: %s\n    Files: %s\n",
				morsel.Index, morsel.Title, truncate(morsel.Description, 200),
				strings.Join(morsel.FileHints, ", ")))
		}
	}

	prompt := fmt.Sprintf(`You are a senior engineering scope reviewer. Review each morsel and ensure it's appropriately sized.

%s

CURRENT MORSELS:
%s

Rules:
1. Each morsel should be 15-120 minutes of work for a single agent
2. If a morsel is too large, split it into smaller morsels
3. If a morsel is too small, note it but don't merge (merging risks scope creep)
4. Preserve dependency indices (update if splits occur)
5. Preserve all existing fields
6. STRUCTURAL/FEATURE SPLIT: Any morsel touching >5 files MUST be split into:
   a. A structural morsel (zero behavior change, all tests pass unchanged)
   b. A feature morsel (new behavior, new tests, depends on structural morsel)
   The structural morsel should be described without using "and" — one concern only.

Respond with ONLY a JSON array of whales (same format as input, with adjustments):
[
  {
    "index": 0,
    "title": "whale title",
    "description": "description",
    "acceptance_criteria": "criteria",
    "parent_scope_item": 0,
    "morsels": [
      {
        "index": 0,
        "title": "morsel title",
        "description": "description",
        "acceptance_criteria": "criteria",
        "design_hints": "hints",
        "file_hints": ["file.go"],
        "depends_on_indices": []
      }
    ]
  }
]`,
		lessonsContext.String(),
		morselSummary.String(),
	)

	cliResult, _, err := a.runAgentWithFailover(ctx, req.Tier, prompt, req.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("scope review LLM call failed: %w", err)
	}

	jsonStr := extractJSONArray(cliResult.Output)
	if jsonStr == "" {
		return nil, fmt.Errorf("scope review did not produce valid JSON. Output:\n%s", truncate(cliResult.Output, 500))
	}

	var scopedWhales []CandidateWhale
	if err := robustParseJSONArray(jsonStr, &scopedWhales); err != nil {
		return nil, fmt.Errorf("failed to parse scope review JSON: %w\nRaw: %s", err, truncate(jsonStr, 500))
	}

	logger.Info(CrabPrefix+" Scoping complete", "Whales", len(scopedWhales))
	return scopedWhales, nil
}

// SizeMorselsActivity estimates effort, assigns priority and risk levels to
// each morsel using lessons DB context and LLM analysis.
func (a *Activities) SizeMorselsActivity(ctx context.Context, req CrabDecompositionRequest, whales []CandidateWhale) ([]SizedMorsel, error) {
	logger := activity.GetLogger(ctx)

	totalMorsels := 0
	for i := range whales {
		totalMorsels += len(whales[i].Morsels)
	}
	logger.Info(CrabPrefix+" Sizing morsels", "Whales", len(whales), "TotalMorsels", totalMorsels)

	// Gather historical context for each morsel.
	var historicalContext strings.Builder
	for _, whale := range whales {
		for _, morsel := range whale.Morsels {
			if a.Store != nil {
				lessons, err := a.Store.SearchLessons(morsel.Title, 3)
				if err == nil && len(lessons) > 0 {
					historicalContext.WriteString(fmt.Sprintf("Morsel '%s' related lessons:\n", morsel.Title))
					for i := range lessons {
						historicalContext.WriteString(fmt.Sprintf("  - [%s] %s\n", lessons[i].Category, lessons[i].Summary))
					}
				}

				if len(morsel.FileHints) > 0 {
					fileLessons, fileErr := a.Store.SearchLessonsByFilePath(morsel.FileHints, 3)
					if fileErr == nil && len(fileLessons) > 0 {
						historicalContext.WriteString(fmt.Sprintf("  File-specific lessons for %s:\n", strings.Join(morsel.FileHints, ", ")))
						for i := range fileLessons {
							historicalContext.WriteString(fmt.Sprintf("  - %s: %s\n",
								strings.Join(fileLessons[i].FilePaths, ", "), fileLessons[i].Summary))
						}
					}
				}
			}
		}
	}

	// Build morsel list for sizing.
	var morselList strings.Builder
	for _, whale := range whales {
		for _, morsel := range whale.Morsels {
			morselList.WriteString(fmt.Sprintf("Whale %d / Morsel %d: %s\n  Description: %s\n  Design hints: %s\n  Files: %s\n  Depends on: %v\n\n",
				whale.Index, morsel.Index, morsel.Title,
				truncate(morsel.Description, 300),
				truncate(morsel.DesignHints, 200),
				strings.Join(morsel.FileHints, ", "),
				morsel.DependsOnIndices,
			))
		}
	}

	prompt := fmt.Sprintf(`You are a senior engineering estimator. Size each morsel with effort, priority, risk, and inferred dependencies.

MORSELS TO SIZE:
%s

HISTORICAL CONTEXT:
%s

For each morsel, provide:
1. estimate_minutes: realistic estimate (15-120 min range typical)
2. priority: 1 (critical) to 4 (low)
3. risk_level: "low", "medium", or "high"
4. sizing_rationale: brief explanation of the estimate
5. labels: relevant labels (e.g. "source:crab", "risk:high")
6. design: expanded design notes incorporating historical context
7. depends_on_indices: confirmed/adjusted dependency indices

Respond with ONLY a JSON array of sized morsels:
[
  {
    "title": "morsel title",
    "description": "description",
    "acceptance_criteria": "criteria",
    "design": "expanded design notes",
    "estimate_minutes": 60,
    "priority": 2,
    "labels": ["source:crab"],
    "file_hints": ["file.go"],
    "depends_on_indices": [],
    "whale_index": 0,
    "risk_level": "medium",
    "sizing_rationale": "why this estimate"
  }
]`,
		morselList.String(),
		truncate(historicalContext.String(), 3000),
	)

	cliResult, _, err := a.runAgentWithFailover(ctx, req.Tier, prompt, req.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("sizing LLM call failed: %w", err)
	}

	jsonStr := extractJSONArray(cliResult.Output)
	if jsonStr == "" {
		return nil, fmt.Errorf("sizing did not produce valid JSON. Output:\n%s", truncate(cliResult.Output, 500))
	}

	var sizedMorsels []SizedMorsel
	if err := robustParseJSONArray(jsonStr, &sizedMorsels); err != nil {
		return nil, fmt.Errorf("failed to parse sizing JSON: %w\nRaw: %s", err, truncate(jsonStr, 500))
	}

	if len(sizedMorsels) == 0 {
		return nil, fmt.Errorf("sizing produced zero morsels")
	}

	// Cap at 50 morsels.
	if len(sizedMorsels) > 50 {
		logger.Warn(CrabPrefix+" Sizing produced excessive morsels, capping", "Total", len(sizedMorsels), "Cap", 50)
		sizedMorsels = sizedMorsels[:50]
	}

	logger.Info(CrabPrefix+" Sizing complete", "SizedMorsels", len(sizedMorsels))
	return sizedMorsels, nil
}

// EmitMorselsActivity writes the approved whales and morsels to the DAG.
// No LLM involved — pure DAG writes.
func (a *Activities) EmitMorselsActivity(ctx context.Context, req CrabDecompositionRequest, whales []CandidateWhale, morsels []SizedMorsel) (*EmitResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(CrabPrefix+" Emitting to DAG", "Whales", len(whales), "Morsels", len(morsels))

	if a.DAG == nil {
		return nil, fmt.Errorf("DAG is not initialized")
	}

	result := &EmitResult{}

	// Step 1: Create whale tasks.
	whaleIDMap := make(map[int]string) // whale index -> created task ID
	for _, whale := range whales {
		parentID := req.ParentWhaleID
		labels := []string{"source:crab", fmt.Sprintf("plan:%s", req.PlanID)}

		whaleID, err := a.DAG.CreateTask(ctx, graph.Task{
			Title:       whale.Title,
			Description: whale.Description,
			Type:        "whale",
			Priority:    1,
			ParentID:    parentID,
			Acceptance:  whale.AcceptanceCriteria,
			Labels:      labels,
			Project:     req.Project,
		})
		if err != nil {
			result.FailedCount++
			result.Details = append(result.Details, fmt.Sprintf("FAILED create whale %q: %v", whale.Title, err))
			logger.Warn(CrabPrefix+" Failed to create whale", "title", whale.Title, "error", err)
			continue
		}

		whaleIDMap[whale.Index] = whaleID
		result.WhaleIDs = append(result.WhaleIDs, whaleID)
		result.Details = append(result.Details, fmt.Sprintf("OK create whale %q -> %s", whale.Title, whaleID))
	}

	// Step 2: Create morsel tasks.
	morselIDMap := make(map[int]string) // flat morsel index -> created task ID
	for i, morsel := range morsels {
		whaleID := whaleIDMap[morsel.WhaleIndex]
		if whaleID == "" {
			// Whale creation failed; skip morsel.
			result.FailedCount++
			result.Details = append(result.Details, fmt.Sprintf("SKIPPED morsel %q: parent whale %d not created", morsel.Title, morsel.WhaleIndex))
			continue
		}

		labels := append(morsel.Labels, "source:crab", fmt.Sprintf("plan:%s", req.PlanID))
		if morsel.RiskLevel == "high" {
			labels = append(labels, "risk:high")
		}

		morselID, err := a.DAG.CreateTask(ctx, graph.Task{
			Title:           morsel.Title,
			Description:     morsel.Description,
			Type:            "morsel",
			Priority:        morsel.Priority,
			ParentID:        whaleID,
			Acceptance:      morsel.AcceptanceCriteria,
			Design:          morsel.Design,
			EstimateMinutes: morsel.EstimateMinutes,
			Labels:          labels,
			Project:         req.Project,
		})
		if err != nil {
			result.FailedCount++
			result.Details = append(result.Details, fmt.Sprintf("FAILED create morsel %q: %v", morsel.Title, err))
			logger.Warn(CrabPrefix+" Failed to create morsel", "title", morsel.Title, "error", err)
			continue
		}

		morselIDMap[i] = morselID
		result.MorselIDs = append(result.MorselIDs, morselID)
		result.Details = append(result.Details, fmt.Sprintf("OK create morsel %q -> %s (whale: %s)", morsel.Title, morselID, whaleID))
	}

	// Step 3: Add dependency edges between morsels.
	for i, morsel := range morsels {
		fromID := morselIDMap[i]
		if fromID == "" {
			continue
		}
		for _, depIdx := range morsel.DependsOnIndices {
			toID := morselIDMap[depIdx]
			if toID == "" {
				result.Details = append(result.Details, fmt.Sprintf("SKIPPED edge %d->%d: target morsel not created", i, depIdx))
				continue
			}
			if err := a.DAG.AddEdge(ctx, fromID, toID); err != nil {
				result.FailedCount++
				result.Details = append(result.Details, fmt.Sprintf("FAILED edge %s->%s: %v", fromID, toID, err))
				logger.Warn(CrabPrefix+" Failed to add dependency edge",
					"from", fromID, "to", toID, "error", err)
			} else {
				result.Details = append(result.Details, fmt.Sprintf("OK edge %s->%s", fromID, toID))
			}
		}
	}

	logger.Info(CrabPrefix+" Emission complete",
		"WhalesCreated", len(result.WhaleIDs),
		"MorselsCreated", len(result.MorselIDs),
		"Failed", result.FailedCount,
	)

	return result, nil
}
