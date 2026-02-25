-- Deferred canonical import for simulated planning session artifacts.
--
-- Usage (from repository root):
--   sqlite3 /path/to/chum.db < docs/architecture/planning_sessions/2026-02-25-execution-option-catalog-simulated/canonical_import.sql
--
-- This script imports:
-- 1) the simulated ceremony traces/snapshots/scores
-- 2) post-hoc negative markers for the one-shot planning output
-- 3) a second negative trace for failed import-attempt churn

.bail on
BEGIN;

-- Idempotent cleanup for the two imported sessions.
DELETE FROM planning_trace_events
WHERE session_id IN (
  'planning-chum-20260225-execution-option-catalog-manual',
  'planning-chum-20260225-import-attempt-negative'
);

DELETE FROM planning_state_snapshots
WHERE session_id IN (
  'planning-chum-20260225-execution-option-catalog-manual',
  'planning-chum-20260225-import-attempt-negative'
);

DELETE FROM planning_action_blacklist
WHERE session_id IN (
  'planning-chum-20260225-execution-option-catalog-manual',
  'planning-chum-20260225-import-attempt-negative'
);

-- --- Base simulated ceremony trace ---
WITH data(row) AS (
  SELECT value
  FROM json_each(CAST(readfile('docs/architecture/planning_sessions/2026-02-25-execution-option-catalog-simulated/planning_trace_events.array.json') AS TEXT))
)
INSERT INTO planning_trace_events (
  session_id, run_id, project, task_id, cycle, stage, node_id, parent_node_id,
  branch_id, option_id, event_type, actor, tool_name, tool_input, tool_output,
  prompt_text, response_text, summary_text, full_text, selected_option, reward,
  metadata_json, created_at
)
SELECT
  json_extract(row, '$.session_id'),
  json_extract(row, '$.run_id'),
  json_extract(row, '$.project'),
  COALESCE(json_extract(row, '$.task_id'), ''),
  COALESCE(json_extract(row, '$.cycle'), 0),
  COALESCE(json_extract(row, '$.stage'), ''),
  COALESCE(json_extract(row, '$.node_id'), ''),
  COALESCE(json_extract(row, '$.parent_node_id'), ''),
  COALESCE(json_extract(row, '$.branch_id'), ''),
  COALESCE(json_extract(row, '$.option_id'), ''),
  COALESCE(json_extract(row, '$.event_type'), ''),
  COALESCE(json_extract(row, '$.actor'), ''),
  COALESCE(json_extract(row, '$.tool_name'), ''),
  COALESCE(json_extract(row, '$.tool_input'), ''),
  COALESCE(json_extract(row, '$.tool_output'), ''),
  COALESCE(json_extract(row, '$.prompt_text'), ''),
  COALESCE(json_extract(row, '$.response_text'), ''),
  COALESCE(json_extract(row, '$.summary_text'), ''),
  COALESCE(json_extract(row, '$.full_text'), ''),
  COALESCE(json_extract(row, '$.selected_option'), ''),
  COALESCE(json_extract(row, '$.reward'), 0),
  COALESCE(json_extract(row, '$.metadata_json'), '{}'),
  COALESCE(json_extract(row, '$.created_at'), datetime('now'))
FROM data;

-- --- Post-hoc negative markers (first failed trace exemplar) ---
WITH data(row) AS (
  SELECT value
  FROM json_each(CAST(readfile('docs/architecture/planning_sessions/2026-02-25-execution-option-catalog-simulated/posthoc_negative_markers.array.json') AS TEXT))
)
INSERT INTO planning_trace_events (
  session_id, run_id, project, task_id, cycle, stage, node_id, parent_node_id,
  branch_id, option_id, event_type, actor, tool_name, tool_input, tool_output,
  prompt_text, response_text, summary_text, full_text, selected_option, reward,
  metadata_json, created_at
)
SELECT
  json_extract(row, '$.session_id'),
  json_extract(row, '$.run_id'),
  json_extract(row, '$.project'),
  COALESCE(json_extract(row, '$.task_id'), ''),
  COALESCE(json_extract(row, '$.cycle'), 0),
  COALESCE(json_extract(row, '$.stage'), ''),
  COALESCE(json_extract(row, '$.node_id'), ''),
  COALESCE(json_extract(row, '$.parent_node_id'), ''),
  COALESCE(json_extract(row, '$.branch_id'), ''),
  COALESCE(json_extract(row, '$.option_id'), ''),
  COALESCE(json_extract(row, '$.event_type'), ''),
  COALESCE(json_extract(row, '$.actor'), ''),
  COALESCE(json_extract(row, '$.tool_name'), ''),
  COALESCE(json_extract(row, '$.tool_input'), ''),
  COALESCE(json_extract(row, '$.tool_output'), ''),
  COALESCE(json_extract(row, '$.prompt_text'), ''),
  COALESCE(json_extract(row, '$.response_text'), ''),
  COALESCE(json_extract(row, '$.summary_text'), ''),
  COALESCE(json_extract(row, '$.full_text'), ''),
  COALESCE(json_extract(row, '$.selected_option'), ''),
  COALESCE(json_extract(row, '$.reward'), 0),
  COALESCE(json_extract(row, '$.metadata_json'), '{}'),
  COALESCE(json_extract(row, '$.created_at'), datetime('now'))
FROM data;

-- Ensure the original plan_agreed row is explicitly zeroed post-hoc.
UPDATE planning_trace_events
SET reward = 0,
    summary_text = 'Post-hoc rejected: plan_agreed from one-shot ceremony without exploration',
    full_text = full_text || '\nposthoc_status=rejected\nposthoc_reason=insufficient_exploration_one_shot'
WHERE session_id = 'planning-chum-20260225-execution-option-catalog-manual'
  AND event_type = 'plan_agreed';

-- --- Second failed trace exemplar (failed import attempt churn) ---
WITH data(row) AS (
  SELECT value
  FROM json_each(CAST(readfile('docs/architecture/planning_sessions/2026-02-25-execution-option-catalog-simulated/negative_import_attempt_trace.array.json') AS TEXT))
)
INSERT INTO planning_trace_events (
  session_id, run_id, project, task_id, cycle, stage, node_id, parent_node_id,
  branch_id, option_id, event_type, actor, tool_name, tool_input, tool_output,
  prompt_text, response_text, summary_text, full_text, selected_option, reward,
  metadata_json, created_at
)
SELECT
  json_extract(row, '$.session_id'),
  json_extract(row, '$.run_id'),
  json_extract(row, '$.project'),
  COALESCE(json_extract(row, '$.task_id'), ''),
  COALESCE(json_extract(row, '$.cycle'), 0),
  COALESCE(json_extract(row, '$.stage'), ''),
  COALESCE(json_extract(row, '$.node_id'), ''),
  COALESCE(json_extract(row, '$.parent_node_id'), ''),
  COALESCE(json_extract(row, '$.branch_id'), ''),
  COALESCE(json_extract(row, '$.option_id'), ''),
  COALESCE(json_extract(row, '$.event_type'), ''),
  COALESCE(json_extract(row, '$.actor'), ''),
  COALESCE(json_extract(row, '$.tool_name'), ''),
  COALESCE(json_extract(row, '$.tool_input'), ''),
  COALESCE(json_extract(row, '$.tool_output'), ''),
  COALESCE(json_extract(row, '$.prompt_text'), ''),
  COALESCE(json_extract(row, '$.response_text'), ''),
  COALESCE(json_extract(row, '$.summary_text'), ''),
  COALESCE(json_extract(row, '$.full_text'), ''),
  COALESCE(json_extract(row, '$.selected_option'), ''),
  COALESCE(json_extract(row, '$.reward'), 0),
  COALESCE(json_extract(row, '$.metadata_json'), '{}'),
  COALESCE(json_extract(row, '$.created_at'), datetime('now'))
FROM data;

-- --- Snapshots ---
WITH data(row) AS (
  SELECT value
  FROM json_each(CAST(readfile('docs/architecture/planning_sessions/2026-02-25-execution-option-catalog-simulated/planning_state_snapshots.array.json') AS TEXT))
)
INSERT INTO planning_state_snapshots (
  session_id, run_id, project, task_id, cycle, stage, state_hash, state_json, stable, reason, created_at
)
SELECT
  json_extract(row, '$.session_id'),
  json_extract(row, '$.run_id'),
  json_extract(row, '$.project'),
  COALESCE(json_extract(row, '$.task_id'), ''),
  COALESCE(json_extract(row, '$.cycle'), 0),
  COALESCE(json_extract(row, '$.stage'), ''),
  json_extract(row, '$.state_hash'),
  COALESCE(json_extract(row, '$.state_json'), '{}'),
  CASE WHEN COALESCE(json_extract(row, '$.stable'), 0) THEN 1 ELSE 0 END,
  COALESCE(json_extract(row, '$.reason'), ''),
  COALESCE(json_extract(row, '$.created_at'), datetime('now'))
FROM data;

WITH data(row) AS (
  SELECT value
  FROM json_each(CAST(readfile('docs/architecture/planning_sessions/2026-02-25-execution-option-catalog-simulated/posthoc_negative_snapshots.array.json') AS TEXT))
)
INSERT INTO planning_state_snapshots (
  session_id, run_id, project, task_id, cycle, stage, state_hash, state_json, stable, reason, created_at
)
SELECT
  json_extract(row, '$.session_id'),
  json_extract(row, '$.run_id'),
  json_extract(row, '$.project'),
  COALESCE(json_extract(row, '$.task_id'), ''),
  COALESCE(json_extract(row, '$.cycle'), 0),
  COALESCE(json_extract(row, '$.stage'), ''),
  json_extract(row, '$.state_hash'),
  COALESCE(json_extract(row, '$.state_json'), '{}'),
  CASE WHEN COALESCE(json_extract(row, '$.stable'), 0) THEN 1 ELSE 0 END,
  COALESCE(json_extract(row, '$.reason'), ''),
  COALESCE(json_extract(row, '$.created_at'), datetime('now'))
FROM data;

-- --- Blacklist entries ---
WITH data(row) AS (
  SELECT value
  FROM json_each(CAST(readfile('docs/architecture/planning_sessions/2026-02-25-execution-option-catalog-simulated/posthoc_blacklist_entries.array.json') AS TEXT))
)
INSERT OR REPLACE INTO planning_action_blacklist (
  id, session_id, project, task_id, cycle, stage, state_hash, action_hash, reason, metadata_json, created_at
)
SELECT
  COALESCE((SELECT id FROM planning_action_blacklist
            WHERE session_id = json_extract(row, '$.session_id')
              AND state_hash = json_extract(row, '$.state_hash')
              AND action_hash = json_extract(row, '$.action_hash')), NULL),
  json_extract(row, '$.session_id'),
  json_extract(row, '$.project'),
  COALESCE(json_extract(row, '$.task_id'), ''),
  COALESCE(json_extract(row, '$.cycle'), 0),
  COALESCE(json_extract(row, '$.stage'), ''),
  json_extract(row, '$.state_hash'),
  json_extract(row, '$.action_hash'),
  COALESCE(json_extract(row, '$.reason'), ''),
  COALESCE(json_extract(row, '$.metadata_json'), '{}'),
  COALESCE(json_extract(row, '$.created_at'), datetime('now'))
FROM data;

-- --- Candidate scores ---
WITH data(row) AS (
  SELECT value
  FROM json_each(CAST(readfile('docs/architecture/planning_sessions/2026-02-25-execution-option-catalog-simulated/planning_candidate_scores.array.json') AS TEXT))
)
INSERT OR REPLACE INTO planning_candidate_scores (
  project, option_id, score_adjustment, successes, failures, last_reason, updated_at
)
SELECT
  json_extract(row, '$.project'),
  json_extract(row, '$.option_id'),
  COALESCE(json_extract(row, '$.score_adjustment'), 0),
  COALESCE(json_extract(row, '$.successes'), 0),
  COALESCE(json_extract(row, '$.failures'), 0),
  COALESCE(json_extract(row, '$.last_reason'), ''),
  COALESCE(json_extract(row, '$.updated_at'), datetime('now'))
FROM data;

WITH data(row) AS (
  SELECT value
  FROM json_each(CAST(readfile('docs/architecture/planning_sessions/2026-02-25-execution-option-catalog-simulated/posthoc_candidate_score_penalties.array.json') AS TEXT))
)
INSERT OR REPLACE INTO planning_candidate_scores (
  project, option_id, score_adjustment, successes, failures, last_reason, updated_at
)
SELECT
  json_extract(row, '$.project'),
  json_extract(row, '$.option_id'),
  COALESCE(json_extract(row, '$.score_adjustment'), 0),
  COALESCE(json_extract(row, '$.successes'), 0),
  COALESCE(json_extract(row, '$.failures'), 0),
  COALESCE(json_extract(row, '$.last_reason'), ''),
  COALESCE(json_extract(row, '$.updated_at'), datetime('now'))
FROM data;

COMMIT;
