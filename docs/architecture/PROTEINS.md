# Proteins: Deterministic Workflow Sequences

> A protein is not a prompt. It's a **program** — a deterministic sequence of tool-calling molecules that produces a predictable artifact. The LLM is the CPU, not the architect.

## Core Principle

**Reduce unpredictability by replacing vague LLM prompts with explicit, granular instructions that call specific tools and produce specific artifacts.**

Bad molecule: _"Research the competitors"_
Good molecule: _"Run brave-search for the 5 closest competitors to {{project}}. For each result: run firecrawl on their URL, download sitemap, screenshot homepage + pricing + features pages via Playwright at 1440px. Save HTML + CSS + screenshots to `./research/competitors/{{domain}}/`. Output: competitor_manifest.json with URLs, page counts, and screenshot paths."_

## Anatomy of a Molecule

```json
{
  "id": "competitor-crawl",
  "name": "Crawl and screenshot competitor sites",
  "action": "script",
  "script": {
    "tool": "brave-search",
    "query": "{{project_description}} competitor website",
    "top_k": 5,
    "then": [
      { "tool": "firecrawl", "target": "{{result.url}}", "output": "./research/competitors/{{result.domain}}/" },
      { "tool": "playwright-screenshot", "urls": ["homepage", "pricing", "features"], "viewports": [1440], "output": "./research/competitors/{{result.domain}}/screenshots/" },
      { "tool": "wget", "target": "{{result.url}}/sitemap.xml", "output": "./research/competitors/{{result.domain}}/sitemap.xml" }
    ]
  },
  "provider": "gemini-flash",
  "output_artifact": "./research/competitors/competitor_manifest.json",
  "timeout_minutes": 15
}
```

Key differences from prompt-based molecules:
- **`action: script`** — this is a tool pipeline, not a chat prompt
- **Explicit tools** — `brave-search`, `firecrawl`, `playwright-screenshot`
- **Explicit outputs** — files in specific locations with known schemas
- **Cheap model** — `gemini-flash` for orchestration, not `claude-sonnet`

## Example Protein: `frontend-design-from-scratch`

### Molecule 1: Competitor Crawl
```yaml
id: competitor-crawl
action: script
provider: gemini-flash  # cheap model for tool orchestration
do: |
  1. brave-search "{{project_description}} competitor website" → top 5 URLs
  2. For each URL:
     - firecrawl → save HTML+CSS to ./research/competitors/{{domain}}/
     - playwright → screenshot homepage, pricing, features at 1440px
     - wget sitemap.xml
  3. Write competitor_manifest.json with all paths
output: ./research/competitors/
parallelism: 5  # crawl all 5 simultaneously
```

### Molecule 2: Critique Each Competitor (parallel × 5)
```yaml
id: competitor-critique
action: prompt
provider: gemini-pro  # needs taste
parallelism: 5  # one agent per competitor
input: ./research/competitors/{{domain}}/
skill: /impeccable-design/critique
do: |
  Load screenshots from ./research/competitors/{{domain}}/screenshots/.
  Run /critique on each page. For each page, evaluate:
  - Visual hierarchy (1-10)
  - Color harmony (1-10)
  - Typography quality (1-10)
  - Whitespace usage (1-10)
  - CTA clarity (1-10)
  - Mobile-readiness (inferred from layout)
  - Accessibility red flags
  Write structured JSON + prose to ./research/competitors/{{domain}}/critique.json
output: ./research/competitors/{{domain}}/critique.json
```

### Molecule 3: Synthesize Design Intelligence
```yaml
id: design-synthesis
action: prompt
provider: claude-sonnet  # needs sophisticated reasoning
input: ./research/competitors/*/critique.json
do: |
  Read all 5 competitor critiques. Produce:
  1. Common patterns across all competitors (what everyone does)
  2. Unique strengths per competitor (what makes each stand out)
  3. Universal weaknesses (opportunities to differentiate)
  4. Recommended design approach that incorporates best practices
     while being demonstrably better than all 5
  5. Specific CSS tokens: color palette, font stack, spacing scale
  Write to ./research/design_brief.md
output: ./research/design_brief.md
```

### Molecule 4: Generate 3 Design Options
```yaml
id: design-options
action: prompt
provider: claude-sonnet
parallelism: 3  # one agent per option
input: ./research/design_brief.md
do: |
  Based on the design brief, create option {{option_number}} of 3.
  Each option should interpret the brief differently:
  - Option 1: Conservative (closest to industry standard)
  - Option 2: Bold (max differentiation)
  - Option 3: Refined (elegant middle ground)
  For each option:
  - Create globals.css with full design system
  - Create a demo page at /demo/option-{{n}}/page.tsx
  - Include header, hero, feature grid, CTA, footer
output: ./demos/option-{{option_number}}/
```

### Molecule 5: Human Review Gate
```yaml
id: design-review
action: human-gate
do: |
  Start local dev server (npm run dev).
  Send notification with URLs:
  - http://localhost:3000/demo/option-1/
  - http://localhost:3000/demo/option-2/
  - http://localhost:3000/demo/option-3/
  Await human selection + feedback.
timeout_minutes: 1440  # 24 hours
fallback: "Select option with highest VisualInspector score"
output: selected_option + feedback.md
```

### Molecule 6: Codify Design System
```yaml
id: codify-design
action: prompt
provider: claude-sonnet
input: selected_option/ + feedback.md
skill: /impeccable-design/teach
do: |
  Using the selected design option and human feedback:
  1. Finalize globals.css as the canonical design system
  2. Extract design tokens to tokens.ts
  3. Create DESIGN_SYSTEM.md documenting all patterns
  4. Run /teach-impeccable to record the design DNA
  This becomes the project's design genome — all future
  frontend morsels inherit these tokens.
output: ./design-system/
```

## Protein Lifecycle

```
┌─────────────────────────────────────────────────┐
│ DISCOVERY                                        │
│ New task type, no matching protein.               │
│ Sophisticated model decomposes from scratch.      │
│ Steps + tool calls + artifacts recorded.          │
└────────────────────┬────────────────────────────┘
                     │ success
┌────────────────────▼────────────────────────────┐
│ CODIFICATION (CalcifyProteinActivity)            │
│ Extract molecule sequence from successful run.    │
│ Record as generation-0 protein with fitness=0.    │
│ Tag with trigger_rules for future matching.       │
└────────────────────┬────────────────────────────┘
                     │ similar task arrives
┌────────────────────▼────────────────────────────┐
│ REUSE (Crab queries protein catalog)             │
│ Match task category → instantiate protein.        │
│ Each molecule → morsel with explicit instructions.│
│ Dependencies auto-wired from molecule order.      │
│ Record execution as a "fold" for tracking.        │
└────────────────────┬────────────────────────────┘
                     │ fold data accumulates
┌────────────────────▼────────────────────────────┐
│ MUTATION (PaleontologistActivity)                │
│ Analyze fold data: which molecules succeed?       │
│ Which burn too many tokens? Which stall?          │
│ Fork protein → tweak molecule order/providers.    │
│ A/B test old vs new protein on next task.         │
└────────────────────┬────────────────────────────┘
                     │ fitness scores diverge
┌────────────────────▼────────────────────────────┐
│ NATURAL SELECTION                                │
│ Higher-fitness proteins get preferential use.     │
│ Low-fitness proteins deprecated → extinct.        │
│ Winning molecules spread to other proteins.       │
└─────────────────────────────────────────────────┘
```

## Meta-Health Layer

Track per protein and per molecule:

| Metric | What it measures |
|--------|-----------------|
| **Token cost per fold** | Are we getting cheaper over generations? |
| **Duration per fold** | Are we getting faster? |
| **Success rate** | Do organisms using this protein pass DoD? |
| **Quality score** | UBS findings + Inspector scores post-completion |
| **Human intervention rate** | How often do humans override/correct? |
| **Molecule failure rate** | Which specific molecules fail most? |

Proteins with declining fitness get flagged for mutation.
Molecules with high failure rates get rewritten or replaced.

## How the Crab Changes

Today:
```
Whale → "Think about how to decompose this" → LLM generates N morsels
```

With proteins:
```
Whale → ClassifyTaskType → "This is a frontend-design task"
  → QueryProteinCatalog("frontend-design") → found 3 proteins
  → SelectFittestProtein → "frontend-design-v3" (fitness 0.87)
  → InstantiateProtein(protein, whale_context)
    → Molecule 1 → morsel with explicit script
    → Molecule 2 → 5 parallel morsels with /critique skill
    → Molecule 3 → morsel with synthesis prompt
    → ...
  → Dependencies auto-wired from molecule order
  → Total crab thinking tokens: ~500 (vs ~15,000 today)
```
