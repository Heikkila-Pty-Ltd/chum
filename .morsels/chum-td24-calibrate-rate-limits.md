---
title: "Calibrate rate limiter caps to actual provider token limits"
status: ready
priority: 4
type: task
labels:
  - whale:infrastructure
  - observability
estimate_minutes: 30
acceptance_criteria: |
  - Snapshot current provider usage % from user (Gemini, Codex, Claude dashboards).
  - Let system dispatch for 4-6 hours.
  - Snapshot usage % again.
  - Calculate: tokens consumed per dispatch = (delta tokens) / (dispatch count in window).
  - Derive: max safe dispatches = (remaining tokens) / (avg tokens per dispatch).
  - Update chum.toml [rate_limits] with calibrated caps.
  - Consider per-provider budgets using [rate_limits.budget] map.
design: |
  **Phase 1:** Ask human for current limits snapshot:
  - Run `gemini` TUI → record % remaining per model
  - Check OpenAI dashboard → record codex remaining
  - Check Anthropic dashboard → record claude remaining
  
  **Phase 2:** Let CHUM run 4-6 hours, record:
  - `SELECT provider, COUNT(*), SUM(output_tokens) FROM dispatches WHERE dispatched_at > datetime('now', '-6 hours') GROUP BY provider`
  
  **Phase 3:** Ask human for new snapshot, compute delta, set caps.
  
  **Stretch:** Look at Gemini API for programmatic quota checking:
  - https://generativelanguage.googleapis.com/v1beta/models — may expose rate limit headers
  - OpenAI: response headers include `x-ratelimit-remaining-tokens`
  - File a follow-up morsel for auto-querying these headers
depends_on: []
---

Calibrate rate limiter caps by measuring actual token burn rate per dispatch
and mapping to real provider limits. Currently caps are set absurdly high
because they count dispatch events, not tokens.
