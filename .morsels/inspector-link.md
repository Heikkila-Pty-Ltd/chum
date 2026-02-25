---
title: "LinkInspector — Full-Site Crawl for Broken Routes"
status: ready
priority: 2
type: task
labels:
  - whale:inspector
  - links
  - crawl
estimate_minutes: 60
acceptance_criteria: |
  - Recursively crawls from root URL following all <a href> tags
  - Reports HTTP 404s, 5xx errors, redirect chains (>2 hops)
  - Identifies orphaned pages (pages with no inbound links)
  - Checks for missing <title> tags and empty <h1> elements
  - Validates sitemap.xml entries resolve correctly
  - Structured JSON report emitted as morsels with label `inspector:link`
design: |
  1. Create `internal/inspector/link.go` with HTTP crawl activity
  2. Add `CrawlSiteActivity` — BFS/DFS crawl with configurable depth
  3. Add `SitemapValidationActivity` — parse and verify sitemap.xml
  4. Wire into `InspectorWorkflow`
depends_on: ["inspector-whale"]
---

Full-site link crawler that catches 404s, redirect chains, and orphaned pages.
