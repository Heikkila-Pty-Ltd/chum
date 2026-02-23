---
title: "Unsupervised Automation Phase 1: Embedding Pipeline"
status: ready
priority: 3
type: feature
labels:
  - whale:learner
  - ai
  - phase:1
estimate_minutes: 60
acceptance_criteria: |
  - `SanitizeLogsActivity` added to ContinuousLearnerWorkflow.
  - Fast LLM (gemini-flash) processes raw terminal logs into Semantic Action Summary + Sanitized Error Geometry.
  - `task_embeddings` table created in cortex.db (float32 BLOBs, 768-dim).
  - `EmbedActivity` calls Google text-embedding-004 API and stores vectors.
  - Graceful degradation: if embedding API fails, task is skipped (not fatal).
design: |
  **Step 1:** Create migration for `task_embeddings` table:
  ```sql
  CREATE TABLE IF NOT EXISTS task_embeddings (
      id INTEGER PRIMARY KEY,
      task_id TEXT NOT NULL,
      project TEXT NOT NULL,
      summary TEXT NOT NULL,
      error_geom TEXT,
      embedding BLOB NOT NULL,
      created_at DATETIME DEFAULT CURRENT_TIMESTAMP
  );
  ```
  
  **Step 2:** Add `SanitizeLogsActivity`:
  - Input: raw terminal stdout/stderr from ChumAgentWorkflow
  - Use gemini-flash to extract Action Summary + Error Geometry
  - Strip timestamps, line numbers, file paths (keep semantic meaning)
  
  **Step 3:** Add `EmbedActivity`:
  - Call text-embedding-004 with the Action Summary
  - Store the float32 vector as a BLOB in task_embeddings
  - Use same Google OAuth as gemini-pro
depends_on: []
---

Phase 1 of the Unsupervised Automation Pipeline. Builds the data
foundation: log sanitization and vector embedding storage.
