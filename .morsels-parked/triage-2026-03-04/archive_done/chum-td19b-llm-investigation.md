---
title: "Auto-Postmortem Step 2: LLM Investigation & Antibody Filing"
status: done
priority: 1
type: feature
labels:
  - whale:learner
  - reliability
estimate_minutes: 45
acceptance_criteria: |
  - PostMortemWorkflow passes failure context to gemini-2.5-pro for root cause analysis.
  - LLM outputs structured JSON: root_cause, severity, proposed_fix, antibody_morsel.
  - FileAntibodyActivity creates a morsel with the proposed fix.
  - Morsel is dispatched through Shark pipeline → DoD → merge.
  - Duplicate failures (same root cause) are deduplicated.
design: |
  **Step 1:** Create `InvestigateFailureActivity`:
  - Feed failure context + repo map to gemini-2.5-pro
  - Prompt: "Analyze this workflow failure. Identify root cause. Propose a fix."
  - Output JSON: {root_cause, severity, proposed_fix, affected_files, antibody_morsel}
  
  **Step 2:** Create `FileAntibodyActivity`:
  - Generate a morsel YAML from the LLM's proposed fix
  - Write to .morsels/ with status: ready
  - Set appropriate priority based on severity
  
  **Step 3:** Deduplication:
  - Hash root_cause + affected_files
  - Skip if an antibody morsel with same hash already exists
depends_on: ["chum-td19a-failure-detection"]
---

Step 2 of the Auto-Postmortem system. Uses a premium LLM to investigate
failures and auto-files antibody morsels for the sharks to fix.
