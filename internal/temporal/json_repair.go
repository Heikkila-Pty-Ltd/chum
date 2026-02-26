package temporal

import "encoding/json"

// repairTruncatedJSONArray attempts to fix truncated JSON array output from LLMs.
// When an LLM hits its output token limit, it often produces:
//
//	[{"key": "value"}, {"key": "val
//
// This function tries to close unclosed braces/brackets to make it parseable.
func repairTruncatedJSONArray(s string) string {
	if json.Valid([]byte(s)) {
		return s // already valid
	}

	// Count unclosed braces and brackets
	openBraces := 0
	openBrackets := 0
	inString := false
	escaped := false

	for _, r := range s {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inString {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch r {
		case '{':
			openBraces++
		case '}':
			openBraces--
		case '[':
			openBrackets++
		case ']':
			openBrackets--
		}
	}

	// If we're inside a string, close it first
	repaired := s
	if inString {
		repaired += `"`
	}

	// Try progressively: close the deepest unclosed structure first
	// Strategy: find the last comma and truncate there, then close
	if lastComma := lastTopLevelComma(repaired); lastComma > 0 && (openBraces > 0 || openBrackets > 0) {
		// Truncate at the last complete element
		truncated := repaired[:lastComma]
		// Close remaining brackets
		for i := 0; i < openBrackets; i++ {
			truncated += "]"
		}
		if json.Valid([]byte(truncated)) {
			return truncated
		}
	}

	// Fallback: just close everything that's open
	for openBraces > 0 {
		repaired += "}"
		openBraces--
	}
	for openBrackets > 0 {
		repaired += "]"
		openBrackets--
	}

	if json.Valid([]byte(repaired)) {
		return repaired
	}

	return s // give up, return original
}

// lastTopLevelComma finds the position of the last comma at array level (depth 1).
// This helps us truncate at the boundary of the last complete array element.
func lastTopLevelComma(s string) int {
	depth := 0
	inString := false
	escaped := false
	lastComma := -1

	for i, r := range s {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inString {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		switch r {
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		case ',':
			if depth == 1 { // array level (inside the outer [])
				lastComma = i
			}
		}
	}
	return lastComma
}

// extractFirstCompleteJSONObject extracts the first complete {...} object from text.
// Used as a last resort when JSON array repair fails — at least save one lesson.
func extractFirstCompleteJSONObject(s string) string {
	start := -1
	depth := 0
	inString := false
	escaped := false

	for i, r := range s {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inString {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if r == '{' {
			if start == -1 {
				start = i
			}
			depth++
		} else if r == '}' {
			depth--
			if depth == 0 && start >= 0 {
				candidate := s[start : i+1]
				if json.Valid([]byte(candidate)) {
					return candidate
				}
				// Not valid — reset and try next object
				start = -1
			}
		}
	}
	return ""
}
