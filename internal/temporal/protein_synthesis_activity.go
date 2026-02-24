package temporal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.temporal.io/sdk/activity"

	"github.com/antigravity-dev/chum/internal/store"
)

// ProteinSynthesisRequest contains the inputs for synthesising a new protein
// from observed success patterns for a species.
type ProteinSynthesisRequest struct {
	Species         string `json:"species"`
	Project         string `json:"project"`
	TopPattern      string `json:"top_pattern"`
	FittestProvider string `json:"fittest_provider"`
	TotalSuccesses  int    `json:"total_successes"`
}

// ProteinSynthesisResult is the output of a protein synthesis attempt.
type ProteinSynthesisResult struct {
	ProteinID  string `json:"protein_id"`
	Name       string `json:"name"`
	Molecules  int    `json:"molecules"`
	Synthesised bool  `json:"synthesised"`
}

// SynthesizeProteinActivity analyses accumulated success patterns for a species
// and generates a new protein — a deterministic molecule sequence that future
// dispatches will follow. This is the bridge from "immune system" (preventing
// errors) to "capability extension" (codifying what works).
//
// The activity:
//  1. Reads existing genome data (patterns, provider genes) for the species
//  2. Reads recent lessons related to the species
//  3. Asks a premium LLM to extract the common step sequence
//  4. Writes a gen-0 protein to the store
//
// After creation, GetProteinInstructionsActivity automatically injects
// the protein into future dispatches for this species — the loop closes itself.
func (a *Activities) SynthesizeProteinActivity(ctx context.Context, req ProteinSynthesisRequest) (*ProteinSynthesisResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(PaleontologistPrefix+" Synthesising protein",
		"Species", req.Species, "Successes", req.TotalSuccesses)

	if a.Store == nil {
		return &ProteinSynthesisResult{}, nil
	}

	// Check if protein already exists for this species
	existing, _ := a.Store.GetProteinForSpecies(req.Species)
	if existing != nil {
		logger.Info(PaleontologistPrefix+" Protein already exists, skipping synthesis",
			"Species", req.Species, "ProteinID", existing.ID)
		return &ProteinSynthesisResult{
			ProteinID:   existing.ID,
			Synthesised: false,
		}, nil
	}

	// Gather context: genome patterns + lessons
	var contextParts []string
	contextParts = append(contextParts,
		fmt.Sprintf("SPECIES: %s (project: %s)", req.Species, req.Project))
	contextParts = append(contextParts,
		fmt.Sprintf("TRACK RECORD: %d consecutive successes", req.TotalSuccesses))

	if req.TopPattern != "" {
		contextParts = append(contextParts,
			fmt.Sprintf("TOP PATTERN FROM GENOME: %s", req.TopPattern))
	}
	if req.FittestProvider != "" {
		contextParts = append(contextParts,
			fmt.Sprintf("FITTEST PROVIDER: %s", req.FittestProvider))
	}

	// Pull genome data for richer context
	genome, err := a.Store.GetGenome(req.Species)
	if err == nil && genome != nil {
		var patternDescs []string
		for _, p := range genome.Patterns {
			patternDescs = append(patternDescs, fmt.Sprintf("- %s", p.Pattern))
		}
		if len(patternDescs) > 0 {
			contextParts = append(contextParts,
				"ACCUMULATED PATTERNS (what works):\n"+strings.Join(patternDescs, "\n"))
		}

		var antibodyDescs []string
		for _, ab := range genome.Antibodies {
			antibodyDescs = append(antibodyDescs, fmt.Sprintf("- AVOID: %s → %s", ab.Pattern, ab.Alternative))
		}
		if len(antibodyDescs) > 0 {
			contextParts = append(contextParts,
				"ANTIBODIES (what to avoid):\n"+strings.Join(antibodyDescs, "\n"))
		}
	}

	// Pull recent lessons for this species
	lessons, err := a.Store.SearchLessons(req.Species, 10)
	if err == nil && len(lessons) > 0 {
		var lessonDescs []string
		for _, l := range lessons {
			lessonDescs = append(lessonDescs, fmt.Sprintf("- [%s] %s", l.Category, l.Summary))
		}
		contextParts = append(contextParts,
			"LESSONS LEARNED:\n"+strings.Join(lessonDescs, "\n"))
	}

	prompt := fmt.Sprintf(`You are a protein synthesis system for an AI coding pipeline.

A protein is a deterministic sequence of "molecules" (steps) that the coding agent follows IN ORDER. Each molecule has an explicit instruction that calls specific tools and produces specific artifacts. NOT prompts — programs.

CONTEXT:
%s

Your job: synthesise a protein for this species. The protein should encode the PROVEN workflow that has succeeded %d times. Extract the common step sequence that leads to success.

RULES:
- Each molecule must be a specific, actionable step (not "do the task")
- First molecule should ALWAYS read existing code/types/interfaces before modifying
- Middle molecules do the actual implementation work
- Last molecule should ALWAYS verify (build, test, lint)
- Use 3-5 molecules (not more — keep it focused)
- Include a provider hint ("any", "gemini-flash", "claude-sonnet") per molecule
- Reference specific tools/commands the agent should run

Respond with ONLY a JSON object:
{
  "name": "Human-readable protein name",
  "molecules": [
    {
      "id": "short-kebab-case-id",
      "order": 1,
      "action": "script|prompt",
      "instruction": "Explicit, deterministic instruction...",
      "skill": "",
      "provider": "any"
    }
  ]
}`, strings.Join(contextParts, "\n\n"), req.TotalSuccesses)

	agent := ResolveTierAgent(a.Tiers, "premium")
	cliResult, err := runAgent(ctx, agent, prompt, "/tmp")
	if err != nil {
		logger.Warn(PaleontologistPrefix+" Protein synthesis LLM failed", "error", err)
		return &ProteinSynthesisResult{Synthesised: false}, nil
	}

	jsonStr := extractJSON(cliResult.Output)
	if jsonStr == "" {
		logger.Warn(PaleontologistPrefix + " Protein synthesis produced no JSON")
		return &ProteinSynthesisResult{Synthesised: false}, nil
	}

	jsonStr = sanitizeLLMJSON(jsonStr)

	var parsed struct {
		Name      string           `json:"name"`
		Molecules []store.Molecule `json:"molecules"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		logger.Warn(PaleontologistPrefix+" Protein synthesis JSON parse failed", "error", err)
		return &ProteinSynthesisResult{Synthesised: false}, nil
	}

	if len(parsed.Molecules) == 0 {
		logger.Warn(PaleontologistPrefix + " Protein synthesis produced zero molecules")
		return &ProteinSynthesisResult{Synthesised: false}, nil
	}

	// Assign a unique ID
	proteinID := fmt.Sprintf("%s-v1", req.Species)

	protein := store.Protein{
		ID:         proteinID,
		Category:   req.Species,
		Name:       parsed.Name,
		Molecules:  parsed.Molecules,
		Generation: 0,
	}

	if err := a.Store.InsertProtein(protein); err != nil {
		logger.Warn(PaleontologistPrefix+" Failed to store synthesised protein", "error", err)
		return &ProteinSynthesisResult{Synthesised: false}, nil
	}

	logger.Info(PaleontologistPrefix+" 🧬 Protein synthesised!",
		"ProteinID", proteinID,
		"Species", req.Species,
		"Molecules", len(parsed.Molecules),
		"Name", parsed.Name)

	// Notify coordination room
	if a.Sender != nil && a.DefaultRoom != "" {
		var molNames []string
		for _, m := range parsed.Molecules {
			molNames = append(molNames, m.ID)
		}
		msg := fmt.Sprintf("🧬 **Protein Synthesised!**\nSpecies: `%s`\nName: %s\nMolecules: %d (%s)\nGeneration: 0\n\nFuture dispatches for this species will follow this protein automatically.",
			req.Species, parsed.Name, len(parsed.Molecules), strings.Join(molNames, " → "))
		_ = a.Sender.SendMessage(ctx, a.DefaultRoom, msg)
	}

	return &ProteinSynthesisResult{
		ProteinID:   proteinID,
		Name:        parsed.Name,
		Molecules:   len(parsed.Molecules),
		Synthesised: true,
	}, nil
}

// MutateProteinRequest contains the inputs for mutating an existing protein
// based on fold retro feedback.
type MutateProteinRequest struct {
	ProteinID     string `json:"protein_id"`
	Species       string `json:"species"`
	RetroVerdict  string `json:"retro_verdict"`  // "rewrite", "split", or "merge"
	RetroImprove  []string `json:"retro_improve"` // improvement suggestions from retro
	RetroFailed   []string `json:"retro_failed"`  // what failed
	FoldCount     int    `json:"fold_count"`       // how many folds the protein has
}

// MutateProteinResult is the output of a protein mutation.
type MutateProteinResult struct {
	NewProteinID string `json:"new_protein_id"`
	Generation   int    `json:"generation"`
	Mutated      bool   `json:"mutated"`
}

// MutateProteinActivity forks an existing protein and applies mutations
// based on retrospective feedback. Both versions coexist — future dispatches
// pick the one with higher fitness (natural selection).
//
// This is evolution: observe → mutate → compete → select.
func (a *Activities) MutateProteinActivity(ctx context.Context, req MutateProteinRequest) (*MutateProteinResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(PaleontologistPrefix+" Mutating protein",
		"ProteinID", req.ProteinID, "Verdict", req.RetroVerdict)

	if a.Store == nil {
		return &MutateProteinResult{}, nil
	}

	// Fetch existing protein
	existing, err := a.Store.GetProteinForSpecies(req.Species)
	if err != nil || existing == nil {
		logger.Warn(PaleontologistPrefix+" Cannot mutate: protein not found",
			"Species", req.Species)
		return &MutateProteinResult{Mutated: false}, nil
	}

	// Build context for the LLM
	existingMolJSON, _ := json.Marshal(existing.Molecules)

	var feedbackParts []string
	if len(req.RetroFailed) > 0 {
		feedbackParts = append(feedbackParts,
			"WHAT FAILED:\n"+strings.Join(req.RetroFailed, "\n"))
	}
	if len(req.RetroImprove) > 0 {
		feedbackParts = append(feedbackParts,
			"IMPROVEMENT SUGGESTIONS:\n"+strings.Join(req.RetroImprove, "\n"))
	}

	prompt := fmt.Sprintf(`You are a protein mutation system. A protein (deterministic workflow sequence) has been used %d times but the latest fold (execution) produced a "%s" verdict.

CURRENT PROTEIN: %s
Name: %s
Generation: %d
Successes: %d, Failures: %d

CURRENT MOLECULES:
%s

FEEDBACK:
%s

Your job: produce a MUTATED version of this protein. Apply the feedback to fix the problems:
- If verdict is "rewrite": significantly restructure the molecules
- If verdict is "split": break a complex molecule into 2-3 simpler ones
- If verdict is "merge": combine redundant molecules

Keep what works. Fix what doesn't. Maintain 3-6 molecules total.

Respond with ONLY a JSON object:
{
  "name": "Updated protein name (append: mutated)",
  "molecules": [
    {
      "id": "short-kebab-case-id",
      "order": 1,
      "action": "script|prompt",
      "instruction": "Explicit instruction...",
      "skill": "",
      "provider": "any"
    }
  ]
}`, req.FoldCount, req.RetroVerdict, existing.ID, existing.Name,
		existing.Generation, existing.Successes, existing.Failures,
		string(existingMolJSON), strings.Join(feedbackParts, "\n\n"))

	agent := ResolveTierAgent(a.Tiers, "premium")
	cliResult, err := runAgent(ctx, agent, prompt, "/tmp")
	if err != nil {
		logger.Warn(PaleontologistPrefix+" Protein mutation LLM failed", "error", err)
		return &MutateProteinResult{Mutated: false}, nil
	}

	jsonStr := extractJSON(cliResult.Output)
	if jsonStr == "" {
		logger.Warn(PaleontologistPrefix + " Protein mutation produced no JSON")
		return &MutateProteinResult{Mutated: false}, nil
	}

	jsonStr = sanitizeLLMJSON(jsonStr)

	var parsed struct {
		Name      string           `json:"name"`
		Molecules []store.Molecule `json:"molecules"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		logger.Warn(PaleontologistPrefix+" Protein mutation JSON parse failed", "error", err)
		return &MutateProteinResult{Mutated: false}, nil
	}

	if len(parsed.Molecules) == 0 {
		logger.Warn(PaleontologistPrefix + " Protein mutation produced zero molecules")
		return &MutateProteinResult{Mutated: false}, nil
	}

	newGen := existing.Generation + 1
	newProteinID := fmt.Sprintf("%s-v%d", req.Species, newGen+1)

	mutant := store.Protein{
		ID:         newProteinID,
		Category:   existing.Category,
		Name:       parsed.Name,
		Molecules:  parsed.Molecules,
		Generation: newGen,
		ParentID:   existing.ID,
	}

	if err := a.Store.InsertProtein(mutant); err != nil {
		logger.Warn(PaleontologistPrefix+" Failed to store mutated protein", "error", err)
		return &MutateProteinResult{Mutated: false}, nil
	}

	logger.Info(PaleontologistPrefix+" 🧬 Protein mutated!",
		"ParentID", existing.ID,
		"NewID", newProteinID,
		"Generation", newGen,
		"Molecules", len(parsed.Molecules),
		"Verdict", req.RetroVerdict)

	// Notify coordination room
	if a.Sender != nil && a.DefaultRoom != "" {
		msg := fmt.Sprintf("🧬 **Protein Mutated!**\nSpecies: `%s`\nParent: `%s` → Child: `%s`\nGeneration: %d\nVerdict: %s\nMolecules: %d\n\nBoth versions now coexist — fitness selects the winner.",
			req.Species, existing.ID, newProteinID, newGen, req.RetroVerdict, len(parsed.Molecules))
		_ = a.Sender.SendMessage(ctx, a.DefaultRoom, msg)
	}

	return &MutateProteinResult{
		NewProteinID: newProteinID,
		Generation:   newGen,
		Mutated:      true,
	}, nil
}

// SynthesizeProteinCandidatesActivity is the orchestrator called by
// PaleontologistWorkflow. It fetches protein candidates from the store
// and synthesises a protein for each candidate that doesn't have one yet.
// Returns the number of proteins successfully synthesised.
func (a *Activities) SynthesizeProteinCandidatesActivity(ctx context.Context, req PaleontologistRequest) (int, error) {
	logger := activity.GetLogger(ctx)
	logger.Info(PaleontologistPrefix + " Synthesising proteins for candidates")

	if a.Store == nil {
		return 0, nil
	}

	candidates, err := a.Store.GetProteinCandidates(5)
	if err != nil {
		return 0, fmt.Errorf("get protein candidates: %w", err)
	}

	synthesised := 0
	for _, c := range candidates {
		if c.HasProtein {
			continue
		}

		result, err := a.SynthesizeProteinActivity(ctx, ProteinSynthesisRequest{
			Species:         c.Species,
			Project:         req.Project,
			TopPattern:      c.TopPattern,
			FittestProvider: c.FittestProvider,
			TotalSuccesses:  c.TotalSuccesses,
		})
		if err != nil {
			logger.Warn(PaleontologistPrefix+" Protein synthesis failed for species",
				"Species", c.Species, "error", err)
			continue
		}
		if result.Synthesised {
			synthesised++
		}
	}

	logger.Info(PaleontologistPrefix+" Protein synthesis pass complete",
		"Synthesised", synthesised)
	return synthesised, nil
}
