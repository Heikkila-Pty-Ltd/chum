package temporal

import (
	"fmt"
	"strings"
)

// ParseMarkdownPlan performs deterministic, line-by-line parsing of a
// semi-structured markdown plan into a ParsedPlan. It expects the format
// produced by the CHUM Crab agent:
//
//	# Plan: <title>
//	## Context
//	<body text>
//	## Scope
//	- [ ] <deliverable>
//	## Acceptance Criteria
//	- <criterion>
//	## Out of Scope
//	- <item>
//
// The parser is forgiving: unknown sections are ignored, optional sections
// (Acceptance Criteria, Out of Scope) may be absent, and whitespace is
// trimmed throughout. Validation requires a non-empty title and at least
// one scope item.
func ParseMarkdownPlan(markdown string) (*ParsedPlan, error) {
	plan := &ParsedPlan{
		RawMarkdown: markdown,
	}

	var (
		currentSection string
		contextLines   []string
		scopeIndex     int
	)

	lines := strings.Split(markdown, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Detect the plan title: "# Plan: <title>"
		if plan.Title == "" && isTitleLine(trimmed) {
			plan.Title = extractTitle(trimmed)
			continue
		}

		// Detect section headers: "## <section>"
		if strings.HasPrefix(trimmed, "## ") {
			section := strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
			currentSection = strings.ToLower(section)
			continue
		}

		// Skip blank lines (but preserve them in context for multiline text)
		if trimmed == "" {
			if currentSection == "context" {
				contextLines = append(contextLines, "")
			}
			continue
		}

		switch currentSection {
		case "context":
			contextLines = append(contextLines, trimmed)

		case "scope":
			item, ok := parseScopeItem(trimmed, scopeIndex)
			if ok {
				plan.ScopeItems = append(plan.ScopeItems, item)
				scopeIndex++
			}

		case "acceptance criteria":
			if bullet, ok := parseBullet(trimmed); ok {
				plan.AcceptanceCriteria = append(plan.AcceptanceCriteria, bullet)
			}

		case "out of scope":
			if bullet, ok := parseBullet(trimmed); ok {
				plan.OutOfScope = append(plan.OutOfScope, bullet)
			}

			// Unknown sections are silently ignored.
		}
	}

	// Assemble context from collected lines, trimming leading/trailing blanks.
	plan.Context = strings.TrimSpace(strings.Join(contextLines, "\n"))

	// Validation.
	if err := validateParsedPlan(plan); err != nil {
		return nil, err
	}

	return plan, nil
}

// isTitleLine checks whether a line is a plan title header.
// Accepts "# Plan:<title>" with or without a space after the colon.
func isTitleLine(line string) bool {
	return strings.HasPrefix(line, "# Plan:") || strings.HasPrefix(line, "# Plan: ")
}

// extractTitle pulls the title text from a "# Plan: <title>" line.
func extractTitle(line string) string {
	after := strings.TrimPrefix(line, "# Plan:")
	return strings.TrimSpace(after)
}

// parseScopeItem attempts to parse a scope checkbox line.
// Supported formats:
//
//   - [ ] description   (uncompleted)
//   - [x] description   (completed, case-insensitive)
//   - [ ] description   (asterisk variant, uncompleted)
//   - [x] description   (asterisk variant, completed)
func parseScopeItem(line string, index int) (ScopeItem, bool) {
	// Normalize leading bullet: accept both "- " and "* "
	var rest string
	if strings.HasPrefix(line, "- ") {
		rest = strings.TrimPrefix(line, "- ")
	} else if strings.HasPrefix(line, "* ") {
		rest = strings.TrimPrefix(line, "* ")
	} else {
		return ScopeItem{}, false
	}

	// Expect a checkbox: "[ ]" or "[x]" / "[X]"
	completed := false
	if strings.HasPrefix(rest, "[ ] ") || rest == "[ ]" {
		rest = strings.TrimPrefix(rest, "[ ]")
	} else if strings.HasPrefix(rest, "[x] ") || rest == "[x]" ||
		strings.HasPrefix(rest, "[X] ") || rest == "[X]" {
		completed = true
		// Strip whichever variant matched.
		if strings.HasPrefix(rest, "[x] ") {
			rest = strings.TrimPrefix(rest, "[x]")
		} else {
			rest = strings.TrimPrefix(rest, "[X]")
		}
	} else {
		return ScopeItem{}, false
	}

	desc := strings.TrimSpace(rest)
	if desc == "" {
		return ScopeItem{}, false
	}

	return ScopeItem{
		Index:       index,
		Description: desc,
		Completed:   completed,
	}, true
}

// parseBullet extracts the text from a "- <text>" bullet line.
func parseBullet(line string) (string, bool) {
	if strings.HasPrefix(line, "- ") {
		text := strings.TrimSpace(strings.TrimPrefix(line, "- "))
		if text != "" {
			return text, true
		}
	}
	if strings.HasPrefix(line, "* ") {
		text := strings.TrimSpace(strings.TrimPrefix(line, "* "))
		if text != "" {
			return text, true
		}
	}
	return "", false
}

// validateParsedPlan enforces mandatory fields on a parsed plan.
func validateParsedPlan(plan *ParsedPlan) error {
	if strings.TrimSpace(plan.Title) == "" {
		return fmt.Errorf("parsed plan has no title: expected a '# Plan: <title>' header")
	}
	if len(plan.ScopeItems) == 0 {
		return fmt.Errorf("parsed plan has no scope items: at least one '- [ ] <deliverable>' is required")
	}
	return nil
}
