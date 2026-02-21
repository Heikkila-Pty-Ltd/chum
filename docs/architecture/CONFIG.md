# Configuration Reference

> Complete reference for `chum.toml`. Every field maps to a Go struct in `internal/config/config.go`.

---

## Configuration File Structure

CHUM uses TOML format. Durations use Go notation (`60s`, `2m`, `24h`).

```toml
# Minimal working configuration
[general]
state_db = "~/.chum/chum.db"
tick_interval = "60s"

[projects.my-project]
enabled = true
beads_dir = ".beads"
workspace = "/home/user/my-project"

[providers.claude]
tier = "balanced"
cli = "claude"
model = "claude-sonnet-4-20250514"

[tiers]
fast     = ["claude"]
balanced = ["claude"]
premium  = ["claude"]
```

---

## `[general]` — Scheduler Core

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `tick_interval` | Duration | `60s` | Scheduler loop frequency |
| `max_per_tick` | int | `3` | Maximum dispatches per scheduler tick |
| `stuck_timeout` | Duration | `30m` | Terminate running workflows older than this; set `0s` to disable |
| `max_retries` | int | `3` | Global retry limit for failed dispatches |
| `retry_backoff_base` | Duration | `5m` | Base delay for exponential backoff |
| `retry_max_delay` | Duration | `30m` | Maximum backoff delay cap |
| `dispatch_cooldown` | Duration | `5m` | Minimum time between re-dispatching the same bead |
| `log_level` | string | `info` | Logging verbosity (`debug`, `info`, `warn`, `error`) |
| `state_db` | string | — | Path to SQLite state database |
| `lock_file` | string | — | Filesystem lock to prevent duplicate schedulers |
| `max_concurrent_coders` | int | `25` | Hard cap on concurrent coder agents |
| `max_concurrent_reviewers` | int | `10` | Hard cap on concurrent reviewer agents |
| `max_concurrent_total` | int | `40` | Hard cap on total concurrent agents |

### Scheduler janitor (stale workflow cleanup)

CHUM runs a janitor pass at the start of every scheduler tick to reclaim concurrency lanes from stale `CHUMAgentWorkflow` executions before normal dispatch.

Cleanup rules:

- `bead_closed` — terminate when bead status is `closed`.
- `bead_deferred` — terminate when bead status is `deferred`.
- `stuck_timeout` — terminate when execution `start_time` is older than `stuck_timeout`. Applies to both known-open and unknown workflows when all project inventory succeeded. Set `stuck_timeout = "0s"` to disable timeout-based termination entirely.

Partial failure handling:

When some enabled projects fail to list beads, the janitor operates in partial-data mode. Unknown workflows (not found in any successful project listing) are conservatively retained:

- no status-based cleanup (`bead_closed`/`bead_deferred`) is applied;
- no timeout cleanup is applied.

If **all** projects fail to list beads, the janitor aborts and returns all running workflows unchanged. This prevents unsafe termination when bead inventory is incomplete.

Project lookup is performed deterministically (sorted enabled projects), and bead statuses are normalized (`open`, `closed`, `deferred`) before classification.

Per-termination log fields:

- `workflow_id`
- `run_id`
- `bead_id`
- `reason` (`bead_closed`, `bead_deferred`, `stuck_timeout`)
- `age` (present for timeout decisions)

Manual verification:

1. Create stale workflow fixtures via test-state manipulation.
2. Ensure a matching project has a running open workflow in Temporal with a known `workflow_id`, `run_id`, and `start_time`.
3. Run one scheduler tick.
4. Confirm a single info log line includes the fields above and that the stale workflow is no longer counted as an occupied slot.

### `[general.retry_policy]` — Default Retry Behaviour

```toml
[general.retry_policy]
max_retries    = 3
initial_delay  = "5m"
backoff_factor = 2.0
max_delay      = "30m"
escalate_after = 2   # escalate to higher tier after N failures
```

### `[general.retry_tiers]` — Per-Tier Retry Overrides

Override retry behaviour by tier name (case-insensitive). Values merge on top of the global policy.

```toml
[general.retry_tiers.fast]
max_retries    = 2
initial_delay  = "2m"
escalate_after = 1

[general.retry_tiers.premium]
max_retries    = 4
initial_delay  = "10m"
```

---

## `[projects.<name>]` — Project Configuration

Each project is a top-level key under `[projects]`.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable this project for scheduling |
| `beads_dir` | string | — | Path to beads directory (relative to workspace) |
| `workspace` | string | — | Absolute path to project root |
| `priority` | int | `0` | Scheduling priority (lower = higher priority) |
| `matrix_room` | string | — | Matrix room for project notifications |
| `base_branch` | string | `main` | Branch to create features from |
| `branch_prefix` | string | `feat/` | Prefix for auto-created feature branches |
| `use_branches` | bool | `false` | Enable branch-per-bead workflow |
| `merge_method` | string | `squash` | PR merge method (`squash`, `merge`, `rebase`) |
| `post_merge_checks` | []string | — | Commands to run after PR merge |
| `auto_revert_on_failure` | bool | `true` | Auto-revert merge if post-merge checks fail |

### Sprint Planning (Optional)

Sprint planning is opt-in. Without these fields, the project runs in continuous mode.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `sprint_planning_day` | string | — | Day of week for planning ceremony (`Monday`, etc.) |
| `sprint_planning_time` | string | — | Time in 24h format (`09:00`) |
| `sprint_capacity` | int | — | Max tasks per sprint (0–1000) |
| `backlog_threshold` | int | — | Minimum backlog size to maintain (0–500) |

### Definition of Done

```toml
[projects.my-project.dod]
checks             = ["go build ./...", "go test ./...", "go vet ./..."]
coverage_min       = 0          # optional coverage floor (%)
require_estimate   = false      # bead must have estimated_minutes > 0
require_acceptance = false      # bead must have acceptance criteria
```

### Per-Project Retry Policy

Overrides the global retry policy for this project only.

```toml
[projects.my-project.retry_policy]
max_retries    = 5
initial_delay  = "10m"
backoff_factor = 3.0
max_delay      = "60m"
escalate_after = 3
```

---

## `[providers.<name>]` — LLM Providers

Map provider names to CLI tools and cost metadata.

```toml
[providers.claude]
tier               = "balanced"
cli                = "claude"
model              = "claude-sonnet-4-20250514"
authed             = true
cost_input_per_mtok  = 3.0     # USD per million input tokens
cost_output_per_mtok = 15.0    # USD per million output tokens

[providers.codex]
tier               = "fast"
cli                = "codex"
model              = "codex-mini-latest"
authed             = true
cost_input_per_mtok  = 1.5
cost_output_per_mtok = 6.0

[providers.opus]
tier               = "premium"
cli                = "claude"
model              = "claude-opus-4-6"
authed             = true
cost_input_per_mtok  = 15.0
cost_output_per_mtok = 75.0
```

| Field | Type | Description |
|-------|------|-------------|
| `tier` | string | Tier classification (`fast`, `balanced`, `premium`) |
| `cli` | string | CLI command to invoke (`claude`, `codex`) |
| `model` | string | Model name passed via `--model` flag |
| `authed` | bool | Whether this provider requires authentication |
| `cost_input_per_mtok` | float | Cost per million input tokens (USD) |
| `cost_output_per_mtok` | float | Cost per million output tokens (USD) |

---

## `[tiers]` — Tier Routing

Map tier names to ordered lists of provider names. The scheduler picks the first available provider in each tier.

```toml
[tiers]
fast     = ["codex", "claude"]         # cheapest, fastest
balanced = ["claude"]                   # default for most work
premium  = ["opus"]                     # complex tasks, review, planning
```

All provider names must exist in `[providers]`. Validation fails if a tier references an unknown provider.

---

## `[dispatch]` — Dispatch Engine

### `[dispatch.cli.<name>]` — CLI Configuration

How CHUM invokes each CLI tool.

```toml
[dispatch.cli.claude]
cmd            = "claude"
prompt_mode    = "stdin"              # "stdin", "file", "arg"
args           = []
model_flag     = "--model"
approval_flags = ["--dangerously-skip-permissions"]

[dispatch.cli.codex]
cmd            = "codex"
prompt_mode    = "arg"
args           = []
model_flag     = "--model"
approval_flags = ["--full-auto"]
```

### `[dispatch.routing]` — Backend Selection

```toml
[dispatch.routing]
fast_backend     = "headless_cli"     # "headless_cli" or "tmux"
balanced_backend = "tmux"
premium_backend  = "tmux"
comms_backend    = "headless_cli"     # for Matrix/comms tasks
retry_backend    = "tmux"
```

### `[dispatch.timeouts]` — Per-Tier Execution Limits

```toml
[dispatch.timeouts]
fast     = "15m"
balanced = "45m"
premium  = "120m"
```

### `[dispatch.git]` — Branch Management

```toml
[dispatch.git]
branch_prefix              = "chum/"     # prefix for auto-created branches
branch_cleanup_days        = 7             # delete merged branches older than N days
merge_strategy             = "squash"      # "merge", "squash", "rebase"
max_concurrent_per_project = 3             # max parallel branches per project
```

### `[dispatch.tmux]` — Tmux Session Management

```toml
[dispatch.tmux]
history_limit  = 50000                     # scrollback buffer per session
session_prefix = "chum-"                 # prefix for tmux session names
```

### `[dispatch.cost_control]` — Cost and Churn Guards

```toml
[dispatch.cost_control]
enabled                      = true
spark_first                  = true        # prefer fast tier for first attempt
retry_escalation_attempt     = 2           # escalate tier after N retries
complexity_escalation_minutes = 120        # escalate if estimate > N minutes
risky_review_labels          = ["risk:high", "security", "migration", "breaking-change", "database"]

# Budget enforcement
daily_cost_cap_usd           = 0           # 0 = unlimited
per_bead_cost_cap_usd        = 0           # 0 = unlimited
per_bead_stage_attempt_limit = 0           # max attempts per stage per bead
stage_attempt_window         = "6h"        # window for attempt counting
stage_cooldown               = "45m"       # cooldown between stage retries
force_spark_at_weekly_usage_pct = 0        # force fast tier above this weekly budget %

# Churn detection — auto-pause on runaway failures
pause_on_churn               = false
churn_pause_window           = "60m"       # sliding window for churn detection
churn_pause_failure_threshold = 12         # consecutive failures before pause
churn_pause_total_threshold  = 24          # total dispatches before pause

# Token waste detection
pause_on_token_waste         = false
token_waste_window           = "24h"
```

### Dispatch Logging

```toml
[dispatch]
log_dir            = "/var/log/chum/dispatches"   # dispatch log directory
log_retention_days = 30                              # auto-purge after N days
```

---

## `[cadence]` — Sprint Cadence

Global sprint cadence shared across all projects. Project-level `sprint_planning_day`/`sprint_planning_time` override per-project scheduling.

```toml
[cadence]
sprint_length     = "1w"         # "1w" or "2w"
sprint_start_day  = "Monday"     # day of week
sprint_start_time = "09:00"      # 24h HH:MM
timezone          = "UTC"        # IANA timezone
```

---

## `[rate_limits]` — Global Rate Limiting

```toml
[rate_limits]
window_5h_cap       = 20        # max dispatches in any 5-hour window
weekly_cap           = 200       # max dispatches per week
weekly_headroom_pct  = 80        # throttle above this % of weekly cap

[rate_limits.budget]
chum      = 60                 # project gets 60% of weekly budget
other-project = 40               # project gets 40% of weekly budget
```

Budget percentages are advisory — the scheduler uses them to prioritize projects when approaching rate limits.

---

## `[health]` — Health Monitoring

```toml
[health]
check_interval           = "5m"
gateway_unit             = "openclaw-gateway.service"   # systemd unit to monitor
gateway_user_service     = false                         # true = use `systemctl --user`
concurrency_warning_pct  = 0.80                          # alert at 80% capacity
concurrency_critical_pct = 0.95                          # critical at 95% capacity
```

---

## `[learner]` — CHUM Learner Configuration

```toml
[learner]
enabled          = true
analysis_window  = "48h"         # look back this far for lessons
cycle_interval   = "6h"          # minimum time between learner cycles
include_in_digest = false        # include learner stats in daily digest
```

---

## `[matrix]` — Matrix Polling (Inbound)

```toml
[matrix]
enabled       = true
poll_interval = "30s"            # polling frequency for new messages
bot_user      = "@chum:matrix.org"   # bot's Matrix user ID
read_limit    = 25               # max messages to read per poll
```

---

## `[reporter]` — Outbound Notifications

```toml
[reporter]
channel           = "matrix"           # notification channel
agent_id          = "main"             # agent identifier for dispatch-based reporting
matrix_bot_account = "hg-reporter-scrum"  # OpenClaw Matrix account for direct notifications
default_room      = "#chum-coordination"   # fallback room when project has no matrix_room
daily_digest_time = "09:00"            # time for daily digest
weekly_retro_day  = "Monday"           # day for weekly retrospective
```

---

## `[chief]` — Chief Scrum Master

```toml
[chief]
enabled              = true
matrix_room          = "#chum-coordination"
model                = "claude-opus-4-6"               # defaults to premium
agent_id             = "chum-chief-scrum"
require_approved_plan = true                            # block dispatch without active plan
```

When `require_approved_plan = true`, implementations are gated behind the plan approval API:

```bash
# Activate a plan
curl -X POST http://localhost:8900/scheduler/plan/activate \
  -H "Content-Type: application/json" \
  -d '{"plan_id": "plan-2026-02-18-main", "approved_by": "operator"}'

# Clear the plan gate
curl -X POST http://localhost:8900/scheduler/plan/clear
```

---

## `[api]` — HTTP API Server

```toml
[api]
bind = "127.0.0.1:8900"

[api.security]
enabled            = false
allowed_tokens     = []
require_local_only = true        # auto-set when binding to non-local with auth disabled
audit_log          = ""
```

See [api-security.md](../api/api-security.md) for full security documentation.

---

## `[workflows]` — Stage-Based Workflows

Define multi-stage execution pipelines triggered by bead labels or types.

```toml
[workflows.security-review]
match_labels = ["security", "risk:high"]
match_types  = ["task"]

[[workflows.security-review.stages]]
name = "implement"
role = "coder"

[[workflows.security-review.stages]]
name = "security-review"
role = "reviewer"

[[workflows.security-review.stages]]
name = "harden"
role = "coder"
```

---

## Complete Production Example

```toml
[general]
state_db               = "~/.chum/chum.db"
lock_file              = "~/.chum/chum.lock"
tick_interval          = "60s"
max_per_tick           = 3
stuck_timeout          = "30m"
max_retries            = 3
dispatch_cooldown      = "5m"
log_level              = "info"
max_concurrent_coders  = 25
max_concurrent_reviewers = 10
max_concurrent_total   = 40

[general.retry_policy]
max_retries    = 3
initial_delay  = "5m"
backoff_factor = 2.0
max_delay      = "30m"
escalate_after = 2

[cadence]
sprint_length     = "1w"
sprint_start_day  = "Monday"
sprint_start_time = "09:00"
timezone          = "Australia/Brisbane"

[projects.chum]
enabled       = true
beads_dir     = ".beads"
workspace     = "/home/user/projects/chum"
priority      = 1
matrix_room   = "#chum-dev"
base_branch   = "master"
use_branches  = true
merge_method  = "squash"

[projects.chum.dod]
checks = ["go build ./...", "go test ./...", "go vet ./..."]

[rate_limits]
window_5h_cap      = 20
weekly_cap          = 200
weekly_headroom_pct = 80

[providers.claude]
tier               = "balanced"
cli                = "claude"
model              = "claude-sonnet-4-20250514"
authed             = true
cost_input_per_mtok  = 3.0
cost_output_per_mtok = 15.0

[providers.codex]
tier               = "fast"
cli                = "codex"
model              = "codex-mini-latest"
authed             = true
cost_input_per_mtok  = 1.5
cost_output_per_mtok = 6.0

[providers.opus]
tier               = "premium"
cli                = "claude"
model              = "claude-opus-4-6"
authed             = true
cost_input_per_mtok  = 15.0
cost_output_per_mtok = 75.0

[tiers]
fast     = ["codex"]
balanced = ["claude"]
premium  = ["opus"]

[dispatch.routing]
fast_backend     = "headless_cli"
balanced_backend = "tmux"
premium_backend  = "tmux"
comms_backend    = "headless_cli"
retry_backend    = "tmux"

[dispatch.timeouts]
fast     = "15m"
balanced = "45m"
premium  = "120m"

[dispatch.git]
branch_prefix              = "chum/"
branch_cleanup_days        = 7
merge_strategy             = "squash"
max_concurrent_per_project = 3

[dispatch.tmux]
history_limit  = 50000
session_prefix = "chum-"

[dispatch.cost_control]
enabled                  = true
spark_first              = true
daily_cost_cap_usd       = 50.0
per_bead_cost_cap_usd    = 10.0
pause_on_churn           = true
churn_pause_window       = "60m"
churn_pause_failure_threshold = 12

[dispatch.cli.claude]
cmd            = "claude"
prompt_mode    = "stdin"
model_flag     = "--model"
approval_flags = ["--dangerously-skip-permissions"]

[dispatch.cli.codex]
cmd            = "codex"
prompt_mode    = "arg"
model_flag     = "--model"
approval_flags = ["--full-auto"]

[api]
bind = "127.0.0.1:8900"

[api.security]
enabled = false

[health]
check_interval           = "5m"
gateway_unit             = "openclaw-gateway.service"
concurrency_warning_pct  = 0.80
concurrency_critical_pct = 0.95

[learner]
enabled         = true
analysis_window = "48h"
cycle_interval  = "6h"

[matrix]
enabled       = true
poll_interval = "30s"
bot_user      = "@chum:matrix.org"
read_limit    = 25

[reporter]
channel           = "matrix"
agent_id          = "main"
default_room      = "#chum-coordination"
daily_digest_time = "09:00"
weekly_retro_day  = "Monday"

[chief]
enabled              = true
matrix_room          = "#chum-coordination"
require_approved_plan = true
```
