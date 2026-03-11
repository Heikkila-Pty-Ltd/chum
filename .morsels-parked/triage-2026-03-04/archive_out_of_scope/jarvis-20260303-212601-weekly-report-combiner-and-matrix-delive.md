---
title: "Weekly report combiner and Matrix delivery"
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
    Create scripts/send-weekly-report.ts that reads reports/traffic-latest.json and reports/leads-latest.json and sends a plain-text summary to a Matrix room via HTTP PUT to the synapse API. Format: 7-day traffic (sessions, users), new leads count, top 3 pages. Matrix homeserver URL and access token via env vars MATRIX_HOMESERVER and MATRIX_ACCESS_TOKEN, room ID via MATRIX_REPORT_ROOM_ID. Acceptance criteria: running the script after both report scripts posts a readable plain-text message to Matrix.
depends_on: []
---

Create scripts/send-weekly-report.ts that reads reports/traffic-latest.json and reports/leads-latest.json and sends a plain-text summary to a Matrix room via HTTP PUT to the synapse API. Format: 7-day traffic (sessions, users), new leads count, top 3 pages. Matrix homeserver URL and access token via env vars MATRIX_HOMESERVER and MATRIX_ACCESS_TOKEN, room ID via MATRIX_REPORT_ROOM_ID. Acceptance criteria: running the script after both report scripts posts a readable plain-text message to Matrix.

_Created by Jarvis at 2026-03-03T21:26:01Z_
