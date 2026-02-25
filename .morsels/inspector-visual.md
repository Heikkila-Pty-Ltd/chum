---
title: "VisualInspector — Playwright + Vision LLM"
status: ready
priority: 2
type: task
labels:
  - whale:inspector
  - visual
  - playwright
estimate_minutes: 120
acceptance_criteria: |
  - Playwright activity navigates target URL at 375px, 768px, 1024px, 1440px viewports
  - Screenshots captured and sent to Vision LLM (Gemini Pro Vision or GPT-4o)
  - Prompt scores each page 1-10 on: visual hierarchy, color contrast, spacing, broken images, overflow
  - Structured JSON output with severity-ranked findings
  - Findings above threshold auto-emit as CHUM morsels with label `inspector:visual`
  - Temporal scheduled workflow triggers on deploy signal or cron
design: |
  1. Create `internal/inspector/visual.go` with Playwright CDP activity
  2. Add `VisualInspectionActivity` — takes URL + viewports, returns screenshots
  3. Add `VisionScoringActivity` — sends screenshots to Vision LLM, parses JSON
  4. Add `InspectorWorkflow` in `internal/temporal/workflow_inspector.go`
  5. Register workflow and activities in worker.go
  6. Schedule via Temporal cron or deploy hook signal
depends_on: ["inspector-whale"]
---

Automated visual regression and aesthetic quality assurance using Playwright screenshots and Vision LLM scoring.
