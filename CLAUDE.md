# Project Rules — Seeded from Pipeline Analysis (Feb 24, 2026)

> This file is read by all AI agents (Claude CLI, Codex, Gemini) before every task.
> It captures accumulated project wisdom from 1,819 dispatches analyzed.

## Rules (Enforced)

These MUST be followed. Violations will cause DoD failure.

- **Always run `go build ./...` before considering work complete** — 85/421 successful outputs ran `go build`, 0/220 failed outputs did
- **Always run `go test ./...` before marking complete** — agents that ran tests had a 0% failure rate (perfect predictor)
- **Always run `go vet ./...`** — required by DoD
- **Run `golangci-lint run --timeout=5m --new-from-rev=HEAD~1`** if available — DoD requires it
- **Do NOT source `.zshrc` or interactive shell init in scripts** — causes instant process death
- **Do NOT use the `bd` command** — it is deprecated and removed; use the CHUM API instead

## Anti-patterns (Avoid)

These patterns have caused failures across 268 failed dispatches.

- **Writing code without reading existing files first** — failed dispatches show 25% read-before-write vs 33% in successes. Always `cat` or `view_file` the target before editing.
- **Skipping planning/strategy** — 19/421 successful outputs had explicit planning keywords; 0/220 failures did. Plan before you code.
- **Ignoring acceptance criteria** — 58% of successes checked DoD/acceptance criteria; only 5% of failures did. Read the acceptance criteria FIRST.
- **Generating entire files instead of patching** — leads to scope drift and import breakage
- **Calling tools without required arguments** — `read` without a path causes infinite retry loops → OOM kill (status 137)

## Good Patterns (Follow)

These approaches have been verified to work across 1,333 successful dispatches.

- **Read-before-write discipline** — always examine target files before modifying
- **Incremental verification** — run `go build` after each change, not just at the end
- **Scope discipline** — only modify files mentioned in the task. SentinelScan checks for drift.
- **Small commits** — commit working increments rather than one large batch

## Common DoD Failures

These checks frequently fail. Address them proactively:

- `golangci-lint run --timeout=5m` (exit 127) — binary may not be on PATH; check `which golangci-lint` first
- `go test ./...` (exit 1) — run tests BEFORE marking done; fix failures locally
- `go vet ./...` (exit 1) — check for unused variables, unreachable code
- `npm run build` (exit 1) — for golf-directory project; check TypeScript types

## Provider Performance (for routing decisions)

| Provider | Best Project | Success Rate | Notes |
|----------|-------------|-------------|-------|
| llama-4-scout | hg-website | 99.9% | Highest volume, best on simple tasks |
| claude-sonnet-4 | cortex | 100% (n=20) | Small sample but perfect |
| gpt-5.3-codex-spark | cortex | 73.5% | Good balanced option |
| codex-spark | cortex | 3.5% | Very poor — mostly escalates |
| gemini-pro | chum | 0.0% | Zero completions on chum |

## Definition of Done

Every change must pass: `go build ./... && go vet ./... && golangci-lint run --timeout=5m`

Run these locally before considering the task complete.
