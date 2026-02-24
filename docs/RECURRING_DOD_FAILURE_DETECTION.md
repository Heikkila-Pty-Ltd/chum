# Recurring DoD Failure Detection

## Problem
The paleontologist and learner were logging individual morsel failures, but **systemic build failures affecting multiple morsels were not being detected or raised**. When the same `npm run build` error killed 10 P0 morsels, no alert was generated.

## Solution
Added **Step 6: Recurring DoD Failure Detection** to the paleontologist workflow that runs every 30 minutes.

## Implementation

### 1. Data Structure (internal/store/paleontology.go)
```go
type RecurringDoDFailure struct {
    Failures    string   // the failure text
    Count       int      // how many times this exact failure appeared
    Projects    []string // which projects are affected
    MorselIDs   []string // which morsels failed with this error
    FirstSeenAt string   // when this failure first appeared
    LastSeenAt  string   // when this failure last appeared
}
```

### 2. Store Query (internal/store/paleontology.go)
`GetRecurringDoDFailures(minCount, since)` queries the `dod_results` table:
```sql
SELECT
    failures,
    COUNT(*) as cnt,
    GROUP_CONCAT(DISTINCT project) as projects,
    GROUP_CONCAT(DISTINCT morsel_id) as morsels,
    MIN(checked_at) as first_seen,
    MAX(checked_at) as last_seen
FROM dod_results
WHERE passed = 0
  AND failures != ''
  AND checked_at >= ?
GROUP BY failures
HAVING cnt >= ?
ORDER BY cnt DESC, last_seen DESC
LIMIT 20
```

### 3. Activity (internal/temporal/activities.go)
`DiscoverRecurringDoDFailuresActivity`:
- Detects patterns with 3+ occurrences in the lookback window (default: 6 hours)
- Logs warnings for all recurring patterns
- **Sends Matrix alert when same error appears 5+ times**
- Records health events for observability
- Returns count of patterns detected

### 4. Workflow Integration (internal/temporal/workflow_paleontologist.go)
Added as Step 6 after cost trend analysis:
```go
// Step 6: Recurring DoD Failure Detection
var recurringFailures int
if err := workflow.ExecuteActivity(sqlCtx, a.DiscoverRecurringDoDFailuresActivity, req).Get(ctx, &recurringFailures); err != nil {
    logger.Warn(PaleontologistPrefix+" Recurring DoD failure detection failed (non-fatal)", "error", err)
} else {
    logger.Info(PaleontologistPrefix+" Recurring DoD failure detection complete", "RecurringFailures", recurringFailures)
}
```

### 5. Database Schema (internal/store/store.go)
Added migration:
```go
addColumnIfNotExists(db, "paleontology_runs", "recurring_failures", "recurring_failures INTEGER NOT NULL DEFAULT 0")
```

Updated `RecordPaleontologyRun` to track recurring failures count.

## Alert Format

When 5+ morsels fail with the same error, the paleontologist sends this Matrix alert:

```
🚨 **SYSTEMIC BUILD FAILURE DETECTED** 🚨

**Pattern:** Same DoD failure across **10 morsels** in the last 6h

**Affected projects:** golf-directory, cortex

**Affected morsels:**
- `parse-form-001`
- `validate-email-002`
- `create-user-003`
- `update-profile-004`
- `delete-account-005`
- ...

**Failure:**
```
npm run build
> next build

Error: Failed to compile
./app/page.tsx
Type error: Property 'user' does not exist on type '{}'
```

**Action required:** This is a systemic issue, not an individual morsel problem.
Investigate the root cause (e.g., broken dependency, missing env var, infrastructure issue)
before dispatching more morsels. Fix the underlying issue to unblock the pipeline.
```

## How It Works

1. **Paleontologist runs every 30 minutes** (scheduled in Temporal)
2. Queries `dod_results` for patterns in last 6 hours
3. Groups failures by exact error text
4. For patterns with 3+ occurrences:
   - Logs warning with full context
   - Records health event
5. For patterns with 5+ occurrences:
   - **Sends Matrix alert to admin room**
   - Escalates as systemic issue

## Tuning

Thresholds can be adjusted in `DiscoverRecurringDoDFailuresActivity`:
- **Detection threshold**: Line 1558 - currently 3 occurrences
- **Alert threshold**: Line 1581 - currently 5 occurrences
- **Lookback window**: PaleontologistRequest.LookbackH - default 6 hours

## Testing

To test the alert system:
1. Create multiple DoD failures with the same error text
2. Wait for next paleontologist run (or trigger manually)
3. Check Matrix admin room for alert
4. Verify health_events table for recorded events

## Monitoring

Track paleontologist runs:
```sql
SELECT * FROM paleontology_runs
ORDER BY run_at DESC
LIMIT 10;
```

Check recurring failures detected:
```sql
SELECT recurring_failures, summary
FROM paleontology_runs
WHERE recurring_failures > 0
ORDER BY run_at DESC;
```

View current recurring patterns:
```sql
SELECT failures, COUNT(*) as cnt
FROM dod_results
WHERE passed = 0 AND failures != ''
GROUP BY failures
HAVING cnt >= 3
ORDER BY cnt DESC;
```
