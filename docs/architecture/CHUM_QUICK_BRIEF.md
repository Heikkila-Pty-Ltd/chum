# CHUM — Quick Brief

**One-liner:** Autonomous agent orchestrator that turns a Git-backed task DAG into reliable, self-improving code execution via Temporal workflows.

---

## What It Does

1. Reads micro-tasks from [Beads](https://github.com/steveyegge/beads) — a dependency-aware Git-backed backlog
2. Generates a structured plan with acceptance criteria (LLM)
3. Waits for human approval (Temporal signal)
4. Dispatches an AI agent (Claude/Codex) to implement the plan
5. Cross-model review — a *different* agent reviews the work
6. DoD gate — `go build`, `go test`, lint. Binary pass/fail
7. After success: learns from the work and grooms the backlog automatically

## Why It's Different

| Feature | Typical AI Workflow | CHUM |
|---------|-------------------|--------|
| Task management | Manual prompt → agent → hope | Morsels DAG with dependency resolution |
| Durability | Script crashes = lost state | Temporal replays from failure point |
| Quality gate | "The LLM said it's fine" | Deterministic: compile, test, lint, Semgrep |
| Review | Self-review (confirmation bias) | Cross-model: Claude reviews Codex, Codex reviews Claude |
| Learning | None | Failures → lessons → Semgrep rules (self-growing immune system) |
| Backlog | Manual grooming | Tactical (per-morsel) + Strategic (daily cron) automated grooming |

## How It Works

```
Morsels DAG → Plan → Human Gate → Execute → Review → Semgrep → DoD
                                   ↑               ↓ (fail)      ↓ (pass)
                                   └───────────────┘              ↓
                                                            CHUM Loop
                                                     ┌──────────┴──────────┐
                                                  Learner            Tactical Groom
                                              (extract lessons)    (reprioritize morsels)
                                              (store in FTS5)      (add dependencies)
                                              (generate Semgrep)   (close stale items)
```

## Stack

| Component | Technology | Why |
|-----------|-----------|-----|
| Language | Go 1.24+ | Single binary, type-safe Temporal SDK, goroutine concurrency |
| Orchestration | Temporal | Durable execution, signal-based gating, native cron |
| Task Graph | Morsels | Git-backed, local-first, dependency DAG |
| Persistence | SQLite + FTS5 | Zero-infra, full-text lesson search |
| Agents | Claude CLI, Codex CLI | Model-agnostic via pluggable CLI interface |
| Static Analysis | Semgrep | CHUM-generated rules for deterministic pattern enforcement |

## Key Docs

| Doc | Purpose |
|-----|---------|
| [CHUM_OVERVIEW.md](CHUM_OVERVIEW.md) | Full design rationale with ADRs |
| [ARCHITECTURE.md](ARCHITECTURE.md) | Technical architecture with diagrams |
| [CHUM_BACKLOG.md](CHUM_BACKLOG.md) | Strategic roadmap |
| [CONFIG.md](CONFIG.md) | Configuration reference |
