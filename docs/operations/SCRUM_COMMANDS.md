# Scrum Master Commands

> CHUM exposes a Matrix-based command interface for real-time project control. Commands are received via the Matrix poller and routed through `internal/matrix/poller.go`.

---

## Access Control

Only senders listed in the allowed command senders configuration can execute commands. Unauthorized senders receive a permission-denied response with usage guidance.

---

## Command Reference

### `status`

Returns a project summary: running dispatches, recent completions, open bead count, and active plan state.

```
> status

📊 chum — 3 running, 12 completed (24h), 47 open beads
Plan: plan-2026-02-20-main (active, approved by operator)
Scheduler: RUNNING | Tick: 60s | Capacity: 8/40
```

---

### `priority <bead-id> <p0|p1|p2|p3|p4>`

Updates a bead's priority. Lower numbers = higher priority.

```
> priority chum-o4ni p1

✅ Updated chum-o4ni priority to p1
```

| Priority | Meaning |
|----------|---------|
| `p0` | Critical — blocks everything, dispatched immediately |
| `p1` | High — dispatched ahead of normal work |
| `p2` | Normal — standard scheduling |
| `p3` | Low — backfill when capacity allows |
| `p4` | Deferred — will not be auto-dispatched |

**Validation:** Priority must be `p0` through `p4` (case-insensitive). Invalid values return an error with the expected format.

---

### `cancel <dispatch-id>`

Cancels a running dispatch by its numeric ID. This kills the agent session and marks the dispatch as `cancelled`.

```
> cancel 1234

✅ Cancelled dispatch 1234 (bead: chum-bvnv, agent: claude)
```

**Validation:** Dispatch ID must be a positive integer. Attempting to cancel an already-completed dispatch returns a descriptive error.

---

### `create task "<title>" "<description>"`

Creates a new task bead with default priority (p2).

```
> create task "Fix login redirect loop" "The /auth/callback endpoint redirects back to /login when session cookie is set but expired"

✅ Created new task chum-x8k2: Fix login redirect loop
```

**Validation:** Both title and description must be quoted. Missing quotes return usage guidance with the expected format.

---

## Error Handling

| Condition | Response |
|-----------|----------|
| Unknown command | Usage guidance listing all supported commands |
| Malformed arguments | Expected format with examples |
| Missing permissions | Polite denial with usage guidance |
| Backend failure | `Command failed: <error message>` |

---

## Architecture

```
Matrix Room → Poller (30s interval) → parseScrumCommand() → handleScrumCommand()
                                                                    ↓
                                                             runScrumCommand()
                                                                    ↓
                                                            sendScrumResponse()
                                                                    ↓
                                                              Matrix Room
```

The poller filters messages by `bot_user` mention, parses the command verb and arguments, validates permissions, executes the command against the beads/store backend, and sends the response back to the originating Matrix room.

---

## Configuration

```toml
[matrix]
enabled       = true
poll_interval = "30s"
bot_user      = "@chum:matrix.org"
read_limit    = 25
```

See [CONFIG.md](../architecture/CONFIG.md) for full Matrix and reporter configuration.
