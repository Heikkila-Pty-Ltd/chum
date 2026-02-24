#!/bin/bash
# Quick failure rate report - shows current trends at a glance

DB_PATH="${CHUM_DB:-$HOME/.chum/chum.db}"

if [ ! -f "$DB_PATH" ]; then
    echo "❌ Database not found at: $DB_PATH"
    echo "Set CHUM_DB environment variable or ensure CHUM is initialized."
    exit 1
fi

echo "🦴 CHUM Failure Rate Report"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""

# Meteor tracking status
echo "☄️  Meteor Tracking (Extinction Risk Assessment)"
degrading=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM (SELECT event_type FROM health_events WHERE event_type = 'failure_rate_degrading' ORDER BY created_at DESC LIMIT 3) WHERE event_type = 'failure_rate_degrading'")
improving=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM (SELECT event_type FROM health_events WHERE event_type = 'failure_rate_improving' ORDER BY created_at DESC LIMIT 3) WHERE event_type = 'failure_rate_improving'")

if [ "$improving" -ge 1 ]; then
    echo "   🌍 GREEN - Meteor Distant"
    echo "   Distance: 🌍........................................☄️"
    echo "   Status: Ecosystem thriving - evolution continues"
elif [ "$degrading" -eq 0 ]; then
    echo "   🌍 GREEN - Meteor Distant (Baseline)"
    echo "   Distance: 🌍........................................☄️"
    echo "   Status: No atmospheric anomalies detected"
elif [ "$degrading" -eq 1 ]; then
    echo "   ⚠️  YELLOW - Meteor Approaching"
    echo "   Distance: 🌍............................☄️"
    echo "   Status: 1 impact warning - monitor atmospheric conditions"
elif [ "$degrading" -eq 2 ]; then
    echo "   🔶 ORANGE - Meteor Incoming"
    echo "   Distance: 🌍..............☄️"
    echo "   Status: 2 consecutive warnings - prepare for impact"
elif [ "$degrading" -eq 3 ]; then
    echo "   🚨 RED - Near Impact"
    echo "   Distance: 🌍....☄️"
    echo "   Status: 3 consecutive warnings - extinction risk critical"
else
    echo "   💥 RED - EXTINCTION EVENT"
    echo "   Distance: 🌍💥"
    echo "   Status: $degrading consecutive impacts - ECOSYSTEM FAILING"
fi
echo ""

# Last 24 hours summary
echo "📊 Last 24 Hours"
sqlite3 "$DB_PATH" <<SQL
SELECT
    '   Total DoD checks: ' || COUNT(*) ||
    '\n   Passed: ' || SUM(CASE WHEN passed = 1 THEN 1 ELSE 0 END) ||
    '\n   Failed: ' || SUM(CASE WHEN passed = 0 THEN 1 ELSE 0 END) ||
    '\n   Failure rate: ' || ROUND(100.0 * SUM(CASE WHEN passed = 0 THEN 1 ELSE 0 END) / COUNT(*), 1) || '%'
FROM dod_results
WHERE checked_at >= datetime('now', '-24 hours');
SQL
echo ""

# Last 7 days trend
echo "📈 Daily Trend (Last 7 Days)"
sqlite3 "$DB_PATH" -column -header <<SQL
SELECT
    DATE(checked_at) as Day,
    COUNT(*) as Total,
    SUM(CASE WHEN passed = 0 THEN 1 ELSE 0 END) as Failed,
    ROUND(100.0 * SUM(CASE WHEN passed = 0 THEN 1 ELSE 0 END) / COUNT(*), 1) || '%' as 'Failure Rate'
FROM dod_results
WHERE checked_at >= datetime('now', '-7 days')
GROUP BY DATE(checked_at)
ORDER BY Day DESC;
SQL
echo ""

# Recurring failures (if any)
echo "🚨 Recurring Failures (3+ occurrences in last 24h)"
sqlite3 "$DB_PATH" -column <<SQL
SELECT
    COUNT(*) || 'x' as Cnt,
    SUBSTR(failures, 1, 60) || '...' as Error
FROM dod_results
WHERE passed = 0
  AND failures != ''
  AND checked_at >= datetime('now', '-24 hours')
GROUP BY failures
HAVING COUNT(*) >= 3
ORDER BY COUNT(*) DESC
LIMIT 5;
SQL

if [ $? -ne 0 ] || [ $(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM (SELECT failures FROM dod_results WHERE passed = 0 AND failures != '' AND checked_at >= datetime('now', '-24 hours') GROUP BY failures HAVING COUNT(*) >= 3)") -eq 0 ]; then
    echo "   ✅ No recurring failures detected"
fi
echo ""

# Recent paleontologist runs
echo "🦴 Recent Paleontologist Runs"
sqlite3 "$DB_PATH" -column -header <<SQL
SELECT
    datetime(run_at, 'localtime') as 'Run At',
    recurring_failures as 'Recurring',
    antibodies_discovered as 'Antibodies',
    species_audited as 'Audited'
FROM paleontology_runs
ORDER BY run_at DESC
LIMIT 3;
SQL
echo ""

# Health events (trends)
echo "📋 Recent Health Events"
sqlite3 "$DB_PATH" -column <<SQL
SELECT
    datetime(created_at, 'localtime') as Time,
    event_type as Type,
    SUBSTR(details, 1, 50) || '...' as Details
FROM health_events
WHERE event_type IN ('failure_rate_improving', 'failure_rate_degrading', 'recurring_dod_failure')
ORDER BY created_at DESC
LIMIT 5;
SQL

if [ $? -ne 0 ] || [ $(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM health_events WHERE event_type IN ('failure_rate_improving', 'failure_rate_degrading', 'recurring_dod_failure')") -eq 0 ]; then
    echo "   (No trend events recorded yet)"
fi
echo ""

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "💡 Tip: Run this script daily to track improvement over time"
echo "🎯 Goal: Failure rate trending down as genomes evolve"
