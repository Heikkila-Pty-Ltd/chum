package store

import (
	"encoding/json"
	"time"
)

// Protein is a deterministic workflow sequence — a series of explicit
// molecule instructions that produce a predictable result. Not prompts — programs.
type Protein struct {
	ID         string     `json:"id"`
	Category   string     `json:"category"`
	Name       string     `json:"name"`
	Molecules  []Molecule `json:"molecules"`
	Generation int        `json:"generation"`
	Successes  int        `json:"successes"`
	Failures   int        `json:"failures"`
	AvgTokens  float64    `json:"avg_tokens"`
	Fitness    float64    `json:"fitness"`
	ParentID   string     `json:"parent_id"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Molecule is a single step in a protein sequence.
type Molecule struct {
	ID          string `json:"id"`
	Order       int    `json:"order"`
	Action      string `json:"action"`      // script, prompt, human-gate, loop
	Instruction string `json:"instruction"` // explicit, deterministic instructions
	Skill       string `json:"skill"`       // e.g. /audit, /critique, /extract
	Provider    string `json:"provider"`    // provider hint or "any"
}

// ProteinFold records a single execution of a protein.
type ProteinFold struct {
	ID          int64     `json:"id"`
	ProteinID   string    `json:"protein_id"`
	Project     string    `json:"project"`
	MorselID    string    `json:"morsel_id"`
	Provider    string    `json:"provider"`
	TotalTokens int       `json:"total_tokens"`
	DurationS   float64   `json:"duration_s"`
	Success     bool      `json:"success"`
	Retro       string    `json:"retro"` // JSON retro data
	CreatedAt   time.Time `json:"created_at"`
}

// MoleculeRetro is the structured output of a post-execution retrospective.
type MoleculeRetro struct {
	Worked     []string `json:"worked"`
	Failed     []string `json:"failed"`
	Improve    []string `json:"improve"`
	TokenWaste string   `json:"token_waste"`
	Verdict    string   `json:"verdict"` // keep, rewrite, split, merge, remove
}

// GetProteinForSpecies returns the fittest protein matching a species category.
func (s *Store) GetProteinForSpecies(species string) (*Protein, error) {
	row := s.db.QueryRow(`
		SELECT id, category, name, molecules, generation, successes, failures,
		  avg_tokens, fitness, parent_id, created_at
		FROM proteins WHERE category = ? OR category LIKE '%' || ?
		ORDER BY fitness DESC, successes DESC
		LIMIT 1`, species, species)

	var p Protein
	var molJSON string
	err := row.Scan(&p.ID, &p.Category, &p.Name, &molJSON, &p.Generation,
		&p.Successes, &p.Failures, &p.AvgTokens, &p.Fitness, &p.ParentID, &p.CreatedAt)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal([]byte(molJSON), &p.Molecules); err != nil {
		return nil, err
	}
	return &p, nil
}

// RecordProteinFold inserts a fold record for a protein execution.
func (s *Store) RecordProteinFold(f ProteinFold) error {
	_, err := s.db.Exec(`INSERT INTO protein_folds
		(protein_id, project, morsel_id, provider, total_tokens, duration_s, success, retro)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ProteinID, f.Project, f.MorselID, f.Provider,
		f.TotalTokens, f.DurationS, f.Success, f.Retro)

	// Update protein stats
	if err == nil {
		if f.Success {
			s.db.Exec(`UPDATE proteins SET successes = successes + 1 WHERE id = ?`, f.ProteinID)
		} else {
			s.db.Exec(`UPDATE proteins SET failures = failures + 1 WHERE id = ?`, f.ProteinID)
		}
	}
	return err
}

// SeedProteins inserts the initial hardcoded proteins if they don't exist yet.
func (s *Store) SeedProteins() error {
	reactComponentProtein := Protein{
		ID:       "component-v1",
		Category: "component",
		Name:     "React Component Build Sequence",
		Molecules: []Molecule{
			{
				ID:     "read-types",
				Order:  1,
				Action: "script",
				Instruction: `BEFORE writing any code, you MUST read the project's type definitions.
Run: cat lib/types.ts (or find the equivalent types file).
Record the EXACT interface/type names and field names for the data type
this component will use. List them explicitly in your plan.
Do NOT guess field names — get them from the source.
If the types file doesn't exist, check: types/, interfaces/, or grep for 'interface' or 'type'.`,
				Provider: "any",
			},
			{
				ID:     "build-component",
				Order:  2,
				Action: "prompt",
				Instruction: `Build the component using ONLY the field names confirmed in the type-reading step.
Rules:
1. Add 'use client' directive if the component uses hooks, event handlers, or onClick
2. Import Image from 'next/image', Link from 'next/link'
3. Import types from the EXACT path found in step 1 (e.g. '@/lib/types')
4. Use design tokens from globals.css — do NOT hardcode colors or spacing
5. After writing the component, run: npm run build
6. If build fails, read the FULL error output and fix the EXACT lines mentioned
7. Do NOT regenerate the entire file — patch only the broken imports/types
8. Run npm run build again after each fix until it passes clean`,
				Provider: "any",
			},
			{
				ID:     "audit-component",
				Order:  3,
				Action: "prompt",
				Skill:  "/audit",
				Instruction: `Audit the completed component:
1. No hardcoded colors — must use CSS variables or design tokens
2. Responsive: test at 375px, 768px, 1440px mentally
3. Hover/focus states present for interactive elements
4. Missing image/data handled gracefully (fallback UI)
5. Run: npm run build — must pass clean with zero warnings
6. Check that all imports resolve to real files
If any issues found, fix them and re-run npm run build.`,
				Provider: "gemini-flash",
			},
		},
		Generation: 0,
	}

	molJSON, err := json.Marshal(reactComponentProtein.Molecules)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(`INSERT OR IGNORE INTO proteins (id, category, name, molecules, generation)
		VALUES (?, ?, ?, ?, ?)`,
		reactComponentProtein.ID, reactComponentProtein.Category,
		reactComponentProtein.Name, string(molJSON), reactComponentProtein.Generation)
	if err != nil {
		return err
	}
	// Update existing protein in case molecules changed
	_, err = s.db.Exec(`UPDATE proteins SET molecules = ?, category = ?, name = ? WHERE id = ?`,
		string(molJSON), reactComponentProtein.Category, reactComponentProtein.Name, reactComponentProtein.ID)
	if err != nil {
		return err
	}

	// Go feature protein — the dominant species for CHUM self-modification tasks.
	goFeatureProtein := Protein{
		ID:       "go-feature-v1",
		Category: "go-feature",
		Name:     "Go Feature Build Sequence",
		Molecules: []Molecule{
			{
				ID:     "read-interfaces",
				Order:  1,
				Action: "script",
				Instruction: `BEFORE writing any code, read the existing interfaces and types.
Run: grep -rn "type.*struct\|type.*interface\|func (" in the relevant package directory.
Record EXACT type names, method signatures, and field names.
Check existing tests to understand expected behavior.
Do NOT guess — get them from the source.`,
				Provider: "any",
			},
			{
				ID:     "implement",
				Order:  2,
				Action: "prompt",
				Instruction: `Implement the feature using ONLY types and interfaces confirmed in step 1.
Rules:
1. Run 'go build ./...' after EVERY file change — fix compile errors immediately
2. Run 'go vet ./...' to catch common mistakes
3. Add or update tests for new/changed functions
4. Run 'go test ./...' — ALL tests must pass, not just the new ones
5. If tests fail, read the FULL error output and fix the EXACT issue
6. Do NOT regenerate entire files — patch only broken lines
7. Check that exported functions have doc comments`,
				Provider: "any",
			},
			{
				ID:     "verify",
				Order:  3,
				Action: "script",
				Instruction: `Verify the implementation:
1. Run: go build ./... — must succeed with zero errors
2. Run: go test ./... — ALL packages must pass
3. Run: go vet ./... — must pass clean
4. Check that no unrelated files were modified (scope discipline)
5. If any check fails, fix and re-run until clean`,
				Provider: "any",
			},
		},
		Generation: 0,
	}

	goMolJSON, err := json.Marshal(goFeatureProtein.Molecules)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(`INSERT OR IGNORE INTO proteins (id, category, name, molecules, generation)
		VALUES (?, ?, ?, ?, ?)`,
		goFeatureProtein.ID, goFeatureProtein.Category,
		goFeatureProtein.Name, string(goMolJSON), goFeatureProtein.Generation)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE proteins SET molecules = ?, category = ?, name = ? WHERE id = ?`,
		string(goMolJSON), goFeatureProtein.Category, goFeatureProtein.Name, goFeatureProtein.ID)
	return err
}

// InsertProtein stores a newly synthesized protein. Uses INSERT OR REPLACE
// so it can safely be called for both new proteins and protein forks.
func (s *Store) InsertProtein(p Protein) error {
	molJSON, err := json.Marshal(p.Molecules)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT OR REPLACE INTO proteins
		(id, category, name, molecules, generation, successes, failures, avg_tokens, fitness, parent_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Category, p.Name, string(molJSON),
		p.Generation, p.Successes, p.Failures, p.AvgTokens, p.Fitness, p.ParentID)
	return err
}
