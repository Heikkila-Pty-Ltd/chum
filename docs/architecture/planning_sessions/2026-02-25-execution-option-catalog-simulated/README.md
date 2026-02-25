# Simulated CHUM Planning Session

Session ID: `planning-chum-20260225-execution-option-catalog-manual`  
Run ID: `manual-run-20260225-01`  
Date: 2026-02-25

This folder stores a manual representation of a CHUM planning ceremony using CHUM-native shapes:

- `planning_trace_events.ndjson`: rows compatible with `planning_trace_events`
- `planning_state_snapshots.ndjson`: rows compatible with `planning_state_snapshots`
- `planning_candidate_scores.ndjson`: rows compatible with `planning_candidate_scores`
- `structured_plan.json`: final architecture decisions and workstream plan
- `task_create_requests.json`: DAG task payloads ready for `POST /tasks`
- `canonical_import.sql`: deferred import script for canonical SQLite DB
- `negative_import_attempt_trace.ndjson`: second negative trace (failed import attempt)
- `*.array.json`: pre-slung arrays used by `canonical_import.sql` (no runtime NDJSON transform needed)
- `posthoc_*`: explicit negative markers, blacklist entries, and penalty records for anti-pattern classification

Use this as a reproducible planning artifact when live `/planning/*` endpoints are unavailable.

## Import Policy

Do not import automatically. Use `canonical_import.sql` later against the canonical CHUM database when the environment is stable.

The negative traces are intentional:

- first negative: one-shot planning output without sufficient exploration
- second negative: wasted-time import attempt that failed due avoidable DB mismatch and should not be repeated
