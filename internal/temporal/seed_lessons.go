package temporal

import (
	"log/slog"

	"github.com/antigravity-dev/chum/internal/store"
)

// SeedLessons contains hardcoded lessons from operational incidents.
// These are "octopus lessons" — patterns discovered through debugging that
// should be available to all future dispatches via the CLAUDE.md synthesis loop.
//
// Category taxonomy:
//   - "antipattern":  something that looks right but breaks at runtime
//   - "gotcha":       a non-obvious behavior that causes confusion
//   - "rule":         a rule that must always be followed
//   - "pattern":      a proven solution pattern
var SeedLessons = []store.StoredLesson{
	// === JSON Parsing: LLM Output ===
	{
		MorselID: "seed-json-parsing-001",
		Project:  "chum",
		Category: "gotcha",
		Summary:  "LLMs produce literal newlines inside JSON string values",
		Detail: `LLMs (Claude, Codex, Gemini) frequently produce JSON responses
with real newline characters (0x0A) inside string values, e.g.:
  {"approach": "Step 1: do X\nStep 2: do Y"}
This causes json.Unmarshal to fail with 'invalid character '\n' in string literal'.

Fix: Use a string-aware char-by-char walker that tracks whether we're inside
a JSON string and escapes literal control chars (\n→\\n, \r→\\r, \t→\\t).
See: sanitizeLLMJSON() in planning_activities.go`,
		FilePaths: []string{
			"internal/temporal/planning_activities.go",
			"internal/temporal/activities.go",
		},
		Labels: []string{"json-parsing", "llm-output", "sanitizer"},
	},
	{
		MorselID: "seed-json-parsing-002",
		Project:  "chum",
		Category: "antipattern",
		Summary:  "Gemini -o json wraps model output in an envelope — must extract response field",
		Detail: `When using 'gemini -o json', the model's output is wrapped in a JSON envelope:
  {"session_id":"...","response":"<escaped model output>","stats":{...}}

The actual model content is inside the "response" field as an ESCAPED STRING.
If parseGeminiOutput doesn't extract this field, downstream extractJSON finds
the outer envelope object instead of the model's actual JSON response.

Root cause of the Gemini 'unexpected end of JSON input' error: extractJSON
matched the outer {session_id...} object, which doesn't match the expected struct.

Fix: In parseGeminiOutput, parse the envelope and set CLIResult.Output = response.
See: agent_parsers.go parseGeminiOutput()`,
		FilePaths: []string{
			"internal/temporal/agent_parsers.go",
			"internal/temporal/agent_cli.go",
		},
		Labels: []string{"json-parsing", "gemini", "cli-envelope", "antipattern"},
	},
	{
		MorselID: "seed-json-parsing-003",
		Project:  "chum",
		Category: "gotcha",
		Summary:  "CLI agents with JSON output mode wrap content differently — each needs its own parser",
		Detail: `Each agent CLI has a different JSON output format:
- Claude (--output-format json): {"result":"<content>","session_id":"...","cost_usd":...}
- Codex (--json): JSONL events, last line has the final output
- Gemini (-o json): {"session_id":"...","response":"<content>","stats":{...}}

The parseAgentOutput() function must route to the correct parser.
If a new agent is added, its JSON envelope must be handled in a new parser,
or the raw model content will be lost inside the envelope.

See: agent_parsers.go parseAgentOutput(), parseGeminiOutput(), parseCodexOutput()`,
		FilePaths: []string{
			"internal/temporal/agent_parsers.go",
		},
		Labels: []string{"json-parsing", "cli-envelope", "multi-agent"},
	},
	{
		MorselID: "seed-json-parsing-004",
		Project:  "chum",
		Category: "pattern",
		Summary:  "Use progressive sanitization with json.Valid checkpoints",
		Detail: `When sanitizing LLM JSON output, apply fixes progressively from least to most aggressive,
checking json.Valid after each phase:
  Phase 1: json.Valid fast path (no-op if already valid)
  Phase 2: Fix double-escaped sequences (\\\\n → \\n)
  Phase 3: fixJSONChars — control chars inside strings + stray backslashes outside strings
  Phase 4: nukeInvalidBackslashes — strip ALL non-standard backslash sequences

This ensures the least destructive fix is applied. Phase 4 (nuclear) only fires
when all gentler phases fail, avoiding data loss from over-aggressive sanitization.

See: sanitizeLLMJSON() in planning_activities.go`,
		FilePaths: []string{
			"internal/temporal/planning_activities.go",
		},
		Labels: []string{"json-parsing", "sanitizer", "pattern", "progressive"},
	},
	{
		MorselID: "seed-json-parsing-005",
		Project:  "chum",
		Category: "pattern",
		Summary:  "robustParseJSON tries 4 extraction strategies before giving up",
		Detail: `When extractJSON fails to produce parseable JSON, the issue is often ordering:
sanitization changes the content which shifts brace positions in extractJSON's depth counter.

robustParseJSON tries 4 strategies:
  1. extractJSON → sanitizeLLMJSON → unmarshal (standard)
  2. sanitizeLLMJSON → extractJSON → unmarshal (sanitize-first fixes brace confusion)
  3. nukeInvalidBackslashes → extractJSON → unmarshal (aggressive cleanup first)
  4. Code-fence-only extraction (bypass brace matching entirely)

Strategy 2 is the key insight: sanitizing BEFORE extraction fixes cases where
stray backslashes confuse extractJSON's brace-depth counting.

See: robustParseJSON() in planning_activities.go`,
		FilePaths: []string{
			"internal/temporal/planning_activities.go",
			"internal/temporal/turtle_activities.go",
		},
		Labels: []string{"json-parsing", "extraction", "pattern", "resilience"},
	},
}

// SeedLessonsIfNeeded stores seed lessons into the database if they
// don't already exist (checked by morsel_id prefix "seed-").
func SeedLessonsIfNeeded(st *store.Store, logger *slog.Logger) {
	if st == nil {
		return
	}

	for _, lesson := range SeedLessons {
		existing, _ := st.GetLessonsByMorsel(lesson.MorselID)
		if len(existing) > 0 {
			continue // already seeded
		}

		id, err := st.StoreLesson(
			lesson.MorselID,
			lesson.Project,
			lesson.Category,
			lesson.Summary,
			lesson.Detail,
			lesson.FilePaths,
			lesson.Labels,
			lesson.SemgrepRuleID,
		)
		if err != nil {
			if logger != nil {
				logger.Warn("failed to seed lesson", "morsel", lesson.MorselID, "error", err)
			}
			continue
		}
		if logger != nil {
			logger.Info("seeded octopus lesson", "id", id, "summary", lesson.Summary)
		}
	}
}
