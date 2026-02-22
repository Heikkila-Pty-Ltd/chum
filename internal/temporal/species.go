package temporal

import (
	"path/filepath"
	"strings"
)

// classifySpecies assigns a task to a species based on heuristics.
// Species are global (not project-scoped) so knowledge cross-pollinates.
// Tier 1: bootstrap heuristics. Tier 2: paleontologist refines over time.
func classifySpecies(taskID, prompt string, files []string) string {
	// Check file extensions for dominant language
	lang := detectDominantLanguage(files)
	if lang != "" {
		// Check for task type hints in the prompt/taskID
		taskType := detectTaskType(taskID, prompt)
		if taskType != "" {
			return lang + "-" + taskType
		}
		return lang + "-general"
	}

	// No file hints — use prompt/taskID keywords
	taskType := detectTaskType(taskID, prompt)
	if taskType != "" {
		return taskType
	}

	// Fallback — species emerge from the fossil record, not from us
	return "general"
}

// detectDominantLanguage returns a language identifier from file extensions.
func detectDominantLanguage(files []string) string {
	counts := map[string]int{}
	for _, f := range files {
		ext := strings.TrimPrefix(filepath.Ext(f), ".")
		switch ext {
		case "go":
			counts["go"]++
		case "tsx", "jsx":
			counts["react"]++
		case "ts", "js":
			counts["js"]++
		case "py":
			counts["python"]++
		case "rs":
			counts["rust"]++
		case "toml", "yaml", "yml", "json":
			counts["config"]++
		case "md", "txt":
			counts["docs"]++
		case "sql":
			counts["sql"]++
		case "sh", "bash":
			counts["shell"]++
		case "css", "scss":
			counts["css"]++
		case "html":
			counts["html"]++
		}
	}

	// Find dominant
	best := ""
	bestCount := 0
	for lang, count := range counts {
		if count > bestCount {
			best = lang
			bestCount = count
		}
	}
	return best
}

// detectTaskType extracts task type hints from taskID and prompt.
func detectTaskType(taskID, prompt string) string {
	combined := strings.ToLower(taskID + " " + prompt)

	// Ordered by specificity
	patterns := []struct {
		keywords []string
		taskType string
	}{
		{[]string{"test", "spec"}, "test"},
		{[]string{"lint", "vet", "golangci"}, "lint"},
		{[]string{"refactor", "rename", "extract"}, "refactor"},
		{[]string{"fix", "bug", "error", "crash"}, "bugfix"},
		{[]string{"migrate", "migration", "schema"}, "migration"},
		{[]string{"component", "widget", "ui"}, "component"},
		{[]string{"api", "endpoint", "route", "handler"}, "api"},
		{[]string{"deploy", "ci", "cd", "pipeline"}, "devops"},
		{[]string{"doc", "readme", "comment"}, "docs"},
		{[]string{"security", "auth", "token", "cve"}, "security"},
	}

	for _, p := range patterns {
		for _, kw := range p.keywords {
			if strings.Contains(combined, kw) {
				return p.taskType
			}
		}
	}
	return ""
}
