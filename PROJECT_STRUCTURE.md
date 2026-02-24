# CHUM Project Structure

Standard Go project layout with Temporal workflow engine at the core.

```
chum/
├── cmd/                          # Application entry points
│   ├── chum/                     # Main binary (API + Temporal worker + cron)
│   │   ├── main.go               #   Entrypoint, config loading, worker/API bootstrap
│   │   └── admin.go              #   Admin CLI commands (disable-anthropic, normalize-morsels)
│   ├── db-backup/                # Database backup utility
│   └── db-restore/               # Database restore utility
│
├── internal/                     # Private application code
│   ├── temporal/                 # ⚡ Temporal workflows + activities (core engine)
│   │   ├── workflow.go           #   CHUMAgentWorkflow — plan→gate→execute→review→DoD
│   │   ├── workflow_groom.go     #   TacticalGroom + StrategicGroom workflows
│   │   ├── workflow_learner.go   #   ContinuousLearner workflow
│   │   ├── planning_workflow.go  #   PlanningCeremony interactive workflow
│   │   ├── activities.go         #   Core activities (plan, execute, review, DoD, record)
│   │   ├── agent_cli.go          #   Agent CLI command builders + runners
│   │   ├── agent_parsers.go      #   Agent output JSON parsers (Claude, Codex, Gemini)
│   │   ├── groom_activities.go   #   Groom activities (mutate, repo map, analysis, briefing)
│   │   ├── learner_activities.go #   Learner activities (extract, store, semgrep rules)
│   │   ├── planning_activities.go#   Planning ceremony activities
│   │   ├── types.go              #   All request/response/domain types
│   │   ├── worker.go             #   Worker bootstrap + workflow/activity registration
│   │   └── workflow_test.go      #   Temporal workflow tests (5 test cases)
│   ├── store/                    # SQLite persistence (domain-split)
│   │   ├── store.go              #   Store struct, schema, Open, migrate, Close
│   │   ├── dispatches.go         #   Dispatch CRUD, overflow queue, provider usage
│   │   ├── safety.go             #   Safety blocks (throttling + coordination guards)
│   │   ├── claims.go             #   Claim leases (morsel ownership locks)
│   │   ├── stages.go             #   MorselStage CRUD (workflow stage tracking)
│   │   ├── metrics.go            #   Health events, tick metrics, quality scores, token usage
│   │   └── store_test.go         #   Store tests
│   ├── api/                      # HTTP API server
│   ├── graph/                    # Morsels DAG integration (CRUD, deps, queries)
│   ├── config/                   # TOML config with hot-reload (SIGHUP)
│   ├── dispatch/                 # Agent dispatch, rate limiting, cost control
│   ├── git/                      # Git operations + DoD post-merge checks
│   ├── chief/                    # ⚠️ Chief/scrum-master agent coordination (not yet wired)
│   ├── cost/                     # ⚠️ Cost tracking and budget controls (not yet wired)
│   ├── matrix/                   # ⚠️ Matrix messaging integration (not yet wired)
│   ├── portfolio/                # Multi-project portfolio management
│   ├── team/                     # ⚠️ Team/agent management (not yet wired)
│
├── configs/                      # Configuration examples
│   ├── chum.runner.toml        #   Production runner config template
│   ├── chum-interactive.toml   #   Interactive development config
│   ├── trial-chum.toml           #   Trial/testing config
│   └── slo-thresholds.json       #   Service Level Objective definitions
│
├── deploy/                       # Deployment
│   ├── docker/                   #   Docker compose files
│   └── systemd/                  #   Systemd service unit files
│
├── docs/                         # Documentation
│   ├── architecture/             #   System architecture, CHUM backlog, config reference
│   │   └── PACKAGE_MAP.md        #   Package coupling metrics + dependency graph
│   ├── api/                      #   API documentation
│   ├── deps/                     #   Auto-generated dependency visualizations
│   ├── development/              #   Developer guides, AI agent onboarding
│   └── operations/               #   Operational guides, scrum commands
│
├── scripts/                      # Utility scripts
│   ├── gen-dep-graph.sh          #   Generate package dependency graph (DOT format)
│   ├── dev/                      #   Development helpers
│   ├── hooks/                    #   Git hooks (branch guard, pre-commit)
│   ├── ops/                      #   Operational maintenance scripts
│   └── release/                  #   Release management scripts
│
├── build/                        # Build outputs
│   └── package/                  #   Container images
│       ├── Dockerfile            #     Main container image
│       └── Dockerfile.agent      #     Agent container image
├── .morsels/                       # Morsels issue tracker data (gitignored runtime data)
├── .openclaw/                    # OpenClaw agent personality files
│
├── Makefile                      # Build automation
├── .golangci.yml                 # Linter config (30 linters, used by CI)
├── go.mod / go.sum               # Go module dependencies
├── VERSION                       # Current release version
├── AGENTS.md                     # AI agent instructions
├── CONTRIBUTING.md               # Contribution guidelines
├── CODE_OF_CONDUCT.md            # Community guidelines
└── LICENSE                       # MIT License
```

## Key Architectural Decisions

| Decision | Rationale |
|----------|-----------|
| **Temporal over in-process scheduler** | Durable execution: if CHUM crashes mid-workflow, Temporal replays from exactly where it left off |
| **Morsels over Jira/Linear** | Git-backed, local-first, dependency-aware DAG. No external service dependency |
| **Cross-model review** | Claude reviews Codex, Codex reviews Claude. Catches model-specific blind spots |
| **CHUM as child workflows** | Fire-and-forget with `PARENT_CLOSE_POLICY_ABANDON`. Learning never blocks execution |
| **SQLite FTS5 for lessons** | Full-text search over accumulated lessons. No external search infrastructure |
| **Semgrep as immune system** | Learner generates `.semgrep/` rules from mistakes. Pre-filters catch repeat offenses for free |

## Make Targets

```bash
make build             # Build chum binary
make build-all         # Build all binaries
make test              # Run all tests
make test-race         # Run tests with race detector (10 packages)
make test-race-ci      # CI test runner with timeout + artifacts
make lint              # Run basic linters (fmt + vet)
make lint-full         # Run full golangci-lint suite (30 linters)
make test-coverage     # Run tests with coverage report
make service-install   # Install systemd service
make release           # Create a release
make help              # Show all targets
```
