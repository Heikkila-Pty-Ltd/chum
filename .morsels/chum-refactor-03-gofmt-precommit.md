---
title: "Format all Go files and add gofmt pre-commit hook"
status: ready
priority: 1
type: maintenance
labels:
  - technical-debt
  - code-quality
  - self-healing
estimate_minutes: 15
acceptance_criteria: |
  - All Go files are formatted: gofmt -l . returns empty
  - Pre-commit hook installed in .git/hooks/pre-commit
  - Hook prevents commits with unformatted code
  - Documentation updated in CONTRIBUTING.md about formatting
  - go build ./... succeeds after formatting
design: |
  **Problem:** 20 Go files are unformatted, creating unnecessary diff noise and inconsistency.

  **Solution:**
  1. Run `gofmt -w .` to format all files
  2. Create `.git/hooks/pre-commit` script that runs `gofmt -l .` and fails if output is non-empty
  3. Make hook executable: `chmod +x .git/hooks/pre-commit`
  4. Update CONTRIBUTING.md to document the formatting requirement

  **Pre-commit hook content:**
  ```bash
  #!/bin/bash
  # Pre-commit hook: verify all Go files are formatted

  UNFORMATTED=$(gofmt -l .)
  if [ -n "$UNFORMATTED" ]; then
    echo "❌ Go files must be formatted. Run: gofmt -w ."
    echo "Unformatted files:"
    echo "$UNFORMATTED"
    exit 1
  fi

  echo "✅ All Go files properly formatted"
  exit 0
  ```

  **Steps:**
  1. Run `gofmt -w .` in project root
  2. Verify no functional changes: `git diff` should only show whitespace
  3. Create `.git/hooks/pre-commit` with above content
  4. Test hook: make a formatting error, try to commit, verify it blocks
  5. Update CONTRIBUTING.md with formatting section
  6. Commit changes

  **Note:** This is a quick win that improves code quality with minimal risk.
---

# Format All Go Files + Pre-commit Hook

20 files are unformatted. Fix them and prevent future formatting drift with a pre-commit hook.

Quick win - 15 minutes to fix a persistent quality issue.
