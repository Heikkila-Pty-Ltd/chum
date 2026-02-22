# CHUM API Endpoints

Last validated against:
- `internal/api/api.go` (`Server.Start` route registration)
- `internal/api/*_handlers.go` (request/response behavior)
- `internal/api/auth.go` (middleware behavior)
- `internal/api/api_test.go` (existing endpoint-level assertions)

Canonical date: **2026-02-22** (UTC).

## Scope and source of truth

This section documents only routes registered in `Server.Start`.

- route pattern
- allowed methods
- middleware behavior
- request/response shapes
- canonical statuses and errors

## Registered routes (implemented)

| Route pattern | Methods | Auth in middleware | Handler | Notes |
| --- | --- | --- | --- | --- |
| `/status` | any | middleware passes all paths with `method != POST` for non-control | `handleStatus` | JSON object with uptime + running count |
| `/projects` | any | same as above | `handleProjects` | Map iteration order is not deterministic |
| `/projects/{project_id}` | any | same as above | `handleProjectDetail` | `GET /projects/` forwards to `/projects` |
| `/health` | any | same as above | `handleHealth` | returns `503` if any `gateway_critical` event in last hour |
| `/metrics` | any | same as above | `handleMetrics` | Prometheus text format |
| `/recommendations` | GET only | same as above | `handleRecommendations` | query-driven list, defaults and clamp |
| `/dispatches/{bead_id}` | any | same as above | `handleDispatchDetail` | 400 when `{bead_id}` is empty |
| `/safety/blocks` | GET only | same as above | `handleSafetyBlocks` | returns counts + block list |
| `/workflows/start` | POST only | wrapped by `RequireAuth` but auth logic currently rejects none | `handleWorkflowStart` | registers `ChumAgentWorkflow` |
| `/workflows/{workflow_id}` | GET only | wrapped by `RequireAuth` but auth logic currently rejects none | `handleWorkflowStatus` via `routeWorkflows` | uses `POST` path for status if method mismatch -> `405` |
| `/workflows/{workflow_id}/approve` | POST only | wrapped by `RequireAuth` but auth logic currently rejects none | `handleWorkflowApprove` via `routeWorkflows` | required JSON body: none |
| `/workflows/{workflow_id}/reject` | POST only | wrapped by `RequireAuth` but auth logic currently rejects none | `handleWorkflowReject` via `routeWorkflows` | required JSON body: none |
| `/planning/start` | POST only | wrapped by `RequireAuth` but auth logic currently rejects none | `handlePlanningStart` | starts `PlanningCeremonyWorkflow` |
| `/planning/{session_id}` | GET only | wrapped by `RequireAuth` but auth logic currently rejects none | `handlePlanningStatus` via `routePlanning` | POST on this path is `405` |
| `/planning/{session_id}/select` | POST only | wrapped by `RequireAuth` but auth logic currently rejects none | `handlePlanningSignal` via `routePlanning` | body: `{ "value": "..." }` |
| `/planning/{session_id}/answer` | POST only | wrapped by `RequireAuth` but auth logic currently rejects none | `handlePlanningSignal` via `routePlanning` | body: `{ "value": "..." }` |
| `/planning/{session_id}/greenlight` | POST only | wrapped by `RequireAuth` but auth logic currently rejects none | `handlePlanningSignal` via `routePlanning` | body: `{ "value": "..." }` |
| `/crab/decompose` | POST only | wrapped by `RequireAuth` but auth logic currently rejects none | `handleCrabDecompose` | starts `CrabDecompositionWorkflow` |
| `/crab/{session_id}` | GET only | wrapped by `RequireAuth` but auth logic currently rejects none | `handleCrabStatus` via `routeCrab` | POST on this path is `405` |
| `/crab/{session_id}/clarify` | POST only | wrapped by `RequireAuth` but auth logic currently rejects none | `handleCrabSignal` via `routeCrab` | body: `{ "value": "..." }` |
| `/crab/{session_id}/review` | POST only | wrapped by `RequireAuth` but auth logic currently rejects none | `handleCrabSignal` via `routeCrab` | body: `{ "value": "..." }` |

> Note: `RequireAuth` enforces auth only for control paths (for example `/scheduler/pause`, `/dispatches/{id}/cancel`, etc.). None of those control paths are currently registered, so all currently exposed routes execute without auth enforcement.

## Cross route references

- `docs/api/api-security.md` now points to this endpoint reference for source-of-truth routing.
- `docs/api/CHUM_LLM_INTERACTION_GUIDE.md` should be read with this page before automation actions.
- `docs/architecture/STINGRAY_DESIGN.md` does not currently define HTTP endpoints; verify via this file when operating automation around external workflows.

## Shared API contract

### Content types

JSON endpoints set:
- `Content-Type: application/json`
- error responses use JSON object shape `{"error":"..."}`

`/metrics` sets:
- `Content-Type: text/plain; version=0.0.4; charset=utf-8`

### Authentication behavior (`RequireAuth`)

`RequireAuth` is attached to `/workflows/*`, `/planning/*`, and `/crab/*` routes.

For non-control requests (any route not matching control method/path checks), middleware calls next handler without auth checks.

Control check list used internally:
- `POST /scheduler/pause`
- `POST /scheduler/resume`
- `POST /scheduler/plan/activate`
- `POST /scheduler/plan/clear`
- `POST /dispatches/{id}/cancel`
- `POST /dispatches/{id}/retry`

Error behavior:
- auth disabled + local-only true + non-local client => `403` (`Access denied: non-local requests not allowed`)
- auth enabled with missing/invalid bearer token => `401` (`Unauthorized: valid token required`)
- all control responses include `WWW-Authenticate: Bearer` header on auth failures

## Endpoint details

### `GET /status`

- Handler: `handleStatus`
- Method: all methods
- Success response:
  - `200`
  - `uptime_s` (`number`)
  - `running_count` (`number`)

### `GET /projects`

- Handler: `handleProjects`
- Method: all methods
- Success response:
  - `200`
  - array of projects:
    - `name` (`string`)
    - `enabled` (`boolean`)
    - `priority` (`number`)

### `GET /projects/{project_id}`

- Handler: `handleProjectDetail`
- Method: all methods
- Empty path `{project_id}` (for example `/projects/`) forwards to `GET /projects`.
- Success response:
  - `200`
  - `name` (`string`)
  - `enabled` (`boolean`)
  - `priority` (`number`)
  - `workspace` (`string`)
  - `beads_dir` (`string`)
- Error response:
  - `404` with `{"error":"project not found"}`

### `GET /health`

- Handler: `handleHealth`
- Method: all methods
- `events_1h` (`number`) uses `GetRecentHealthEvents(1)`.
- `recent_events` each item:
  - `type` (`string`)
  - `details` (`string`)
  - `dispatch_id` (`string`)
  - `bead_id` (`string`)
  - `time` (`string`, RFC3339)
- Success response:
  - `200` when no `gateway_critical`
  - `503` when at least one `gateway_critical` exists

### `GET /metrics`

- Handler: `handleMetrics`
- Method: all methods
- Response:
  - `200`
  - Prometheus exposition format text
  - key names include:
    - `chum_dispatches_total`
    - `chum_dispatches_failed_total`
    - `chum_dispatches_running`
    - `chum_dispatches_running_by_stage`
    - `chum_tokens_total`
    - `chum_cost_usd_total`
    - `chum_uptime_seconds`
    - others documented in handler implementation

### `GET /recommendations`

- Handler: `handleRecommendations`
- Method: GET only
- Query parameters:
  - `q` (`string`, optional)
  - `hours` (`int`, optional, default `24`, valid range `1` to `168`, invalid or out-of-range resets to default)
  - `limit` (`int`, optional, default `20`, max `100`)
- Response:
  - `200` (even when no matches)
  - `recommendations` (array):
    - `id` (`number`)
    - `bead_id` (`string`)
    - `project` (`string`)
    - `category` (`string`)
    - `summary` (`string`)
    - `detail` (`string`)
    - `files` (`string[]`)
    - `labels` (`string[]`)
    - `created_at` (`string`, RFC3339)
  - `hours` (`number`)
  - `count` (`number`)
  - `generated_at` (`string`, RFC3339)

### `GET /dispatches/{bead_id}`

- Handler: `handleDispatchDetail`
- Method: all methods
- Path rule:
  - `{bead_id}` required and must not be empty
- Success response (`200`):
  - `bead_id` (`string`)
  - `dispatches` (`array`)
    - `id` (`number`)
    - `agent` (`string`)
    - `provider` (`string`)
    - `tier` (`string`)
    - `status` (`string`)
    - `stage` (`string`)
    - `exit_code` (`number`)
    - `duration_s` (`number`)
    - `dispatched_at` (`string`, RFC3339)
    - `session_name` (`string`)
    - `output_tail` (`string`)
    - `failure_category` (`string`, optional)
    - `failure_summary` (`string`, optional)
- Error responses:
  - `400` if `{bead_id}` empty
  - `500` if store lookup fails

### `GET /safety/blocks`

- Handler: `handleSafetyBlocks`
- Method: GET only
- Success response (`200`):
  - `total` (`number`)
  - `counts_by_type` (`map[string]int`)
  - `blocks` (`array`)
    - `scope` (`string`)
    - `block_type` (`string`)
    - `blocked_until` (`string`, RFC3339)
    - `reason` (`string`)
    - `metadata` (`object`, optional)
    - `created_at` (`string`, RFC3339)
- Error response:
  - `405` on non-GET methods
  - `500` on store read failures

## Workflow control endpoints

### `POST /workflows/start`

- Handler: `handleWorkflowStart`
- Request body (`temporal.TaskRequest`):
  - `task_id` (`string`, required)
  - `prompt` (`string`, required)
  - `project` (`string`, optional)
  - `agent` (`string`, default `claude`)
  - `reviewer` (`string`, optional passthrough)
  - `work_dir` (`string`, default `/tmp/workspace`)
  - `provider` (`string`, optional)
  - `dod_checks` (`string[]`, optional)
  - `slow_step_threshold` (`number`, nanoseconds, optional, default from `general.slow_step_threshold`)
- Success response (`200`):
  - `workflow_id` (`string`)
  - `run_id` (`string`)
  - `status` (`string`, value: `"started"`)
- Error responses:
  - `400` invalid JSON, missing `task_id` or `prompt`
  - `405` non-POST method
  - `500` temporal connect/start failure

### `POST /workflows/{workflow_id}/approve`

- Handler: `handleWorkflowApprove`
- Request body: none
- Success response (`200`):
  - `workflow_id` (`string`)
  - `status` (`string`, value: `"approved"`)
- Error responses:
  - `405` non-POST
  - `500` Temporal connection or signal failure

### `POST /workflows/{workflow_id}/reject`

- Handler: `handleWorkflowReject`
- Request body: none
- Success response (`200`):
  - `workflow_id` (`string`)
  - `status` (`string`, value: `"rejected"`)
- Error responses:
  - `405` non-POST
  - `500` Temporal connection or signal failure

### `GET /workflows/{workflow_id}`

- Handler: `handleWorkflowStatus`
- Method: GET only
- Error response:
  - `400` for empty `workflow_id`
  - `404` for workflow query failures
  - `500` temporal describe failure
- Success response:
  - `200`
  - `workflow_id` (`string`)
  - `run_id` (`string`)
  - `type` (`string`)
  - `status` (`string`)
  - `start_time` (`string`, RFC3339)
  - `close_time` (`string`, RFC3339, optional when present)

## Planning endpoints

### `POST /planning/start`

- Handler: `handlePlanningStart`
- Request body (`temporal.PlanningRequest`):
  - `project` (`string`, required)
  - `agent` (`string`, default `claude`)
  - `tier` (`string`, optional)
  - `work_dir` (`string`, required)
  - `slow_step_threshold` (`number`, nanoseconds, optional)
- Success response (`200`):
  - `session_id` (`string`)
  - `run_id` (`string`)
  - `status` (`string`, value: `"grooming_backlog"`)
- Error responses:
  - `400` invalid JSON, missing required fields
  - `405` non-POST
  - `500` temporal connect/start failure

### `GET /planning/{session_id}`

- Handler: `handlePlanningStatus`
- Method: GET only
- Success response (`200`):
  - `session_id` (`string`)
  - `run_id` (`string`)
  - `status` (`string`)
  - `start_time` (`string`, RFC3339)
  - `close_time` (`string`, RFC3339, optional when present)
  - optional `note` (`string`) while running
- Error responses:
  - `400` empty `{session_id}`
  - `405` non-GET
  - `404` unknown session
  - `500` temporal describe failure

### `POST /planning/{session_id}/select`
### `POST /planning/{session_id}/answer`
### `POST /planning/{session_id}/greenlight`

- Handler: `routePlanning` -> `handlePlanningSignal`
- Request body:
  - JSON object with required `value` (`string`)
- Success response (`200`):
  - `session_id` (`string`)
  - `signal` (`string`: `item-selected`, `answer`, `greenlight`)
  - `value` (`string`)
- Error responses:
  - `400` invalid JSON (expected `{"value":"..."}`)
  - `405` non-POST
  - `500` temporal connect or signal error

## Crab decomposition endpoints

### `POST /crab/decompose`

- Handler: `handleCrabDecompose`
- Request body (`temporal.CrabDecompositionRequest`):
  - `project` (`string`, required)
  - `work_dir` (`string`, required)
  - `plan_markdown` (`string`, required)
  - `plan_id` (`string`, optional, default `plan-<project>-<unix_ts>`)
  - `tier` (`string`, optional)
  - `parent_whale_id` (`string`, optional)
- Success response (`200`):
  - `session_id` (`string`)
  - `run_id` (`string`)
  - `plan_id` (`string`)
  - `status` (`string`, value: `"parsing"`)
- Error responses:
  - `400` invalid JSON or missing required fields
  - `405` non-POST
  - `500` temporal connect/start failure

### `GET /crab/{session_id}`

- Handler: `handleCrabStatus`
- Method: GET only
- Success response (`200`):
  - `session_id` (`string`)
  - `run_id` (`string`)
  - `status` (`string`)
  - `start_time` (`string`, RFC3339)
  - `close_time` (`string`, RFC3339, optional)
  - optional `note` while running
- Error responses:
  - `400` empty `{session_id}`
  - `405` non-GET
  - `404` unknown session
  - `500` temporal describe failure

### `POST /crab/{session_id}/clarify`
### `POST /crab/{session_id}/review`

- Handler: `routeCrab` -> `handleCrabSignal`
- Request body:
  - JSON object with required `value` (`string`)
- Success response (`200`):
  - `session_id` (`string`)
  - `signal` (`string`: `crab-clarification`, `crab-review`)
  - `value` (`string`)
- Error responses:
  - `400` invalid JSON (expected `{"value":"..."}`)
  - `405` non-POST
  - `500` temporal connect or signal error

## Error and status matrix

Implemented endpoint statuses:

| Status | When used |
| --- | --- |
| `200` | request accepted and handled |
| `400` | invalid payload, missing required path param, or malformed query/body |
| `401` | auth required and token missing/invalid on control path |
| `403` | local-only mode blocks remote request on control path |
| `404` | missing resource (project/workflow/planning/crab session) |
| `405` | wrong method |
| `500` | temporal connect, dispatch/describe failures, store/query failures |

### Consistency guarantee

This list is generated from and checked against:
- route registration in `Server.Start`
- method dispatch in each handler
- existing tests in `internal/api/api_test.go`

Any endpoint absent from `Server.Start` should be treated as non-authoritative and implemented as stale documentation.
