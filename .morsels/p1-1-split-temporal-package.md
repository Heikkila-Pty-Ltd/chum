---
title: "Split internal/temporal into temporal, activity, and agent packages"
status: open
priority: 1
type: refactor
labels:
  - architecture
  - refactor
  - dogfood
estimate_minutes: 120
acceptance_criteria: |
  - internal/temporal/ contains ONLY workflow definitions (workflow.go, workflow_*.go) and worker.go
  - internal/activity/ contains ALL activity implementations (*_activities.go, activities.go)
  - internal/agent/ contains agent CLI builders (agent_cli.go) and output parsers (agent_parsers.go)
  - go build ./... passes with zero errors
  - go test ./... passes with zero failures
  - go vet ./... reports zero issues
  - No circular import dependencies between the three packages
  - worker.go correctly imports and registers activities from internal/activity/
  - All existing tests continue to pass without modification (or with minimal import path updates)
design: |
  ## Current State
  internal/temporal/ is a 9,721 LOC god package with fan-out 5 (config, dispatch, git, graph, store).
  It contains workflows, activities, CLI builders, parsers, and types all in one package.

  ## Target State
  Split into three focused packages:

  ### internal/temporal/ (workflows + worker)
  Keep: workflow.go, workflow_groom.go, workflow_learner.go, planning_workflow.go,
        workflow_dispatcher.go, worker.go, constants.go, types.go

  ### internal/activity/ (all activities)
  Move: activities.go, groom_activities.go, learner_activities.go,
        planning_activities.go, and the Activities/DispatchActivities structs

  ### internal/agent/ (CLI + parsers)
  Move: agent_cli.go, agent_parsers.go

  ## Approach
  1. Create internal/activity/ and internal/agent/ directories
  2. Move files, update package declarations
  3. Update import paths in workflow files and worker.go
  4. Update worker.go to import and register activities from the new packages
  5. Run go build, go test, go vet to verify
  6. Fix any circular dependency issues

  ## Risk
  Medium — the split is mechanical but circular imports could surface if activities
  reference workflow types or vice versa. The types.go file may need to stay in
  temporal/ or move to a shared types package.
