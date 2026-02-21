package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDisableAnthropicInConfigContentRemovesAnthropicTablesAndTierEntries(t *testing.T) {
	input := `
[providers.claude-sonnet]
tier = "balanced"
model = "claude-sonnet-4-20250514"

[providers.openai]
tier = "balanced"
model = "gpt-5.3-codex"

[providers.partner]
tier = "balanced"
model = "anthropic-special-proxy"

[tiers]
fast = ["codex"]
balanced = ["codex", "claude-sonnet", "anthropic-legacy"]
premium = ["claude-opus", "codex-max"]

[dispatch.cli.claude]
cmd = "claude"

[dispatch.cli.codex]
cmd = "codex"

[chief]
model = "claude-opus-4-6"
`

	got, changed := disableAnthropicInConfigContent(input, "gpt-5.3-codex")
	if !changed {
		t.Fatal("expected config content to change")
	}

	unwanted := []string{
		"[providers.claude-sonnet]",
		`model = "claude-sonnet-4-20250514"`,
		"[providers.partner]",
		"[dispatch.cli.claude]",
		`"claude-sonnet"`,
		`"anthropic-legacy"`,
		`"claude-opus"`,
	}
	for _, value := range unwanted {
		if strings.Contains(got, value) {
			t.Fatalf("unexpected anthrophic value remained: %q\nresult:\n%s", value, got)
		}
	}

	expected := []string{
		"[providers.openai]",
		`balanced = ["codex"]`,
		`premium = ["codex-max"]`,
		`model = "gpt-5.3-codex"`,
		"[dispatch.cli.codex]",
	}
	for _, value := range expected {
		if !strings.Contains(got, value) {
			t.Fatalf("expected value missing: %q\nresult:\n%s", value, got)
		}
	}
}

func TestDisableAnthropicInConfigContentNoOpWhenNothingMatches(t *testing.T) {
	input := `
[providers.codex]
model = "gpt-5.3-codex"

[tiers]
balanced = ["codex"]
`

	got, changed := disableAnthropicInConfigContent(input, "gpt-5.3-codex")
	if changed {
		t.Fatalf("expected unchanged content, got change:\n%s", got)
	}
	if got != input {
		t.Fatalf("expected exact original output when unchanged\nwant:\n%s\ngot:\n%s", input, got)
	}
}

func TestDisableAnthropicInConfigContentDefaultsFallbackChiefModel(t *testing.T) {
	input := `
[chief]
model = "claude-sonnet-4-20250514"
`

	got, changed := disableAnthropicInConfigContent(input, "")
	if !changed {
		t.Fatal("expected chief model replacement")
	}
	if !strings.Contains(got, `model = "gpt-5.3-codex"`) {
		t.Fatalf("expected default fallback chief model, got:\n%s", got)
	}
}

func TestSetTickIntervalInConfigContentUpdatesGeneralTickInterval(t *testing.T) {
	input := `
[general]
tick_interval = "60s"
max_per_tick = 1

[reporter]
channel = "matrix"
`

	got, changed, err := setTickIntervalInConfigContent(input, "2m")
	if err != nil {
		t.Fatalf("setTickIntervalInConfigContent returned error: %v", err)
	}
	if !changed {
		t.Fatal("expected config content to change")
	}
	if !strings.Contains(got, `tick_interval = "2m"`) {
		t.Fatalf("expected tick_interval to update to 2m, got:\n%s", got)
	}
}

func TestSetTickIntervalInConfigContentNoOpWhenAlreadySet(t *testing.T) {
	input := `
[general]
tick_interval = "2m"
`

	got, changed, err := setTickIntervalInConfigContent(input, "2m")
	if err != nil {
		t.Fatalf("setTickIntervalInConfigContent returned error: %v", err)
	}
	if changed {
		t.Fatalf("expected no changes, got:\n%s", got)
	}
	if got != input {
		t.Fatalf("expected exact original output when unchanged\nwant:\n%s\ngot:\n%s", input, got)
	}
}

func TestSetTickIntervalInConfigContentErrorsWhenGeneralTickIntervalMissing(t *testing.T) {
	input := `
[general]
max_per_tick = 1
`

	_, _, err := setTickIntervalInConfigContent(input, "2m")
	if err == nil {
		t.Fatal("expected error when tick_interval is missing")
	}
}

func TestSetTickIntervalInConfigFileRejectsInvalidDuration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chum.toml")
	if err := os.WriteFile(path, []byte("[general]\ntick_interval = \"60s\"\n"), 0o644); err != nil {
		t.Fatalf("write temp config: %v", err)
	}

	_, err := setTickIntervalInConfigFile(path, "not-a-duration")
	if err == nil {
		t.Fatal("expected invalid duration error")
	}
}
