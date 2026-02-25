---
title: "Document workflow call graph and execution paths"
status: ready
priority: 1
type: documentation
labels:
  - technical-debt
  - observability
  - self-healing
estimate_minutes: 90
acceptance_criteria: |
  - New doc created: docs/architecture/WORKFLOW_CALL_GRAPH.md
  - Document contains ASCII diagram showing all workflow relationships
  - Document explains 3-lane dispatcher routing (familiar/unfamiliar/complex)
  - Document lists all 10+ workflows with purpose and triggering conditions
  - Document includes decision tree for routing logic
  - Cross-references added from ARCHITECTURE.md and README.md
design: |
  **Problem:** CHUM has 10+ workflows with unclear relationships. New developers can't
  trace execution paths. Debugging cross-workflow issues is difficult.

  **Current workflows:**
  - ChumAgentWorkflow (main execution)
  - DispatcherWorkflow (scheduler)
  - CambrianExplosionWorkflow (parallel provider competition)
  - TurtleToCrabWorkflow (planning + decomposition pipeline)
  - TurtleWorkflow (single-stage planning)
  - CrabDecompositionWorkflow (task decomposition)
  - PlanningCeremonyWorkflow (interactive planning)
  - StrategicGroomWorkflow (strategic grooming)
  - TacticalGroomWorkflow (tactical grooming)
  - ContinuousLearnerWorkflow (learning loop)
  - JanitorWorkflow (cleanup)
  - PaleontologistWorkflow (archeology)
  - CalcificationWorkflow (pattern crystallization)

  **Document structure:**

  1. **Overview** - High-level purpose of workflow orchestration

  2. **Dispatcher Logic** - The heart of routing
     ```
     DispatcherWorkflow (scheduled every tick_interval)
       ├─> Lane 1: Familiar (Gen > 0) → ChumAgentWorkflow
       ├─> Lane 2: Unfamiliar (Gen 0) → CambrianExplosionWorkflow
       └─> Lane 3: Complex (Score > 70) → TurtleToCrabWorkflow
     ```

  3. **Main Execution Paths**
     - Lane 1: ChumAgentWorkflow → plan → execute → review → DoD → record
     - Lane 2: CambrianExplosionWorkflow → parallel execution → winner selection
     - Lane 3: TurtleToCrabWorkflow → TurtleWorkflow → CrabDecompositionWorkflow → spawn morsels

  4. **Background Workflows** - Fire-and-forget child workflows
     - ContinuousLearnerWorkflow (triggered after successful completions)
     - JanitorWorkflow (triggered on schedule for cleanup)
     - CalcificationWorkflow (triggered after N consecutive successes)
     - PaleontologistWorkflow (triggered for pattern analysis)

  5. **Decision Tree** - ASCII flowchart for dispatcher routing

  6. **Workflow Details Table**
     | Workflow | Triggered By | Child Workflows | Purpose |
     |----------|--------------|-----------------|---------|
     | ... | ... | ... | ... |

  7. **Parent-Child Relationships**
     - Which workflows spawn which
     - ABANDON vs TERMINATE parent close policies

  **Steps:**
  1. Read all workflow files to understand relationships
  2. Trace dispatcher logic in workflow_dispatcher.go lines 145-170
  3. Create ASCII diagrams using box-drawing characters
  4. Document each workflow's purpose and triggering conditions
  5. Add cross-references to other architecture docs

  **Success metric:** A new developer can read this doc and understand the entire
  workflow execution model in 15 minutes.
---

# Document Workflow Call Graph

10+ workflows exist with unclear relationships. Create a comprehensive call graph
document so developers can understand execution paths and debug cross-workflow issues.

This unblocks debugging and future refactoring by making the system legible.
