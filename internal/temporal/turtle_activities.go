package temporal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/antigravity-dev/chum/internal/graph"
	"go.temporal.io/sdk/activity"
)

// TurtleExploreActivity runs all 3 planning agents in parallel to independently
// analyze the task. Each agent produces an approach, scope, risks, and morsel breakdown.
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

			cliResult, err := runAgent(ctx, agentName, prompt, req.WorkDir)
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

			cliResult, err := runAgent(ctx, agentName, prompt, req.WorkDir)
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
	cliResult, err := runAgent(ctx, agent, prompt, req.WorkDir)
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

// TurtleDecomposeActivity breaks the consensus plan into concrete, shark-sized morsels.
func (a *Activities) TurtleDecomposeActivity(
	ctx context.Context,
	req TurtlePlanningRequest,
	consensus TurtleConsensus,
) ([]TurtleMorsel, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(TurtlePrefix+" Decomposing consensus into morsels", "Items", len(consensus.Items))

	// Build items context
	var itemsSummary strings.Builder
	for i, item := range consensus.Items {
		itemsSummary.WriteString(fmt.Sprintf("\n%d. **%s** (%s effort, %d%% confidence)\n   %s\n",
			i+1, item.Title, item.Effort, item.Confidence, item.Description))
	}

	prompt := fmt.Sprintf(`You are decomposing a planned project into bite-sized morsels for autonomous AI execution.

TASK: %s
PROJECT: %s

MERGED PLAN:
%s

DELIVERABLES:
%s

Break this into concrete morsels. Each morsel should be:
- Completable by a single AI agent in 15-30 minutes
- Have clear acceptance criteria
- Have testable DoD (Definition of Done) checks
- Be independent or have explicit dependencies

Respond with ONLY a JSON array:
[
  {
    "title": "Short morsel title",
    "description": "What exactly to implement — be specific about files, functions, patterns",
    "acceptance_criteria": "How to know it's done",
    "dod_checks": ["go build ./...", "go test ./..."],
    "priority": 1,
    "estimate_minutes": 20,
    "labels": ["feature", "dispatcher"],
    "depends_on": []
  }
]

Order by dependency: independent morsels first, then dependent ones.
IMPORTANT: Each morsel must be small enough for a shark. If something is too big, split it further.`,
		req.TaskID, req.Project, consensus.MergedPlan, itemsSummary.String())

	agent := ResolveTierAgent(a.Tiers, "balanced")
	cliResult, err := runAgent(ctx, agent, prompt, req.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("decomposition failed: %w", err)
	}

	var morsels []TurtleMorsel
	if err := robustParseJSONArray(cliResult.Output, &morsels); err != nil {
		return nil, fmt.Errorf("parse morsels JSON: %w", err)
	}

	if len(morsels) == 0 {
		return nil, fmt.Errorf("decomposition produced no morsels")
	}

	// Cap at 10 morsels — if more, the task is probably still too big
	if len(morsels) > 10 {
		logger.Warn(TurtlePrefix+" Too many morsels, capping at 10", "Original", len(morsels))
		morsels = morsels[:10]
	}

	logger.Info(TurtlePrefix+" Morsels decomposed", "Count", len(morsels))
	return morsels, nil
}

// TurtleEmitActivity writes the decomposed morsels to the DAG as ready tasks.
func (a *Activities) TurtleEmitActivity(
	ctx context.Context,
	req TurtlePlanningRequest,
	morsels []TurtleMorsel,
) ([]string, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(TurtlePrefix+" Emitting morsels to DAG", "Count", len(morsels))

	if a.DAG == nil {
		return nil, fmt.Errorf("DAG not configured — cannot emit morsels")
	}

	var emitted []string
	for _, m := range morsels {
		dodJSON, _ := json.Marshal(m.DoDChecks)

		task := graph.Task{
			Title:           m.Title,
			Description:     m.Description + "\n\nAcceptance Criteria: " + m.AcceptanceCriteria + "\n\nDoD Checks: " + string(dodJSON),
			Status:          "ready",
			Priority:        m.Priority,
			Type:            "morsel",
			Labels:          m.Labels,
			EstimateMinutes: m.EstimateMinutes,
			Acceptance:      m.AcceptanceCriteria,
			DependsOn:       m.DependsOn,
			Project:         req.Project,
			ParentID:        req.TaskID,
		}

		id, err := a.DAG.CreateTask(ctx, task)
		if err != nil {
			logger.Error(TurtlePrefix+" Failed to emit morsel", "Title", m.Title, "error", err)
			continue
		}
		emitted = append(emitted, id)
		logger.Info(TurtlePrefix+" Morsel emitted", "ID", id, "Title", m.Title)
	}

	if len(emitted) == 0 {
		return nil, fmt.Errorf("failed to emit any morsels")
	}

	logger.Info(TurtlePrefix+" Emission complete", "Emitted", len(emitted), "Total", len(morsels))
	return emitted, nil
}
