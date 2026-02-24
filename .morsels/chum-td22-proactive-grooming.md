---
title: "Proactive whale slicing during strategic groom"
status: ready
priority: 1
type: feature
labels:
  - whale:infrastructure
  - ai
estimate_minutes: 45
acceptance_criteria: |
  - StrategicGroomWorkflow identifies whale-sized morsels (estimate > 90 min OR type=whale).
  - For each whale, the groom spawns a CrabDecompositionWorkflow to auto-slice it.
  - Decomposed sub-morsels are filed with proper dependencies and estimates.
  - Parent whale is marked as `status: done # decomposed`.
  - Morning briefing includes a "Whales Sliced" section listing decompositions.
  - Matrix notification sent for each whale decomposition.
design: |
  **Step 1:** Add whale detection to `StrategicAnalysisActivity`:
  - After analysis, scan open tasks for: estimate_minutes > 90 OR type == "whale"
  - Return a `whales_to_decompose` list in the StrategicAnalysis result
  
  **Step 2:** Add decomposition step to `StrategicGroomWorkflow`:
  - After mutations are applied, iterate `whales_to_decompose`
  - For each whale, spawn CrabDecompositionWorkflow as a child
  - Wait for completion (with timeout)
  
  **Step 3:** Auto-close parent whale:
  - On successful decomposition, update parent morsel to `status: done`
  - Add `# decomposed into <child-ids>` comment
  
  **Step 4:** Reporting:
  - Include decomposition results in morning briefing
  - Send Matrix notification for each whale sliced
depends_on: ["chum-td21-matrix-notifications"]
---

The strategic groom should proactively identify and auto-slice whale morsels
using the crab decomposition pipeline, not wait for humans to do it.
