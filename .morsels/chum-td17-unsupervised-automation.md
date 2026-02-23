---
title: "Implement Unsupervised Automation Pipeline"
status: backlog
priority: 3
type: feature
labels:
  - whale:learner
  - ai
estimate_minutes: 180
acceptance_criteria: |
  - System vector-embeds LearnerRequest payloads (Diff and Execution Terminal Output) into a vector store (e.g. pgvector or Qdrant).
  - A new asynchronous workflow/activity periodically clusters these embeddings to find semantic similarity.
  - When a cluster of similar tasks is found, it uses gemini-3.1-pro to synthesize the repetitive shell commands/code.
  - It automatically generates a `protein_candidate` backlog morsel detailing a proposed script or Temporal workflow.
design: |
  **Context:** The current learner extracts lessons per-bead, missing macro-patterns across time. To build an unsupervised automation pipeline, we must embed and cluster the agent's work history.
  
  **Implementation Steps:**
  1. Add vector embedding to `ContinuousLearnerWorkflow`. Extract chunks from `LearnerRequest.ExecutionOutput` and `DiffSummary`. 
  2. Setup pgvector/Qdrant in the storage tier for storing embeddings alongside task metadata.
  3. Create an asynchronous job (e.g., in `StrategicGroomWorkflow` or a new cron) that clusters the embeddings (K-means or DB-SCAN).
  4. Build a new activity that passes the items in high-density clusters to Gemini 3.1 Pro with the "extract common boilerplate into script" prompt.
  5. The model outputs a generated bash script / Temporal workflow candidate.
  6. Call `a.DAG.CreateTask()` to file it as a `protein_candidate` morsel.
---

This morsel tracks the implementation of the Unsupervised Automation Pipeline as documented in ARCHITECTURE.md. It actively paves the cowpaths by identifying when agents do the same thing repeatedly and scripting it.
