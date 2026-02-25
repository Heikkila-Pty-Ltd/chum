#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

echo "[cutover-smoke] validating planning timeout and stale-session guard defaults"
go test ./internal/config -run 'TestDispatchCostControlPlanningTimeoutDefaults|TestDispatchCostControlPlanningStaleThresholdMustExceedSessionTimeout' -count=1

echo "[cutover-smoke] validating planning ceremony happy path, rebound path, and timeout protection"
go test ./internal/temporal -run 'TestPlanningWorkflowEmitsAdaptiveCeremonyTraceEvents|TestPlanningWorkflowTreatsCrabEscalationAsValidRebound|TestPlanningWorkflowTimesOutWaitingForSelection' -count=1

echo "[cutover-smoke] validating dispatcher planning queue behavior"
go test ./internal/temporal -run 'TestDispatcherBypassesPlanningForCrabEmittedMorsel|TestDispatcherDefersPlanningWhilePlanningSessionIsRunning|TestDispatcherStartsAtMostOnePlanningCeremonyPerTick|TestSeededPlanningRequestIncludesTimeouts|TestIsStalePlanningWorkflow' -count=1

echo "[cutover-smoke] validating control-channel prompt interpretation for timeout state"
go test ./internal/api -run 'TestBuildPlanningPromptResponseSelectingPhase|TestBuildPlanningPromptResponseGreenlightPhase|TestBuildPlanningPromptResponseTimedOutPhase' -count=1

echo "[cutover-smoke] PASS"
