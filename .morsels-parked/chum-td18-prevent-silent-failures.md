---
title: "Prevent silent failures in CHUM pipeline"
status: ready
priority: 1
type: task
labels:
  - whale:infrastructure
  - reliability
estimate_minutes: 60
acceptance_criteria: |
  - All non-fatal error paths that currently log WARN and continue must be audited.
  - Critical subsystems (learner lessons, semgrep, CLAUDE.md synthesis, dispatch recording) must propagate errors visibly when they degrade.
  - Add a health_events table entry or metric counter when a "graceful degradation" occurs more than N times for the same error class.
  - The morning briefing should include a "System Health" section listing any recurring silent failures from the last 24 hours.
design: |
  **Context:** The bead_id→morsel_id rename caused cascading silent failures across the learner (GetRecentLessons), groom (lessons context), and dispatcher (RecordOutcome). All were logged as WARN and silently skipped, meaning the system appeared healthy while operating without its immune system.
  
  **Approach:**
  1. Add a `RecordHealthEvent` helper that logs + stores degradation events in `health_events` table.
  2. Replace bare `logger.Warn` calls in critical paths with `RecordHealthEvent` + `logger.Warn`.
  3. Add a threshold: if the same health event fires >5 times in 1 hour, escalate to ERROR and emit a notification.
  4. Include health events summary in `GenerateMorningBriefingActivity`.
depends_on: ["fix-octopus-fts5"]
---

# Prevent Silent Failures

Audit all `logger.Warn` + `return nil` paths in the CHUM pipeline and ensure recurring
degradation is surfaced visibly rather than silently swallowed.
