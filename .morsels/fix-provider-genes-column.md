---
title: "Add provider_genes column to genomes table"
status: ready
priority: 0
type: task
labels:
  - whale:infrastructure
  - bug
  - schema
estimate_minutes: 5
acceptance_criteria: |
  - genomes table has provider_genes TEXT column (default '{}')
  - Migration added to store.go via addColumnIfNotExists
  - EvolveGenomeActivity no longer crashes with "no such column: provider_genes"
  - go build and go test ./internal/store/... pass cleanly
design: |
  1. Add migration in store.go: addColumnIfNotExists(db, "genomes", "provider_genes", "provider_genes TEXT NOT NULL DEFAULT '{}'")
  2. Update Genome struct in genomes.go if not already present
  3. Verify EvolveGenomeWithCost writes to this column
depends_on: []
---

EvolveGenomeActivity crashes when writing provider fitness data because the
genomes table is missing the provider_genes column. This prevents the Selfish
Gene model from recording which providers succeed at which species.
