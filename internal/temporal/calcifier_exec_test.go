package temporal

import (
	"testing"
)

func TestCompareOutputs(t *testing.T) {
	tests := []struct {
		name     string
		a, b     string
		expected bool
	}{
		{"exact match", "hello world", "hello world", true},
		{"trailing whitespace", "hello world\n", "hello world", true},
		{"windows line endings", "line1\r\nline2\r\n", "line1\nline2\n", true},
		{"different content", "hello", "world", false},
		{"empty vs content", "", "something", false},
		{"both empty", "", "", true},
		{"leading whitespace", "  hello  ", "hello", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := compareOutputs(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("compareOutputs(%q, %q) = %v, want %v", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

func TestExtractScriptContent(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			"markdown fenced",
			"```python\n#!/usr/bin/env python3\nprint('hello')\n```",
			"#!/usr/bin/env python3\nprint('hello')",
		},
		{
			"raw shebang",
			"Here's the script:\n#!/usr/bin/env python3\nprint('hello')",
			"#!/usr/bin/env python3\nprint('hello')",
		},
		{
			"just content",
			"print('hello')",
			"print('hello')",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractScriptContent(tt.input)
			if result != tt.expect {
				t.Errorf("extractScriptContent = %q, want %q", result, tt.expect)
			}
		})
	}
}

func TestDetectScriptLanguage(t *testing.T) {
	tests := []struct {
		name    string
		content string
		lang    string
		ext     string
	}{
		{"python shebang", "#!/usr/bin/env python3\nprint('hi')", "python", "py"},
		{"bash shebang", "#!/usr/bin/env bash\necho hi", "bash", "sh"},
		{"sh shebang", "#!/bin/sh\necho hi", "bash", "sh"},
		{"no shebang", "import sys\nprint('hi')", "python", "py"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lang, ext := detectScriptLanguage(tt.content)
			if lang != tt.lang || ext != tt.ext {
				t.Errorf("detectScriptLanguage = (%q, %q), want (%q, %q)", lang, ext, tt.lang, tt.ext)
			}
		})
	}
}
