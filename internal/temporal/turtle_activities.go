package temporal

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"go.temporal.io/sdk/activity"
)

// TurtlePlanActivity runs a single LLM call to produce a high-level plan.
// This is the simplified replacement for the 3-phase ceremony (Explore→Deliberate→Converge).
// One agent analyzes the task and produces a TurtleConsensus directly.
func (a *Activities) TurtlePlanActivity(ctx context.Context, req TurtlePlanningRequest) (*TurtleConsensus, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(TurtlePrefix+" Single-stage planning", "TaskID", req.TaskID, "Project", req.Project)

	prompt := fmt.Sprintf(`You are a senior engineering planner. Analyze this task and produce a structured plan.

TASK: %s

PROJECT: %s

DESCRIPTION:
%s

Analyze the task thoroughly. Consider:
- What files/functions need to change?
- What are the risks and dependencies?
- What's the simplest path to a working solution?
- How should the work be broken into bite-sized deliverables (15-30 min each)?

Produce a JSON object with:
{
  "merged_plan": "Your implementation plan in 3-5 paragraphs. Be specific about files, patterns, and approach.",
  "confidence_score": 85,
  "items": [
    {
      "title": "Deliverable title",
      "description": "What needs to be built — specific enough for an AI agent to execute",
      "confidence": 90,
      "effort": "small|medium|large"
    }
  ],
  "disagreements": []
}

IMPORTANT: Output ONLY the JSON object, no markdown fences.
The confidence_score is 0-100 representing how confident you are in this plan.
Each item should be a self-contained unit of work.`, req.TaskID, req.Project, req.Description)

	agent := ResolveTierAgent(a.Tiers, req.Tier)
	if agent == "" {
		agent = ResolveTierAgent(a.Tiers, "balanced")
	}
	cliResult, err := a.runAgent(ctx, agent, prompt, req.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("planning failed: %w", err)
	}

	var consensus TurtleConsensus
	if err := robustParseJSON(cliResult.Output, &consensus); err != nil {
		logger.Warn(TurtlePrefix+" Plan JSON parse failed, returning raw plan",
			"error", err, "OutputLen", len(cliResult.Output))
		// Fallback: wrap the raw output as a single-item plan
		consensus = TurtleConsensus{
			MergedPlan:      cliResult.Output,
			ConfidenceScore: 50,
			Items: []ConsensusItem{{
				Title:       req.TaskID,
				Description: "(auto-generated from unparseable LLM output)",
				Confidence:  50,
				Effort:      "medium",
			}},
		}
	}

	logger.Info(TurtlePrefix+" Plan produced",
		"Score", consensus.ConfidenceScore, "Items", len(consensus.Items))

	return &consensus, nil
}

// TurtleExploreActivity runs all 3 planning agents in parallel to independently
// analyze the task. Each agent produces an approach, scope, risks, and morsel breakdown.
// DEPRECATED: Replaced by TurtlePlanActivity for single-stage planning.
func (a *Activities) TurtleExploreActivity(ctx context.Context, req TurtlePlanningRequest) ([]TurtleProposal, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(TurtlePrefix+" Exploring task", "TaskID", req.TaskID, "Agents", len(PlanningAgents))

	prompt := fmt.Sprintf(`You are a senior engineering planner analyzing a complex task.

TASK: %s

PROJECT: %s

DESCRIPTION:
%s

Analyze this task independently. Produce a JSON object with:
{
  "approach": "Your proposed implementation approach in 2-3 paragraphs. Be specific about files, functions, and patterns.",
  "scope": "Estimated effort: one of 'small (1-2h)', 'medium (2-4h)', 'large (4-8h)', 'epic (8h+)'",
  "risks": ["risk 1", "risk 2", "risk 3"],
  "morsels": ["morsel 1: one-line description", "morsel 2: ...", "morsel 3: ..."],
  "confidence": 75
}

The "confidence" field is 0-100: how confident you are this approach will work.
The "morsels" are bite-sized work units that a single AI agent can complete in 15-30 minutes each.

Be opinionated. Say what you think the best approach is and WHY.
Consider: What could go wrong? What dependencies exist? What's the simplest path?`, req.TaskID, req.Project, req.Description)

	type agentResult struct {
		agent    string
		proposal TurtleProposal
		err      error
	}

	results := make(chan agentResult, len(PlanningAgents))
	var wg sync.WaitGroup

	for _, agent := range PlanningAgents {
		wg.Add(1)
		go func(agentName string) {
			defer wg.Done()

			cliResult, err := a.runAgent(ctx, agentName, prompt, req.WorkDir)
			if err != nil {
				results <- agentResult{agent: agentName, err: err}
				return
			}

			var proposal TurtleProposal
			if err := robustParseJSON(cliResult.Output, &proposal); err != nil {
				results <- agentResult{agent: agentName, err: fmt.Errorf("parse JSON: %w", err)}
				return
			}
			proposal.Agent = agentName
			results <- agentResult{agent: agentName, proposal: proposal}
		}(agent)
	}

	// Close channel after all goroutines complete
	go func() {
		wg.Wait()
		close(results)
	}()

	var proposals []TurtleProposal
	for r := range results {
		if r.err != nil {
			logger.Warn(TurtlePrefix+" Agent exploration failed (non-fatal)",
				"Agent", r.agent, "error", r.err)
			// Create a stub proposal so we can continue with the agents that succeeded
			proposals = append(proposals, TurtleProposal{
				Agent:      r.agent,
				Approach:   fmt.Sprintf("(exploration failed: %s)", r.err),
				Confidence: 0,
			})
		} else {
			logger.Info(TurtlePrefix+" Agent proposal received",
				"Agent", r.agent, "Confidence", r.proposal.Confidence,
				"Morsels", len(r.proposal.Morsels))
			proposals = append(proposals, r.proposal)
		}
	}

	if len(proposals) == 0 {
		return nil, fmt.Errorf("all agents failed exploration")
	}

	return proposals, nil
}

// TurtleDeliberateActivity runs one round of cross-review. Each agent sees all
// proposals and prior critiques, then produces a synthesis and revised position.
func (a *Activities) TurtleDeliberateActivity(
	ctx context.Context,
	req TurtlePlanningRequest,
	proposals []TurtleProposal,
	priorCritiques []TurtleCritique,
	round int,
) ([]TurtleCritique, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(TurtlePrefix+" Deliberation round", "Round", round, "Proposals", len(proposals))

	// Build proposal summary for context
	var proposalSummary strings.Builder
	for _, p := range proposals {
		proposalSummary.WriteString(fmt.Sprintf("\n## %s's Proposal (confidence: %d%%)\n", p.Agent, p.Confidence))
		proposalSummary.WriteString(fmt.Sprintf("Approach: %s\n", p.Approach))
		proposalSummary.WriteString(fmt.Sprintf("Scope: %s\n", p.Scope))
		proposalSummary.WriteString(fmt.Sprintf("Risks: %s\n", strings.Join(p.Risks, "; ")))
		proposalSummary.WriteString(fmt.Sprintf("Morsels: %s\n", strings.Join(p.Morsels, "; ")))
	}

	// Include prior critiques if this isn't round 1
	var priorContext string
	if len(priorCritiques) > 0 {
		var sb strings.Builder
		sb.WriteString("\n\n## Prior Deliberation\n")
		for _, c := range priorCritiques {
			sb.WriteString(fmt.Sprintf("\n### %s (round %d)\n", c.Agent, c.Round))
			sb.WriteString(fmt.Sprintf("Agreements: %s\n", c.Agreements))
			sb.WriteString(fmt.Sprintf("Disagreements: %s\n", c.Disagreements))
			sb.WriteString(fmt.Sprintf("Revised: %s\n", c.Revised))
		}
		priorContext = sb.String()
	}

	prompt := fmt.Sprintf(`You are participating in round %d of a multi-agent planning deliberation.

TASK: %s

All proposals from the team:
%s
%s

Review ALL proposals. Identify agreements and disagreements. Then produce your revised position.

Respond with ONLY a JSON object:
{
  "synthesis": "Your synthesis of all proposals in 2-3 sentences",
  "agreements": "Areas where all agents agree",
  "disagreements": "Areas of divergence that need resolution. If you agree with everything, leave empty.",
  "revised": "Your revised approach after considering all perspectives"
}

Be constructive. Look for the BEST ideas across all proposals. Converge toward the strongest approach.`,
		round, req.TaskID, proposalSummary.String(), priorContext)

	type critiqueResult struct {
		agent    string
		critique TurtleCritique
		err      error
	}

	results := make(chan critiqueResult, len(PlanningAgents))
	var wg sync.WaitGroup

	for _, agent := range PlanningAgents {
		wg.Add(1)
		go func(agentName string) {
			defer wg.Done()

			cliResult, err := a.runAgent(ctx, agentName, prompt, req.WorkDir)
			if err != nil {
				results <- critiqueResult{agent: agentName, err: err}
				return
			}

			var critique TurtleCritique
			if err := robustParseJSON(cliResult.Output, &critique); err != nil {
				results <- critiqueResult{agent: agentName, err: fmt.Errorf("parse JSON: %w", err)}
				return
			}
			critique.Agent = agentName
			critique.Round = round
			results <- critiqueResult{agent: agentName, critique: critique}
		}(agent)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var critiques []TurtleCritique
	for r := range results {
		if r.err != nil {
			logger.Warn(TurtlePrefix+" Agent deliberation failed (non-fatal)",
				"Agent", r.agent, "Round", round, "error", r.err)
		} else {
			critiques = append(critiques, r.critique)
		}
	}

	return critiques, nil
}

// TurtleConvergeActivity synthesizes all proposals and critiques into a merged
// plan with per-item confidence scores.
func (a *Activities) TurtleConvergeActivity(
	ctx context.Context,
	req TurtlePlanningRequest,
	proposals []TurtleProposal,
	critiques []TurtleCritique,
) (*TurtleConsensus, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(TurtlePrefix+" Converging", "Proposals", len(proposals), "Critiques", len(critiques))

	// Build COMPRESSED context — keep under ~4k tokens to avoid truncation.
	// Only include essential information: approach summary, morsels, and latest round.
	var ctxBuf strings.Builder
	ctxBuf.WriteString("## Proposals (compressed)\n")
	for _, p := range proposals {
		ctxBuf.WriteString(fmt.Sprintf("\n### %s (confidence: %d%%)\n%s\nMorsels: %s\n",
			p.Agent, p.Confidence,
			truncate(p.Approach, 300), // compress approach
			strings.Join(p.Morsels, "; ")))
	}

	// Only include the LATEST round of critiques (earlier rounds are redundant
	// since each round revises the prior position)
	latestRound := 0
	for _, c := range critiques {
		if c.Round > latestRound {
			latestRound = c.Round
		}
	}
	if latestRound > 0 {
		ctxBuf.WriteString(fmt.Sprintf("\n## Final Deliberation (round %d)\n", latestRound))
		for _, c := range critiques {
			if c.Round == latestRound {
				ctxBuf.WriteString(fmt.Sprintf("\n### %s\nAgreed: %s\nDisagreed: %s\nRevised: %s\n",
					c.Agent,
					truncate(c.Agreements, 200),
					truncate(c.Disagreements, 200),
					truncate(c.Revised, 300)))
			}
		}
	}

	prompt := fmt.Sprintf(`You are the synthesis agent. Merge all proposals and deliberation into a final consensus plan.

TASK: %s

%s

Produce a JSON object with:
{
  "merged_plan": "The final merged implementation plan in 3-5 paragraphs",
  "confidence_score": 85,
  "items": [
    {
      "title": "Deliverable title",
      "description": "What needs to be built",
      "confidence": 90,
      "effort": "small|medium|large"
    }
  ],
  "disagreements": ["Any unresolved disagreements — empty array if consensus"]
}

IMPORTANT: Keep your response concise. Output ONLY the JSON object, no markdown fences.
The confidence_score is 0-100 representing overall team consensus.
Per-item confidence shows how aligned the team is on each specific deliverable.`,
		req.TaskID, ctxBuf.String())

	// Use the first agent (balanced tier) for synthesis
	agent := ResolveTierAgent(a.Tiers, "balanced")
	cliResult, err := a.runAgent(ctx, agent, prompt, req.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("convergence failed: %w", err)
	}

	var consensus TurtleConsensus
	if err := robustParseJSON(cliResult.Output, &consensus); err != nil {
		// Fallback: synthesize a consensus directly from the proposals
		// instead of crashing the entire ceremony.
		logger.Warn(TurtlePrefix+" Consensus JSON parse failed, synthesizing from proposals",
			"error", err, "OutputLen", len(cliResult.Output))

		// Build a merged plan from the highest-confidence proposal
		var bestProposal *TurtleProposal
		for i := range proposals {
			if bestProposal == nil || proposals[i].Confidence > bestProposal.Confidence {
				bestProposal = &proposals[i]
			}
		}
		if bestProposal == nil {
			return nil, fmt.Errorf("no proposals available for fallback consensus")
		}

		// Build items from the best proposal's morsels
		var items []ConsensusItem
		for _, m := range bestProposal.Morsels {
			items = append(items, ConsensusItem{
				Title:       m,
				Description: m,
				Confidence:  bestProposal.Confidence,
				Effort:      "medium",
			})
		}

		consensus = TurtleConsensus{
			MergedPlan:      bestProposal.Approach,
			ConfidenceScore: bestProposal.Confidence,
			Items:           items,
			Disagreements:   []string{"(auto-synthesized: LLM convergence output was truncated)"},
		}
	}

	logger.Info(TurtlePrefix+" Convergence result",
		"Score", consensus.ConfidenceScore, "Items", len(consensus.Items),
		"Disagreements", len(consensus.Disagreements))

	return &consensus, nil
}
