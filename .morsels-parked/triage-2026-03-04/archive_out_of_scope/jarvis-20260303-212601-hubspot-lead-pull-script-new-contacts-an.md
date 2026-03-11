---
title: "HubSpot lead pull script — new contacts and form submissions last 7 days"
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
    Create scripts/report-leads.ts that queries HubSpot Contacts API (v3) for contacts created in last 7 days. Fields: firstname, lastname, email, hs_lead_source, createdate, associatedcompanyid. Also query Forms API for submission counts by form ID. Output to reports/leads-latest.json. Use HUBSPOT_API_KEY env var. Acceptance criteria: script runs standalone, writes valid JSON with contact count and per-form submission counts.
depends_on: []
---

Create scripts/report-leads.ts that queries HubSpot Contacts API (v3) for contacts created in last 7 days. Fields: firstname, lastname, email, hs_lead_source, createdate, associatedcompanyid. Also query Forms API for submission counts by form ID. Output to reports/leads-latest.json. Use HUBSPOT_API_KEY env var. Acceptance criteria: script runs standalone, writes valid JSON with contact count and per-form submission counts.

_Created by Jarvis at 2026-03-03T21:26:01Z_
