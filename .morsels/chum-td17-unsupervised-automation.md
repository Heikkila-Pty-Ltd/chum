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
  - System vector-embeds LearnerRequest payloads into `sqlite-vec` (kept in single-binary).
  - Raw terminal logs are first sanitized by a fast LLM into a `Semantic Action Summary` and `Sanitized Error Trace`.
  - Clustering is event-driven (e.g., every N tasks), not cron-based.
  - When a cluster of similar action summaries reaches a "signal point", `gemini-3.1-pro` synthesizes a deterministic bash or TS script.
  - System automatically generates a `protein_candidate` backlog morsel with the script.
  - Clusters of identical `Sanitized Error Traces` feed directly into system Antibodies/Priming Instructions.
design: |
  **Context:** The current learner extracts lessons per-bead, missing macro-patterns across time. To build an unsupervised automation pipeline, we must embed and cluster the agent's work history.
  
  **Implementation Steps:**
  1. Add `sqlite-vec` to CHUM.
  2. Implement a `SanitizeLogsActivity` pass that strips raw logs of timestamps/line numbers to generate geometric summaries.
  3. Emit and store vector embeddings of these summaries into SQLite.
  4. Implement an event-driven cluster check (DBSCAN) every X tasks.
  5. Dense clusters of actions trigger a generation of a deterministic script (not a loose markdown workflow).
  6. Call `a.DAG.CreateTask()` to file it as a `protein_candidate` morsel.
---

This morsel tracks the implementation of the Unsupervised Automation Pipeline as documented in ARCHITECTURE.md. It actively paves the cowpaths by identifying when agents do the same thing repeatedly and scripting it.
