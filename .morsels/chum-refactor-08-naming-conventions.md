---
title: "Standardize naming conventions and document metaphor usage"
status: open
priority: 4
type: documentation
labels:
  - technical-debt
  - maintainability
  - self-healing
estimate_minutes: 60
acceptance_criteria: |
  - New section added to CONTRIBUTING.md: "Naming Conventions"
  - Metaphor usage documented with examples
  - Glossary created: docs/GLOSSARY.md mapping terms to concepts
  - Guidelines for when to use each metaphor (biological, ocean, chemistry)
  - Examples of good vs bad naming
  - No code changes required (documentation only)
design: |
  **Problem:** The codebase uses inconsistent metaphors across different components:
  - **Biological:** Genome, DNA, antibodies, species, organism, Cambrian explosion, evolution
  - **Ocean creatures:** Shark, turtle, crab, stingray, whale
  - **Chemistry:** Calcifier, protein synthesis
  - **Software:** Activities, workflows, dispatcher

  This makes it hard for new developers to understand naming patterns and predict
  component names.

  **Solution: Document the metaphor system**

  **Core metaphors (keep these):**

  1. **Evolutionary Biology (CHUM's core philosophy)**
     - `genome` = accumulated successful patterns for a task species
     - `species` = task type classification
     - `organism` = individual task execution attempt
     - `antibodies` = recorded failures to avoid
     - `Cambrian Explosion` = parallel provider competition
     - `DNA` = successful code patterns
     - `evolution` = learning from success/failure

  2. **Ocean Creatures (execution agents)**
     - `shark` = main coding agent (fast, focused, disposable)
     - `turtle` = planning agent (slow, deliberate, consensus-building)
     - `crab` = decomposition agent (breaks large tasks into morsels)
     - `stingray` = code analysis agent (scanning, probing)
     - `whale` = background process agents (learner, janitor)

  3. **Chemistry (pattern crystallization)**
     - `calcification` = hardening patterns into deterministic scripts
     - `protein synthesis` = generating executable scripts from patterns
     - `crystallization` = evolution from stochastic → deterministic

  **Naming guidelines:**

  - **Workflows:** Use creature names for agent workflows (TurtleWorkflow, CrabDecompositionWorkflow)
  - **Activities:** Use verb + domain (ExecuteAgentActivity, ParseOutputActivity)
  - **Background processes:** Use biological terms (ContinuousLearnerWorkflow, JanitorWorkflow)
  - **Data structures:** Use clear domain terms (TaskRequest, ExecutionResult)
  - **Avoid mixing metaphors:** Don't create "SharkGenomeProteinActivity"

  **Glossary structure (docs/GLOSSARY.md):**

  ```markdown
  # CHUM Glossary

  ## Core Concepts
  - **Morsel:** A unit of work (task/issue) tracked in .morsels/
  - **Organism:** A single execution attempt of a morsel
  - **Species:** Task type classification for genomic memory

  ## Evolutionary Biology Terms
  - **Genome:** Accumulated successful patterns...
  - **Antibodies:** Recorded failures...
  - **Cambrian Explosion:** Parallel provider competition...

  ## Agent Types (Ocean Creatures)
  - **Shark:** Fast coding agent...
  - **Turtle:** Planning agent...
  - **Crab:** Decomposition agent...

  ## Pattern Crystallization
  - **Calcification:** Converting successful patterns...
  - **Protein:** Deterministic script synthesized...

  ## Temporal/Infrastructure
  - **Workflow:** Temporal workflow...
  - **Activity:** Temporal activity...
  ```

  **CONTRIBUTING.md update:**
  - Add "Naming Conventions" section
  - Link to GLOSSARY.md
  - Provide examples of good naming
  - Explain when to introduce new terms

  **Steps:**
  1. Audit current codebase for all metaphor usage
  2. Create docs/GLOSSARY.md with comprehensive term definitions
  3. Update CONTRIBUTING.md with naming guidelines section
  4. Add examples of good vs confusing names
  5. Cross-reference from README.md

  **Success metric:** A new developer can read the glossary and predict what
  "StingrayAnalysisActivity" does without reading code.
depends_on: ["chum-refactor-04-workflow-call-graph"]
---

# Standardize Naming Conventions

Mixed metaphors (genome, shark, calcifier, protein) make the codebase confusing.
Document the metaphor system, create a glossary, and establish naming guidelines
for future development.

This is documentation-only (no code changes) but unblocks understanding for all
future refactoring work.
