---
title: "Write a Temporal schedule for huntergalloway.com.au weekly lead+traffic report"
status: ready
priority: 1
type: feature
labels:
  - jarvis-initiative
  - chum
estimate_minutes: 30
acceptance_criteria: |
  - Task completed as described
design: |
    Create a Temporal workflow (in a new hg-reports package or standalone worker) that runs weekly and: (1) pulls GA4 session data via the existing analytics integration in hg-website, (2) pulls HubSpot contact/lead data, (3) posts a summary to the Jarvis Matrix channel via the post_to_csb.py pattern. Acceptance: schedule fires weekly, report appears in Matrix with session count, top pages, and new leads for the past 7 days.
depends_on: []
---

Create a Temporal workflow (in a new hg-reports package or standalone worker) that runs weekly and: (1) pulls GA4 session data via the existing analytics integration in hg-website, (2) pulls HubSpot contact/lead data, (3) posts a summary to the Jarvis Matrix channel via the post_to_csb.py pattern. Acceptance: schedule fires weekly, report appears in Matrix with session count, top pages, and new leads for the past 7 days.

_Created by Jarvis at 2026-03-03T21:32:25Z_
