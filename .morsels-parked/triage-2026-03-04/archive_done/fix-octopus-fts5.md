---
title: "Fix Octopus FTS5 syntax error in lesson search"
status: done # fixed in PR #21 (FTS5 metachar sanitization, empty MATCH guard, JSON repair pipeline)
priority: 0
type: task
labels:
  - whale:infrastructure
  - bug
  - octopus
estimate_minutes: 10
acceptance_criteria: |
  - ExtractLessonsActivity no longer crashes with FTS5 syntax error near "["
  - Lesson search query properly escapes special characters in search terms
  - JSON parsing of lesson extraction handles escaped characters correctly
  - go test ./internal/... passes cleanly
design: |
  1. Find the FTS5 query in ExtractLessonsActivity (likely in store or activities)
  2. Sanitize the search term to escape FTS5 special chars: [ ] ( ) * " -
  3. Fix JSON parsing to handle backslash-escaped content from LLM output
depends_on: []
---

The Octopus ContinuousLearnerWorkflow crashes when searching existing lessons
because task IDs containing brackets (e.g. "[w3-1]") are not escaped for FTS5
query syntax. Additionally, the JSON parser fails on backslash-escaped content
in LLM lesson extraction output.
