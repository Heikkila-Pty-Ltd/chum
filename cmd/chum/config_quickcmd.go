package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

var (
	tableHeaderRe       = regexp.MustCompile(`^\s*\[([^\]]+)\]\s*$`)
	modelAssignRe       = regexp.MustCompile(`^(\s*model\s*=\s*")([^"]*)(".*)$`)
	tickIntervalAssignRe = regexp.MustCompile(`^(\s*tick_interval\s*=\s*")([^"]*)(".*)$`)
	tierAssignRe        = regexp.MustCompile(`^(\s*)(fast|balanced|premium)(\s*=\s*)\[(.*)\](\s*)$`)
	quotedStringRe      = regexp.MustCompile(`"([^"]+)"`)
)

func disableAnthropicInConfigFile(path, fallbackModel string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read config %s: %w", path, err)
	}

	updated, changed := disableAnthropicInConfigContent(string(raw), fallbackModel)
	if !changed {
		return false, nil
	}

	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return false, fmt.Errorf("write config %s: %w", path, err)
	}
	return true, nil
}

func setTickIntervalInConfigFile(path, tickInterval string) (bool, error) {
	interval := strings.TrimSpace(tickInterval)
	if interval == "" {
		return false, fmt.Errorf("tick interval is required")
	}
	if _, err := time.ParseDuration(interval); err != nil {
		return false, fmt.Errorf("invalid tick interval %q: %w", interval, err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read config %s: %w", path, err)
	}

	updated, changed, err := setTickIntervalInConfigContent(string(raw), interval)
	if err != nil {
		return false, fmt.Errorf("update tick interval in %s: %w", path, err)
	}
	if !changed {
		return false, nil
	}

	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return false, fmt.Errorf("write config %s: %w", path, err)
	}
	return true, nil
}

func setTickIntervalInConfigContent(input, tickInterval string) (output string, changed bool, err error) {
	interval := strings.TrimSpace(tickInterval)
	if interval == "" {
		return input, false, fmt.Errorf("tick interval is required")
	}
	if _, parseErr := time.ParseDuration(interval); parseErr != nil {
		return input, false, fmt.Errorf("invalid tick interval %q: %w", interval, parseErr)
	}
	if strings.TrimSpace(input) == "" {
		return input, false, fmt.Errorf("config content is empty")
	}

	lines := strings.Split(input, "\n")
	currentTable := ""
	changed = false
	found := false

	for i, line := range lines {
		if header, ok := parseTableHeader(line); ok {
			currentTable = strings.ToLower(strings.TrimSpace(header))
		}
		if currentTable != "general" {
			continue
		}
		m := tickIntervalAssignRe.FindStringSubmatch(line)
		if len(m) != 4 {
			continue
		}
		found = true
		updated := m[1] + interval + m[3]
		if updated != line {
			lines[i] = updated
			changed = true
		}
	}

	if !found {
		return input, false, fmt.Errorf("[general] tick_interval not found")
	}

	output = strings.Join(lines, "\n")
	if strings.HasSuffix(input, "\n") && !strings.HasSuffix(output, "\n") {
		output += "\n"
	}

	return output, changed, nil
}

func disableAnthropicInConfigContent(input, fallbackModel string) (string, bool) {
	if strings.TrimSpace(input) == "" {
		return input, false
	}

	lines := strings.Split(input, "\n")
	skip := make([]bool, len(lines))
	changed := false

	type tableBlock struct {
		header string
		start  int
		end    int // exclusive
	}

	var blocks []tableBlock
	for i := 0; i < len(lines); i++ {
		header, ok := parseTableHeader(lines[i])
		if !ok {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if _, nextIsHeader := parseTableHeader(lines[j]); nextIsHeader {
				end = j
				break
			}
		}
		blocks = append(blocks, tableBlock{
			header: header,
			start:  i,
			end:    end,
		})
	}

	for _, block := range blocks {
		if shouldRemoveTable(block.header, lines[block.start:block.end]) {
			for i := block.start; i < block.end; i++ {
				skip[i] = true
			}
			changed = true
		}
	}

	currentTable := ""
	outLines := make([]string, 0, len(lines))
	for i, line := range lines {
		if skip[i] {
			continue
		}

		if header, ok := parseTableHeader(line); ok {
			currentTable = strings.ToLower(strings.TrimSpace(header))
		}

		updated := line
		if currentTable == "tiers" {
			trimmed := trimAnthropicTierValues(updated)
			if trimmed != updated {
				updated = trimmed
				changed = true
			}
		}
		if currentTable == "chief" {
			replaced := replaceAnthropicChiefModel(updated, fallbackModel)
			if replaced != updated {
				updated = replaced
				changed = true
			}
		}

		outLines = append(outLines, updated)
	}

	// Collapse only oversized blank runs to keep file readable after table removals.
	outLines = collapseBlankRuns(outLines, 2)

	output := strings.Join(outLines, "\n")
	if strings.HasSuffix(input, "\n") && !strings.HasSuffix(output, "\n") {
		output += "\n"
	}

	return output, changed
}

func parseTableHeader(line string) (string, bool) {
	m := tableHeaderRe.FindStringSubmatch(line)
	if len(m) != 2 {
		return "", false
	}
	return strings.TrimSpace(m[1]), true
}

func shouldRemoveTable(header string, blockLines []string) bool {
	lowerHeader := strings.ToLower(strings.TrimSpace(header))
	if lowerHeader == "dispatch.cli.claude" {
		return true
	}
	if !strings.HasPrefix(lowerHeader, "providers.") {
		return false
	}

	providerID := strings.TrimPrefix(lowerHeader, "providers.")
	if looksAnthropic(providerID) {
		return true
	}

	for _, line := range blockLines {
		m := modelAssignRe.FindStringSubmatch(line)
		if len(m) != 4 {
			continue
		}
		if looksAnthropic(m[2]) {
			return true
		}
	}
	return false
}

func trimAnthropicTierValues(line string) string {
	m := tierAssignRe.FindStringSubmatch(line)
	if len(m) != 6 {
		return line
	}

	values := quotedStringRe.FindAllStringSubmatch(m[4], -1)
	if len(values) == 0 {
		return line
	}

	filtered := make([]string, 0, len(values))
	for _, value := range values {
		if len(value) < 2 {
			continue
		}
		name := strings.TrimSpace(value[1])
		if looksAnthropic(name) {
			continue
		}
		filtered = append(filtered, `"`+name+`"`)
	}

	return m[1] + m[2] + m[3] + "[" + strings.Join(filtered, ", ") + "]" + m[5]
}

func replaceAnthropicChiefModel(line, fallbackModel string) string {
	if strings.TrimSpace(fallbackModel) == "" {
		fallbackModel = "gpt-5.3-codex"
	}
	m := modelAssignRe.FindStringSubmatch(line)
	if len(m) != 4 {
		return line
	}
	if !looksAnthropic(m[2]) {
		return line
	}
	return m[1] + fallbackModel + m[3]
}

func looksAnthropic(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(lower, "claude") || strings.Contains(lower, "anthropic")
}

func collapseBlankRuns(lines []string, maxRun int) []string {
	if maxRun < 1 {
		maxRun = 1
	}
	out := make([]string, 0, len(lines))
	blankRun := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			blankRun++
			if blankRun > maxRun {
				continue
			}
		} else {
			blankRun = 0
		}
		out = append(out, line)
	}
	return out
}
