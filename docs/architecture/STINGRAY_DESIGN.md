# Stingray: Autonomous Code Health Auditor

## The Gap

CHUM already has three self-improvement loops:

| System | Trigger | Looks At | Blind Spot |
|--------|---------|----------|------------|
| **Learner** | After each bead | Diffs + DoD failures from that bead | Only sees what just changed |
| **Tactical Groom** | After each bead | Open backlog + completed task context | Reshuffles tasks, doesn't analyze code |
| **Strategic Groom** | Daily cron | `go list -json` + backlog + lessons | Surface-level repo structure, no deep reads |

None of them ever **read the actual code** looking for structural problems. They're all reactive — they learn from work that was done, not from the codebase as it exists. The strategic groomer gets a compressed package list but never opens a file.

Additionally, none of them audit **documentation** — stale READMEs, missing godoc, outdated architecture docs, undocumented public APIs, config files that reference removed features.

The **Stingray** fills this gap: a periodic workflow that glides across the seabed of the codebase, sensing buried issues with electroreception — god objects, tech debt, stale docs, dependency rot, coverage gaps, coupling, OSS opportunities. Finds what's hidden under the surface that nobody's actively looking at.

Log prefix: `🦂 STINGRAY`

## Design

### New Temporal Workflow: `StingrayWorkflow`

**Trigger:** Configurable cron (default: every 12 hours, e.g. `0 */12 * * *`).
At the pace sharks churn through beads, the codebase can shift significantly in a single day. Twice-daily catches drift before it compounds. Can also be triggered manually via API or after a strategic groom. Frequency is configurable per-project — high-velocity projects might run every 6h, stable ones daily.

**Pipeline:**

```
GatherMetricsActivity          (2 min, subprocess-only, no LLM cost)
    │
    ├─ go vet ./...
    ├─ golangci-lint run --out-format json (gocognit, funlen, cyclop, dupl)
    ├─ go test -coverprofile + go tool cover -func (coverage %)
    ├─ go list -m -u all (outdated deps)
    ├─ custom file metrics: LOC per file, methods per type, fan-in/fan-out
    ├─ grep TODO/HACK/FIXME/WORKAROUND with file:line context
    ├─ go mod graph (dependency tree for circular dep detection)
    │
    ▼
AnalyzeCodeHealthActivity      (10 min, premium LLM, heartbeat)
    │
    │  Input: metrics bundle + compressed repo map + recent lessons
    │  Output: []Finding (typed, with evidence and severity)
    │
    ▼
DeduplicateActivity            (30s, no LLM)
    │
    │  Check against:
    │  - Open beads with label "source:stingray"
    │  - Previous findings in stingray_findings table
    │  - Recently closed beads (don't re-file resolved items)
    │
    ▼
FileBacklogItemsActivity       (1 min, no LLM)
    │
    │  **High severity** → auto-file as P2 bead + surface warning notification
    │    (god objects, security, circular deps, critical doc drift)
    │    Notification via Matrix/email (when implemented) so humans see it
    │  **Medium/low** → stored in findings table only
    │    Surfaced in next planning session / morning briefing for triage
    │    Planning ceremony (chief or human) promotes to beads if warranted
    │  All beads tagged: source:stingray, category:{finding_type}
    │
    ▼
RecordStingrayRunActivity    (30s, no LLM)
    │
    │  Store findings for trend tracking:
    │  - stingray_runs table: timestamp, project, finding counts
    │  - stingray_findings table: type, severity, file, status
    │  - Feed metrics into morning briefing / retrospectives
    │
    ▼
(Optional) GenerateHealthReportActivity  (2 min, fast LLM)
    │
    │  Write stingray_report.md to project root
    │  Trend lines: "coverage improved 62%→68%, god objects decreased 4→2"
```

### Finding Types (Analyzers)

Each analyzer is a section of the LLM prompt, not a separate interface. The LLM gets all the metrics at once and produces typed findings. This keeps it simple — one LLM call, not N.

**God Object Detection**
- Evidence: files >500 LOC, types with >12 methods, files importing >10 packages
- Gathered by: custom `wc -l`, `go doc -short`, AST method counting
- Example finding: `"internal/store/store.go has 47 methods on *Store — consider splitting into StoreReader/StoreWriter/StoreAdmin"`

**Tech Debt Scan**
- Evidence: TODO/HACK/FIXME/WORKAROUND comments with surrounding context
- Gathered by: grep with file:line, grouped by age (git blame)
- Example: `"12 TODOs older than 90 days in internal/dispatch/ — 3 reference 'V0' patterns that may be ready to evolve"`

**Dependency Health**
- Evidence: `go list -m -u all` for outdated modules, `govulncheck` for CVEs
- Gathered by: subprocess
- Example: `"golang.org/x/crypto is 8 months behind latest — contains 2 moderate CVEs"`

**Coverage Gaps**
- Evidence: `go tool cover -func` output, sorted by uncovered %
- Gathered by: subprocess
- Example: `"internal/scheduler/ has 23% coverage — critical dispatch logic untested"`

**Package Structure**
- Evidence: package dependency graph, fan-in/fan-out counts, naming patterns
- Gathered by: `go list -json ./...` + graph analysis
- Example: `"internal/temporal/ has 22 files — consider splitting workflow definitions from activities"`

**OSS Bolt-On Opportunities**
- Evidence: hand-rolled utilities that have mature OSS replacements
- Gathered by: LLM reads utility files + knows ecosystem
- Example: `"internal/dispatch/ratelimit.go implements a sliding window counter — consider golang.org/x/time/rate or uber-go/ratelimit"`

**Circular Dependencies / Coupling**
- Evidence: `go mod graph` + import analysis
- Gathered by: subprocess + graph traversal
- Example: `"internal/config imports internal/store which imports internal/config via health checks — break cycle with interface"`

**Documentation Drift**
- Evidence: stale READMEs, missing godoc on exported types, outdated arch docs, config references to removed features, undocumented public APIs
- Gathered by: diff between exported symbols (`go doc -short`) and documented symbols; file modification dates vs doc modification dates; grep config keys against docs
- Sub-checks:
  - **Stale docs**: docs/ files not updated in >30 days while their referenced code changed
  - **Missing godoc**: exported functions/types with no doc comment
  - **Config drift**: keys in chum.toml not mentioned in any doc file
  - **Dead references**: docs referencing functions/files that no longer exist
  - **README freshness**: README.md last modified vs last 50 commits
- Example: `"docs/architecture/CHUM_OVERVIEW.md references 'internal/beads/' which was deprecated — update to reflect graph/ migration"`
- Example: `"14 exported functions in internal/scheduler/ have no godoc comment"`

### Data Model

```sql
CREATE TABLE stingray_runs (
    id INTEGER PRIMARY KEY,
    project TEXT NOT NULL,
    run_at DATETIME NOT NULL DEFAULT (datetime('now')),
    findings_total INTEGER NOT NULL DEFAULT 0,
    findings_new INTEGER NOT NULL DEFAULT 0,
    findings_resolved INTEGER NOT NULL DEFAULT 0,
    metrics_json TEXT NOT NULL DEFAULT '{}'  -- coverage %, LOC, dep count, etc.
);

CREATE TABLE stingray_findings (
    id INTEGER PRIMARY KEY,
    run_id INTEGER NOT NULL REFERENCES stingray_runs(id),
    project TEXT NOT NULL,
    category TEXT NOT NULL,  -- god_object, tech_debt, dep_health, coverage, structure, oss_opportunity, coupling, doc_drift
    severity TEXT NOT NULL,  -- high, medium, low
    title TEXT NOT NULL,
    detail TEXT NOT NULL,
    file_path TEXT NOT NULL DEFAULT '',
    evidence TEXT NOT NULL DEFAULT '',
    bead_id TEXT NOT NULL DEFAULT '',  -- linked bead if filed
    status TEXT NOT NULL DEFAULT 'open',  -- open, filed, resolved, wont_fix
    first_seen DATETIME NOT NULL DEFAULT (datetime('now')),
    last_seen DATETIME NOT NULL DEFAULT (datetime('now'))
);
```

**Key: `last_seen` tracking.** If a finding appears in consecutive runs, it's persistent — bump `last_seen`. If it disappears, mark `resolved`. This gives trend tracking for free.

### Config

```toml
[stingray]
enabled = true
schedule = "0 */12 * * *"  # every 12 hours
max_findings_per_run = 15
auto_file_threshold = "medium"  # file beads for medium+ severity; low = report only
include_oss_suggestions = true
cooldown_if_no_changes = "24h"  # skip run if no commits since last run
```

The `cooldown_if_no_changes` prevents wasted LLM calls when nothing changed — the first activity checks `git log --since` and short-circuits if the codebase is idle.

### How It Differs From Strategic Groom

| Aspect | Strategic Groom | Stingray |
|--------|----------------|--------------|
| **Frequency** | Daily | Every 12h (configurable, skips if idle) |
| **Input** | Package list + backlog state | Full static analysis metrics + file reads + doc audit |
| **Focus** | "What should we work on next?" | "What's wrong with the code and docs?" |
| **Output** | Reprioritized backlog + morning briefing | Typed findings + new backlog items + health report |
| **LLM tier** | Premium | Premium (analysis) + none (metrics gathering) |
| **Reads code?** | No (just `go list` summaries) | Yes (metrics + targeted file reads for evidence) |
| **Reads docs?** | No | Yes (staleness, accuracy, coverage) |

They complement each other: the strategic groom decides priority order, the stingray feeds it new items to prioritize.

### Integration Points

1. **Morning Briefing** — Strategic groom's briefing includes "Stingray last ran N days ago, found X new issues, Y trending worse"
2. **Learner** — Stingray findings inform lesson extraction: "this file was flagged as a god object, and the latest change made it worse"
3. **CLAUDE.md** — High-severity findings appear as warnings: "WARNING: internal/store/store.go is a god object (47 methods). Split before adding more."
4. **Retrospective** — Chief pulls stingray trends for retro data

## Files to Create/Modify

### New
- `internal/temporal/workflow_stingray.go` — Workflow definition
- `internal/temporal/stingray_activities.go` — All activities
- `internal/temporal/stingray_types.go` — Finding, StingrayRun, config types

### Modified
- `internal/store/stingray.go` — New store methods for findings/runs tables
- `internal/store/schema.go` — Add stingray tables to EnsureSchema
- `internal/config/config.go` — Add `[stingray]` config section
- `cmd/chum/main.go` or `cmd/chum/main.go` — Register cron workflow
- `chum.toml` — Add `[stingray]` section

## Verification
1. `go build ./...`
2. `go test ./internal/temporal/...` (workflow + activity unit tests)
3. Manual trigger via Temporal CLI or API endpoint, observe findings filed as beads
4. Second run with no code changes — verify dedup (no duplicate beads)
