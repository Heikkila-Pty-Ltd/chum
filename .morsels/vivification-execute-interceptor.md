---
title: "Wire ExecuteActivity interceptor for calcified scripts"
status: ready
priority: 1
type: task
labels:
  - calcifier
  - interceptor
  - margin-protection
estimate_minutes: 90
acceptance_criteria: |
  - Active calcified scripts bypass LLM entirely (exit code 0 = success, non-zero = quarantine)
  - SHA-256 integrity check passes before script execution
  - Shadow scripts run concurrently with LLM, outputs compared, match/failure counts updated in store
  - Shadow scripts promote to active after shadow_matches >= promote_threshold (3)
  - Script failure triggers QuarantineAndRewireActivity fallback
  - go build ./... clean, existing tests pass
design: |
  Modify ExecuteActivity in activities.go:
  1. Before LLM dispatch, call store.GetActiveScriptForType(morselType)
  2. If active script found:
     a. verifyScriptIntegrity(path, sha256) — reject tampered scripts
     b. execCalcifiedScript(ctx, path, prompt) — run with 5min timeout
     c. If exit 0: return output as ExecutionResult, skip LLM entirely
     d. If non-zero: call QuarantineAndRewireActivity, fall through to LLM
  3. If no active script, check store.GetShadowScriptForType(morselType)
  4. If shadow script found:
     a. Run LLM dispatch normally
     b. Concurrently run execCalcifiedScript on shadow script
     c. compareOutputs(llmOutput, scriptOutput)
     d. If match: store.UpdateScriptShadowCounts(id, 1, 0)
     e. If shadow_matches >= promote_threshold: PromoteCalcifiedScriptActivity
     f. If mismatch: store.UpdateScriptShadowCounts(id, 0, 1)
  5. Falls through to normal LLM dispatch if no scripts exist
depends_on: []
---

Wire the runtime interceptor into `ExecuteActivity` so calcified scripts actually bypass the LLM at execution time. This is the core margin-protection mechanism — every active script means $0 inference cost for that morsel type.

The shadow validation path runs both LLM and script concurrently, comparing outputs. After 3 consecutive matches, the script is promoted and the LLM is fired for that task type permanently.
