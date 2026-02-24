# Proteins: Deterministic Workflow Sequences

> A protein is not a prompt. It's a **program** — a deterministic sequence of tool-calling molecules that produces a predictable artifact. The LLM is the CPU, not the architect.

## Core Principle

**Reduce unpredictability by replacing vague LLM prompts with explicit, granular instructions that call specific tools and produce specific artifacts.**

Bad molecule: _"Research the competitors"_
Good molecule: _"Run brave-search for the 5 closest competitors to {{project}}. For each result: run firecrawl on their URL, download sitemap, screenshot homepage + pricing + features pages via Playwright at 1440px. Save HTML + CSS + screenshots to `./research/competitors/{{domain}}/`. Output: competitor_manifest.json with URLs, page counts, and screenshot paths."_

## Impeccable Command Library

All 17 commands from [impeccable.style](https://impeccable.style) are available as molecule building blocks:

| Command | When to use in a protein |
|---------|--------------------------|
| `/teach-impeccable` | **Input**: teach the model your project's design DNA before any frontend work |
| `/audit` | **Gate**: run after any design change to check compliance with system |
| `/critique` | **Research**: evaluate competitor or own designs for strengths/weaknesses |
| `/extract` | **Harvest**: extract reusable design components from existing pages |
| `/normalize` | **Cleanup**: harmonize inconsistent spacing, sizing, typography |
| `/polish` | **Refinement**: final pass for micro-interactions, shadows, transitions |
| `/simplify` | **Refinement**: remove visual clutter, reduce cognitive load |
| `/clarify` | **Refinement**: improve information hierarchy and scannability |
| `/harden` | **Robustness**: edge cases, error states, loading states, empty states |
| `/bolder` | **Differentiation**: amplify visual impact, stronger brand expression |
| `/quieter` | **Restraint**: tone down excessive effects, restore elegance |
| `/animate` | **Motion**: add purposeful transitions and micro-animations |
| `/colorize` | **Color**: improve palette, contrast ratios, semantic color usage |
| `/delight` | **Polish**: surprising details that make the UX memorable |
| `/optimize` | **Performance**: reduce paint, layout shifts, animation costs |
| `/adapt` | **Responsive**: breakpoint-specific refinements |
| `/onboard` | **UX**: first-run experience, progressive disclosure |

7 reference domains (auto-loaded): typography, color-and-contrast, spatial-design, motion-design, interaction-design, responsive-design, ux-writing.

---

## Example Protein: `frontend-design-from-scratch`

### Phase 1: Competitive Intelligence

#### Molecule 1: Competitor Discovery + Crawl
```yaml
id: competitor-crawl
action: script
provider: gemini-flash  # cheap model for tool orchestration
parallelism: 5  # crawl all 5 simultaneously
do: |
  1. brave-search "{{project_description}} competitor website" → top 5 URLs
  2. For each URL, in parallel:
     a. firecrawl → save full HTML + CSS to ./research/competitors/{{domain}}/html/
     b. playwright → screenshot all pages found in site menu
        (typically: homepage, pricing, features, about, contact)
        at 375px, 768px, 1440px viewports
     c. wget sitemap.xml → ./research/competitors/{{domain}}/sitemap.xml
     d. Save all to a folder tagged by domain
  3. Write competitor_manifest.json indexing all paths
output: ./research/competitors/competitor_manifest.json
```

#### Molecule 2: Extract Components from Each Competitor (parallel × 5)
```yaml
id: competitor-extract
action: prompt
provider: gemini-pro
parallelism: 5
input: ./research/competitors/{{domain}}/html/
skill: /extract
do: |
  Load the downloaded HTML + CSS for {{domain}}.
  Run /extract to identify and isolate all reusable design components:
  - Navigation patterns (header, mobile menu, breadcrumbs)
  - Hero sections (layout, CTA placement, imagery style)
  - Card patterns (product cards, feature cards, pricing cards)
  - Form patterns (input styles, validation, button styles)
  - Footer patterns (layout, link structure, social)
  - Typography scale (heading sizes, body, captions)
  - Color tokens (primary, secondary, accent, neutrals)
  - Spacing tokens (padding, margin, gap patterns)
  For each component:
  - Save isolated HTML/CSS snippet to ./research/competitors/{{domain}}/components/{{component_name}}.html
  - Save a metadata JSON with: component_type, visual_weight, complexity_score
  Write component_inventory.json listing all extracted components.
output: ./research/competitors/{{domain}}/components/component_inventory.json
```

#### Molecule 3: Critique Each Competitor (parallel × 5)
```yaml
id: competitor-critique
action: prompt
provider: gemini-pro
parallelism: 5
input: ./research/competitors/{{domain}}/
skill: /critique
do: |
  Load screenshots from ./research/competitors/{{domain}}/screenshots/.
  Run /critique on each page. For each page, evaluate:
  - Visual hierarchy (1-10) + specific observations
  - Color harmony (1-10) + palette analysis
  - Typography quality (1-10) + font identification
  - Whitespace / spatial design (1-10)
  - CTA clarity and conversion design (1-10)
  - Mobile-readiness (from 375px screenshots)
  - Accessibility red flags
  - Anti-patterns detected (per Impeccable anti-pattern list)
  Write structured JSON to ./research/competitors/{{domain}}/critique.json
output: ./research/competitors/{{domain}}/critique.json
```

### Phase 2: Cross-Competitor Pattern Discovery

#### Molecule 4: Find Common Components via Vector Search
```yaml
id: component-clustering
action: script
provider: gemini-flash
input: ./research/competitors/*/components/component_inventory.json
do: |
  1. Load all component inventories across all 5 competitors
  2. For each component, generate a text embedding (component_type + HTML structure + visual description)
  3. Cluster components by cosine similarity (threshold > 0.75)
  4. For each cluster:
     - Label the pattern (e.g. "hero-with-cta", "pricing-card-3-tier")
     - Identify the best exemplar (highest critique score from its site)
     - Record frequency (how many competitors use this pattern)
  5. Rank clusters by frequency × average quality score
  Write to ./research/common_patterns.json with cluster labels, exemplars, and frequency
output: ./research/common_patterns.json
```

#### Molecule 5: Synthesize Design Intelligence
```yaml
id: design-synthesis
action: prompt
provider: claude-sonnet  # needs sophisticated reasoning
input:
  - ./research/competitors/*/critique.json
  - ./research/common_patterns.json
do: |
  Read all 5 competitor critiques and the common pattern clusters.
  Produce a design brief containing:
  1. COMMON PATTERNS: What all competitors do (table stakes — must have)
  2. UNIQUE STRENGTHS: What makes each competitor stand out (steal the best ideas)
  3. UNIVERSAL WEAKNESSES: Shared blind spots (opportunities to differentiate)
  4. RECOMMENDED APPROACH: Design strategy that:
     - Incorporates all table-stakes patterns
     - Steals the best unique elements from each competitor
     - Exploits every universal weakness to be demonstrably better
  5. SPECIFIC TOKENS: Color palette, font stack, spacing scale, border radii
  Write to ./research/design_brief.md
output: ./research/design_brief.md
```

### Phase 3: Iterative Design Refinement

#### Molecule 6: Generate 3 Design Options
```yaml
id: design-options
action: prompt
provider: claude-sonnet
parallelism: 3
input: ./research/design_brief.md + ./research/common_patterns.json
do: |
  Based on the design brief, create option {{n}} of 3:
  - Option 1: Conservative (closest to industry best practice)
  - Option 2: Bold (maximum differentiation from competitors)
  - Option 3: Refined (elegant synthesis, best of both)
  For each option:
  - Create globals.css with complete design system (tokens, utilities, components)
  - Create demo page at app/demo/option-{{n}}/page.tsx
  - Include: nav, hero, feature grid, social proof, pricing, CTA, footer
  - Use actual content (not lorem ipsum) based on {{project_description}}
output: app/demo/option-{{n}}/
```

#### Molecule 7: Iterative Audit/Critique Loop (repeat until convergence)
```yaml
id: design-refinement-loop
action: loop
max_iterations: 5
convergence: "critique finds 0 issues with severity > minor"
provider: claude-sonnet
input: app/demo/option-{{selected}}/
steps:
  - skill: /audit
    do: |
      Run /audit against the current design. Check compliance with:
      - All Impeccable anti-patterns (no Inter, no pure black, no card nesting, etc.)
      - Typography scale consistency
      - Color contrast ratios (WCAG AA minimum)
      - Spacing rhythm consistency
      Write findings to ./refinement/pass-{{iteration}}/audit.json

  - skill: /critique
    do: |
      Run /critique on the current state. Identify:
      - Remaining visual hierarchy issues
      - Opportunities for stronger differentiation
      - UX writing improvements
      Write findings to ./refinement/pass-{{iteration}}/critique.json

  - skill: /bolder
    condition: "iteration <= 2"
    do: |
      Apply /bolder to amplify brand expression.
      Strengthen visual impact without sacrificing usability.

  - skill: /harden
    do: |
      Apply /harden — add error states, loading states, empty states,
      edge cases. Make every component bulletproof.

  - skill: /simplify
    condition: "audit found complexity warnings"
    do: |
      Apply /simplify — remove visual clutter revealed by audit.
      Reduce cognitive load. Every element must earn its place.

output: app/demo/option-{{selected}}/  # updated in place
creates_morsels: true  # each iteration creates a dependent morsel
```

#### Molecule 8: Human Review Gate
```yaml
id: design-review
action: human-gate
do: |
  npm run dev → start local dev server.
  Send notification to human with:
  - URLs for all 3 design options
  - Link to ./refinement/ showing all audit/critique passes
  - "Which option do you prefer? Any feedback?"
  Await human selection + feedback.
timeout_minutes: 1440
fallback: "Select option with highest cumulative audit score"
output: selected_option + feedback.md
```

### Phase 4: Design System Codification

#### Molecule 9: Codify Design System
```yaml
id: codify-design-system
action: prompt
provider: claude-sonnet
input: selected_option/ + feedback.md
skill: /teach-impeccable
do: |
  Using the winning design and human feedback:
  1. Finalize globals.css as the canonical design system
  2. Extract all tokens to design-tokens.ts (colors, fonts, spacing, shadows, radii)
  3. Create DESIGN_SYSTEM.md with usage examples for every pattern
  4. Run /teach-impeccable to record the project's design DNA
  This file becomes part of the project genome — injected into
  every future frontend morsel's prompt.
output: ./design-system/
```

#### Molecule 10: Pre-Generate Component Library
```yaml
id: generate-component-library
action: prompt
provider: claude-sonnet
parallelism: 5
input: ./design-system/ + ./research/common_patterns.json
do: |
  Based on the codified design system and the common patterns
  discovered across competitors, pre-generate the most-likely-needed
  components for this project type:

  From common_patterns.json, extract the top {{N}} most frequent
  component patterns. For each pattern:
  1. Create a React component using the project's design tokens
  2. Include all variants (primary, secondary, sizes, states)
  3. Add TypeScript props interface
  4. Add Storybook-compatible documentation
  5. Save to components/ui/{{component_name}}.tsx

  These become the project's design asset library. Future
  design morsels will search this library before building new.
output: components/ui/
```

### Phase 5: Ongoing Design Morsels (Crab Enhancement)

When any future morsel with a `ui` or `frontend` label arrives:

```yaml
id: crab-design-morsel-enhancement
action: crab-instruction
do: |
  Before decomposing this morsel, the crab must:
  1. Run /teach-impeccable to load the project's design DNA
  2. Search components/ui/ for reusable components that match
     the design requirements of this morsel
  3. Include found components in the morsel description:
     "REUSABLE COMPONENTS AVAILABLE: Button (components/ui/Button.tsx),
      Card (components/ui/Card.tsx), ..."
  4. Include design system reference:
     "DESIGN SYSTEM: see design-system/DESIGN_SYSTEM.md and
      design-system/design-tokens.ts for all tokens"
  5. Add /audit as a DoD check for this morsel
```

---

## Protein Lifecycle

```
DISCOVERY → CODIFICATION → REUSE → MUTATION → NATURAL SELECTION

1. New task type, no matching protein
   → Sophisticated model decomposes from scratch
   → Steps + tool calls + artifacts recorded

2. CalcifyProteinActivity extracts molecule sequence
   → generation-0 protein with fitness=0

3. Similar task arrives → Crab queries protein catalog
   → Instantiates protein → each molecule = morsel
   → Dependencies auto-wired from molecule order
   → Execution recorded as a "fold"

4. PaleontologistActivity analyzes fold data
   → Fork protein → tweak molecules, providers, order
   → A/B test old vs new protein

5. Higher-fitness proteins get preferential use
   → Low-fitness proteins go extinct
   → Winning molecules spread to other proteins
```

## Automated Retrospectives

Every molecule and every protein fold gets a **mini-retro** — a cheap, structured post-mortem that feeds directly back into protein evolution.

### Molecule-Level Retro (runs after each step)

```yaml
retro:
  provider: gemini-flash  # retros should be cheap
  input:
    - molecule definition (what was asked)
    - execution log (what actually happened)
    - token usage + duration
    - UBS findings (if any)
    - DoD result (if applicable)
  output: molecule_retro.json
  schema:
    worked: ["Used correct import paths", "Component rendered on first try"]
    failed: ["Hardcoded color instead of using design token", "Missed mobile breakpoint"]
    improve: ["Should have read tokens.ts before writing CSS", "Add 375px viewport check"]
    token_waste: "650 tokens spent on a retry that could be avoided by reading types first"
    verdict: "keep | rewrite | split | merge | remove"
```

### Protein-Level Retro (runs after all molecules complete)

```yaml
retro:
  provider: claude-sonnet  # needs reasoning for cross-molecule analysis
  input:
    - all molecule retros from this fold
    - total protein metrics (tokens, duration, success, quality)
    - comparison to previous folds of same protein (if any)
  output: protein_retro.json
  schema:
    sequence_issues: ["Molecule 3 had info Molecule 5 needed — should swap order"]
    redundant_steps: ["Molecule 2 and 4 both checked typography — merge"]
    missing_steps: ["No mobile viewport check until Molecule 7 — add earlier"]
    provider_mismatch: ["Molecule 1 used claude-sonnet but only needed gemini-flash"]
    recommended_mutations:
      - action: swap_order
        molecules: [3, 5]
        reason: "Molecule 5 needed competitor data that Molecule 3 produces"
      - action: merge
        molecules: [2, 4]
        reason: "Both run /critique on typography — single pass is cheaper"
      - action: add_step
        after: 1
        template: "Verify all competitor URLs resolve before crawling"
      - action: change_provider
        molecule: 1
        from: claude-sonnet
        to: gemini-flash
        reason: "Tool orchestration doesn't need expensive reasoning"
```

### How Retros Drive Mutations

```
Fold 1: protein-v1 runs → retro says "swap molecules 3 and 5"
Fold 2: protein-v1 runs again → same retro finding
Fold 3: PaleontologistActivity sees pattern (2+ identical findings)
  → Forks protein-v1 → protein-v2 with molecules swapped
  → Next similar task: A/B test v1 vs v2
  → v2 wins (fewer tokens, higher quality)
  → v1 deprecated, v2 becomes default
```

Retro findings that appear **once** are noted. Findings that appear **twice** trigger a mutation proposal. Findings that appear **three times** trigger an automatic fork — the system doesn't wait for permission. Nature has no meetings.

## The Fossil Record (Cross-Project Genome Accumulation)

> After several frontend projects, the system should have templated components, sections, buttons, cards — a fossil record. The next project doesn't reinvent the wheel; it evolves from the previous generation's best work.

Every molecule has a **fossilization hook** — a post-success extraction step that deposits reusable artifacts into the cross-project fossil record.

### What gets fossilized

| Artifact type | Where it lives | How it's reused |
|--------------|----------------|-----------------|
| **Components** | `fossils/components/hero-section-v3.tsx` | Crab includes in future design morsels |
| **Design tokens** | `fossils/tokens/luxury-dark-palette.ts` | Pre-loaded into `/teach-impeccable` |
| **CSS patterns** | `fossils/css/responsive-grid-3col.css` | Injected into design system molecule |
| **Critique findings** | `fossils/critiques/competitor-teardowns/` | Skip competitor research for similar projects |
| **Protein folds** | `fossils/proteins/frontend-design-v3.yaml` | Entire protein sequences that worked |
| **Anti-patterns** | `fossils/antibodies/never-use-inter.md` | Injected as Impeccable anti-pattern overrides |

### Fossilization hooks per molecule

```yaml
# Every molecule can declare what to fossilize on success:
fossilize:
  - type: component
    source: components/ui/CourseCard.tsx
    tags: [card, image-top, hover-glow, responsive]
    embed: true  # generate vector embedding for fuzzy search

  - type: tokens
    source: design-system/design-tokens.ts
    tags: [dark-mode, luxury, gold-accent]
    embed: true

  - type: critique
    source: ./research/competitors/{{domain}}/critique.json
    tags: [golf, directory, competitor]
    embed: true
```

### How fossils are retrieved

When a new project starts a design protein:

```
Molecule 0 (auto-injected): Search Fossil Record
  → Vector search: "golf directory dark luxury design system"
  → Returns: 3 matching component fossils, 1 token fossil, 2 critique fossils
  → Inject into Molecule 6 (design options):
    "FOSSIL RECORD — reusable components from past projects:
     - CourseCard (fossils/components/course-card-v2.tsx) — image-top card with hover glow
     - LuxuryHero (fossils/components/luxury-hero-v1.tsx) — gradient overlay with CTA
     - DarkPalette (fossils/tokens/luxury-dark-palette.ts) — tested gold-on-dark scheme
     Evolve from these. Do NOT reinvent."
```

### Accumulation over time

```
Project 1 (golf-directory):
  → Fossilizes: CourseCard, DarkPalette, HeroSection, NavBar
  → Records: "luxury dark theme" design DNA

Project 2 (wine-cellar-app):
  → Searches fossils → finds CourseCard pattern, DarkPalette
  → Evolves: WineCard (from CourseCard fossil), RefinedDarkPalette
  → Fossilizes: WineCard, RefinedDarkPalette, TastingNotes component

Project 3 (boutique-hotel-booking):
  → Searches fossils → finds WineCard, RefinedDarkPalette, HeroSection
  → Evolves: RoomCard (from WineCard → CourseCard lineage)
  → The card component has now evolved through 3 generations
  → Each generation is faster to build and higher quality
```

### The Vector Store

Fossils need fuzzy matching — "I need a card component for a luxury product" should find `CourseCard` even if the words don't match exactly.

```sql
CREATE TABLE fossils (
  id           TEXT PRIMARY KEY,
  type         TEXT NOT NULL,        -- component, tokens, critique, protein, antibody
  source_path  TEXT NOT NULL,        -- where the artifact lives
  project      TEXT NOT NULL,        -- which project created it
  tags         TEXT NOT NULL DEFAULT '[]',
  content_hash TEXT NOT NULL,        -- dedup identical fossils
  quality_score REAL NOT NULL DEFAULT 0,  -- from UBS + Inspector + DoD
  usage_count  INTEGER NOT NULL DEFAULT 0, -- how many times reused
  embedding    BLOB,                 -- vector embedding for fuzzy search
  created_at   DATETIME NOT NULL DEFAULT (datetime('now'))
);
```

---

## Meta-Health Metrics

Per protein and per molecule:

| Metric | Signal |
|--------|--------|
| Token cost per fold | Getting cheaper? |
| Duration per fold | Getting faster? |
| Success rate | Organisms pass DoD? |
| Quality score | UBS + Inspector scores |
| Human intervention rate | How often do humans override? |
| Molecule failure rate | Which steps fail most? |
| Convergence speed | How many /audit iterations needed? |

## How the Crab Changes

Today:
```
Whale → LLM thinks from scratch → N morsels
```

With proteins:
```
Whale → ClassifyTaskType → "frontend-design"
  → QueryProteinCatalog → "frontend-design-v3" (fitness 0.87)
  → InstantiateProtein
    → 10 molecules → 10+ morsels with explicit instructions
    → Skills attached: /extract, /critique, /audit, /teach-impeccable
    → Dependencies auto-wired, parallelism explicit
  → Crab thinking tokens: ~500 (vs ~15,000)
```
