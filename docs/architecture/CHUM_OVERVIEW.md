# CHUM Design Rationale

> _Why this architecture? Why these trade-offs? This doc explains the reasoning behind every major decision in CHUM._

---

## Why CHUM Exists

Most AI coding workflows are manual: pick a task, prompt an agent, review the output, commit, repeat. This works for one person on one project. It breaks when you want:

- **Continuous execution** across multiple projects overnight
- **Deterministic quality gates** that aren't "the LLM said it's fine"
- **Learning from mistakes** so the same bug category doesn't burn tokens twice
- **Fault tolerance** when an agent hangs, crashes, or hallucinates `rm -rf /`

CHUM is the orchestration layer that sits between your task backlog (Morsels) and your agent runtimes (Claude, Codex, etc.) and makes all of the above automatic.

---

## Core Design Decisions

### ADR-001: Temporal over In-Process Scheduler

**Context:** The original CHUM v0 used a tick-based in-process scheduler with SQLite state. Every 60 seconds, it would scan morsels, dispatch agents, and reconcile. This worked but had a fatal flaw: if the process crashed mid-dispatch, state was lost.

**Decision:** Migrate the execution engine to Temporal workflows.

**Rationale:**
- **Durability.** If CHUM dies mid-workflow, Temporal replays from exactly where it left off. No state reconstruction needed.
- **Visibility.** Temporal UI shows every workflow execution, activity attempt, and failure — free observability.
- **Fan-out.** Temporal makes it trivial to run N parallel child workflows (future: Monte Carlo execution).
- **Signals.** The human gate is a Temporal signal — clean, built-in, no polling.
- **Timers and cron.** StrategicGroom runs on `CronSchedule: "0 5 * * *"` — Temporal handles it natively.

**Rejected alternatives:**
- *In-process scheduler with WAL recovery:* Fragile, requires custom replay logic.
- *Celery / Bull / Sidekiq:* Python/Node/Ruby ecosystems. We're Go-native.
- *AWS Step Functions:* Vendor lock-in, latency, cost at scale.

---

### ADR-002: Morsels over Jira/Linear/GitHub Issues

**Context:** We needed a dependency-aware task graph. Commercial tools (Jira, Linear) are cloud-hosted and API-rate-limited. GitHub Issues lack first-class dependency edges.

**Decision:** Use Morsels — a Git-backed, local-first issue tracker with dependency DAG support.

**Rationale:**
- **Local-first.** No network calls to read the backlog. Zero-latency task queries.
- **Git-backed.** Issues are JSONL files in the repo. Full version history via `git log`. No vendor lock.
- **Dependency DAG.** `bd` natively supports `blocks:`, `parent-child:`, `discovered-from:` edges. Cross-project deps work out of the box.
- **Programmable.** The `morsels` Go package lets CHUM query, create, update, and mutate morsels programmatically — no REST API, no rate limits, no OAuth tokens.

**Trade-offs accepted:**
- No web UI for non-technical stakeholders (acceptable: CHUM is a developer tool).
- Merge conflicts on `issues.jsonl` in multi-agent environments (mitigated by morsel ownership locks).

---

### ADR-003: Cross-Model Review (Claude ↔ Codex)

**Context:** LLMs reviewing their own output exhibit confirmation bias. A model that wrote buggy code will often approve it in review.

**Decision:** The implementing agent and reviewing agent must be *different models*. Claude reviews Codex's work. Codex reviews Claude's.

**Rationale:**
- **Different blind spots.** Each model family has characteristic failure modes. Cross-pollination catches what self-review misses.
- **Agent swap on rejection.** When a review fails, the reviewer becomes the implementer with the review context injected. The rejected code becomes negative examples. Up to 3 handoffs before escalation.
- **Measurable.** We record which model pairs have the highest first-pass review acceptance rate. This data feeds the learner.

**Why not just test?** Tests catch functional bugs. Reviews catch architectural mistakes, naming confusion, missing edge cases, and "technically works but is wrong in spirit" code. Both layers are necessary.

---

### ADR-004: Human Gate Before Execution

**Context:** Autonomous code execution without human oversight is dangerous. The system can generate and commit code that compiles and passes tests but is semantically wrong.

**Decision:** Every plan must be approved by a human via Temporal signal before entering the coding engine.

**Rationale:**
- **Plan space is cheap, implementation is expensive.** An LLM generating a structured plan costs ~1K tokens. Implementing that plan costs ~50K tokens. It's 50x cheaper to reject a bad plan than to reject bad code.
- **Explicit scope control.** The human gate prevents scope creep — the agent can only implement what was approved.
- **Audit trail.** Every approval is a Temporal event with timestamp and signal payload. Full accountability.

**Future:** As confidence grows, the gate can be relaxed for low-risk morsels (e.g., `priority >= P3`, `complexity = fast`).

---

### ADR-005: Semgrep as Immune System (LATM)

**Context:** LLMs make the same categories of mistakes repeatedly. The classic fix is "add it to the prompt," but prompts grow unbounded and don't enforce deterministically.

**Decision:** The ContinuousLearner extracts lessons from failures and generates `.semgrep/` rules that run as a pre-filter before DoD.

**Rationale:**
- **Deterministic enforcement.** A Semgrep rule either matches or doesn't. No LLM inference, no temperature variance, no hallucination.
- **Free at runtime.** Semgrep AST matching is ~100ms. Compare to `go build` (5-10s) or `go test` (30s+). The pre-filter catches repeat offenses before burning expensive compute.
- **Self-growing.** Every mistake teaches the system. Over time, the rule set grows to cover the project's specific antipattern surface. *The factory builds its own immune system.*
- **LATM principle.** This is "LLM as Tool Maker" — the stochastic model generates deterministic artifacts (Semgrep rules) that outlive any single conversation.

**This is the architectural insight most people miss.** The value isn't in the LLM generating code. The value is in the LLM generating *rules* that prevent future LLMs from making the same mistakes.

---

### ADR-006: CHUM as Abandoned Child Workflows

**Context:** After a morsel completes, the system should extract lessons and groom the backlog. But these operations must never block the next morsel's execution.

**Decision:** ContinuousLearner and TacticalGroom run as child workflows with `PARENT_CLOSE_POLICY_ABANDON`.

**Rationale:**
- **Non-blocking.** The parent workflow completes immediately after spawning children. Zero added latency to the main loop.
- **Durable.** If the learner crashes mid-extraction, Temporal retries it. No lesson is lost.
- **Isolation.** A bug in lesson extraction doesn't affect code execution. Different failure domains.
- **Wait-for-start pattern.** We call `GetChildWorkflowExecution().Get()` to ensure the child is actually scheduled before the parent exits. This prevents Temporal from garbage-collecting unstarted children.

---

### ADR-007: SQLite over Postgres

**Context:** CHUM needs a persistence layer for dispatches, outcomes, lessons, and health events.

**Decision:** SQLite with WAL mode and FTS5 for full-text lesson search.

**Rationale:**
- **Zero infrastructure.** No database server to provision, connect to, or maintain. The state database is a single file.
- **Colocated with the worker.** The Temporal worker and SQLite database run on the same host. Sub-millisecond reads.
- **FTS5 for lessons.** Full-text search over accumulated lessons without deploying Elasticsearch. `SELECT * FROM lessons_fts WHERE lessons_fts MATCH 'nil pointer'` just works.
- **Backup is `cp`.** Copy the `.db` file. Done.

**When to upgrade:** If CHUM ever needs multi-worker horizontal scaling, SQLite becomes the bottleneck. That day is not today.

---

### ADR-008: Go over Python

**Context:** Most AI tooling is Python-first. Why is CHUM in Go?

**Decision:** Go for the orchestrator, LLMs via CLI subprocesses.

**Rationale:**
- **Temporal SDK.** The Go Temporal SDK is first-class, battle-tested at Uber-scale.
- **Single-binary deployment.** `go build` produces one static binary. No virtualenvs, no `pip install`, no Docker required (though supported).
- **Concurrency model.** Goroutines and channels are a natural fit for fan-out/fan-in workflow patterns.
- **Type safety.** Workflow parameters and activity signatures are compile-time checked. A Python `dict` flowing through Temporal activities is a runtime bomb.

**The LLMs don't care what language calls them.** Claude and Codex are invoked via CLI (`claude --print`, `codex exec`). The orchestrator's language is irrelevant to the agent runtime.

---

### ADR-009: Dual-Speed Grooming (Tactical + Strategic)

**Context:** Backlog hygiene is critical but has two different rhythms.

**Decision:** Two separate grooming workflows operating at different speeds.

**Rationale:**

| | Tactical Groom | Strategic Groom |
|---|---|---|
| **Trigger** | Per morsel completion | Cron: daily at 5 AM |
| **LLM tier** | Fast (cheap) | Premium (expensive) |
| **Scope** | Adjacent morsels only | Entire backlog + repo map |
| **Actions** | Reprioritize, add deps, close stale | Deep analysis, split/merge morsels, morning briefing |
| **Latency budget** | < 30 seconds | < 5 minutes |

This mirrors real Scrum: tactical grooming happens in standup (fast, narrow), strategic grooming happens in sprint planning (slow, broad).

---

## Positioning: CHUM vs Everything Else

| System | Primary Role | Relationship |
|--------|-------------|--------------|
| **OpenClaw** | Agent runtime + gateway/control plane | CHUM executes work through it |
| **Gas Town** | Multi-agent workspace/orchestration framework | CHUM focuses on policy/ops, not town topology |
| **Morsels** | Git-backed issue tracker + dependency DAG | CHUM's input layer |
| **Temporal** | Durable workflow execution engine | CHUM's execution substrate |
| **CHUM** | Autonomous dispatch policy + learning loop | Sits above all of the above |

---

## Non-Goals

CHUM is explicitly **not** trying to be:

- A chatbot or assistant (that's OpenClaw)
- A workspace management UI (that's Gas Town)
- An issue tracker (that's Morsels)
- A CI/CD pipeline (CHUM triggers code-level work, not build/deploy)
- A replacement for human architects (the human gate exists for a reason)
