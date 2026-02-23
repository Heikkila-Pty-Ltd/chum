---
title: "Unsupervised Automation Phase 3: Protein Synthesis & Self-Test"
status: ready
priority: 3
type: feature
labels:
  - whale:learner
  - ai
  - phase:3
estimate_minutes: 60
acceptance_criteria: |
  - SynthesizeProteinActivity uses gemini-2.5-pro to generate deterministic .sh or .ts scripts.
  - Scripts are written to the project's scripts/ directory.
  - A protein_candidate morsel is auto-filed with the script attached.
  - The morsel is dispatched through the standard Shark pipeline → DoD → merge.
  - On DoD FAIL, the morsel is escalated for human review during grooming.
design: |
  **Step 1:** Create `SynthesizeProteinActivity`:
  - Triggered by ProteinSynthesisSignal from Phase 2
  - Collects all tasks in the dense cluster
  - Feeds their action summaries to gemini-2.5-pro
  - Prompt: "These N tasks all did the same thing. Write a deterministic script."
  - Output: .sh or .ts script
  
  **Step 2:** Write script to `scripts/`:
  - Filename derived from cluster summary
  - Make executable (chmod +x)
  
  **Step 3:** Auto-file protein_candidate morsel:
  - Use DAG.CreateTask() to file the morsel
  - Include the script path in the design field
  - Set status: ready so Shark picks it up
  
  **Step 4:** Shark self-test loop:
  - Shark executes the morsel → writes tests → runs DoD
  - PASS → merged, morsel closed
  - FAIL → escalated for human grooming
depends_on: ["chum-td17b-clustering"]
---

Phase 3 of the Unsupervised Automation Pipeline. Synthesizes deterministic
scripts from clustered patterns and self-tests via the Shark pipeline.
