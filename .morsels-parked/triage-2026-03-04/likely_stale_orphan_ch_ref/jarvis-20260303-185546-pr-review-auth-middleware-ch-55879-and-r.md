---
title: "PR review: auth middleware (ch-55879) and rate limit config (ch-79602)"
status: ready
priority: 1
type: feature
labels:
  - jarvis-initiative
  - chum
estimate_minutes: 30
acceptance_criteria: |
  - Task completed as described
design: |
    Both ch-55879 and ch-79602 are at needs_review. Review the open PRs for these two tasks. For ch-55879: check that RequireAuth extracts session token from both Cookie 'session' and Authorization Bearer, calls ValidateSession, returns 401 JSON on failure, and sets user_id via context.WithValue. Check UserFromContext helper exists. For ch-79602: check RateLimit and RateLimitRule structs, CleanupInterval field, TOML tag rate_limit, and sensible defaults in Load(). Approve or request changes. Acceptance criteria: both PRs either merged or have specific actionable review comments.
depends_on: []
---

Both ch-55879 and ch-79602 are at needs_review. Review the open PRs for these two tasks. For ch-55879: check that RequireAuth extracts session token from both Cookie 'session' and Authorization Bearer, calls ValidateSession, returns 401 JSON on failure, and sets user_id via context.WithValue. Check UserFromContext helper exists. For ch-79602: check RateLimit and RateLimitRule structs, CleanupInterval field, TOML tag rate_limit, and sensible defaults in Load(). Approve or request changes. Acceptance criteria: both PRs either merged or have specific actionable review comments.

_Created by Jarvis at 2026-03-03T18:55:46Z_
