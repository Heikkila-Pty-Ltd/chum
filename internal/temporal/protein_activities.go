package temporal

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.temporal.io/sdk/activity"

	"github.com/antigravity-dev/chum/internal/store"
)

// GetProteinInstructionsActivity looks up the fittest protein for a species
// and returns formatted molecule instructions for prompt injection.
func (a *Activities) GetProteinInstructionsActivity(ctx context.Context, species string) (string, error) {
	logger := activity.GetLogger(ctx)
	if a.Store == nil {
		return "", nil
	}

	protein, err := a.Store.GetProteinForSpecies(species)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil // no protein found — normal for new species
		}
		logger.Warn(OctopusPrefix+" Protein lookup failed (non-fatal)", "species", species, "error", err)
		return "", nil
	}

	// Format molecule instructions as a structured protocol
	var lines []string
	lines = append(lines, fmt.Sprintf("🧬 PROTEIN PROTOCOL: %s (gen %d, %d successes)",
		protein.Name, protein.Generation, protein.Successes))
	lines = append(lines, "Follow these steps IN ORDER. Each step must complete before the next begins.")
	lines = append(lines, "")

	for _, mol := range protein.Molecules {
		skillTag := ""
		if mol.Skill != "" {
			skillTag = fmt.Sprintf(" [skill: %s]", mol.Skill)
		}
		lines = append(lines, fmt.Sprintf("### STEP %d: %s%s", mol.Order, strings.ToUpper(mol.ID), skillTag))
		lines = append(lines, mol.Instruction)
		lines = append(lines, "")
	}

	result := strings.Join(lines, "\n")
	logger.Info(SharkPrefix+" Protein instructions injected",
		"ProteinID", protein.ID,
		"Species", species,
		"Molecules", len(protein.Molecules),
	)

	return result, nil
}

// RecordProteinFoldActivity records the result of a protein-guided execution.
func (a *Activities) RecordProteinFoldActivity(ctx context.Context, fold store.ProteinFold) error {
	logger := activity.GetLogger(ctx)
	if a.Store == nil {
		return nil
	}

	if err := a.Store.RecordProteinFold(fold); err != nil {
		logger.Warn(OctopusPrefix+" Protein fold recording failed (non-fatal)", "error", err)
		return nil
	}

	outcome := "failed"
	if fold.Success {
		outcome = "passed"
	}
	logger.Info(OctopusPrefix+" Protein fold recorded",
		"ProteinID", fold.ProteinID,
		"MorselID", fold.MorselID,
		"Outcome", outcome,
	)
	return nil
}

// MoleculeRetroActivity runs a cheap post-execution retrospective
// and returns the structured retro data.
func (a *Activities) MoleculeRetroActivity(ctx context.Context, req MoleculeRetroRequest) (*store.MoleculeRetro, error) {
	logger := activity.GetLogger(ctx)

	// The retro is actually produced by analyzing the execution results.
	// For the MVP we do a deterministic analysis (no LLM call needed):
	retro := &store.MoleculeRetro{
		Verdict: "keep",
	}

	if req.DoDPassed {
		retro.Worked = append(retro.Worked, "DoD checks passed")
		if req.UBSCritical == 0 {
			retro.Worked = append(retro.Worked, "Zero UBS critical findings")
		}
		if req.BuildPassed {
			retro.Worked = append(retro.Worked, "npm run build passed clean")
		}
	} else {
		retro.Failed = append(retro.Failed, "DoD checks failed")
		if req.UBSCritical > 0 {
			retro.Failed = append(retro.Failed, fmt.Sprintf("UBS found %d critical issues", req.UBSCritical))
		}
		if !req.BuildPassed {
			retro.Failed = append(retro.Failed, "npm run build failed")
		}
		if req.AttemptCount > 2 {
			retro.Improve = append(retro.Improve, "Consider splitting molecule into smaller steps for failed attempts")
			retro.Verdict = "rewrite"
		}
	}

	if req.TokensUsed > 50000 {
		retro.TokenWaste = fmt.Sprintf("%d tokens used — consider cheaper model or more explicit instructions", req.TokensUsed)
		retro.Improve = append(retro.Improve, "High token usage — molecule instructions may be too vague")
	}

	logger.Info(OctopusPrefix+" Molecule retro completed",
		"ProteinID", req.ProteinID,
		"Verdict", retro.Verdict,
		"Worked", len(retro.Worked),
		"Failed", len(retro.Failed),
	)

	return retro, nil
}

// MoleculeRetroRequest contains inputs for a retro analysis.
type MoleculeRetroRequest struct {
	ProteinID    string `json:"protein_id"`
	MorselID     string `json:"morsel_id"`
	DoDPassed    bool   `json:"dod_passed"`
	BuildPassed  bool   `json:"build_passed"`
	UBSCritical  int    `json:"ubs_critical"`
	AttemptCount int    `json:"attempt_count"`
	TokensUsed   int    `json:"tokens_used"`
	DurationS    float64 `json:"duration_s"`
}

// FormatRetroJSON serializes a MoleculeRetro to JSON for storage.
func FormatRetroJSON(retro *store.MoleculeRetro) string {
	b, err := json.Marshal(retro)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// Ensure time is used (for future LLM-based retro calls).
var _ = time.Now
