# CHUM API Endpoints

Last validated against `internal/api/api.go` and `internal/api` handlers on 2026-02-22.

## Scope and source of truth

This file documents only routes actually registered in `Server.Start` and backed by the handler methods in `internal/api/*.go`.

Canonical route shapes come from:

- `internal/api/api.go` (mux registration)
- `internal/api/*_handlers.go` (request/response code paths)
- `internal/api/auth.go` + `internal/api/auth_test.go` (middleware behavior)

Related references:

- [`api-security.md`](./api-security.md) — control-plane auth model for mutable routes.
- [`CHUM_LLM_INTERACTION_GUIDE.md`](./CHUM_LLM_INTERACTION_GUIDE.md) — examples of endpoint-driven operational workflows.

> Note: `/scheduler/*` and `/teams/*` routes are not currently registered in code.

## Authentication model

`Server.Start` wraps all `/workflows/*`, `/planning/*`, and `/crab/*` registrations with `RequireAuth`, but that middleware currently enforces auth only for these hard-coded control paths:

- `POST /scheduler/pause`
- `POST /scheduler/resume`
- `POST /scheduler/plan/activate`
- `POST /scheduler/plan/clear`
- `POST /dispatches/{id}/cancel`
- `POST /dispatches/{id}/retry`

Because those control paths are not registered, every currently exposed endpoint is effectively unauthenticated in practice.

Authentication modes still apply to middleware logic when a control path is added:

- `api.security.enabled=false`, `require_local_only=true`: non-local requests are rejected with `403`.
- `api.security.enabled=true`: control requests require `Authorization: Bearer <token>` and return `401` on missing/invalid token.
- Audit log format (if configured): `{"error":"...", "path":"...", "status_code":..., "duration":"..."}`.

## Error envelope

When returning an error, handlers use:

```json
{"error":"message"}
```

with content type `application/json`.

## Route matrix (implemented routes only)

| Group | Route pattern | Methods | Handler | Auth | Notes |
| --- | --- | --- | --- | --- | --- |
| Monitoring | `/status` | all methods | `handleStatus` | None | Returns service uptime and currently running dispatch count. |
| Monitoring | `/projects` | all methods | `handleProjects` | None | Returns configured project inventory. |
| Monitoring | `/projects/{project}` | all methods | `handleProjectDetail` | None | Falls back to `/projects` when `{project}` is empty. |
| Monitoring | `/health` | all methods | `handleHealth` | None | Returns `503` when gateway critical events exist in recent window. |
| Monitoring | `/metrics` | all methods | `handleMetrics` | None | Prometheus text exposition (`text/plain; version=0.0.4; charset=utf-8`). |
| Monitoring | `/recommendations` | all methods | `handleRecommendations` | None | `GET` only; other methods return `405`. |
| Dispatch history | `/dispatches/{bead_id}` | all methods | `handleDispatchDetail` | None | `bead_id` is path suffix after `/dispatches/`. |
| Safety | `/safety/blocks` | all methods | `handleSafetyBlocks` | None | `GET` only; other methods return `405`. |
| Workflow control | `/workflows/start` | `POST` | `handleWorkflowStart` | Wrapped | Enforces required fields in JSON payload. |
| Workflow control | `/workflows/{workflow_id}` | `GET`, `POST` for `approve/reject` | `routeWorkflows` | Wrapped | `POST` routes for `/approve` and `/reject`, default `GET` status. |
| Planning control | `/planning/start` | `POST` | `handlePlanningStart` | Wrapped | Starts planning ceremony workflow. |
| Planning control | `/planning/{session_id}` | `GET`, `POST` for sub-actions | `routePlanning` | Wrapped | `POST` for `/select`, `/answer`, `/greenlight`, default `GET` status. |
| Crab control | `/crab/decompose` | `POST` | `handleCrabDecompose` | Wrapped | Starts crab decomposition workflow. |
| Crab control | `/crab/{session_id}` | `GET`, `POST` for sub-actions | `routeCrab` | Wrapped | `POST` for `/clarify` and `/review`, default `GET` status. |

## GET endpoints and payloads

### `GET /status`

- Handler: `handleStatus`
- Method behavior: no method gating.
- Response: `200`
- Response body:
  - `uptime_s` (`number`)
  - `running_count` (`number`)

### `GET /projects`

- Handler: `handleProjects`
- Method behavior: no method gating.
- Response: `200`
- Response body: array of project objects
  - `name` (`string`)
  - `enabled` (`boolean`)
  - `priority` (`number`)

### `GET /projects/{project}`

- Handler: `handleProjectDetail`
- Method behavior: no method gating.
- Path behavior:
  - `/projects/` with empty `{project}` is forwarded to `/projects`.
- Responses:
  - `200` project object (`name`, `enabled`, `priority`, `workspace`, `beads_dir`)
  - `404` with `{"error":"project not found"}` when project is unknown

### `GET /health`

- Handler: `handleHealth`
- Method behavior: no method gating.
- Status behavior:
  - `200` when no recent `gateway_critical` events are found.
  - `503` when at least one recent critical event exists.
- Response body:
  - `healthy` (`boolean`)
  - `events_1h` (`number`)
  - `recent_events` (array)
    - `type` (`string`)
    - `details` (`string`)
    - `dispatch_id` (`string`)
    - `bead_id` (`string`)
    - `time` (`string`, RFC3339)

### `GET /metrics`

- Handler: `handleMetrics`
- Method behavior: no method gating.
- Response: `200`
- Response content type: `text/plain; version=0.0.4; charset=utf-8`
- Body: Prometheus exposition metrics for dispatches, cost, token usage, DoD results, safety blocks, uptime.

### `GET /recommendations`

- Handler: `handleRecommendations`
- Method behavior: returns `405` for non-`GET` methods.
- Query parameters:
  - `q` (`string`, optional)
  - `hours` (`int`, default `24`, clamped `1..168`, invalid values -> `24`)
  - `limit` (`int`, default `20`, max `100`, invalid values ignored)
- Response: `200`
- Response body:
  - `recommendations` (array)
    - `id` (`number`)
    - `bead_id` (`string`)
    - `project` (`string`)
    - `category` (`string`)
    - `summary` (`string`)
    - `detail` (`string`)
    - `files` (array of `string`)
    - `labels` (array of `string`)
    - `created_at` (`string`, `time.RFC3339`)
  - `hours` (`number`)
  - `count` (`number`)
  - `generated_at` (`string`, RFC3339)

### `GET /dispatches/{bead_id}`

- Handler: `handleDispatchDetail`
- Method behavior: no method gating.
- Path requirement: `{bead_id}` must be non-empty.
- Response:
  - `400` if `{bead_id}` empty
  - `500` if store query fails
  - `200` success
- Response body:
  - `bead_id` (`string`)
  - `dispatches` (array)
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

### `GET /safety/blocks`

- Handler: `handleSafetyBlocks`
- Method behavior: returns `405` for non-`GET` methods.
- Response: `200`
- Response body:
  - `total` (`number`)
  - `counts_by_type` (map of `string` → `number`)
  - `blocks` (array)
    - `scope` (`string`)
    - `block_type` (`string`)
    - `blocked_until` (`string`, RFC3339)
    - `reason` (`string`)
    - `metadata` (object, optional)
    - `created_at` (`string`, RFC3339)

## workflow and planning controls

### `POST /workflows/start`

- Handler: `handleWorkflowStart`
- Request body (`temporal.TaskRequest`):
  - `task_id` (`string`, required)
  - `prompt` (`string`, required)
  - `project` (`string`, optional in handler validation)
  - `agent` (`string`, default `"claude"`)
  - `reviewer` (`string`, optional passthrough)
  - `work_dir` (`string`, default `"/tmp/workspace"`)
  - `provider` (`string`, optional)
  - `dod_checks` (array, optional)
  - `slow_step_threshold` (`number`, `time.Duration` nanoseconds, default from config)
- Method: `POST` only.
- Responses:
  - `200` with `{workflow_id, run_id, status:"started"}`
  - `400` invalid JSON or missing required fields
  - `500` temporal client/dispatch error

### `POST /workflows/{workflow_id}/approve`

- Handler: `handleWorkflowApprove` via `routeWorkflows`
- Request body: none
- Method: `POST` only.
- Response:
  - `200` `{workflow_id, status:"approved"}`
  - `405` for non-`POST`
  - `500` temporal signal/connect error
  - `404` only if workflow was unavailable at signal time

### `POST /workflows/{workflow_id}/reject`

- Handler: `handleWorkflowReject` via `routeWorkflows`
- Request body: none
- Method: `POST` only.
- Response:
  - `200` `{workflow_id, status:"rejected"}`
  - `405` for non-`POST`
  - `500` temporal signal/connect error

### `GET /workflows/{workflow_id}`

- Handler: `handleWorkflowStatus`
- Method: `GET` only.
- Response:
  - `200` with:
    - `workflow_id` (`string`)
    - `run_id` (`string`)
    - `type` (`string`)
    - `status` (`string`)
    - `start_time` (`string`, RFC3339)
    - `close_time` (`string`, RFC3339, optional)
  - `400` missing `{workflow_id}`
  - `404` unknown workflow
  - `500` temporal client connect error

### `POST /planning/start`

- Handler: `handlePlanningStart`
- Request body (`temporal.PlanningRequest`):
  - `project` (`string`, required)
  - `agent` (`string`, default `"claude"`)
  - `tier` (`string`, optional passthrough)
  - `work_dir` (`string`, required)
  - `slow_step_threshold` (`number`, `time.Duration` nanoseconds)
- Method: `POST` only.
- Response:
  - `200` `{session_id, run_id, status:"grooming_backlog"}`
  - `400` invalid JSON or missing required fields
  - `500` temporal client/connect error

### `POST /planning/{session_id}/select`

- Handler: `handlePlanningSignal`
- Request body: `{"value":"..."}`
- Method: `POST` only.
- Signal: `item-selected`
- Response:
  - `200` `{session_id, signal, value}`
  - `400` invalid JSON payload
  - `405` non-`POST`
  - `500` temporal signal error
  - `404` workflow not found

### `POST /planning/{session_id}/answer`

- Handler: `handlePlanningSignal`
- Request body: `{"value":"..."}`
- Method: `POST` only.
- Signal: `answer`
- Response behavior same as `/select`.

### `POST /planning/{session_id}/greenlight`

- Handler: `handlePlanningSignal`
- Request body: `{"value":"..."}`
- Method: `POST` only.
- Signal: `greenlight`
- Response behavior same as `/select`.

### `GET /planning/{session_id}`

- Handler: `handlePlanningStatus`
- Method: `GET` only.
- Response:
  - `200`:
    - `session_id` (`string`)
    - `run_id` (`string`)
    - `status` (`string`)
    - `start_time` (`string`, RFC3339)
    - `close_time` (`string`, RFC3339, optional)
    - `note` (`string`, optional)
  - `400` missing `{session_id}`
  - `405` non-`GET`
  - `404` workflow not found
  - `500` temporal connect error

### `POST /crab/decompose`

- Handler: `handleCrabDecompose`
- Request body (`temporal.CrabDecompositionRequest`):
  - `project` (`string`, required)
  - `plan_markdown` (`string`, required)
  - `work_dir` (`string`, required)
  - `plan_id` (`string`, default `"plan-{project}-{unix}"`)
  - `tier` (`string`, optional)
  - `parent_whale_id` (`string`, optional)
- Method: `POST` only.
- Response:
  - `200` `{session_id, run_id, plan_id, status:"parsing"}`
  - `400` missing required fields or invalid JSON
  - `500` temporal client/connect error

### `POST /crab/{session_id}/clarify`

- Handler: `handleCrabSignal`
- Request body: `{"value":"..."}`
- Method: `POST` only.
- Signal: `crab-clarification`
- Response:
  - `200` `{session_id, signal, value}`
  - `400` invalid JSON payload
  - `405` non-`POST`
  - `500` temporal signal error
  - `404` workflow not found

### `POST /crab/{session_id}/review`

- Handler: `handleCrabSignal`
- Request body: `{"value":"..."}`
- Method: `POST` only.
- Signal: `crab-review`
- Response behavior same as `/clarify`.

### `GET /crab/{session_id}`

- Handler: `handleCrabStatus`
- Method: `GET` only.
- Response:
  - `200`:
    - `session_id` (`string`)
    - `run_id` (`string`)
    - `status` (`string`)
    - `start_time` (`string`, RFC3339)
    - `note` (`string`, optional)
    - `close_time` (`string`, RFC3339, optional)
  - `400` missing `{session_id}`
  - `405` non-`GET`
  - `404` workflow not found
  - `500` temporal connect error

## Canonical status/error patterns

- `200` on successful JSON responses.
- `400` malformed JSON or missing required request/path fields.
- `405` when a handler enforces GET/POST but method differs.
- `404` when workflow/session/project/resource is unknown.
- `500` on temporal/store/dispatch start or signal failures.
- `503` only for `/health` when recent critical gateway events are present.
- `401`/`403` documented in auth middleware but not reached by current registered routes.

## Known gaps

- No route currently serves scheduler control or team endpoints.
- `/scheduler/*` and `/dispatches/{id}/cancel|retry` are documented in auth tests and comments as control paths but are currently unimplemented in mux registration.
- Control-plane routes should be added in `internal/api/api.go` with matching tests before they are treated as active.
- `RequireAuth` is currently a global shell around control path matching; endpoint-level auth for non-control writes is therefore not configured yet.

## Related docs

- [docs/api/api-security.md](./api-security.md) — authentication/audit model and operational examples.
- [docs/api/CHUM_LLM_INTERACTION_GUIDE.md](./CHUM_LLM_INTERACTION_GUIDE.md) — operator playbooks against active endpoints.
- [docs/architecture/CONFIG.md](../architecture/CONFIG.md) — API bind/security settings referenced by auth behavior.
