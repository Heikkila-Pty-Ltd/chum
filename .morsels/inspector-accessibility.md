---
title: "AccessibilityInspector — axe-core + Lighthouse Audit"
status: ready
priority: 2
type: task
labels:
  - whale:inspector
  - a11y
  - lighthouse
estimate_minutes: 90
acceptance_criteria: |
  - Runs axe-core against every route at multiple viewports
  - Reports WCAG 2.1 AA violations with severity and remediation guidance
  - Runs Lighthouse performance + accessibility audits for each page
  - Structured scores (performance, accessibility, best practices, SEO)
  - Findings above threshold auto-emit as morsels with label `inspector:a11y`
  - Remediation steps included in morsel description for Shark consumption
design: |
  1. Create `internal/inspector/accessibility.go`
  2. Add `AxeCoreActivity` — run axe-core via Playwright, parse violations
  3. Add `LighthouseActivity` — run Lighthouse CLI, parse JSON report
  4. Wire into `InspectorWorkflow` as a parallel branch
depends_on: ["inspector-whale"]
---

WCAG compliance checking and Lighthouse performance/accessibility auditing.
