# Failure Rate Tracking System — Implementation Summary

## Overview

Implemented comprehensive failure rate tracking and trending to measure **continuous improvement** — the core metric for validating the evolutionary system's effectiveness.

## What Was Built

### 1. Data Structures (internal/store/paleontology.go)

#### FailureRateTrend
Tracks DoD failure rate for a time window:
- `Project`, `WindowStart`, `WindowEnd`
- `TotalDispatches`, `DoDPassed`, `DoDFailed`
- `FailureRate` (percentage)

#### FailureRateDelta
Compares current vs previous windows:
- `CurrentRate`, `PreviousRate`, `Delta`
- `Trend` ("improving", "degrading", "stable")
- Classified as degrading if delta > +5%, improving if < -5%

### 2. Query Methods (internal/store/paleontology.go)

```go
// Single window analysis
GetFailureRateTrend(project, windowStart, windowEnd) → *FailureRateTrend

// Current vs previous comparison
GetFailureRateDelta(project, windowHours) → *FailureRateDelta

// Time-series data for charting
GetFailureRateHistory(project, windows, windowHours) → []FailureRateTrend
```

### 3. Paleontologist Activity (internal/temporal/activities.go)

**AnalyzeFailureRateTrendsActivity**:
- Calculates current vs previous failure rate
- Logs trends with full context
- Sends Matrix alerts for significant changes:
  - **Improving** → Default room (celebration)
  - **Degrading** → Admin room (escalation)
- Records health events for dashboard tracking
- Only sends alerts if sample size >= 10 dispatches

### 4. Workflow Integration (internal/temporal/workflow_paleontologist.go)

Added **Step 7: Failure Rate Trend Analysis**
- Runs after recurring DoD failure detection
- Executes every 30 minutes (paleontologist schedule)
- Non-fatal (never blocks pipeline)

### 5. Documentation

#### docs/FAILURE_RATE_TRACKING.md
- Comprehensive guide to failure rate tracking
- SQL queries for analysis and dashboards
- Sprint-over-sprint comparison queries
- Before/after genome evolution validation
- Tuning parameters
- Success criteria (short/medium/long-term goals)

#### docs/RECURRING_DOD_FAILURE_DETECTION.md
- Systemic failure detection system
- Matrix alert formats
- Testing procedures

### 6. CLI Reporting Tool

**scripts/failure-rate-report.sh**
- Quick command-line failure rate report
- Shows last 24h summary
- Daily trend for last 7 days
- Recurring failures
- Recent paleontologist runs
- Health event timeline

Usage:
```bash
./scripts/failure-rate-report.sh
```

## How It Works

### Automatic Monitoring (Every 30 Minutes)

1. Paleontologist workflow runs on schedule
2. Executes AnalyzeFailureRateTrendsActivity
3. Queries DoD results for current window (last 6h)
4. Queries DoD results for previous window (6h before that)
5. Calculates failure rate delta
6. Classifies trend (improving/degrading/stable)
7. Sends Matrix alerts if significant change detected
8. Records health event for tracking

### Matrix Alert Examples

#### Improving Trend (Sent to Default Room)
```
📈 ✅ **IMPROVEMENT DETECTED** — Failure Rate Trend Report

**Current failure rate:** 12.5% (5/40 dispatches)
**Previous failure rate:** 22.0% (11/50 dispatches)
**Change:** -9.5% points

**Window:** Last 6h vs previous 6h

**Goal:** Continuous reduction through evolutionary learning.
```

#### Degrading Trend (Sent to Admin Room)
```
📉 ⚠️ **DEGRADATION DETECTED** — Failure Rate Trend Report

**Current failure rate:** 35.0% (14/40 dispatches)
**Previous failure rate:** 18.0% (9/50 dispatches)
**Change:** +17.0% points

**Action Required:** Investigate recent changes that may have
introduced systemic issues.
```

## Success Metrics

### Immediate (< 1 hour) ✅
- [x] Failure rates tracked every 30 minutes
- [x] Matrix alerts sent for significant trends
- [x] Health events recorded for dashboard
- [x] CLI tool for instant reports
- [x] Antibodies created and injected immediately

### Short-term (24-48 hours) 🎯
- [ ] Recurring patterns detected and antibodies prevent repeats
- [ ] Failure rate shows 5-10% improvement
- [ ] Species with 3+ dispatches show learning

### Medium-term (3-7 days) 🎯
- [ ] Failure rate reduces by 20-30% as antibodies accumulate
- [ ] Recurring patterns drop from 5+ to <3 occurrences
- [ ] Genomes with 10+ generations outperform newborns

### Long-term (2-4 weeks) 🎯
- [ ] Failure rate stabilizes at <10%
- [ ] Most failures are "new" (not recurring patterns)
- [ ] Genome evolution provably improves success rates
- [ ] Calcified patterns replace LLM for repeated tasks

## Key Queries for Analysis

### Daily Failure Rate (Last 14 Days)
```sql
SELECT
    DATE(checked_at) as day,
    COUNT(*) as total_checks,
    ROUND(100.0 * SUM(CASE WHEN passed = 0 THEN 1 ELSE 0 END) / COUNT(*), 1) as failure_rate_pct
FROM dod_results
WHERE checked_at >= datetime('now', '-14 days')
GROUP BY DATE(checked_at)
ORDER BY day DESC;
```

### Sprint-over-Sprint Comparison
```sql
SELECT
    sb.sprint_number,
    ROUND(100.0 * SUM(CASE WHEN dr.passed = 0 THEN 1 ELSE 0 END) / COUNT(dr.id), 1) as failure_rate_pct
FROM sprint_boundaries sb
LEFT JOIN dod_results dr ON dr.checked_at >= sb.sprint_start AND dr.checked_at < sb.sprint_end
GROUP BY sb.sprint_number
ORDER BY sb.sprint_number DESC;
```

### Before/After Genome Evolution (Species Effectiveness)
```sql
SELECT
    CASE
        WHEN d.dispatched_at < g.last_evolved THEN 'Before Evolution'
        ELSE 'After Evolution'
    END as period,
    COUNT(*) as total,
    SUM(CASE WHEN d.status = 'completed' THEN 1 ELSE 0 END) as successes,
    ROUND(100.0 * SUM(CASE WHEN d.status != 'completed' THEN 1 ELSE 0 END) / COUNT(*), 1) as failure_rate
FROM dispatches d
JOIN genomes g ON g.species = d.morsel_id
WHERE d.morsel_id = 'your-species-id'
GROUP BY period;
```

## Integration Points

### Stingray Dashboard
- Query `GetFailureRateHistory()` for time-series charts
- Query `GetFailureRateDelta()` for current trend badge
- Read `health_events` table for timeline view

### Morning Briefing
Include in daily summary:
```
📊 Failure Rate (24h): 12.3% ↓ (was 18.5%)
🎯 Trend: Improving
```

### Sprint Retrospective
- Review sprint-over-sprint failure rate improvement
- Validate that genome evolution is working
- Identify species that need manual intervention

## Files Modified

1. **internal/store/paleontology.go** (+140 lines)
   - FailureRateTrend, FailureRateDelta structs
   - GetFailureRateTrend, GetFailureRateDelta, GetFailureRateHistory methods

2. **internal/temporal/activities.go** (+70 lines)
   - AnalyzeFailureRateTrendsActivity

3. **internal/temporal/workflow_paleontologist.go** (+10 lines)
   - Step 7: Failure Rate Trend Analysis

4. **docs/FAILURE_RATE_TRACKING.md** (new, 380 lines)
   - Complete documentation and queries

5. **scripts/failure-rate-report.sh** (new, 130 lines)
   - CLI reporting tool

## Testing

### Verify Implementation
```bash
# Build and check for errors
go build ./...

# Run failure rate report
./scripts/failure-rate-report.sh

# Check paleontologist is scheduled
# (Look for PaleontologistWorkflow in Temporal UI)
```

### Simulate Trend Detection
```sql
-- Insert fake DoD failures to trigger degrading trend
INSERT INTO dod_results (morsel_id, project, passed, failures, check_results)
SELECT 'test-morsel-' || seq, 'test-project', 0, 'npm run build failed', ''
FROM (SELECT 0 AS seq UNION SELECT 1 UNION SELECT 2 UNION SELECT 3 UNION SELECT 4
      UNION SELECT 5 UNION SELECT 6 UNION SELECT 7 UNION SELECT 8 UNION SELECT 9);

-- Wait for next paleontologist run (within 30 min)
-- Check Matrix for degrading trend alert
```

## Tuning Parameters

| Parameter | Location | Default | Purpose |
|-----------|----------|---------|---------|
| Window size | workflow_paleontologist.go:38 | 6 hours | Time window for comparison |
| Trend threshold | activities.go:1633 | ±5% | Delta to classify as improving/degrading |
| Min sample size | activities.go:1664 | 10 dispatches | Minimum for sending alerts |
| Alert room | activities.go:1655 | Admin for degrading | Where to send notifications |

## Next Steps

1. **Monitor for 1 week**: Observe baseline failure rates
2. **Sprint review**: Include failure rate trends in retrospectives
3. **Genome validation**: Query before/after evolution to prove effectiveness
4. **Dashboard**: Build Stingray visualization using GetFailureRateHistory()
5. **Alerting tuning**: Adjust thresholds if too noisy or too quiet

## Goal Achievement

The system now provides:
- ✅ **Automatic tracking** of failure rates over time
- ✅ **Trend detection** (improving/degrading/stable)
- ✅ **Instant feedback** via Matrix alerts every 30 min
- ✅ **Historical analysis** via SQL queries and CLI tool
- ✅ **Validation mechanism** to prove evolutionary learning works

**The goal is continuous reduction.** If failure rates don't trend downward over months, the evolutionary system needs tuning (antibody thresholds, genome evolution logic, or species classification).
