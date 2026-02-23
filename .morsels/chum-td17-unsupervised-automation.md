---
title: "Implement Unsupervised Automation Pipeline"
status: ready
priority: 3
type: feature
labels:
  - whale:learner
  - ai
estimate_minutes: 180
acceptance_criteria: |
  - System vector-embeds task summaries into BLOBs in `task_embeddings` table (pure Go, no CGO).
  - Raw terminal logs are sanitized by a fast LLM into Semantic Action Summary + Sanitized Error Geometry.
  - Clustering is event-driven (every N completions, configurable via `chum.toml`).
  - DBSCAN params (epsilon, minPts) configurable under `[learner.clustering]`.
  - Action clusters → protein scripts (`.sh`/`.ts` in `scripts/`).
  - Error clusters → antibodies/priming instructions.
  - Protein scripts auto-filed as `protein_candidate` morsels → dispatched through Shark → DoD → self-merge.
  - Humans review during grooming only; CHUM self-tests and self-merges.
design: |
  **Phase 1: Data Pre-processing & Embedding**
  - Add `SanitizeLogsActivity` (fast LLM → Action Summary + Error Geometry).
  - Create `task_embeddings` table (float32 BLOBs, 768-dim).
  - `EmbedActivity` calls Google `text-embedding-004` API (same OAuth as gemini-pro).
  
  **Phase 2: Signal Point & Clustering**
  - Pure-Go cosine similarity + DBSCAN (no sqlite-vec, no CGO).
  - Completion counter in `RecordOutcomeActivity` triggers cluster check every N tasks.
  - Dense clusters emit `ProteinSynthesisSignal` or `AntibodySynthesisSignal`.
  
  **Phase 3: Protein Synthesis & Self-Test**
  - `SynthesizeProteinActivity` uses `gemini-3.1-pro` to generate deterministic scripts.
  - Auto-file morsel → Shark pipeline → DoD gate → merge or escalate.
depends_on: []
---

# Unsupervised Automation Pipeline

Watches the herd, clusters repetitive human/agent behavior, and paves the cowpaths
into deterministic scripts. Fully autonomous self-test loop via Shark.
