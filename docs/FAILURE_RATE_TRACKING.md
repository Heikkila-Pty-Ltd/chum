# Failure Rate Tracking & Reduction Metrics

## Overview

The evolutionary system's effectiveness is measured by **continuous reduction in DoD failure rates over time**. The paleontologist now tracks failure rate trends every 30 minutes and alerts on significant changes.

## Goal: Continuous Improvement

As the system accumulates:
- **Antibodies** (what went wrong)
- **Fossils** (extinct approaches)
- **Patterns** (what works)
- **Genome evolution** (species-specific adaptations)

...failure rates should **trend downward** over weeks/months.

## Metrics Tracked

### 1. Failure Rate Trend
- **Current window** vs **previous window** comparison
- Default: Last 6h vs previous 6h
- Measured as: `(failed DoD checks / total DoD checks) × 100`

### 2. Trend Classification
- **Improving**: Failure rate decreased by >5 percentage points
- **Degrading**: Failure rate increased by >5 percentage points
- **Stable**: Change within ±5 percentage points

### 3. Automatic Alerts
Every 30 minutes, the paleontologist:
- ✅ Calculates current vs previous failure rate
- ✅ Sends Matrix notification if **improving** (to default room)
- ✅ Sends Matrix alert if **degrading** (to admin room)
- ✅ Records health event for dashboard tracking

## Matrix Alert Format

### Improving Trend
```
📈 ✅ **IMPROVEMENT DETECTED** — Failure Rate Trend Report

**Current failure rate:** 12.5% (5/40 dispatches)
**Previous failure rate:** 22.0% (11/50 dispatches)
**Change:** -9.5% points

**Window:** Last 6h vs previous 6h

**Goal:** Continuous reduction through evolutionary learning.
Track progress: Check failure rate over last 7 days to see if antibodies
and genome evolution are reducing recurring failures.
```

### Degrading Trend (Escalated to Admin)
```
📉 ⚠️ **DEGRADATION DETECTED** — Failure Rate Trend Report

**Current failure rate:** 35.0% (14/40 dispatches)
**Previous failure rate:** 18.0% (9/50 dispatches)
**Change:** +17.0% points

**Window:** Last 6h vs previous 6h

**Action Required:** Investigate recent changes that may have introduced
systemic issues. Check recurring DoD failure alerts for patterns.
```

## SQL Queries for Analysis

### Current Failure Rate (Last 24 Hours)
```sql
SELECT
    COUNT(*) as total_checks,
    SUM(CASE WHEN passed = 1 THEN 1 ELSE 0 END) as passed,
    SUM(CASE WHEN passed = 0 THEN 1 ELSE 0 END) as failed,
    ROUND(100.0 * SUM(CASE WHEN passed = 0 THEN 1 ELSE 0 END) / COUNT(*), 1) as failure_rate_pct
FROM dod_results
WHERE checked_at >= datetime('now', '-24 hours');
```

### Failure Rate by Project (Last 7 Days)
```sql
SELECT
    project,
    COUNT(*) as total_checks,
    SUM(CASE WHEN passed = 0 THEN 1 ELSE 0 END) as failed,
    ROUND(100.0 * SUM(CASE WHEN passed = 0 THEN 1 ELSE 0 END) / COUNT(*), 1) as failure_rate_pct
FROM dod_results
WHERE checked_at >= datetime('now', '-7 days')
GROUP BY project
ORDER BY failure_rate_pct DESC;
```

### Daily Failure Rate Trend (Last 14 Days)
```sql
SELECT
    DATE(checked_at) as day,
    COUNT(*) as total_checks,
    SUM(CASE WHEN passed = 1 THEN 1 ELSE 0 END) as passed,
    SUM(CASE WHEN passed = 0 THEN 1 ELSE 0 END) as failed,
    ROUND(100.0 * SUM(CASE WHEN passed = 0 THEN 1 ELSE 0 END) / COUNT(*), 1) as failure_rate_pct
FROM dod_results
WHERE checked_at >= datetime('now', '-14 days')
GROUP BY DATE(checked_at)
ORDER BY day DESC;
```

### Hourly Failure Rate (Last 24 Hours) — For Spike Detection
```sql
SELECT
    strftime('%Y-%m-%d %H:00', checked_at) as hour,
    COUNT(*) as total_checks,
    SUM(CASE WHEN passed = 0 THEN 1 ELSE 0 END) as failed,
    ROUND(100.0 * SUM(CASE WHEN passed = 0 THEN 1 ELSE 0 END) / COUNT(*), 1) as failure_rate_pct
FROM dod_results
WHERE checked_at >= datetime('now', '-24 hours')
GROUP BY strftime('%Y-%m-%d %H:00', checked_at)
ORDER BY hour DESC;
```

### Failure Rate Before/After Genome Evolution (Species Analysis)
```sql
-- Get dispatches before and after genome evolution for a species
-- to measure if evolution actually helped

SELECT
    'Before Evolution' as period,
    COUNT(*) as total_dispatches,
    SUM(CASE WHEN d.status = 'completed' THEN 1 ELSE 0 END) as successes,
    ROUND(100.0 * SUM(CASE WHEN d.status != 'completed' THEN 1 ELSE 0 END) / COUNT(*), 1) as failure_rate_pct
FROM dispatches d
WHERE d.morsel_id = 'your-species-id'
  AND d.dispatched_at < (SELECT last_evolved FROM genomes WHERE species = 'your-species-id')

UNION ALL

SELECT
    'After Evolution' as period,
    COUNT(*) as total_dispatches,
    SUM(CASE WHEN d.status = 'completed' THEN 1 ELSE 0 END) as successes,
    ROUND(100.0 * SUM(CASE WHEN d.status != 'completed' THEN 1 ELSE 0 END) / COUNT(*), 1) as failure_rate_pct
FROM dispatches d
WHERE d.morsel_id = 'your-species-id'
  AND d.dispatched_at >= (SELECT last_evolved FROM genomes WHERE species = 'your-species-id');
```

### Health Event Timeline (Trend Changes)
```sql
SELECT
    created_at,
    event_type,
    details
FROM health_events
WHERE event_type IN ('failure_rate_improving', 'failure_rate_degrading', 'recurring_dod_failure')
ORDER BY created_at DESC
LIMIT 20;
```

### Paleontologist Run Summary (Recent Activity)
```sql
SELECT
    run_at,
    antibodies_discovered,
    genes_mutated,
    proteins_nominated,
    species_audited,
    cost_alerts,
    recurring_failures,
    summary
FROM paleontology_runs
ORDER BY run_at DESC
LIMIT 10;
```

## Dashboard Visualization

### Weekly Failure Rate Chart
Use `GetFailureRateHistory()` to generate chart data:

```go
// Get last 7 days of daily failure rates
trends, err := store.GetFailureRateHistory("", 7, 24)

// Output for charting:
// Day 1: 15.2%
// Day 2: 18.3%
// Day 3: 14.1%
// Day 4: 12.8%  ← improving!
// Day 5: 11.5%
// Day 6: 13.2%
// Day 7: 10.9%  ← continuing to improve
```

### Sprint-over-Sprint Comparison
```sql
-- Compare failure rates across sprints
SELECT
    sb.sprint_number,
    sb.sprint_start,
    sb.sprint_end,
    COUNT(dr.id) as total_checks,
    SUM(CASE WHEN dr.passed = 0 THEN 1 ELSE 0 END) as failed,
    ROUND(100.0 * SUM(CASE WHEN dr.passed = 0 THEN 1 ELSE 0 END) / COUNT(dr.id), 1) as failure_rate_pct
FROM sprint_boundaries sb
LEFT JOIN dod_results dr ON dr.checked_at >= sb.sprint_start AND dr.checked_at < sb.sprint_end
GROUP BY sb.sprint_number
ORDER BY sb.sprint_number DESC
LIMIT 5;
```

## Success Criteria

### Immediate (< 1 hour)
- ✅ Recurring failures detected and raised within 30 minutes
- ✅ Failure rate tracked and reported every 30 minutes
- ✅ Matrix alerts sent for significant trends
- ✅ Antibodies created and injected into next dispatch

### Short-term (24-48 hours)
- 🎯 Recurring patterns detected and antibodies prevent repeat failures
- 🎯 Failure rate shows measurable reduction (5-10% improvement)
- 🎯 Species with 3+ dispatches start showing learning

### Medium-term (3-7 days)
- 🎯 Failure rate reduces by 20-30% as antibodies accumulate
- 🎯 Recurring patterns drop to <3 occurrences (from 5+)
- 🎯 Genomes with 10+ generations show clear success rate advantage

### Long-term (2-4 weeks)
- 🎯 Failure rate stabilizes at <10%
- 🎯 Most failures are "new" errors (not recurring patterns)
- 🎯 Genome evolution demonstrably improves species success rates
- 🎯 Calcified patterns (deterministic scripts) replace LLM for repeated tasks

## Tuning

### Alert Thresholds
File: `internal/temporal/activities.go:1633`
```go
// Change from 5% to 10% for less sensitive alerts
if delta.Delta < -10.0 {
    delta.Trend = "improving"
} else if delta.Delta > 10.0 {
    delta.Trend = "degrading"
}
```

### Window Size
File: `internal/temporal/workflow_paleontologist.go:38`
```go
// Change default from 6 hours to 12 hours
if req.LookbackH <= 0 {
    req.LookbackH = 12
}
```

### Minimum Sample Size
File: `internal/temporal/activities.go:1664`
```go
// Only send alerts if >= 20 dispatches (instead of 10)
if delta.CurrentDispatches >= 20 {
    // send alert
}
```

## Integration with Existing Systems

### Stingray Dashboard
The failure rate trend data is available via:
- `GetFailureRateDelta()` - current vs previous comparison
- `GetFailureRateHistory()` - time-series data for charts
- Health events table - timeline of trend changes

### Morning Briefing
Include failure rate summary:
```
📊 **Failure Rate** (last 24h): 12.3% (5/41 dispatches)
Trend: ↓ Improving (was 18.5% in previous 24h)
```

### Sprint Retrospective
Query sprint-over-sprint failure rate improvement to measure evolutionary learning effectiveness.

## Next Steps

1. **Weekly Review**: Check `health_events` for trend changes
2. **Sprint Planning**: Factor failure rates into velocity estimates
3. **Genome Validation**: Query before/after evolution to prove genome effectiveness
4. **Alert Fatigue**: Tune thresholds if getting too many stable-state notifications
