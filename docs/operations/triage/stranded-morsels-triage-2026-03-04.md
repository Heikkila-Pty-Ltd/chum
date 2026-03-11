# Stranded Morsels Triage (2026-03-04)

- Total stranded morsel files: **162**
- archive_done: **12**
- archive_invalid: **1**
- archive_user_confirmed_resolved: **2**
- archive_out_of_scope: **8**
- archive_superseded_duplicate: **34**
- likely_stale_orphan_ch_ref: **44**
- keep_candidate: **61**

## Keep Candidates

- `chum-td17a-embedding-pipeline.md` — Unsupervised Automation Phase 1: Embedding Pipeline
- `chum-td17b-clustering.md` — Unsupervised Automation Phase 2: Signal Point Clustering
- `chum-td17c-protein-synthesis.md` — Unsupervised Automation Phase 3: Protein Synthesis & Self-Test
- `chum-td22-proactive-grooming.md` — Proactive whale slicing during strategic groom
- `chum-td24-calibrate-rate-limits.md` — Calibrate rate limiter caps to actual provider token limits
- `jarvis-20260227-092731-dependency-update-payloadcms-suite-eslin.md` — Dependency update: PayloadCMS suite + eslint 8→10 major upgrade
- `jarvis-20260303-084542-fix-missing-time-import-in-internal-ast-.md` — Fix missing time import in internal/ast/embed_test.go
- `jarvis-20260303-084654-dispatcher-re-queue-needs-review-tasks-w.md` — Dispatcher: re-queue needs_review tasks with no active workflow
- `jarvis-20260303-085526-implement-scanorphanedreviewsactivity-on.md` — Implement ScanOrphanedReviewsActivity on DispatchActivities
- `jarvis-20260303-101547-test-agentworkflow-handles-pr-creation-f.md` — Test AgentWorkflow handles PR creation failure gracefully
- `jarvis-20260303-102544-fix-initiatives-db-query-jarvis-db-schem.md` — Fix initiatives DB query — jarvis.db schema mismatch
- `jarvis-20260303-103052-decompose-epic-planning-remaining-subtas.md` — Decompose epic-planning: remaining subtasks after plan-04
- `jarvis-20260303-104056-refine-and-implement-health-check-endpoi.md` — Refine and implement health check endpoint for chum-factory API
- `jarvis-20260303-105051-refine-epic-planning-decompose-into-exec.md` — Refine epic-planning: decompose into executable subtasks
- `jarvis-20260303-110600-fix-cortex-auto-reviewer-diagnose-review.md` — Fix Cortex auto-reviewer: diagnose reviewer_error on PRs #30,32,33,35,37,38,39,40
- `jarvis-20260303-113550-investigate-broken-api-routes-on-port-97.md` — Investigate broken API routes on port 9780
- `jarvis-20260303-114051-audit-disabled-workflows-and-create-re-e.md` — Audit disabled workflows and create re-enable task list
- `jarvis-20260303-114051-re-enable-first-cortex-workflow-under-ch.md` — Re-enable first cortex workflow under CHUM orchestration
- `jarvis-20260303-132049-add-unit-tests-for-notifyactivity-path-s.md` — Add unit tests for NotifyActivity path selection and failure behavior
- `jarvis-20260303-133543-add-tests-for-notifyactivity-path-select.md` — Add tests for NotifyActivity path selection and failure behavior
- `jarvis-20260303-135122-audit-chum-store-package-for-conflicts-w.md` — Audit Chum store package for conflicts with Cortex store interfaces
- `jarvis-20260303-135122-port-cortex-dag-implementation-to-chum-p.md` — Port Cortex DAG implementation to Chum planning package
- `jarvis-20260303-135122-port-cortex-dispatchstore-and-metricssto.md` — Port Cortex DispatchStore and MetricsStore interfaces to Chum
- `jarvis-20260303-135122-port-cortex-tracestore-interface-and-sql.md` — Port Cortex TraceStore interface and SQLite implementation to Chum
- `jarvis-20260303-140105-fix-unexported-useridkey-in-auth-middlew.md` — Fix unexported userIDKey in auth middleware so UserFromContext works cross-package
- `jarvis-20260303-152920-split-temporal-move-activity-files-to-in.md` — Split temporal: move activity files to internal/activity/
- `jarvis-20260303-152920-split-temporal-move-agent-cli-go-and-age.md` — Split temporal: move agent_cli.go and agent_parsers.go to internal/agent/
- `jarvis-20260303-152920-split-temporal-update-worker-go-to-impor.md` — Split temporal: update worker.go to import from new packages
- `jarvis-20260303-152921-extract-store-interfaces-define-reader-a.md` — Extract store interfaces: define Reader and Writer in iface.go
- `jarvis-20260303-152921-extract-store-interfaces-update-consumer.md` — Extract store interfaces: update consumers to accept interfaces
- `jarvis-20260303-152921-split-temporal-resolve-circular-imports-.md` — Split temporal: resolve circular imports and shared types
- `jarvis-20260303-181106-fix-unexported-useridkey-in-auth-middlew.md` — Fix unexported userIDKey in auth middleware
- `jarvis-20260303-185044-health-check-endpoint-for-chum-factory-a.md` — Health check endpoint for chum-factory API (cf-d0i)
- `jarvis-20260303-194058-fix-auth-middleware-contextkey-visibilit.md` — Fix auth middleware contextKey visibility
- `jarvis-20260303-203548-port-activity-definitions-from-cortex-to.md` — Port activity definitions from Cortex to Chum temporal layer
- `jarvis-20260303-203548-port-failure-classifier-to-chum-temporal.md` — Port failure classifier to Chum temporal layer
- `jarvis-20260303-203548-port-planning-workflow-to-chum-temporal-.md` — Port planning workflow to Chum temporal layer
- `jarvis-20260303-203548-port-workflow-dispatcher-to-chum-tempora.md` — Port workflow dispatcher to Chum temporal layer
- `jarvis-20260303-203549-add-temporal-workflow-tests-for-planning.md` — Add temporal workflow tests for planning workflow
- `jarvis-20260303-212601-temporal-schedule-for-weekly-hg-report.md` — Temporal schedule for weekly HG report
- `jarvis-20260303-215715-wire-and-validate-dashboard-api-addition.md` — Wire and validate dashboard API additions
- `jarvis-20260303-215716-open-prs-for-jarvis-rename-conventions-f.md` — Open PRs for jarvis/rename-conventions, fix/notify-collision, fix/review-recovery
- `jarvis-20260303-220613-fix-broken-build-unknown-fields-dag-and-.md` — Fix broken build: unknown fields DAG and WebDir in jarvis.API struct
- `jarvis-20260303-221612-fix-build-failure-jarvis-api-missing-dag.md` — Fix build failure: jarvis.API missing DAG and WebDir fields in cmd/chum/main.go
- `jarvis-20260303-222108-fix-build-failure-remove-stale-dag-and-w.md` — Fix build failure: remove stale DAG and WebDir fields from API instantiation
- `jarvis-20260303-223557-diagnose-why-dispatcherworkflow-is-not-s.md` — Diagnose why DispatcherWorkflow is not starting
- `jarvis-20260303-224042-debug-why-dispatcherworkflow-never-start.md` — Debug why DispatcherWorkflow never starts
- `jarvis-20260303-225603-diagnose-and-fix-dispatcherworkflow-not-.md` — Diagnose and fix DispatcherWorkflow not starting in chum serve
- `jarvis-20260303-225603-reset-stale-running-tasks-to-ready-on-ch.md` — Reset stale 'running' tasks to 'ready' on chum serve startup
- `jarvis-20260303-230128-diagnose-why-chum-serve-doesn-t-start-di.md` — Diagnose why chum serve doesn't start DispatcherWorkflow
- `jarvis-20260303-230129-fix-chum-serve-to-actually-start-dispatc.md` — Fix chum serve to actually start DispatcherWorkflow on launch
- `jarvis-20260303-231110-add-chum-task-update-status-command-to-c.md` — Add chum task update --status command to CLI
- `jarvis-20260303-231110-diagnose-and-fix-dispatcherworkflow-not-.md` — Diagnose and fix DispatcherWorkflow not re-scheduling after termination
- `jarvis-20260303-233144-debug-why-dispatcherworkflow-never-start.md` — Debug why DispatcherWorkflow never starts from chum serve
- `jarvis-20260303-235608-increase-initiativeworkflow-runpicoclawa.md` — Increase InitiativeWorkflow RunPicoClawActivity timeout from 5m to 15m
- `jarvis-20260304-000614-add-mtime-based-caching-to-parser-parsef.md` — Add mtime-based caching to Parser.ParseFile
- `jarvis-20260304-000614-implement-internal-ratelimit-package-wit.md` — Implement internal/ratelimit package with token-bucket limiter
- `jarvis-20260304-000615-decompose-epic-planning-ceremony-into-im.md` — Decompose epic-planning ceremony into implementation tasks
- `jarvis-20260304-004109-diagnose-needs-review-tasks-with-no-asso.md` — Diagnose needs_review tasks with no associated PRs
- `jarvis-20260304-004707-fix-pr-creation-in-chum-factory-missing-.md` — Fix PR creation in chum-factory — missing local main branch
- `jarvis-20260304-005041-ingest-morsels-from-cortex-morsels-direc.md` — Ingest morsels from cortex/.morsels directory

## Archive/Likely-Stale

### archive_done
- `chum-td17-unsupervised-automation.md` — Implement Unsupervised Automation Pipeline (frontmatter status is done)
- `chum-td18-prevent-silent-failures.md` — Prevent silent failures in CHUM pipeline (frontmatter status is done)
- `chum-td19-auto-postmortem.md` — Auto-investigate workflow failures with post-mortem LLM analysis (frontmatter status is done)
- `chum-td19a-failure-detection.md` — Auto-Postmortem Step 1: Failure Detection & History Fetching (frontmatter status is done)
- `chum-td19b-llm-investigation.md` — Auto-Postmortem Step 2: LLM Investigation & Antibody Filing (frontmatter status is done)
- `chum-td20-blast-radius-scanner.md` — Add pre-crab blast radius scanner (frontmatter status is done)
- `chum-td21-matrix-notifications.md` — Add Matrix/Hex notifications for escalations and groom results (frontmatter status is done)
- `chum-td23-auto-mark-done.md` — Auto-mark morsel as done when shark catches land (frontmatter status is done)
- `chum-td25-auto-unblock-deps.md` — Auto-unblock morsels when all dependencies are done (frontmatter status is done)
- `fix-octopus-fts5.md` — Fix Octopus FTS5 syntax error in lesson search (frontmatter status is done)
- `fix-provider-genes-column.md` — Add provider_genes column to genomes table (frontmatter status is done)
- `fix-semgrep-registration.md` — Register RunSemgrepScanActivity in Temporal worker (frontmatter status is done)

### archive_invalid
- `jarvis-20260227-051455-title.md` — <title> (placeholder/invalid title)

### archive_user_confirmed_resolved
- `jarvis-20260304-002101-kill-duplicate-chum-serve-and-prevent-do.md` — Kill duplicate chum serve and prevent double-start (user confirmed duplicate chum serve issue is fixed)
- `jarvis-20260304-005641-prevent-duplicate-chum-serve-processes-o.md` — Prevent duplicate chum serve processes on restart (user confirmed duplicate chum serve issue is fixed)

### archive_out_of_scope
- `jarvis-20260303-211602-lead-reporting-identify-data-sources.md` — Lead reporting: identify data sources (external marketing/reporting scope)
- `jarvis-20260303-211602-lead-traffic-weekly-report-generator.md` — Lead + traffic weekly report generator (external marketing/reporting scope)
- `jarvis-20260303-211602-traffic-reporting-connect-google-search-.md` — Traffic reporting: connect Google Search Console API (external marketing/reporting scope)
- `jarvis-20260303-212047-set-up-lead-and-traffic-reporting-baseli.md` — Set up lead and traffic reporting baseline (external marketing/reporting scope)
- `jarvis-20260303-212600-ga4-traffic-report-script-weekly-session.md` — GA4 traffic report script — weekly sessions, users, top pages (external marketing/reporting scope)
- `jarvis-20260303-212601-hubspot-lead-pull-script-new-contacts-an.md` — HubSpot lead pull script — new contacts and form submissions last 7 days (external marketing/reporting scope)
- `jarvis-20260303-212601-weekly-report-combiner-and-matrix-delive.md` — Weekly report combiner and Matrix delivery (external marketing/reporting scope)
- `jarvis-20260303-213225-write-a-temporal-schedule-for-huntergall.md` — Write a Temporal schedule for huntergalloway.com.au weekly lead+traffic report (external marketing/reporting scope)

### archive_superseded_duplicate
- `jarvis-20260303-081535-re-attempt-test-agentworkflow-pr-creatio.md` — Re-attempt: Test AgentWorkflow PR creation failure path (superseded by newer morsel: jarvis-20260303-213224-retry-test-agentworkflow-pr-creation-fai.md)
- `jarvis-20260303-084123-fix-missing-time-import-in-internal-ast-.md` — Fix missing time import in internal/ast/embed_test.go (superseded by newer morsel: jarvis-20260303-084542-fix-missing-time-import-in-internal-ast-.md)
- `jarvis-20260303-095105-fix-dod-failed-test-agentworkflow-pr-cre.md` — Fix dod_failed: Test AgentWorkflow PR creation failure path (superseded by newer morsel: jarvis-20260303-213224-retry-test-agentworkflow-pr-creation-fai.md)
- `jarvis-20260303-100105-investigate-and-fix-dod-failed-ch-29f1e-.md` — Investigate and fix dod_failed ch-29f1e: Test AgentWorkflow PR creation failure path (superseded by newer morsel: jarvis-20260303-164612-retry-dod-failed-test-agentworkflow-pr-c.md)
- `jarvis-20260303-100544-fix-dod-failed-agentworkflow-pr-creation.md` — Fix dod_failed: AgentWorkflow PR creation failure path test (superseded by newer morsel: jarvis-20260303-213224-retry-test-agentworkflow-pr-creation-fai.md)
- `jarvis-20260303-101043-fix-dod-failed-test-agentworkflow-pr-cre.md` — Fix dod_failed: Test AgentWorkflow PR creation failure path (ch-29f1e) (superseded by newer morsel: jarvis-20260303-213224-retry-test-agentworkflow-pr-creation-fai.md)
- `jarvis-20260303-102045-retry-test-agentworkflow-pr-creation-fai.md` — Retry: Test AgentWorkflow PR creation failure path (ch-29f1e) (superseded by newer morsel: jarvis-20260303-213224-retry-test-agentworkflow-pr-creation-fai.md)
- `jarvis-20260303-103052-rewrite-ch-29f1e-agentworkflow-pr-creati.md` — Rewrite ch-29f1e: AgentWorkflow PR creation failure test (superseded by newer morsel: jarvis-20260303-170632-retry-ch-29f1e-agentworkflow-pr-creation.md)
- `jarvis-20260303-110601-refine-ch-29f1e-test-agentworkflow-pr-cr.md` — Refine ch-29f1e: Test AgentWorkflow PR creation failure path — dod_failed (superseded by newer morsel: jarvis-20260303-115537-fix-ch-29f1e-test-agentworkflow-pr-creat.md)
- `jarvis-20260303-112603-retry-agentworkflow-pr-creation-failure-.md` — Retry AgentWorkflow PR creation failure path test (ch-29f1e dod_failed) (superseded by newer morsel: jarvis-20260303-115537-fix-ch-29f1e-test-agentworkflow-pr-creat.md)
- `jarvis-20260303-114554-fix-dod-failed-ch-29f1e-agentworkflow-pr.md` — Fix dod_failed ch-29f1e: AgentWorkflow PR creation failure path test (superseded by newer morsel: jarvis-20260303-213224-retry-test-agentworkflow-pr-creation-fai.md)
- `jarvis-20260303-120622-retry-ch-29f1e-test-agentworkflow-pr-cre.md` — Retry ch-29f1e: Test AgentWorkflow PR creation failure path (superseded by newer morsel: jarvis-20260303-213224-retry-test-agentworkflow-pr-creation-fai.md)
- `jarvis-20260303-121545-retry-ch-29f1e-test-agentworkflow-pr-cre.md` — Retry ch-29f1e: Test AgentWorkflow PR creation failure path (superseded by newer morsel: jarvis-20260303-213224-retry-test-agentworkflow-pr-creation-fai.md)
- `jarvis-20260303-122552-fix-dod-failed-test-agentworkflow-pr-cre.md` — Fix dod_failed: Test AgentWorkflow PR creation failure path (ch-29f1e) (superseded by newer morsel: jarvis-20260303-213224-retry-test-agentworkflow-pr-creation-fai.md)
- `jarvis-20260303-122552-fix-unexported-contextkey-in-auth-middle.md` — Fix unexported contextKey in auth middleware (superseded by newer morsel: jarvis-20260303-182552-fix-unexported-contextkey-in-auth-middle.md)
- `jarvis-20260303-124556-retry-ch-29f1e-test-agentworkflow-pr-cre.md` — Retry ch-29f1e: Test AgentWorkflow PR creation failure path (superseded by newer morsel: jarvis-20260303-213224-retry-test-agentworkflow-pr-creation-fai.md)
- `jarvis-20260303-131549-add-tests-for-notifyactivity-path-select.md` — Add tests for NotifyActivity path selection and failure behavior (superseded by newer morsel: jarvis-20260303-133543-add-tests-for-notifyactivity-path-select.md)
- `jarvis-20260303-132608-retry-test-agentworkflow-pr-creation-fai.md` — Retry: Test AgentWorkflow PR creation failure path (superseded by newer morsel: jarvis-20260303-213224-retry-test-agentworkflow-pr-creation-fai.md)
- `jarvis-20260303-134047-fix-dod-failed-test-agentworkflow-pr-cre.md` — Fix dod_failed: Test AgentWorkflow PR creation failure path (ch-29f1e) (superseded by newer morsel: jarvis-20260303-213224-retry-test-agentworkflow-pr-creation-fai.md)
- `jarvis-20260303-140549-fix-unexported-useridkey-in-auth-middlew.md` — Fix unexported userIDKey in auth middleware (superseded by newer morsel: jarvis-20260303-181106-fix-unexported-useridkey-in-auth-middlew.md)
- `jarvis-20260303-143603-fix-unexported-contextkey-in-auth-middle.md` — Fix unexported contextKey in auth middleware (ch-65386 unblock) (superseded by newer morsel: jarvis-20260303-182552-fix-unexported-contextkey-in-auth-middle.md)
- `jarvis-20260303-145618-investigate-and-fix-dod-failed-ch-29f1e-.md` — Investigate and fix dod_failed ch-29f1e AgentWorkflow PR creation failure path (superseded by newer morsel: jarvis-20260303-164612-retry-dod-failed-test-agentworkflow-pr-c.md)
- `jarvis-20260303-151604-recover-ch-29f1e-test-agentworkflow-pr-c.md` — Recover ch-29f1e: Test AgentWorkflow PR creation failure path (superseded by newer morsel: jarvis-20260303-213224-retry-test-agentworkflow-pr-creation-fai.md)
- `jarvis-20260303-152139-retry-test-agentworkflow-pr-creation-fai.md` — Retry: Test AgentWorkflow PR creation failure path (superseded by newer morsel: jarvis-20260303-213224-retry-test-agentworkflow-pr-creation-fai.md)
- `jarvis-20260303-164154-fix-dod-failed-agentworkflow-pr-creation.md` — Fix dod_failed: AgentWorkflow PR creation failure path test (superseded by newer morsel: jarvis-20260303-213224-retry-test-agentworkflow-pr-creation-fai.md)
- `jarvis-20260303-164612-fix-unexported-contextkey-in-auth-middle.md` — Fix unexported contextKey in auth middleware (ch-65386) (superseded by newer morsel: jarvis-20260303-182552-fix-unexported-contextkey-in-auth-middle.md)
- `jarvis-20260303-172048-retry-ch-29f1e-test-agentworkflow-pr-cre.md` — Retry ch-29f1e: Test AgentWorkflow PR creation failure path (superseded by newer morsel: jarvis-20260303-213224-retry-test-agentworkflow-pr-creation-fai.md)
- `jarvis-20260303-173123-fix-dod-failed-test-agentworkflow-pr-cre.md` — Fix dod_failed: Test AgentWorkflow PR creation failure path (ch-29f1e) (superseded by newer morsel: jarvis-20260303-213224-retry-test-agentworkflow-pr-creation-fai.md)
- `jarvis-20260303-180111-retry-test-agentworkflow-pr-creation-fai.md` — Retry: Test AgentWorkflow PR creation failure path (ch-29f1e) (superseded by newer morsel: jarvis-20260303-213224-retry-test-agentworkflow-pr-creation-fai.md)
- `jarvis-20260303-181551-retry-ch-29f1e-test-agentworkflow-pr-cre.md` — Retry ch-29f1e: Test AgentWorkflow PR creation failure path (superseded by newer morsel: jarvis-20260303-213224-retry-test-agentworkflow-pr-creation-fai.md)
- `jarvis-20260303-183601-fix-dod-failed-test-agentworkflow-pr-cre.md` — Fix dod_failed: Test AgentWorkflow PR creation failure path (ch-29f1e) (superseded by newer morsel: jarvis-20260303-213224-retry-test-agentworkflow-pr-creation-fai.md)
- `jarvis-20260303-184554-retry-test-agentworkflow-pr-creation-fai.md` — Retry: Test AgentWorkflow PR creation failure path (superseded by newer morsel: jarvis-20260303-213224-retry-test-agentworkflow-pr-creation-fai.md)
- `jarvis-20260303-193624-fix-dod-failed-ch-29f1e-test-agentworkfl.md` — Fix dod_failed ch-29f1e: Test AgentWorkflow PR creation failure path (superseded by newer morsel: jarvis-20260303-213224-retry-test-agentworkflow-pr-creation-fai.md)
- `jarvis-20260303-202604-test-agentworkflow-pr-creation-failure-p.md` — Test AgentWorkflow PR creation failure path (retry) (superseded by newer morsel: jarvis-20260303-213224-retry-test-agentworkflow-pr-creation-fai.md)

### likely_stale_orphan_ch_ref
- `jarvis-20260303-082545-fix-task-status-not-updating-to-needs-re.md` — Fix task status not updating to needs_review after agent completes PR (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-083045-add-chum-task-update-subcommand.md` — Add chum task update subcommand (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-084654-cli-chum-review-task-task-id-for-manual-.md` — CLI: chum review --task TASK_ID for manual review trigger (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-094545-investigate-and-fix-dod-failed-on-ch-29f.md` — Investigate and fix dod_failed on ch-29f1e AgentWorkflow PR creation failure test (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-102544-investigate-and-fix-dod-failed-on-ch-29f.md` — Investigate and fix dod_failed on ch-29f1e: Test AgentWorkflow PR creation failure path (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-105051-refine-ch-29f1e-clarify-dod-and-unblock-.md` — Refine ch-29f1e: clarify DoD and unblock dod_failed state (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-110042-fix-ch-d2cbe-push-failure.md` — Fix ch-d2cbe push failure (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-110601-fix-ch-d2cbe-push-failed-on-test-dispatc.md` — Fix ch-d2cbe: push_failed on Test DispatcherWorkflow scan failure task (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-114554-recover-stuck-needs-review-tasks-ch-5587.md` — Recover stuck needs_review tasks ch-55879 and ch-79602 (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-115537-fix-ch-29f1e-test-agentworkflow-pr-creat.md` — Fix ch-29f1e: Test AgentWorkflow PR creation failure path (dod_failed) (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-115537-unstick-ch-55879-auth-middleware-needs-r.md` — Unstick ch-55879: auth middleware needs_review with no open PR (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-115537-unstick-ch-79602-ratelimit-config-needs-.md` — Unstick ch-79602: RateLimit config needs_review with no open PR (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-120622-rebase-ch-79602-onto-current-master-and-.md` — Rebase ch-79602 onto current master and open PR (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-121545-fix-unexported-contextkey-in-auth-middle.md` — Fix unexported contextKey in auth middleware (PR #44) (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-130050-investigate-and-re-queue-ch-29f1e-agentw.md` — Investigate and re-queue ch-29f1e: AgentWorkflow PR creation failure path (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-134547-fix-exported-context-key-in-auth-middlew.md` — Fix exported context key in auth middleware (PR #44) (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-144559-investigate-and-re-run-dod-failed-task-c.md` — Investigate and re-run dod_failed task ch-29f1e (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-144559-reset-zombie-task-ch-65386-to-open-state.md` — Reset zombie task ch-65386 to open state (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-164612-retry-dod-failed-test-agentworkflow-pr-c.md` — Retry dod_failed: Test AgentWorkflow PR creation failure path (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-170215-re-decompose-ch-29f1e-testagentworkflow-.md` — Re-decompose ch-29f1e: TestAgentWorkflow PR creation failure path (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-170632-retry-ch-29f1e-agentworkflow-pr-creation.md` — Retry ch-29f1e: AgentWorkflow PR creation failure test (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-172049-review-and-merge-ch-55879-implement-auth.md` — Review and merge ch-55879: Implement auth middleware (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-175557-unblock-auth-middleware-review-ch-55879.md` — Unblock auth middleware review ch-55879 (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-175558-verify-rate-limiter-task-dependency-chai.md` — Verify rate-limiter task dependency chain is correct (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-182552-fix-unexported-contextkey-in-auth-middle.md` — Fix unexported contextKey in auth middleware (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-183055-fix-unexported-contextkey-breaks-userfro.md` — Fix unexported contextKey breaks UserFromContext across packages (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-183600-fix-exported-userfromcontext-context-key.md` — Fix exported UserFromContext context key in auth middleware (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-185546-fix-dod-failed-testagentworkflow-prcreat.md` — Fix dod_failed: TestAgentWorkflow_PRCreationFailure (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-185546-pr-review-auth-middleware-ch-55879-and-r.md` — PR review: auth middleware (ch-55879) and rate limit config (ch-79602) (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-190051-fix-unexported-contextkey-type-in-auth-m.md` — Fix unexported contextKey type in auth middleware (PR #44) (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-191608-implement-ch-c8689-test-agentworkflow-me.md` — Implement ch-c8689: Test AgentWorkflow merge failure path (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-191608-implement-ch-ddd02-test-parsereposlug-an.md` — Implement ch-ddd02: Test parseRepoSlug and reviewStateToOutcome (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-191608-resolve-dod-failed-on-ch-29f1e-verify-fi.md` — Resolve dod_failed on ch-29f1e: verify fix commit 9b6395d covers PR creation failure path (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-195637-fix-dod-failed-on-ch-29f1e-testagentwork.md` — Fix dod_failed on ch-29f1e: TestAgentWorkflow_PRCreationFailure (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-210558-export-contextkey-in-auth-middleware.md` — Export contextKey in auth middleware (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-213224-retry-test-agentworkflow-pr-creation-fai.md` — Retry: Test AgentWorkflow PR creation failure path (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-220119-decompose-ch-57379-into-per-package-call.md` — Decompose ch-57379 into per-package caller updates (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-220119-fix-ch-32633-acceptance-criteria-and-unb.md` — Fix ch-32633 acceptance criteria and unblock PR creation failure test (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-235129-diagnose-and-fix-dispatcherworkflow-not-.md` — Diagnose and fix DispatcherWorkflow not starting from chum serve (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260303-235129-mark-stale-needs-review-tasks-as-done-wh.md` — Mark stale needs_review tasks as done where PRs are merged (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260304-003630-fix-chum-serve-exits-immediately-after-s.md` — Fix: chum serve exits immediately after startup instead of blocking (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260304-004707-reset-stale-needs-review-tasks-with-no-c.md` — Reset stale needs_review tasks with no commits to ready (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260304-005641-mark-tasks-done-when-their-branch-has-no.md` — Mark tasks done when their branch has no diff vs master (references ch-* IDs absent from tasks/dispatches/morsel_stages)
- `jarvis-20260304-010116-fix-orphaned-running-tasks-after-workflo.md` — Fix orphaned 'running' tasks after workflow completion (references ch-* IDs absent from tasks/dispatches/morsel_stages)

