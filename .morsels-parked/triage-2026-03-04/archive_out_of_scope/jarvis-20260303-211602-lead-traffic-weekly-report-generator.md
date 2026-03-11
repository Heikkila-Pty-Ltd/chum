---
title: "Lead + traffic weekly report generator"
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
    Build a script at scripts/weekly-report.ts that reads reports/gsc-latest.json and Payload CMS lead data (from previous morsels), and outputs a human-readable weekly summary to reports/weekly-YYYY-MM-DD.md. Include: total sessions, top pages, enquiry count, new leads. Acceptance criteria: script runs end-to-end, output is readable and accurate, no fabricated data.
depends_on: []
---

Build a script at scripts/weekly-report.ts that reads reports/gsc-latest.json and Payload CMS lead data (from previous morsels), and outputs a human-readable weekly summary to reports/weekly-YYYY-MM-DD.md. Include: total sessions, top pages, enquiry count, new leads. Acceptance criteria: script runs end-to-end, output is readable and accurate, no fabricated data.

_Created by Jarvis at 2026-03-03T21:16:02Z_
