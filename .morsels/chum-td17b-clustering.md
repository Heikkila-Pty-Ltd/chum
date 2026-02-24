---
title: "Unsupervised Automation Phase 2: Signal Point Clustering"
status: ready
priority: 3
type: feature
labels:
  - whale:learner
  - ai
  - phase:2
estimate_minutes: 60
acceptance_criteria: |
  - Pure-Go cosine similarity function implemented (no CGO).
  - DBSCAN clustering algorithm running over task_embeddings table.
  - Completion counter in RecordOutcomeActivity triggers cluster check every N tasks.
  - `[learner.clustering]` config section added to chum.toml (epsilon, minPts, interval).
  - Dense clusters emit ProteinSynthesisSignal or AntibodySynthesisSignal.
design: |
  **Step 1:** Implement cosine similarity in Go:
  ```go
  func cosineSim(a, b []float32) float32 { ... }
  ```
  
  **Step 2:** Implement DBSCAN:
  - Load all embeddings from task_embeddings
  - Find epsilon-neighborhoods via brute-force cosine search
  - Cluster with minPts threshold
  - Return cluster labels
  
  **Step 3:** Add signal point trigger:
  - Counter in RecordOutcomeActivity increments on each completion
  - When count % signal_point_interval == 0, run DBSCAN
  - If any cluster hits protein_cluster_threshold, emit signal
  
  **Step 4:** Add config:
  ```toml
  [learner.clustering]
  signal_point_interval = 10
  dbscan_epsilon = 0.3
  dbscan_min_points = 3
  protein_cluster_threshold = 5
  antibody_cluster_threshold = 3
  ```
depends_on: ["chum-td17a-embedding-pipeline"]
---

Phase 2 of the Unsupervised Automation Pipeline. Implements clustering
over the embedding vectors to detect repetitive patterns.
