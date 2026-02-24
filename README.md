# CHUM (Continuous Hyper Utility Module)
Architected to silently devour legacy paperwork industries with deterministic, evolutionary precision.

[![CI](https://github.com/Heikkila-Pty-Ltd/chum/actions/workflows/ci.yml/badge.svg)](https://github.com/Heikkila-Pty-Ltd/chum/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/Heikkila-Pty-Ltd/chum)](https://go.dev/)

> *Ephemeral agents learn nothing and die. Enduring systems evolve.*

CHUM is a **Darwinian execution substrate** for production AI work. It treats coding tasks not as transient prompts, but as environmental pressures that select for the fittest provider models. CHUM embodies principles from evolutionary biology—specifically the *Selfish Gene*—where successful approaches are encoded into a durable species genome, and failed branches serve as antibodies.

## 🧬 Core Concept: The Selfish Gene & Genomic Memory

Instead of launching disposable agents, CHUM tracks the evolution of specific task "Species."

- **The Genome**: A SQLite-backed persistent memory structure per species.
- **Patterns (DNA)**: Approaches that pass the Definition of Done (DoD) become permanent DNA, automatically injected into future prompts for that species.
- **Antibodies**: Approaches that fail become defensive antibodies, warning future organisms away from known dead ends.
- **Provider Fitness**: CHUM evaluates models (Codex, Claude, Gemini, DeepSeek) based on their survival rate (DoD pass) vs. their token cost. The "selfish gene" ensures that only the most cost-effective and successful provider genes propagate.

## 🌋 The Cambrian Explosion Protocol

When CHUM encounters a completely novel task (Generation 0), it triggers a **Cambrian Explosion**:

1. Identical isolated git worktrees are spawned across multiple LLM providers (e.g., Gemini Flash, Codex Spark, DeepSeek R1) in parallel.
2. Each organism attempts the task simultaneously.
3. The environment (Definition of Done checks like `go test` or `npm run build`) aggressively filters the population.
4. The first organism to pass DoD wins.
5. The winning provider's code is committed, pushed, and its "gene" is recorded in the species genome for all future generations. All competing worktrees are cleanly destroyed.

## 🧊 Hibernation & Token Conservation

Evolution is expensive. If a species enters an unrecoverable escalation cascade (failing repeatedly across all provider tiers), CHUM flags it for **Hibernation**.

- Hibernation cuts token burn instantly.
- The organism sleeps until a human intervenes or the underlying repository environment changes.

## 🏗 Infrastructure & The Moat

- **Go + Temporal SDK + SQLite Hot State**
- **Temporal** orchestrates the parallel branches of the Cambrian Explosion, managing asynchronous LLM calls and retry cascades.
- **SQLite** is the single source of truth for the Genome, storing patterns, antibodies, and workflow state. File-based task tracking is rejected to avoid non-deterministic I/O under high concurrency load.

### Git Governance

Git is the environment, not the orchestrator. It is used only for:
- Isolated test sandboxes during explosions.
- Final accumulation of surviving genes (commits).

## 📊 Observability

- **Continuous Learner**: An asynchronous background process that runs over successful outputs, synthesizing `CLAUDE.md` rules and Semgrep patterns.
- **Matrix Integration**: Built-in reporter to broadcast evolutionary milestones (e.g., "Cambrian Explosion winner selected") directly into Matrix rooms via `spritzbot`.

## 🚀 Operational Notes

- Build: `make build`
- Test: `make test`
- Execute: `./chum --config <path> --dev`

## 📖 Documentation

- `docs/architecture/ARCHITECTURE.md`
- `docs/architecture/CONFIG.md`
- `docs/architecture/CHUM_OVERVIEW.md`

## License
MIT — see [LICENSE](LICENSE).
