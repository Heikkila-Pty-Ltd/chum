package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// GenomeEntry represents a single pattern, antibody, or fossil in a species genome.
// Inspired by ALMA's anti-pattern schema, adapted for evolutionary biology model.
type GenomeEntry struct {
	Pattern     string   `json:"pattern"`               // what was attempted
	Reason      string   `json:"reason,omitempty"`      // why it succeeded/failed
	Alternative string   `json:"alternative,omitempty"` // what to do instead (antibodies/fossils only)
	Count       int      `json:"count"`                 // how many times observed
	Generation  int      `json:"generation"`            // which generation it was discovered
	Files       []string `json:"files,omitempty"`       // affected files
	Agent       string   `json:"agent,omitempty"`       // which agent produced this
}

// ProviderGene tracks which provider/model works best for this species.
// The selfish gene: provider preferences persist across generations of mortal organisms.
type ProviderGene struct {
	Provider   string  `json:"provider"`    // e.g. "claude", "codex-spark", "gemini"
	Successes  int     `json:"successes"`
	Failures   int     `json:"failures"`
	TotalCost  float64 `json:"total_cost"`  // USD spent through this provider
	Generation int     `json:"generation"` // when first observed
}

// Fitness returns the provider's fitness score: success rate / cost per attempt.
// Higher is better. A cheap provider that always succeeds is maximally fit.
func (pg ProviderGene) Fitness() float64 {
	total := pg.Successes + pg.Failures
	if total == 0 {
		return 0
	}
	successRate := float64(pg.Successes) / float64(total)
	costPerAttempt := pg.TotalCost / float64(total)
	if costPerAttempt == 0 {
		return successRate * 1000 // free = maximally efficient
	}
	return successRate / costPerAttempt
}

// Genome represents the accumulated evolutionary memory for a task species.
// Species are global and phylogenetic — "go-test-fix" crosses project boundaries.
// Every organism is mortal. The genome is what persists.
type Genome struct {
	Species        string         `json:"species"`
	ParentSpecies  string         `json:"parent_species"`
	Patterns       []GenomeEntry  `json:"patterns"`        // DNA: approaches that passed DoD
	Antibodies     []GenomeEntry  `json:"antibodies"`      // Defensive knowledge from failures
	Fossils        []GenomeEntry  `json:"fossils"`         // Extinct approaches (failed 3+ times)
	ProviderGenes  []ProviderGene `json:"provider_genes"` // Selfish genes: which models survive
	Generation     int            `json:"generation"`
	Successes      int            `json:"successes"`
	Failures       int            `json:"failures"`
	TotalCostUSD   float64        `json:"total_cost_usd"` // Total energy consumed by this species
	Hibernating    bool           `json:"hibernating"`
	LastEvolved    *time.Time     `json:"last_evolved,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

// FossilThreshold is the number of times an antibody must appear before
// it auto-promotes to a fossil (extinct approach). Nature's selection pressure.
const FossilThreshold = 3

// Fitness returns the species-level fitness: success rate / cost per organism.
// Higher is better. Cheap species that always succeed are maximally fit.
func (g *Genome) Fitness() float64 {
	total := g.Successes + g.Failures
	if total == 0 {
		return 0
	}
	successRate := float64(g.Successes) / float64(total)
	costPerAttempt := g.TotalCostUSD / float64(total)
	if costPerAttempt == 0 {
		return successRate * 1000
	}
	return successRate / costPerAttempt
}

// FittestProvider returns the provider gene with highest fitness.
// This is the selfish gene in action: the fittest provider persists.
func (g *Genome) FittestProvider() *ProviderGene {
	var best *ProviderGene
	for i := range g.ProviderGenes {
		if best == nil || g.ProviderGenes[i].Fitness() > best.Fitness() {
			best = &g.ProviderGenes[i]
		}
	}
	return best
}

// ensureGenomesTable creates the genomes table if it doesn't exist.
func (s *Store) ensureGenomesTable() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS genomes (
		species        TEXT    PRIMARY KEY,
		parent_species TEXT    NOT NULL DEFAULT '',
		patterns       TEXT    NOT NULL DEFAULT '[]',
		antibodies     TEXT    NOT NULL DEFAULT '[]',
		fossils        TEXT    NOT NULL DEFAULT '[]',
		provider_genes TEXT    NOT NULL DEFAULT '[]',
		generation     INTEGER NOT NULL DEFAULT 0,
		successes      INTEGER NOT NULL DEFAULT 0,
		failures       INTEGER NOT NULL DEFAULT 0,
		total_cost_usd REAL    NOT NULL DEFAULT 0,
		hibernating    BOOLEAN NOT NULL DEFAULT 0,
		last_evolved   DATETIME,
		created_at     DATETIME NOT NULL DEFAULT (datetime('now'))
	)`)
	return err
}

// GetGenome fetches the genome for a species, or returns an empty genome if none exists.
func (s *Store) GetGenome(species string) (*Genome, error) {
	g := &Genome{Species: species}
	var patternsJSON, antibodiesJSON, fossilsJSON, providerGenesJSON string
	var lastEvolved sql.NullString

	err := s.db.QueryRow(
		`SELECT species, parent_species, patterns, antibodies, fossils, provider_genes,
		        generation, successes, failures, total_cost_usd, hibernating, last_evolved, created_at
		 FROM genomes WHERE species = ?`, species,
	).Scan(&g.Species, &g.ParentSpecies, &patternsJSON, &antibodiesJSON, &fossilsJSON, &providerGenesJSON,
		&g.Generation, &g.Successes, &g.Failures, &g.TotalCostUSD, &g.Hibernating, &lastEvolved, &g.CreatedAt)

	if err == sql.ErrNoRows {
		return g, nil // empty genome — species not yet observed
	}
	if err != nil {
		return nil, fmt.Errorf("get genome %s: %w", species, err)
	}

	if err := json.Unmarshal([]byte(patternsJSON), &g.Patterns); err != nil {
		g.Patterns = nil
	}
	if err := json.Unmarshal([]byte(antibodiesJSON), &g.Antibodies); err != nil {
		g.Antibodies = nil
	}
	if err := json.Unmarshal([]byte(fossilsJSON), &g.Fossils); err != nil {
		g.Fossils = nil
	}
	if err := json.Unmarshal([]byte(providerGenesJSON), &g.ProviderGenes); err != nil {
		g.ProviderGenes = nil
	}
	if lastEvolved.Valid {
		t, err := time.Parse("2006-01-02 15:04:05", lastEvolved.String)
		if err != nil {
			return nil, fmt.Errorf("parse last_evolved for species %s: %w", species, err)
		}
		g.LastEvolved = &t
	}
	return g, nil
}

// EvolveGenome mutates a species genome based on a DoD outcome.
// On success: the approach is added to patterns (DNA).
// On failure: the approach is added to antibodies. If an antibody appears
// FossilThreshold times, it auto-promotes to a fossil (extinct approach).
// Provider genes track which model worked at what cost — the selfish gene
// optimizes provider choice for its own survival across generations.
func (s *Store) EvolveGenome(species string, doDPassed bool, entry GenomeEntry) error {
	return s.EvolveGenomeWithCost(species, doDPassed, entry, "", 0)
}

// EvolveGenomeWithCost evolves the genome with cost and provider tracking.
// The organism dies after this call. Only the genome persists.
func (s *Store) EvolveGenomeWithCost(species string, doDPassed bool, entry GenomeEntry, provider string, costUSD float64) error {
	g, err := s.GetGenome(species)
	if err != nil {
		return err
	}

	g.Generation++
	g.TotalCostUSD += costUSD
	entry.Generation = g.Generation

	// Evolve provider gene — the selfish gene tracks which models survive
	if provider != "" {
		mergedProvider := false
		for i, pg := range g.ProviderGenes {
			if pg.Provider == provider {
				g.ProviderGenes[i].TotalCost += costUSD
				if doDPassed {
					g.ProviderGenes[i].Successes++
				} else {
					g.ProviderGenes[i].Failures++
				}
				mergedProvider = true
				break
			}
		}
		if !mergedProvider {
			pg := ProviderGene{
				Provider:   provider,
				TotalCost:  costUSD,
				Generation: g.Generation,
			}
			if doDPassed {
				pg.Successes = 1
			} else {
				pg.Failures = 1
			}
			g.ProviderGenes = append(g.ProviderGenes, pg)
		}
	}

	if doDPassed {
		g.Successes++
		entry.Count = 1
		merged := false
		for i, p := range g.Patterns {
			if p.Pattern == entry.Pattern {
				g.Patterns[i].Count++
				merged = true
				break
			}
		}
		if !merged {
			g.Patterns = append(g.Patterns, entry)
		}
	} else {
		g.Failures++
		merged := false
		for i, a := range g.Antibodies {
			if a.Pattern == entry.Pattern {
				g.Antibodies[i].Count++
				if g.Antibodies[i].Count >= FossilThreshold {
					fossil := g.Antibodies[i]
					g.Fossils = append(g.Fossils, fossil)
					g.Antibodies = append(g.Antibodies[:i], g.Antibodies[i+1:]...)
				}
				merged = true
				break
			}
		}
		if !merged {
			entry.Count = 1
			g.Antibodies = append(g.Antibodies, entry)
		}
	}

	// Serialize and upsert
	patternsJSON, err := json.Marshal(g.Patterns)
	if err != nil {
		return fmt.Errorf("marshal patterns for %s: %w", species, err)
	}
	antibodiesJSON, err := json.Marshal(g.Antibodies)
	if err != nil {
		return fmt.Errorf("marshal antibodies for %s: %w", species, err)
	}
	fossilsJSON, err := json.Marshal(g.Fossils)
	if err != nil {
		return fmt.Errorf("marshal fossils for %s: %w", species, err)
	}
	providerGenesJSON, err := json.Marshal(g.ProviderGenes)
	if err != nil {
		return fmt.Errorf("marshal provider_genes for %s: %w", species, err)
	}

	_, err = s.db.Exec(`INSERT INTO genomes (species, parent_species, patterns, antibodies, fossils, provider_genes, generation, successes, failures, total_cost_usd, hibernating, last_evolved)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(species) DO UPDATE SET
			patterns = excluded.patterns,
			antibodies = excluded.antibodies,
			fossils = excluded.fossils,
			provider_genes = excluded.provider_genes,
			generation = excluded.generation,
			successes = excluded.successes,
			failures = excluded.failures,
			total_cost_usd = excluded.total_cost_usd,
			hibernating = excluded.hibernating,
			last_evolved = excluded.last_evolved`,
		species, g.ParentSpecies, string(patternsJSON), string(antibodiesJSON), string(fossilsJSON),
		string(providerGenesJSON), g.Generation, g.Successes, g.Failures, g.TotalCostUSD, g.Hibernating,
	)
	return err
}

// HibernateGenome flags a species as hibernating, usually when the immune system cascade fails completely.
func (s *Store) HibernateGenome(species string) error {
	_, err := s.db.Exec(`UPDATE genomes SET hibernating = 1 WHERE species = ?`, species)
	return err
}

// GetGenomeForPrompt returns a formatted genome string for injection into task prompts.
// Includes phage inheritance from parent species (horizontal gene transfer).
func (s *Store) GetGenomeForPrompt(species string) (string, error) {
	g, err := s.GetGenome(species)
	if err != nil {
		return "", err
	}

	// If species has no data, try parent (phage inheritance)
	if g.Generation == 0 && g.ParentSpecies != "" {
		g, err = s.GetGenome(g.ParentSpecies)
		if err != nil {
			return "", err
		}
		if g.Generation == 0 {
			return "", nil
		}
	}

	if g.Generation == 0 {
		return "", nil // no evolutionary history yet
	}

	survivalRate := 0.0
	if total := g.Successes + g.Failures; total > 0 {
		survivalRate = float64(g.Successes) / float64(total) * 100
	}

	result := fmt.Sprintf("SPECIES GENOME: %s (gen %d, %.0f%% survival, fitness %.1f, $%.4f/organism)\n",
		g.Species, g.Generation, survivalRate, g.Fitness(), g.TotalCostUSD/float64(g.Successes+g.Failures))

	if best := g.FittestProvider(); best != nil {
		result += fmt.Sprintf("FITTEST PROVIDER: %s (fitness %.1f, %d/%d success, $%.4f/attempt)\n",
			best.Provider, best.Fitness(), best.Successes, best.Successes+best.Failures, best.TotalCost/float64(best.Successes+best.Failures))
	}

	if len(g.Patterns) > 0 {
		result += "\nPATTERNS (replicate these):\n"
		for _, p := range g.Patterns {
			result += fmt.Sprintf("- %s [%dx success]\n", p.Pattern, p.Count)
		}
	}

	if len(g.Antibodies) > 0 {
		result += "\nANTIBODIES (guard against these):\n"
		for _, a := range g.Antibodies {
			reason := ""
			if a.Reason != "" {
				reason = " — " + a.Reason
			}
			result += fmt.Sprintf("- %s [%dx failure%s]\n", a.Pattern, a.Count, reason)
		}
	}

	if len(g.Fossils) > 0 {
		result += "\nFOSSILS (DO NOT attempt — extinct approaches):\n"
		for _, f := range g.Fossils {
			result += fmt.Sprintf("X EXTINCT: %s — %dx failures\n", f.Pattern, f.Count)
		}
	}

	return result, nil
}

// SpreadPhages propagates successful patterns from one species to all
// phylogenetically compatible species (same parent_species lineage).
// Only patterns (DNA) spread — antibodies and fossils are species-specific.
func (s *Store) SpreadPhages(sourceSpecies string) error {
	g, err := s.GetGenome(sourceSpecies)
	if err != nil || g.ParentSpecies == "" || len(g.Patterns) == 0 {
		return err
	}

	// Find siblings — species with the same parent
	rows, err := s.db.Query(
		`SELECT species FROM genomes WHERE parent_species = ? AND species != ?`,
		g.ParentSpecies, sourceSpecies,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	var siblings []string
	for rows.Next() {
		var sib string
		if err := rows.Scan(&sib); err == nil {
			siblings = append(siblings, sib)
		}
	}

	// Infect siblings with strong patterns (count >= 3)
	for _, sib := range siblings {
		sibGenome, err := s.GetGenome(sib)
		if err != nil {
			continue
		}
		for _, pattern := range g.Patterns {
			if pattern.Count < 3 {
				continue // only spread proven phages
			}
			// Check if sibling already has this pattern
			exists := false
			for _, sp := range sibGenome.Patterns {
				if sp.Pattern == pattern.Pattern {
					exists = true
					break
				}
			}
			if !exists {
				phage := pattern
				phage.Count = 1 // reset count in new host
				phage.Reason = fmt.Sprintf("phage from %s (gen %d)", sourceSpecies, pattern.Generation)
				if err := s.EvolveGenome(sib, true, phage); err != nil {
					return nil
				}
			}
		}
	}

	return nil
}
