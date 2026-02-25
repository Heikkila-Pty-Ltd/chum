---
title: "DataInspector — API Probe + Schema Validation"
status: ready
priority: 2
type: task
labels:
  - whale:inspector
  - data
  - supabase
estimate_minutes: 90
acceptance_criteria: |
  - Probes Supabase API for all rows in target tables (courses, countries)
  - Validates required fields populated (no NULLs in non-nullable columns)
  - HEAD-checks all URL fields (website, booking_url, image_url) for HTTP 2xx
  - Validates phone numbers via libphonenumber regex patterns
  - Validates lat/lng coordinates fall within declared country bounding box
  - Cross-references course names against Google Places API (optional, gated by env var)
  - Flags synthetic patterns: uniform par, templated URLs, AI prose signatures
  - Structured JSON findings emitted as morsels with label `inspector:data`
design: |
  1. Create `internal/inspector/data.go` with Supabase query activity
  2. Add `DataValidationActivity` — schema checks, URL probes, geo validation
  3. Add `SyntheticDataDetectionActivity` — heuristic AI-content detection
  4. Wire into `InspectorWorkflow` as a parallel branch
depends_on: ["inspector-whale"]
---

Validates data completeness, authenticity, and catches synthetic/placeholder content.
