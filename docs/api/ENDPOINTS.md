# CHUM API Endpoints

Canonical date: **2026-02-22 (UTC)**.

## Scope and source-of-truth

This is the live route registry for endpoints registered in `internal/api/api.go`.

Paths listed here must match:

- `internal/api/api.go` route registrations.
- `internal/api/handlers_*.go` request and response implementations.
- `internal/api/auth.go` middleware behavior.
- `internal/api/api_test.go` coverage assertions.

## Registered route matrix

| Route pattern | Methods | Handler | Auth behavior | Response format |
| --- | --- | --- | --- | --- |
| `/status` | any | `handleStatus` | wrapped by middleware, no control check for this path | `application/json` |
| `/projects` | any | `handleProjects` | same as above | `application/json` |
| `/projects/{project_id}` (via `/projects/`) | any | `handleProjectDetail` | same as above | `application/json` |
| `/health` | any | `handleHealth` | same as above | `application/json` |
| `/metrics` | any | `handleMetrics` | same as above | `text/plain; version=0.0.4; charset=utf-8` |
| `/recommendations` | GET only | `handleRecommendations` | same as above | `application/json` |
| `/dispatches/{bead_id}` (via `/dispatches/`) | any | `handleDispatchDetail` | same as above | `application/json` |
| `/safety/blocks` | GET only | `handleSafetyBlocks` | same as above | `application/json` |
| `/workflows/start` | POST only | `handleWorkflowStart` | routed through `RequireAuth`; not a control endpoint under current middleware logic | `application/json` |
| `/workflows/{workflow_id}` | GET only | `routeWorkflows -> handleWorkflowStatus` | routed through `RequireAuth`; not a control endpoint | `application/json` |
| `/workflows/{workflow_id}/approve` | POST only | `routeWorkflows -> handleWorkflowApprove` | routed through `RequireAuth`; not a control endpoint | `application/json` |
| `/workflows/{workflow_id}/reject` | POST only | `routeWorkflows -> handleWorkflowReject` | routed through `RequireAuth`; not a control endpoint | `application/json` |
| `/planning/start` | POST only | `handlePlanningStart` | routed through `RequireAuth`; not a control endpoint | `application/json` |
| `/planning/{session_id}` | GET only | `routePlanning -> handlePlanningStatus` | routed through `RequireAuth`; not a control endpoint | `application/json` |
| `/planning/{session_id}/select` | POST only | `routePlanning -> handlePlanningSignal` | routed through `RequireAuth`; not a control endpoint | `application/json` |
| `/planning/{session_id}/answer` | POST only | `routePlanning -> handlePlanningSignal` | routed through `RequireAuth`; not a control endpoint | `application/json` |
| `/planning/{session_id}/greenlight` | POST only | `routePlanning -> handlePlanningSignal` | routed through `RequireAuth`; not a control endpoint | `application/json` |
| `/crab/decompose` | POST only | `handleCrabDecompose` | routed through `RequireAuth`; not a control endpoint | `application/json` |
| `/crab/{session_id}` | GET only | `routeCrab -> handleCrabStatus` | routed through `RequireAuth`; not a control endpoint | `application/json` |
| `/crab/{session_id}/clarify` | POST only | `routeCrab -> handleCrabSignal` | routed through `RequireAuth`; not a control endpoint | `application/json` |
| `/crab/{session_id}/review` | POST only | `routeCrab -> handleCrabSignal` | routed through `RequireAuth`; not a control endpoint | `application/json` |

## Middleware and auth behavior

`RequireAuth` is attached to `/workflows/*`, `/planning/*`, and `/crab/*`.

The middleware only enforces control checks when all are true:

- request method is `POST`.
- path is one of:
  - `/scheduler/pause`
  - `/scheduler/resume`
  - `/scheduler/plan/activate`
  - `/scheduler/plan/clear`
  - `/dispatches/{id}/cancel`
  - `/dispatches/{id}/retry`

No registered route in `api.go` currently matches these control patterns. Operational effect: all currently exposed endpoints execute with no auth enforcement in live middleware flow.

Auth outcomes when control rules do match:

- auth disabled and `require_local_only=true` with non-local source: `403` and message `Access denied: non-local requests not allowed`.
- auth enabled with missing/invalid token: `401` and message `Unauthorized: valid token required`.
- valid token or local override: request proceeds.

All control-auth responses also carry `WWW-Authenticate: Bearer`.

## Shared response contracts

All JSON handlers return `Content-Type: application/json`.

Error responses use this shape:

```json
{"error":"..."}
```

All successful JSON responses are route-specific.

`/metrics` response is plain text with:

`Content-Type: text/plain; version=0.0.4; charset=utf-8`

## Health and monitoring endpoints

### `GET /status`

- method: any
- response shape:
  - `uptime_s` (`number`)
  - `running_count` (`number`)
- status: always `200`.

### `GET /projects`

- method: any
- response shape:
  - array of objects:
    - `name` (`string`)
    - `enabled` (`boolean`)
    - `priority` (`number`)
- status: `200`.

### `GET /projects/{project_id}`

- method: any
- empty trailing path (`/projects/`) is normalized to `/projects`.
- response shape:
  - `name` (`string`)
  - `enabled` (`boolean`)
  - `priority` (`number`)
  - `workspace` (`string`)
  - `beads_dir` (`string`)
- status:
  - `200` when found
  - `404` when project is unknown.

### `GET /health`

- method: any
- response shape:
  - `healthy` (`boolean`)
  - `events_1h` (`number`)
  - `recent_events` (`array`)
    - `type` (`string`)
    - `details` (`string`)
    - `dispatch_id` (`string`)
    - `bead_id` (`string`)
    - `time` (`string`, RFC3339)
- status:
  - `200` when no `gateway_critical` events in last hour
  - `503` when `gateway_critical` exists

### `GET /metrics`

- method: any
- status: `200`
- body format: Prometheus exposition text.
- includes counters and gauges:
  - `chum_dispatches_total`
  - `chum_dispatches_failed_total`
  - `chum_dispatches_running`
  - `chum_dispatches_running_by_stage`
  - `chum_tokens_total`
  - `chum_cost_usd_total`
  - `chum_uptime_seconds`
  - plus additional metrics for activity duration, outcomes, and safety blocks.

### `GET /recommendations`

- query params:
  - `q` (`string`, optional)
  - `hours` (`int`, default `24`, valid range `1` to `168`, invalid values -> `24`)
  - `limit` (`int`, default `20`, max `100`)
- response shape:
  - `recommendations` (`array`)
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
- status:
  - `200` always for valid method/queries
  - `405` when method is not `GET`

### `GET /dispatches/{bead_id}`

- method: any
- `bead_id` is required after `/dispatches/`; empty value returns `400`.
- response shape:
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
- status:
  - `200` on success
  - `400` when bead id is empty
  - `500` when store lookup fails

### `GET /safety/blocks`

- method: `GET` only
- response shape:
  - `total` (`number`)
  - `counts_by_type` (`map[string]int`)
  - `blocks` (`array`)
    - `scope` (`string`)
    - `block_type` (`string`)
    - `blocked_until` (`string`, RFC3339)
    - `reason` (`string`)
    - `metadata` (`object`, optional)
    - `created_at` (`string`, RFC3339)
- status:
  - `200` on success
  - `405` for non-GET
  - `500` on store errors

## Workflow endpoints

### `POST /workflows/start`

- request body (`temporal.TaskRequest`):
  - `task_id` (`string`, required)
  - `prompt` (`string`, required)
  - optional: `project`, `agent`, `reviewer`, `work_dir`, `provider`, `model`, `dod_checks`, `slow_step_threshold`, `escalation_chain`
- defaults:
  - `agent` defaults to `claude`
  - `work_dir` defaults to `/tmp/workspace`
  - `slow_step_threshold` defaults to `general.slow_step_threshold`
- response:
  - `workflow_id` (`string`)
  - `run_id` (`string`)
  - `status` (`string`, value `started`)
- status:
  - `200` success
  - `400` missing required fields
  - `405` non-`POST`
  - `500` Temporal connect/start failure

### `GET /workflows/{workflow_id}`

- response:
  - `workflow_id` (`string`)
  - `run_id` (`string`)
  - `type` (`string`)
  - `status` (`string`)
  - `start_time` (`string`, RFC3339)
  - `close_time` (`string`, RFC3339, optional)
- errors:
  - `400` for empty workflow id
  - `404` when workflow is not found
  - `500` on Temporal describe failures
  - `405` for non-`GET`

### `POST /workflows/{workflow_id}/approve`

- no request body
- response:
  - `workflow_id` (`string`)
  - `status` (`string`, value `approved`)
- errors:
  - `405` for non-`POST`
  - `500` on signal/send failure

### `POST /workflows/{workflow_id}/reject`

- no request body
- response:
  - `workflow_id` (`string`)
  - `status` (`string`, value `rejected`)
- errors:
  - `405` for non-`POST`
  - `500` on signal/send failure

## Planning endpoints

### `POST /planning/start`

- request body (`temporal.PlanningRequest`):
  - `project` (`string`, required)
  - `agent` (`string`, default `claude`)
  - `tier` (`string`, optional)
  - `work_dir` (`string`, required)
  - `slow_step_threshold` (`number`, optional)
- response:
  - `session_id` (`string`)
  - `run_id` (`string`)
  - `status` (`string`, value `grooming_backlog`)
- errors:
  - `400` required fields missing
  - `405` non-`POST`
  - `500` Temporal connect/start failure

### `GET /planning/{session_id}`

- response:
  - `session_id` (`string`)
  - `run_id` (`string`)
  - `status` (`string`)
  - `start_time` (`string`, RFC3339)
  - `close_time` (`string`, RFC3339, optional)
  - optional `note` (`string`) while session is running
- errors:
  - `400` empty session id
  - `404` session not found
  - `405` non-`GET`
  - `500` Temporal describe failure

### Planning signal endpoints

For each of:

- `POST /planning/{session_id}/select`
- `POST /planning/{session_id}/answer`
- `POST /planning/{session_id}/greenlight`

- method: `POST`
- body:
  - `value` (`string`, required)
- response:
  - `session_id` (`string`)
  - `signal` (`string`)
    - `item-selected` for `select`
    - `answer` for `answer`
    - `greenlight` for `greenlight`
  - `value` (`string`)
- errors:
  - `400` invalid JSON body
  - `405` non-`POST`
  - `500` on signal/send failure

## Crab endpoints

### `POST /crab/decompose`

- request body (`temporal.CrabDecompositionRequest`):
  - `project` (`string`, required)
  - `work_dir` (`string`, required)
  - `plan_markdown` (`string`, required)
  - optional: `plan_id`, `tier`, `parent_whale_id`
- defaults:
  - `plan_id` auto-generated as `plan-<project>-<unix_ts>` when empty
- response:
  - `session_id` (`string`)
  - `run_id` (`string`)
  - `plan_id` (`string`)
  - `status` (`string`, value `parsing`)
- errors:
  - `400` missing required fields
  - `405` non-`POST`
  - `500` Temporal connect/start failure

### `GET /crab/{session_id}`

- response:
  - `session_id` (`string`)
  - `run_id` (`string`)
  - `status` (`string`)
  - `start_time` (`string`, RFC3339)
  - `close_time` (`string`, RFC3339, optional)
  - optional `note` (`string`) while session is running
- errors:
  - `400` empty session id
  - `404` session not found
  - `405` non-`GET`
  - `500` Temporal describe failure

### Crab signal endpoints

For each of:

- `POST /crab/{session_id}/clarify`
- `POST /crab/{session_id}/review`

- method: `POST`
- body:
  - `value` (`string`, required)
- response:
  - `session_id` (`string`)
  - `signal` (`string`)
    - `crab-clarification` for `clarify`
    - `crab-review` for `review`
  - `value` (`string`)
- errors:
  - `400` invalid JSON body
  - `405` non-`POST`
  - `500` on signal/send failure

## Representative examples

### Start a workflow run

```bash
curl -X POST \
  -H "Content-Type: application/json" \
  -d '{"task_id":"task-001","project":"main","prompt":"Refactor dispatcher config","agent":"claude","work_dir":"/tmp/workspace"}' \
  http://127.0.0.1:8080/workflows/start
```

### Send planning greenlight

```bash
curl -X POST \
  -H "Content-Type: application/json" \
  -d '{"value":"approve"}' \
  http://127.0.0.1:8080/planning/session-abc/greenlight
```

### Query a workflow status

```bash
curl http://127.0.0.1:8080/workflows/session-abc
```

## Error and status matrix

| Status | Use |
| --- | --- |
| `200` | request accepted and response returned |
| `400` | malformed request payload or missing required path fields |
| `401` | control auth path rejected token |
| `403` | control local-only policy rejected remote request |
| `404` | missing project, workflow, planning session, or crab session |
| `405` | unsupported HTTP method for route |
| `500` | Temporal or store/persistent-layer failure |

## Verification

### Runtime verification commands

```bash
rg -n "handleStatus|handleProjects|handleRecommendations|routeWorkflows|routePlanning|routeCrab|RequireAuth|isControlEndpoint" internal/api/*.go
go test ./internal/api -run Endpoint -count=1
```

### Consistency checks

- All route behavior is driven by `internal/api/api.go` registrations.
- `api_test.go` should remain the canonical behavior test layer for request/response assertions.
- `docs/api/api-security.md` must stay aligned with the note that currently listed control endpoints are not registered for execution.

## Known limitations

- `/teams` is not registered in `api.go` despite legacy references in other docs.
- `/scheduler/*` and `/dispatches/{id}/cancel|retry` are expected by auth config but are not registered in this release.
- `workflows`, `planning`, and `crab` routes pass through `RequireAuth`, yet currently do not trigger auth enforcement because they are not control paths in `isControlEndpoint`.
- Some registered GET endpoints do not explicitly reject unsupported methods and will report method-specific handler errors only for subset routes.
- Dispatch path semantics for `/dispatches/{bead_id}` currently only accept any method and rely on handler path parsing.
