---
title: "Traffic reporting: connect Google Search Console API"
status: ready
priority: 1
type: feature
labels:
  - jarvis-initiative
  - hg-website
estimate_minutes: 30
acceptance_criteria: |
  - Task completed as described
design: |
    Add a script at scripts/gsc-report.ts that authenticates with Google Search Console via service account, fetches last 30 days of clicks/impressions/CTR for huntergalloway.com.au, and writes a JSON report to reports/gsc-latest.json. Acceptance criteria: script runs with node/ts-node, produces valid JSON, no hardcoded secrets (use env vars).
depends_on: []
---

Add a script at scripts/gsc-report.ts that authenticates with Google Search Console via service account, fetches last 30 days of clicks/impressions/CTR for huntergalloway.com.au, and writes a JSON report to reports/gsc-latest.json. Acceptance criteria: script runs with node/ts-node, produces valid JSON, no hardcoded secrets (use env vars).

_Created by Jarvis at 2026-03-03T21:16:02Z_
