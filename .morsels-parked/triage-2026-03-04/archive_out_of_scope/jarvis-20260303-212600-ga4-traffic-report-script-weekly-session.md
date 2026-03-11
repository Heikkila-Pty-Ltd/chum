---
title: "GA4 traffic report script — weekly sessions, users, top pages"
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
    Create a script at scripts/report-traffic.ts (or .js) that queries GA4 Data API for the last 7 days. Metrics: sessions, users, bounce rate. Dimensions: page path (top 10). Output structured JSON to reports/traffic-latest.json. Use service account credentials from env var GOOGLE_APPLICATION_CREDENTIALS. Acceptance criteria: script runs via `npx ts-node scripts/report-traffic.ts`, writes valid JSON, handles auth errors gracefully.
depends_on: []
---

Create a script at scripts/report-traffic.ts (or .js) that queries GA4 Data API for the last 7 days. Metrics: sessions, users, bounce rate. Dimensions: page path (top 10). Output structured JSON to reports/traffic-latest.json. Use service account credentials from env var GOOGLE_APPLICATION_CREDENTIALS. Acceptance criteria: script runs via `npx ts-node scripts/report-traffic.ts`, writes valid JSON, handles auth errors gracefully.

_Created by Jarvis at 2026-03-03T21:26:00Z_
