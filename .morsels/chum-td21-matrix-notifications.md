---
title: "Add Matrix/Hex notifications for escalations and groom results"
status: ready
priority: 1
type: feature
labels:
  - whale:infrastructure
  - notifications
estimate_minutes: 45
acceptance_criteria: |
  - Matrix room receives formatted messages for:
    1. Escalations (DoD failure after all retries)
    2. Circuit breaker trips
    3. Strategic groom morning briefing summary
    4. Workflow failures (PostMortem trigger events)
  - Messages include: project, task ID, error summary, link to Temporal UI.
  - Matrix room ID and access token configurable in chum.toml.
  - Failures to send notifications are non-fatal (log WARN, don't crash).
design: |
  **Step 1:** Add `[notifications.matrix]` config to chum.toml:
  ```toml
  [notifications.matrix]
  enabled = true
  homeserver = "https://matrix.org"
  room_id = "!abc:matrix.org"
  access_token = "env:MATRIX_ACCESS_TOKEN"
  ```
  
  **Step 2:** Create `internal/notify/matrix.go`:
  - Simple HTTP client calling Matrix `/_matrix/client/v3/rooms/{roomId}/send/m.room.message`
  - Formatted messages with markdown
  - Non-fatal: wrap in a helper that logs WARN on failure
  
  **Step 3:** Wire into existing activities:
  - `EscalateActivity` — send escalation notification
  - `ScanCandidatesActivity` — send circuit breaker trip notifications
  - `GenerateMorningBriefingActivity` — send briefing summary
  - `PostMortemWorkflow` (when built) — send failure investigation results
depends_on: []
---

Notifications via Matrix/Hex so the human sees escalations, circuit breakers,
and morning briefings without checking Temporal UI or logs.
