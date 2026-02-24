# Doomsday Clock — System Health Monitoring for Hex

## Overview

The **Doomsday Clock** is an escalating warning system that alerts Hex (the scrum master agent) when the system's failure rate is degrading. It's **NOT a hard gate** — Hex makes the final decision on whether to pause dispatching.

---

## How It Works

### Clock Mechanism

Every 30 minutes, the paleontologist:
1. Calculates current vs previous failure rate
2. Records a health event (`failure_rate_improving` / `failure_rate_degrading` / `failure_rate_stable`)
3. Calculates the **degradation streak** (consecutive degrading periods)
4. Computes a **health score** (0-100)
5. Sends an alert to Hex via Matrix with escalating urgency

### Health Score Calculation

```
Starting Score: 100 (healthy)
Each degrading period: -15 points
Any improving period: Reset to 100
```

### Alert Levels

| Score | Alert Level | Clock Time | Degradation Streak | Action |
|-------|-------------|------------|-------------------|--------|
| **85-100** | 🟢 GREEN | 12:00 AM (Healthy) | 0-1 | Normal operations |
| **70-84** | 🟡 YELLOW | 11:45 PM (Warning) | 1 | Monitor closely |
| **40-69** | 🟠 ORANGE | 11:55 PM (Critical) | 2-3 | Consider pausing low-priority work |
| **15-39** | 🔴 RED | 11:59 PM (Emergency) | 4-5 | Pause non-critical, investigate |
| **0-14** | 🔴 RED | 🔴 MIDNIGHT | 6+ | **STOP THE LINE** |

---

## Matrix Alert Format (Sent to Hex)

### Green (Healthy)
```
✅ **SYSTEM HEALTHY** — Doomsday Clock Report

🕐 **Clock Time:** 12:00 AM (Healthy)
📊 **Health Score:** 100/100 (green)
📉 **Degradation Streak:** 0 consecutive periods
📈 **Improvement Streak:** 2 consecutive periods

**Current Failure Rate:** 12.5% (5 failed / 40 total)
**Previous Failure Rate:** 18.0% (9 failed / 50 total)
**Change:** -5.5% points (improving)

**Recommendation for Hex:**
System healthy - continue normal operations

**Window:** Last 6h vs previous 6h
**Next Check:** 30 minutes
```

### Yellow (Warning)
```
⚠️ **WARNING: Degradation Detected** — Doomsday Clock Report

🕐 **Clock Time:** 11:45 PM (Warning)
📊 **Health Score:** 85/100 (yellow)
📉 **Degradation Streak:** 1 consecutive periods
📈 **Improvement Streak:** 0 consecutive periods

**Current Failure Rate:** 28.0% (14 failed / 50 total)
**Previous Failure Rate:** 22.0% (11 failed / 50 total)
**Change:** +6.0% points (degrading)

**Recommendation for Hex:**
Monitor closely - check for pattern changes

**Window:** Last 6h vs previous 6h
**Next Check:** 30 minutes
```

### Orange (Critical)
```
🔶 **CRITICAL: Multiple Degrading Periods** — Doomsday Clock Report

🕐 **Clock Time:** 11:55 PM (Critical)
📊 **Health Score:** 55/100 (orange)
📉 **Degradation Streak:** 3 consecutive periods
📈 **Improvement Streak:** 0 consecutive periods

**Current Failure Rate:** 42.0% (21 failed / 50 total)
**Previous Failure Rate:** 35.0% (17 failed / 49 total)
**Change:** +7.0% points (degrading)

**Recommendation for Hex:**
Consider pausing low-priority work - investigate root cause

**Window:** Last 6h vs previous 6h
**Next Check:** 30 minutes
```

### Red (Emergency)
```
🚨 **EMERGENCY: System Failing** — Doomsday Clock Report

🕐 **Clock Time:** 🔴 MIDNIGHT (System Failing)
📊 **Health Score:** 10/100 (red)
📉 **Degradation Streak:** 6 consecutive periods
📈 **Improvement Streak:** 0 consecutive periods

**Current Failure Rate:** 65.0% (32 failed / 49 total)
**Previous Failure Rate:** 58.0% (29 failed / 50 total)
**Change:** +7.0% points (degrading)

**Recommendation for Hex:**
🚨 STOP THE LINE: Pause dispatching until root cause identified and fixed

**Window:** Last 6h vs previous 6h
**Next Check:** 30 minutes
```

---

## Hex's Decision Framework

### Green (Score 85-100)
- **Action:** None - business as usual
- **Dispatching:** Full speed ahead
- **Monitoring:** Passive

### Yellow (Score 70-84)
- **Action:** Increase monitoring frequency
- **Dispatching:** Continue, but watch for patterns
- **Monitoring:** Check recurring DoD failures report
- **Consider:** Are antibodies being created? Are genomes evolving?

### Orange (Score 40-69)
- **Action:** Investigate root cause actively
- **Dispatching:** Pause P2/P3 work, continue P0/P1 only
- **Monitoring:** Real-time - check every 30 min
- **Investigation:**
  - Check recurring DoD failures (is same error hitting multiple morsels?)
  - Check recent code changes (was something merged that broke the build?)
  - Check infrastructure (did a dependency update, service go down?)

### Red (Score 0-39)
- **Action:** **PAUSE ALL DISPATCHING** (except critical fixes)
- **Monitoring:** Continuous
- **Investigation:**
  - Identify root cause ASAP
  - Fix underlying issue (don't just restart - actually fix)
  - Once fixed, clear the degradation streak manually
  - Resume dispatching slowly (1-2 morsels to test)

---

## CLI Report

Run `./scripts/failure-rate-report.sh` to see current clock status:

```bash
🦴 CHUM Failure Rate Report
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

🕐 Doomsday Clock Status
   🔶 ORANGE - Critical (11:55 PM)
   Status: 3 consecutive degrading periods - consider pausing

📊 Last 24 Hours
   Total DoD checks: 95
   Passed: 55
   Failed: 40
   Failure rate: 42.1%
...
```

---

## What Triggers Clock Movement?

### Clock Advances (Toward Midnight)
- Failure rate increases by >5% points
- Happens in consecutive 30-min periods
- Antibodies not preventing repeat failures
- Genomes not evolving (learner broken?)

### Clock Resets (Back to Midnight = 12:00 AM)
- **Any improving period** resets the clock to 100
- Failure rate decreases by >5% points
- Antibodies working as intended

---

## Manual Clock Reset (For Hex)

If Hex identifies and fixes a root cause, manually reset the clock:

```sql
-- Clear recent degradation events
DELETE FROM health_events
WHERE event_type = 'failure_rate_degrading'
  AND created_at > datetime('now', '-2 hours');

-- Insert an improvement event to reset the clock
INSERT INTO health_events (event_type, details)
VALUES ('failure_rate_improving', 'Manual reset after root cause fixed: [describe fix]');
```

Or via safety block:
```sql
-- Remove any safety blocks that might have been set
DELETE FROM safety_blocks WHERE block_type = 'failure_rate_pause';
```

---

## Integration with Other Systems

### Recurring DoD Failures
- When clock reaches ORANGE, check recurring DoD failure alerts
- Same error hitting 5+ morsels = systemic issue
- Fix the systemic issue, not individual morsels

### Genome Evolution
- Degradation may indicate genomes not evolving
- Check: Are antibodies being created? (`SELECT COUNT(*) FROM genomes WHERE antibodies != '[]'`)
- Check: Are genomes being injected? (search logs for "Genome injected")

### Morning Briefing
Include clock status:
```
🕐 System Health: 🟡 YELLOW (11:45 PM)
   - 1 degrading period detected
   - Monitor for continued degradation
```

---

## Typical Scenarios

### Scenario 1: Broken Dependency
```
11:00 - npm update breaks @next/font
11:30 - Paleontologist detects 40% failure rate (was 15%)
        🟡 YELLOW - 1 degrading period
12:00 - Still 40% - recurring pattern: "Module not found: @next/font"
        🔶 ORANGE - 2 degrading periods
        Recommendation: Pause low-priority, investigate
12:30 - Hex investigates, finds npm update broke things
        Rolls back package.json
13:00 - Failure rate drops to 10%
        ✅ GREEN - Clock reset, normal operations resumed
```

### Scenario 2: False Alarm (Hard Batch of Work)
```
14:00 - New sprint with complex features starts
14:30 - Failure rate 35% (was 20%, but work is genuinely harder)
        🟡 YELLOW - 1 degrading period
15:00 - Antibodies created, genomes evolving
        Failure rate 28% (improving!)
        ✅ GREEN - Clock reset
        (Not systemic - just harder work that system is learning from)
```

### Scenario 3: Learner Broken
```
10:00 - Learner workflow stops spawning (Temporal worker down)
10:30 - Same errors repeat 5+ times (no antibodies created)
        Failure rate 45%
        🟡 YELLOW
11:00 - Still repeating same errors
        Failure rate 50%
        🔶 ORANGE
11:30 - No improvement
        Failure rate 55%
        🔴 RED - EMERGENCY
        Recommendation: STOP THE LINE
12:00 - Hex investigates, finds Temporal worker crashed
        Restarts worker, verifies learner spawns
        Manually resets clock
12:30 - Antibodies working again
        Failure rate 30%
        ✅ GREEN
```

---

## Tuning

### Sensitivity (how quickly clock advances)
File: `internal/store/paleontology.go:520`
```go
// Change from -15 to -20 for faster escalation
score.Score = 100 - (score.DegradationStreak * 20)
```

### Alert Threshold (when to classify as degrading)
File: `internal/store/paleontology.go:162`
```go
// Change from 5% to 10% for less sensitive clock
if delta.Delta < -10.0 {
    delta.Trend = "improving"
} else if delta.Delta > 10.0 {
    delta.Trend = "degrading"
}
```

### Minimum Sample Size (avoid noise)
File: `internal/temporal/activities.go:1685`
```go
// Only send if >= 20 dispatches (instead of 10)
if delta.CurrentDispatches >= 20 {
```

---

## Key Difference from Hard Gate

| Aspect | Hard Gate | Doomsday Clock |
|--------|-----------|----------------|
| **Blocks dispatching?** | Yes, automatically | No - Hex decides |
| **False positive impact** | Stops all work | Just a warning |
| **Human in loop** | No | Yes (Hex) |
| **Escalation** | Binary (blocked / not blocked) | Gradual (green → yellow → orange → red) |
| **Recovery** | Manual unblock required | Auto-resets on improvement |

The clock provides **situational awareness** to Hex without removing agency. Hex can:
- Ignore yellow warnings if work is genuinely harder
- Act on orange warnings by pausing low-priority work
- Respect red warnings by investigating immediately

This is **collaborative AI** — the paleontologist provides data, Hex makes strategic decisions.

---

## Success Metrics

- **Green >80% of the time** = Healthy system
- **Yellow 10-15% of the time** = Normal variation (harder work batches)
- **Orange <5% of the time** = Occasional issues, quickly resolved
- **Red <1% of the time** = Rare emergencies only
- **Midnight = 0%** = Never reach this (catch issues at orange)

If the clock is frequently red, the evolutionary system needs tuning or there are systemic infrastructure issues.
