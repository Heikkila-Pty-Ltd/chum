#!/usr/bin/env bash
set -euo pipefail

DB_PATH="${1:-${CHUM_STATE_DB:-$HOME/.local/share/chum/chum.db}}"
LIMIT="${2:-20}"
SESSION_ID="${3:-}"

if ! command -v sqlite3 >/dev/null 2>&1; then
	echo "sqlite3 is required but not installed" >&2
	exit 1
fi

if [[ ! -f "$DB_PATH" ]]; then
	echo "planning trace db not found: $DB_PATH" >&2
	exit 1
fi

if ! [[ "$LIMIT" =~ ^[0-9]+$ ]]; then
	echo "limit must be an integer, got: $LIMIT" >&2
	exit 1
fi

table_exists="$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='planning_trace_events';")"
if [[ "$table_exists" != "1" ]]; then
	echo "planning_trace_events table missing in: $DB_PATH" >&2
	exit 1
fi

interaction_columns="$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM pragma_table_info('planning_trace_events') WHERE name IN ('interaction_class','interaction_type','human_interactive');")"
if [[ "$interaction_columns" != "3" ]]; then
	echo "planning_trace_events is missing interaction classification columns; start CHUM once with the latest build to migrate schema" >&2
	exit 1
fi

echo "DB: $DB_PATH"
echo
echo "== Session Scoreboard (latest $LIMIT) =="
sqlite3 -header -column "$DB_PATH" "
WITH session_stats AS (
	SELECT
		session_id,
		MIN(created_at) AS started_at,
		MAX(created_at) AS ended_at,
		COUNT(*) AS events,
		SUM(CASE WHEN event_type = 'tool_call' THEN 1 ELSE 0 END) AS tool_calls,
		SUM(CASE WHEN event_type = 'llm_call' THEN 1 ELSE 0 END) AS llm_calls,
		SUM(CASE WHEN event_type = 'gate_fail' THEN 1 ELSE 0 END) AS gate_failures,
		SUM(CASE WHEN event_type = 'rollback_applied' THEN 1 ELSE 0 END) AS rollbacks,
		SUM(CASE WHEN event_type = 'blacklist_blocked' THEN 1 ELSE 0 END) AS blacklist_blocks,
		SUM(CASE WHEN event_type = 'trace_review' THEN 1 ELSE 0 END) AS trace_reviews,
		SUM(CASE WHEN event_type = 'alternative_trace_candidate' THEN 1 ELSE 0 END) AS alt_candidates,
		SUM(CASE WHEN human_interactive = 1 THEN 1 ELSE 0 END) AS human_steps,
		SUM(CASE WHEN interaction_type = 'start_session' THEN 1 ELSE 0 END) AS human_session_controls,
		SUM(CASE WHEN interaction_type = 'select_item' THEN 1 ELSE 0 END) AS human_selects,
		SUM(CASE WHEN interaction_type = 'answer_question' THEN 1 ELSE 0 END) AS human_answers,
		SUM(CASE WHEN interaction_type IN ('greenlight_go', 'greenlight_realign', 'greenlight_decision') THEN 1 ELSE 0 END) AS human_decisions,
		ROUND(SUM(reward), 3) AS total_reward,
		MAX(CASE WHEN event_type = 'plan_agreed' THEN 1 ELSE 0 END) AS agreed,
		MAX(cycle) AS max_cycle
	FROM planning_trace_events
	GROUP BY session_id
)
SELECT
	session_id,
	started_at,
	ended_at,
	events,
	tool_calls,
	llm_calls,
	gate_failures,
	rollbacks,
	blacklist_blocks,
	trace_reviews,
	alt_candidates,
	human_steps,
	ROUND((100.0 * human_steps) / CASE WHEN events > 0 THEN events ELSE 1 END, 1) AS human_step_pct,
	human_session_controls,
	human_selects,
	human_answers,
	human_decisions,
	total_reward,
	agreed,
	max_cycle
FROM session_stats
ORDER BY started_at DESC
LIMIT ${LIMIT};
"

echo
echo "== Human Interaction Types (latest $LIMIT sessions) =="
sqlite3 -header -column "$DB_PATH" "
WITH latest_sessions AS (
	SELECT session_id
	FROM planning_trace_events
	GROUP BY session_id
	ORDER BY MAX(created_at) DESC
	LIMIT ${LIMIT}
)
SELECT
	e.session_id,
	e.interaction_class,
	e.interaction_type,
	COUNT(*) AS events
FROM planning_trace_events e
WHERE e.session_id IN (SELECT session_id FROM latest_sessions)
  AND e.human_interactive = 1
GROUP BY e.session_id, e.interaction_class, e.interaction_type
ORDER BY e.session_id DESC, events DESC, e.interaction_type ASC;
"

echo
echo "== Repeated Human Patterns (automation candidates) =="
sqlite3 -header -column "$DB_PATH" "
SELECT
	interaction_type,
	COALESCE(NULLIF(summary_text, ''), '[no-summary]') AS pattern,
	COUNT(*) AS frequency
FROM planning_trace_events
WHERE human_interactive = 1
GROUP BY interaction_type, pattern
HAVING COUNT(*) > 1
ORDER BY frequency DESC, interaction_type ASC
LIMIT ${LIMIT};
"

echo
echo "== Winning Plans (latest $LIMIT) =="
sqlite3 -header -column "$DB_PATH" "
SELECT
	session_id,
	project,
	task_id,
	cycle,
	branch_id,
	option_id,
	ROUND(reward, 3) AS reward,
	created_at,
	summary_text
FROM planning_trace_events
WHERE event_type = 'plan_agreed'
ORDER BY id DESC
LIMIT ${LIMIT};
"

if [[ -n "$SESSION_ID" ]]; then
	# SQLite string literal escaping.
	SESSION_ESCAPED="${SESSION_ID//\'/\'\'}"
	echo
	echo "== Full Trace Dump: $SESSION_ID =="
	sqlite3 -header -column "$DB_PATH" "
SELECT
	id,
	created_at,
	cycle,
	stage,
	node_id,
	parent_node_id,
	branch_id,
	option_id,
	event_type,
	interaction_class,
	interaction_type,
	human_interactive,
	actor,
	tool_name,
	reward,
	summary_text,
	full_text
FROM planning_trace_events
WHERE session_id = '${SESSION_ESCAPED}'
ORDER BY id ASC;
"
fi
