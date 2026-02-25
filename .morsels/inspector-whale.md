---
title: "Inspector Bot Species — Self-Healing QA Loop"
status: ready
priority: 2
type: whale
labels:
  - whale:inspector
  - qa
  - automation
design: |
  Specialised inspector bots that run post-deploy and generate morsels for
  every defect found above a severity threshold. The morsels flow through the
  standard CHUM pipeline (Crab → Shark → DoD), creating a self-healing loop:

    deploy → inspect → fix → re-inspect → close

  Four inspector species:
  1. VisualInspector   — Playwright screenshots + Vision LLM scoring
  2. DataInspector     — API probe + schema validation + cross-reference
  3. LinkInspector     — Full-site crawl for broken routes and 404s
  4. AccessibilityInspector — axe-core + Lighthouse audits

  Each inspector is a Temporal scheduled workflow that:
  - Runs against a target project URL or Supabase endpoint
  - Produces structured JSON findings
  - Calls EmitMorselsActivity for any finding above severity threshold
  - Tags morsels with inspector species label for traceability

  Integration points:
  - New `InspectorWorkflow` registered in worker.go
  - Scheduled via Temporal cron (e.g. every deploy, or hourly)
  - Inspector findings stored in `inspector_findings` table
  - Re-inspection closes the morsel loop on DoD pass
depends_on: []
---

# Inspector Bot Species

Automated QA layer that closes the deploy→inspect→fix→re-inspect loop.
