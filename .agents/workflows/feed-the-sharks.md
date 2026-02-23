---
description: How to submit tasks/morsels to the CHUM pipeline for shark consumption
---

# Feeding the Sharks — CHUM Task API

Use these API endpoints to create, list, and inspect tasks in the CHUM pipeline. Tasks created via this API are automatically reviewed by a Crab workflow for sizing and dependency validation.

## API Base

```
http://127.0.0.1:8900
```

## Endpoints

### Create a Task (POST /tasks)

Submit a new morsel to the CHUM queue. A Crab workflow automatically fires to review sizing and dependencies.

```bash
curl -X POST http://127.0.0.1:8900/tasks \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Fix login button alignment on mobile",
    "description": "The login button overflows on screens < 375px wide.",
    "project": "golf-directory",
    "priority": 1,
    "type": "bugfix",
    "labels": ["ux", "mobile"],
    "estimate_minutes": 15,
    "acceptance_criteria": "Button renders correctly on 320px-wide viewport",
    "depends_on": []
  }'
```

**Required fields:**
- `title` — short, descriptive task title
- `project` — must match an enabled project in `chum.toml` (e.g. `golf-directory`, `chum`)

**Optional fields:**
| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `description` | string | `""` | Detailed problem/solution description |
| `priority` | int | `0` | Lower = higher priority. P0 = critical |
| `status` | string | `"ready"` | Usually `ready`. Use `open` for ungroomed |
| `type` | string | `"task"` | One of: `task`, `bugfix`, `morsel`, `epic` |
| `labels` | string[] | `[]` | Tags for categorization |
| `estimate_minutes` | int | `0` | Estimated effort |
| `acceptance_criteria` | string | `""` | Definition of done |
| `design` | string | `""` | Implementation hints |
| `notes` | string | `""` | Additional context |
| `depends_on` | string[] | `[]` | Task IDs this depends on |

**Response (201):**
```json
{
  "id": "golf-directory-42",
  "status": "ready",
  "project": "golf-directory"
}
```

### List Tasks (GET /tasks?project=...)

```bash
# All tasks in a project
curl "http://127.0.0.1:8900/tasks?project=golf-directory"

# Filter by status
curl "http://127.0.0.1:8900/tasks?project=golf-directory&status=ready"

# Multiple statuses
curl "http://127.0.0.1:8900/tasks?project=golf-directory&status=ready,open"
```

### Get Single Task (GET /tasks/{id})

```bash
curl http://127.0.0.1:8900/tasks/golf-directory-42
```

## Priority Guide

| Priority | Meaning | Dispatch Order |
|----------|---------|----------------|
| 0 | Critical (P0) | First |
| 1 | High (P1) | Second |
| 2 | Medium (P2) | Third |
| 3 | Low (P3) | Last |

## What Happens After Submission

1. **Task created** in the DAG database with specified status
2. **Crab review** fires asynchronously to validate sizing and dependencies
3. **Dispatcher** picks up `ready` tasks on the next 2-minute tick
4. **Shark** executes the task using the configured AI agent
5. **DoD check** verifies acceptance criteria
6. **Task closed** on successful completion

## Available Projects

Check which projects are enabled:
```bash
curl http://127.0.0.1:8900/projects
```
