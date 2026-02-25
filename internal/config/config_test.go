package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "cortex.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

const validConfig = `
[general]
tick_interval = "60s"
max_per_tick = 3
stuck_timeout = "30m"
max_retries = 2
log_level = "info"
state_db = "/tmp/cortex-test.db"

[projects.test]
enabled = true
beads_dir = "/tmp/test/.beads"
workspace = "/tmp/test"
priority = 1

[rate_limits]
window_5h_cap = 20
weekly_cap = 200
weekly_headroom_pct = 80

[providers.cerebras]
tier = "fast"
authed = false
model = "llama-4-scout"

[providers.claude-max20]
tier = "balanced"
authed = true
model = "claude-sonnet-4-20250514"

[tiers]
fast = ["cerebras"]
balanced = ["claude-max20"]
premium = []

[health]
check_interval = "2m"
gateway_unit = "openclaw-gateway.service"

[reporter]
channel = "matrix"
agent_id = "main"
daily_digest_time = "09:00"
weekly_retro_day = "Monday"

[api]
bind = "127.0.0.1:8900"
`

func withProjectMatrixRoom(t *testing.T, cfg, room string) string {
	t.Helper()
	target := "priority = 1\n"
	replacement := fmt.Sprintf("priority = 1\nmatrix_room = %q\n", room)
	updated := strings.Replace(cfg, target, replacement, 1)
	if updated == cfg {
		t.Fatal("failed to inject project matrix_room into test config")
	}
	return updated
}

func withReporterDefaultRoom(t *testing.T, cfg, room string) string {
	t.Helper()
	target := "agent_id = \"main\"\n"
	replacement := fmt.Sprintf("agent_id = \"main\"\ndefault_room = %q\n", room)
	updated := strings.Replace(cfg, target, replacement, 1)
	if updated == cfg {
		t.Fatal("failed to inject reporter default_room into test config")
	}
	return updated
}

func TestLoadValidConfig(t *testing.T) {
	path := writeTestConfig(t, validConfig)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.General.TickInterval.Duration != 60*time.Second {
		t.Errorf("TickInterval = %v, want 60s", cfg.General.TickInterval)
	}
	if cfg.General.MaxPerTick != 3 {
		t.Errorf("MaxPerTick = %d, want 3", cfg.General.MaxPerTick)
	}
	if cfg.General.StuckTimeout.Duration != 30*time.Minute {
		t.Errorf("StuckTimeout = %v, want 30m", cfg.General.StuckTimeout)
	}
	if !cfg.Projects["test"].Enabled {
		t.Error("test project should be enabled")
	}
	if cfg.Providers["cerebras"].Tier != "fast" {
		t.Error("cerebras should be fast tier")
	}
	if cfg.API.Bind != "127.0.0.1:8900" {
		t.Errorf("API.Bind = %q, want 127.0.0.1:8900", cfg.API.Bind)
	}
}

func TestLoadNoEnabledProject(t *testing.T) {
	cfg := `
[general]
state_db = "/tmp/cortex-test.db"

[projects.test]
enabled = false
beads_dir = "/tmp/test/.beads"
workspace = "/tmp/test"
priority = 1

[providers]
[tiers]
[health]
[reporter]
[api]
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for no enabled projects")
	}
}

func TestResolveRoomPrefersProjectRoom(t *testing.T) {
	cfg := withProjectMatrixRoom(t, withReporterDefaultRoom(t, validConfig, "!fallback:matrix.org"), "!project-test:matrix.org")
	path := writeTestConfig(t, cfg)
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if got := loaded.ResolveRoom("test"); got != "!project-test:matrix.org" {
		t.Fatalf("ResolveRoom(test) = %q, want !project-test:matrix.org", got)
	}
}

func TestResolveRoomFallsBackToReporterDefault(t *testing.T) {
	cfg := withReporterDefaultRoom(t, validConfig, "!fallback:matrix.org")
	path := writeTestConfig(t, cfg)
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if got := loaded.ResolveRoom("test"); got != "!fallback:matrix.org" {
		t.Fatalf("ResolveRoom(test) = %q, want !fallback:matrix.org", got)
	}
	if got := loaded.ResolveRoom("missing-project"); got != "!fallback:matrix.org" {
		t.Fatalf("ResolveRoom(missing-project) = %q, want !fallback:matrix.org", got)
	}
}

func TestResolveRoomBackwardCompatible(t *testing.T) {
	path := writeTestConfig(t, validConfig)
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if got := loaded.ResolveRoom("test"); got != "" {
		t.Fatalf("ResolveRoom(test) = %q, want empty string", got)
	}
}

func TestMissingProjectRoomRouting(t *testing.T) {
	path := writeTestConfig(t, validConfig)
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	missing := loaded.MissingProjectRoomRouting()
	if len(missing) != 1 || missing[0] != "test" {
		t.Fatalf("MissingProjectRoomRouting() = %v, want [test]", missing)
	}

	withDefault := withReporterDefaultRoom(t, validConfig, "!fallback:matrix.org")
	path = writeTestConfig(t, withDefault)
	loaded, err = Load(path)
	if err != nil {
		t.Fatalf("Load failed with default room: %v", err)
	}
	if missing = loaded.MissingProjectRoomRouting(); len(missing) != 0 {
		t.Fatalf("MissingProjectRoomRouting() with default_room = %v, want []", missing)
	}

	withProjectRoom := withProjectMatrixRoom(t, validConfig, "!project-test:matrix.org")
	path = writeTestConfig(t, withProjectRoom)
	loaded, err = Load(path)
	if err != nil {
		t.Fatalf("Load failed with project room: %v", err)
	}
	if missing = loaded.MissingProjectRoomRouting(); len(missing) != 0 {
		t.Fatalf("MissingProjectRoomRouting() with project matrix_room = %v, want []", missing)
	}
}

func TestLoadMatrixConfigDefaults(t *testing.T) {
	path := writeTestConfig(t, validConfig)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Matrix.Enabled {
		t.Fatal("matrix.enabled should default to false")
	}
	if cfg.Matrix.PollInterval.Duration != 30*time.Second {
		t.Fatalf("matrix.poll_interval default = %v, want 30s", cfg.Matrix.PollInterval.Duration)
	}
	if cfg.Matrix.ReadLimit != 25 {
		t.Fatalf("matrix.read_limit default = %d, want 25", cfg.Matrix.ReadLimit)
	}
}

func TestLoadMatrixConfigEnabled(t *testing.T) {
	cfg := validConfig + `

[matrix]
enabled = true
poll_interval = "45s"
bot_user = "@cortex-bot:matrix.org"
read_limit = 40
`
	path := writeTestConfig(t, cfg)
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if !loaded.Matrix.Enabled {
		t.Fatal("matrix.enabled should be true")
	}
	if loaded.Matrix.PollInterval.Duration != 45*time.Second {
		t.Fatalf("matrix.poll_interval = %v, want 45s", loaded.Matrix.PollInterval.Duration)
	}
	if loaded.Matrix.BotUser != "@cortex-bot:matrix.org" {
		t.Fatalf("matrix.bot_user = %q, want @cortex-bot:matrix.org", loaded.Matrix.BotUser)
	}
	if loaded.Matrix.ReadLimit != 40 {
		t.Fatalf("matrix.read_limit = %d, want 40", loaded.Matrix.ReadLimit)
	}
}

func TestLoadMatrixConfigInvalidReadLimit(t *testing.T) {
	cfg := validConfig + `

[matrix]
enabled = true
read_limit = -1
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid matrix.read_limit")
	}
	if !strings.Contains(err.Error(), "matrix.read_limit") {
		t.Fatalf("expected matrix.read_limit validation error, got: %v", err)
	}
}

func TestLoadUnknownProviderInTier(t *testing.T) {
	cfg := `
[general]
state_db = "/tmp/cortex-test.db"

[projects.test]
enabled = true
beads_dir = "/tmp/test/.beads"
workspace = "/tmp/test"
priority = 1

[providers.cerebras]
tier = "fast"
authed = false
model = "llama"

[tiers]
fast = ["cerebras", "nonexistent"]
balanced = []
premium = []

[health]
[reporter]
[api]
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for unknown provider in tier")
	}
}

func TestDurationUnmarshal(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"60s", 60 * time.Second},
		{"2m", 2 * time.Minute},
		{"1h", time.Hour},
		{"500ms", 500 * time.Millisecond},
	}
	for _, tt := range tests {
		var d Duration
		if err := d.UnmarshalText([]byte(tt.input)); err != nil {
			t.Errorf("UnmarshalText(%q) error: %v", tt.input, err)
			continue
		}
		if d.Duration != tt.want {
			t.Errorf("UnmarshalText(%q) = %v, want %v", tt.input, d.Duration, tt.want)
		}
	}
}

func TestDurationUnmarshalInvalid(t *testing.T) {
	var d Duration
	if err := d.UnmarshalText([]byte("not-a-duration")); err == nil {
		t.Error("expected error for invalid duration")
	}
}

func TestLoadWithWorkflows(t *testing.T) {
	cfg := validConfig + `

[workflows.dev]
match_labels = ["code", "backend"]
match_types = ["task", "bug"]

[[workflows.dev.stages]]
name = "implement"
role = "coder"

[[workflows.dev.stages]]
name = "review"
role = "reviewer"
`
	path := writeTestConfig(t, cfg)
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	wf, ok := loaded.Workflows["dev"]
	if !ok {
		t.Fatal("expected workflows.dev to parse")
	}
	if len(wf.Stages) != 2 {
		t.Fatalf("expected 2 stages, got %d", len(wf.Stages))
	}
	if wf.Stages[0].Name != "implement" || wf.Stages[0].Role != "coder" {
		t.Fatalf("unexpected first stage: %+v", wf.Stages[0])
	}
}

func TestLoadWorkflowValidationDuplicateStageName(t *testing.T) {
	cfg := validConfig + `

[workflows.dev]

[[workflows.dev.stages]]
name = "implement"
role = "coder"

[[workflows.dev.stages]]
name = "implement"
role = "reviewer"
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected duplicate stage name validation error")
	}
}

func TestLoadWorkflowValidationUnknownRole(t *testing.T) {
	cfg := validConfig + `

[workflows.dev]

[[workflows.dev.stages]]
name = "implement"
role = "astronaut"
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected unknown role validation error")
	}
}

func TestLoadWorkflowValidationMissingStageNameOrRole(t *testing.T) {
	cfgMissingName := validConfig + `

[workflows.dev]

[[workflows.dev.stages]]
role = "coder"
`
	path := writeTestConfig(t, cfgMissingName)
	if _, err := Load(path); err == nil {
		t.Fatal("expected missing stage name validation error")
	}

	cfgMissingRole := validConfig + `

[workflows.dev]

[[workflows.dev.stages]]
name = "implement"
`
	path = writeTestConfig(t, cfgMissingRole)
	if _, err := Load(path); err == nil {
		t.Fatal("expected missing stage role validation error")
	}
}

func TestLoadWorkflowValidationEmptyWorkflowsSection(t *testing.T) {
	cfg := validConfig + `

[workflows]
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected empty workflows section validation error")
	}
}

func TestLoadRateLimitBudgetValid(t *testing.T) {
	cfg := validConfig + `

[rate_limits.budget]
project-a = 60
project-b = 40
`
	path := writeTestConfig(t, cfg)
	config, err := Load(path)
	if err != nil {
		t.Fatalf("expected valid budget config to load: %v", err)
	}

	if config.RateLimits.Budget == nil {
		t.Fatal("expected budget to be parsed")
	}

	if config.RateLimits.Budget["project-a"] != 60 {
		t.Errorf("expected project-a budget 60, got %d", config.RateLimits.Budget["project-a"])
	}

	if config.RateLimits.Budget["project-b"] != 40 {
		t.Errorf("expected project-b budget 40, got %d", config.RateLimits.Budget["project-b"])
	}
}

func TestLoadRateLimitBudgetSumNot100(t *testing.T) {
	cfg := validConfig + `

[rate_limits.budget]
project-a = 60
project-b = 50
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected budget sum validation error")
	}

	if !strings.Contains(err.Error(), "must sum to 100") {
		t.Errorf("expected sum validation error, got: %v", err)
	}
}

func TestLoadRateLimitBudgetNegativePercentage(t *testing.T) {
	cfg := validConfig + `

[rate_limits.budget]
project-a = -10
project-b = 50
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected negative budget validation error")
	}

	if !strings.Contains(err.Error(), "cannot be negative") {
		t.Errorf("expected negative budget validation error, got: %v", err)
	}
}

func TestLoadRateLimitBudgetOver100Percentage(t *testing.T) {
	cfg := validConfig + `

[rate_limits.budget]
project-a = 150
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected over 100% budget validation error")
	}

	if !strings.Contains(err.Error(), "cannot exceed 100%") {
		t.Errorf("expected over 100%% budget validation error, got: %v", err)
	}
}

func TestLoadRateLimitBudgetOptional(t *testing.T) {
	// Test that config without budget section still works
	path := writeTestConfig(t, validConfig)
	config, err := Load(path)
	if err != nil {
		t.Fatalf("expected config without budget to load: %v", err)
	}

	if config.RateLimits.Budget != nil && len(config.RateLimits.Budget) > 0 {
		t.Error("expected budget to be nil or empty when not configured")
	}
}

func TestRateLimitsGetProjectBudget(t *testing.T) {
	rl := &RateLimits{
		Budget: map[string]int{
			"project-a": 60,
			"project-b": 40,
		},
	}

	// Test existing project
	if budget := rl.GetProjectBudget("project-a"); budget != 60 {
		t.Errorf("expected project-a budget 60, got %d", budget)
	}

	// Test non-existing project
	if budget := rl.GetProjectBudget("project-c"); budget != 0 {
		t.Errorf("expected non-existing project budget 0, got %d", budget)
	}

	// Test nil budget map
	rl.Budget = nil
	if budget := rl.GetProjectBudget("project-a"); budget != 0 {
		t.Errorf("expected budget 0 when budget map is nil, got %d", budget)
	}
}

func TestLoadChiefConfigValid(t *testing.T) {
	cfg := validConfig + `

[chief]
enabled = true
matrix_room = "!coordination:matrix.org"
model = "claude-opus-4-6"
agent_id = "cortex-chief-scrum"
`
	path := writeTestConfig(t, cfg)
	config, err := Load(path)
	if err != nil {
		t.Fatalf("expected valid chief config to load: %v", err)
	}

	if !config.Chief.Enabled {
		t.Error("expected chief to be enabled")
	}

	if config.Chief.MatrixRoom != "!coordination:matrix.org" {
		t.Errorf("expected matrix_room '!coordination:matrix.org', got %q", config.Chief.MatrixRoom)
	}

	if config.Chief.Model != "claude-opus-4-6" {
		t.Errorf("expected model 'claude-opus-4-6', got %q", config.Chief.Model)
	}

	if config.Chief.AgentID != "cortex-chief-scrum" {
		t.Errorf("expected agent_id 'cortex-chief-scrum', got %q", config.Chief.AgentID)
	}
}

func TestLoadChiefConfigDefaults(t *testing.T) {
	cfg := validConfig + `

[chief]
enabled = true
matrix_room = "!coordination:matrix.org"
`
	path := writeTestConfig(t, cfg)
	config, err := Load(path)
	if err != nil {
		t.Fatalf("expected chief config with defaults to load: %v", err)
	}

	if config.Chief.Model != "claude-opus-4-6" {
		t.Errorf("expected default model 'claude-opus-4-6', got %q", config.Chief.Model)
	}

	if config.Chief.AgentID != "cortex-chief-scrum" {
		t.Errorf("expected default agent_id 'cortex-chief-scrum', got %q", config.Chief.AgentID)
	}
}

func TestLoadChiefConfigDisabled(t *testing.T) {
	cfg := validConfig + `

[chief]
enabled = false
`
	path := writeTestConfig(t, cfg)
	config, err := Load(path)
	if err != nil {
		t.Fatalf("expected disabled chief config to load: %v", err)
	}

	if config.Chief.Enabled {
		t.Error("expected chief to be disabled")
	}
}

func TestLoadChiefConfigMissingMatrixRoom(t *testing.T) {
	cfg := validConfig + `

[chief]
enabled = true
agent_id = "cortex-chief-scrum"
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing matrix_room")
	}

	if !strings.Contains(err.Error(), "no matrix_room configured") {
		t.Errorf("expected matrix_room validation error, got: %v", err)
	}
}

func TestLoadChiefConfigOptional(t *testing.T) {
	// Test that config without [chief] section still works (backward compatibility)
	path := writeTestConfig(t, validConfig)
	config, err := Load(path)
	if err != nil {
		t.Fatalf("expected config without chief section to load: %v", err)
	}

	if config.Chief.Enabled {
		t.Error("expected chief to be disabled by default")
	}

	// Test that defaults are still applied even when disabled
	if config.Chief.Model != "claude-opus-4-6" {
		t.Errorf("expected default model to be applied, got %q", config.Chief.Model)
	}

	if config.Chief.AgentID != "cortex-chief-scrum" {
		t.Errorf("expected default agent_id to be applied, got %q", config.Chief.AgentID)
	}
}

func TestLoadDispatchValidConfig(t *testing.T) {
	cfg := validConfig + `

[dispatch.cli.claude]
cmd = "claude"
prompt_mode = "stdin"
args = ["--print"]
model_flag = "--model"
approval_flags = ["--dangerously-skip-permissions"]

[dispatch.cli.codex]
cmd = "codex"
prompt_mode = "file"
args = ["exec", "--full-auto"]
model_flag = "-m"
approval_flags = []

[dispatch.routing]
fast_backend = "headless_cli"
balanced_backend = "tmux"
premium_backend = "tmux"
`
	path := writeTestConfig(t, cfg)
	config, err := Load(path)
	if err != nil {
		t.Fatalf("expected valid dispatch config to load: %v", err)
	}

	// Verify CLI configs were parsed
	if len(config.Dispatch.CLI) != 2 {
		t.Errorf("expected 2 CLI configs, got %d", len(config.Dispatch.CLI))
	}

	claude := config.Dispatch.CLI["claude"]
	if claude.Cmd != "claude" {
		t.Errorf("expected claude cmd, got %q", claude.Cmd)
	}
	if claude.ModelFlag != "--model" {
		t.Errorf("expected --model flag, got %q", claude.ModelFlag)
	}
}

func TestLoadDispatchInvalidBackend(t *testing.T) {
	cfg := validConfig + `

[dispatch.routing]
fast_backend = "invalid_backend"
balanced_backend = "tmux"
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid backend type")
	}

	if !strings.Contains(err.Error(), "invalid backend type") {
		t.Errorf("expected invalid backend error, got: %v", err)
	}
}

func TestLoadDispatchMissingCLIConfig(t *testing.T) {
	cfg := validConfig + `

[providers.test-provider]
tier = "fast"
authed = true
model = "test-model"
cli = "nonexistent"

[dispatch.routing]
fast_backend = "headless_cli"
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing CLI config")
	}

	if !strings.Contains(err.Error(), "references undefined CLI config") {
		t.Errorf("expected undefined CLI config error, got: %v", err)
	}
}

func TestLoadDispatchInvalidCLIConfig(t *testing.T) {
	tests := []struct {
		name   string
		config string
		error  string
	}{
		{
			name: "missing cmd",
			config: `
[dispatch.cli.test]
prompt_mode = "stdin"
`,
			error: "cmd is required",
		},
		{
			name: "invalid prompt_mode",
			config: `
[dispatch.cli.test]
cmd = "test"
prompt_mode = "invalid"
`,
			error: "invalid prompt_mode",
		},
		{
			name: "invalid model_flag format",
			config: `
[dispatch.cli.test]
cmd = "test"
model_flag = "model"
`,
			error: "must start with '-'",
		},
		{
			name: "invalid approval_flag format",
			config: `
[dispatch.cli.test]
cmd = "test"
approval_flags = ["skip-perms"]
`,
			error: "must start with '-'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig + tt.config
			path := writeTestConfig(t, cfg)
			_, err := Load(path)
			if err == nil {
				t.Fatal("expected validation error")
			}

			if !strings.Contains(err.Error(), tt.error) {
				t.Errorf("expected error containing %q, got: %v", tt.error, err)
			}
		})
	}
}

func TestLoadDispatchValidCLIConfigs(t *testing.T) {
	cfg := validConfig + `

[dispatch.cli.claude]
cmd = "claude"
prompt_mode = "stdin"
model_flag = "--model"
approval_flags = ["--dangerously-skip-permissions", "--yes"]

[dispatch.cli.codex]
cmd = "codex"
prompt_mode = "file"
model_flag = "-m"
approval_flags = []

[dispatch.cli.aider]
cmd = "aider"
prompt_mode = "arg"
model_flag = "--model"

[dispatch.routing]
fast_backend = "headless_cli"
`
	path := writeTestConfig(t, cfg)
	config, err := Load(path)
	if err != nil {
		t.Fatalf("expected valid CLI configs to load: %v", err)
	}

	if len(config.Dispatch.CLI) != 3 {
		t.Errorf("expected 3 CLI configs, got %d", len(config.Dispatch.CLI))
	}

	// Test each CLI config
	claude := config.Dispatch.CLI["claude"]
	if claude.PromptMode != "stdin" {
		t.Errorf("expected stdin prompt_mode, got %q", claude.PromptMode)
	}
	if len(claude.ApprovalFlags) != 2 {
		t.Errorf("expected 2 approval flags, got %d", len(claude.ApprovalFlags))
	}

	aider := config.Dispatch.CLI["aider"]
	if aider.PromptMode != "arg" {
		t.Errorf("expected arg prompt_mode, got %q", aider.PromptMode)
	}
	if len(aider.ApprovalFlags) != 0 {
		t.Errorf("expected 0 approval flags, got %d", len(aider.ApprovalFlags))
	}
}

func TestValidateDispatchConfigNilCLI(t *testing.T) {
	// Test config with no CLI section
	cfg := &Config{
		Dispatch: Dispatch{
			Routing: DispatchRouting{
				FastBackend: "headless_cli",
			},
		},
		Providers: make(map[string]Provider),
	}

	err := ValidateDispatchConfig(cfg)
	if err != nil {
		t.Errorf("expected nil CLI config to be valid, got: %v", err)
	}
}

// Cadence Configuration Tests

func TestLoadCadenceConfigDefaults(t *testing.T) {
	path := writeTestConfig(t, validConfig)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("expected default cadence config to load: %v", err)
	}

	if cfg.Cadence.SprintLength != "1w" {
		t.Fatalf("default sprint_length = %q, want 1w", cfg.Cadence.SprintLength)
	}
	if cfg.Cadence.SprintStartDay != "Monday" {
		t.Fatalf("default sprint_start_day = %q, want Monday", cfg.Cadence.SprintStartDay)
	}
	if cfg.Cadence.SprintStartTime != "09:00" {
		t.Fatalf("default sprint_start_time = %q, want 09:00", cfg.Cadence.SprintStartTime)
	}
	if cfg.Cadence.Timezone != "UTC" {
		t.Fatalf("default timezone = %q, want UTC", cfg.Cadence.Timezone)
	}
}

func TestLoadCadenceConfigValid(t *testing.T) {
	cfg := validConfig + `

[cadence]
sprint_length = "2w"
sprint_start_day = "Wednesday"
sprint_start_time = "10:30"
timezone = "America/New_York"
`
	path := writeTestConfig(t, cfg)
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("expected valid cadence config to load: %v", err)
	}

	length, err := loaded.Cadence.SprintLengthDuration()
	if err != nil {
		t.Fatalf("expected sprint length parse to succeed: %v", err)
	}
	if length != 14*24*time.Hour {
		t.Fatalf("parsed sprint length = %v, want 336h", length)
	}
	weekday, err := loaded.Cadence.StartWeekday()
	if err != nil {
		t.Fatalf("expected weekday parse to succeed: %v", err)
	}
	if weekday != time.Wednesday {
		t.Fatalf("parsed weekday = %v, want Wednesday", weekday)
	}
	hour, minute, err := loaded.Cadence.StartClock()
	if err != nil {
		t.Fatalf("expected clock parse to succeed: %v", err)
	}
	if hour != 10 || minute != 30 {
		t.Fatalf("parsed clock = %02d:%02d, want 10:30", hour, minute)
	}
	loc, err := loaded.Cadence.LoadLocation()
	if err != nil {
		t.Fatalf("expected timezone parse to succeed: %v", err)
	}
	if loc.String() != "America/New_York" {
		t.Fatalf("parsed location = %q, want America/New_York", loc.String())
	}
}

func TestLoadCadenceConfigInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		section string
		wantErr string
	}{
		{
			name: "invalid length",
			section: `
[cadence]
sprint_length = "0w"
`,
			wantErr: "invalid sprint_length",
		},
		{
			name: "invalid day",
			section: `
[cadence]
sprint_start_day = "Funday"
`,
			wantErr: "invalid sprint_start_day",
		},
		{
			name: "invalid time",
			section: `
[cadence]
sprint_start_time = "9:00"
`,
			wantErr: "invalid sprint_start_time",
		},
		{
			name: "invalid timezone",
			section: `
[cadence]
timezone = "Mars/Olympus"
`,
			wantErr: "invalid timezone",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTestConfig(t, validConfig+tt.section)
			_, err := Load(path)
			if err == nil {
				t.Fatalf("expected error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error to contain %q, got: %v", tt.wantErr, err)
			}
		})
	}
}

// Sprint Planning Configuration Tests

func TestLoadSprintPlanningConfigValid(t *testing.T) {
	cfg := validConfig + `

[projects.sprint-project]
enabled = true
beads_dir = "/tmp/sprint-test/.beads"
workspace = "/tmp/sprint-test"
priority = 1
sprint_planning_day = "Monday"
sprint_planning_time = "09:00"
sprint_capacity = 30
backlog_threshold = 45
`
	path := writeTestConfig(t, cfg)
	config, err := Load(path)
	if err != nil {
		t.Fatalf("expected valid sprint planning config to load: %v", err)
	}

	project := config.Projects["sprint-project"]
	if project.SprintPlanningDay != "Monday" {
		t.Errorf("expected sprint_planning_day 'Monday', got %q", project.SprintPlanningDay)
	}
	if project.SprintPlanningTime != "09:00" {
		t.Errorf("expected sprint_planning_time '09:00', got %q", project.SprintPlanningTime)
	}
	if project.SprintCapacity != 30 {
		t.Errorf("expected sprint_capacity 30, got %d", project.SprintCapacity)
	}
	if project.BacklogThreshold != 45 {
		t.Errorf("expected backlog_threshold 45, got %d", project.BacklogThreshold)
	}
}

func TestLoadSprintPlanningConfigBackwardCompatibility(t *testing.T) {
	// Test that projects without sprint planning config work normally
	path := writeTestConfig(t, validConfig)
	config, err := Load(path)
	if err != nil {
		t.Fatalf("expected config without sprint planning to load: %v", err)
	}

	project := config.Projects["test"]
	if project.SprintPlanningDay != "" {
		t.Errorf("expected empty sprint_planning_day, got %q", project.SprintPlanningDay)
	}
	if project.SprintPlanningTime != "" {
		t.Errorf("expected empty sprint_planning_time, got %q", project.SprintPlanningTime)
	}
	if project.SprintCapacity != 0 {
		t.Errorf("expected sprint_capacity 0, got %d", project.SprintCapacity)
	}
	if project.BacklogThreshold != 0 {
		t.Errorf("expected backlog_threshold 0, got %d", project.BacklogThreshold)
	}
}

func TestLoadSprintPlanningConfigInvalidDay(t *testing.T) {
	cfg := validConfig + `

[projects.sprint-project]
enabled = true
beads_dir = "/tmp/sprint-test/.beads"
workspace = "/tmp/sprint-test"
sprint_planning_day = "InvalidDay"
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid sprint planning day")
	}

	if !strings.Contains(err.Error(), "invalid sprint_planning_day") {
		t.Errorf("expected invalid day validation error, got: %v", err)
	}
}

func TestLoadSprintPlanningConfigInvalidTime(t *testing.T) {
	tests := []struct {
		name string
		time string
	}{
		{"invalid format", "9:00"},
		{"invalid format short", "9:0"},
		{"non-numeric hour", "ab:00"},
		{"non-numeric minute", "09:ab"},
		{"invalid hour", "25:00"},
		{"invalid minute", "09:60"},
		{"missing colon", "0900"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig + fmt.Sprintf(`

[projects.sprint-project]
enabled = true
beads_dir = "/tmp/sprint-test/.beads"
workspace = "/tmp/sprint-test"
sprint_planning_time = "%s"
`, tt.time)
			path := writeTestConfig(t, cfg)
			_, err := Load(path)
			if err == nil {
				t.Fatalf("expected error for invalid time format: %s", tt.time)
			}

			if !strings.Contains(err.Error(), "invalid sprint_planning_time") {
				t.Errorf("expected time validation error, got: %v", err)
			}
		})
	}
}

func TestLoadSprintPlanningConfigValidTimes(t *testing.T) {
	validTimes := []string{"00:00", "09:30", "23:59", "12:00", "18:45"}

	for _, validTime := range validTimes {
		t.Run("valid_time_"+validTime, func(t *testing.T) {
			cfg := validConfig + fmt.Sprintf(`

[projects.sprint-project]
enabled = true
beads_dir = "/tmp/sprint-test/.beads"
workspace = "/tmp/sprint-test"
sprint_planning_time = "%s"
`, validTime)
			path := writeTestConfig(t, cfg)
			config, err := Load(path)
			if err != nil {
				t.Fatalf("expected valid time %s to load: %v", validTime, err)
			}

			if config.Projects["sprint-project"].SprintPlanningTime != validTime {
				t.Errorf("expected time %s, got %s", validTime, config.Projects["sprint-project"].SprintPlanningTime)
			}
		})
	}
}

func TestLoadSprintPlanningConfigNegativeCapacity(t *testing.T) {
	cfg := validConfig + `

[projects.sprint-project]
enabled = true
beads_dir = "/tmp/sprint-test/.beads"
workspace = "/tmp/sprint-test"
sprint_capacity = -1
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for negative sprint capacity")
	}

	if !strings.Contains(err.Error(), "sprint_capacity cannot be negative") {
		t.Errorf("expected negative capacity validation error, got: %v", err)
	}
}

func TestLoadSprintPlanningConfigExcessiveCapacity(t *testing.T) {
	cfg := validConfig + `

[projects.sprint-project]
enabled = true
beads_dir = "/tmp/sprint-test/.beads"
workspace = "/tmp/sprint-test"
sprint_capacity = 1001
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for excessive sprint capacity")
	}

	if !strings.Contains(err.Error(), "sprint_capacity seems unreasonably large") {
		t.Errorf("expected excessive capacity validation error, got: %v", err)
	}
}

func TestLoadSprintPlanningConfigNegativeThreshold(t *testing.T) {
	cfg := validConfig + `

[projects.sprint-project]
enabled = true
beads_dir = "/tmp/sprint-test/.beads"
workspace = "/tmp/sprint-test"
backlog_threshold = -1
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for negative backlog threshold")
	}

	if !strings.Contains(err.Error(), "backlog_threshold cannot be negative") {
		t.Errorf("expected negative threshold validation error, got: %v", err)
	}
}

func TestLoadSprintPlanningConfigExcessiveThreshold(t *testing.T) {
	cfg := validConfig + `

[projects.sprint-project]
enabled = true
beads_dir = "/tmp/sprint-test/.beads"
workspace = "/tmp/sprint-test"
backlog_threshold = 501
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for excessive backlog threshold")
	}

	if !strings.Contains(err.Error(), "backlog_threshold seems unreasonably large") {
		t.Errorf("expected excessive threshold validation error, got: %v", err)
	}
}

func TestLoadSprintPlanningConfigThresholdLessThanCapacity(t *testing.T) {
	cfg := validConfig + `

[projects.sprint-project]
enabled = true
beads_dir = "/tmp/sprint-test/.beads"
workspace = "/tmp/sprint-test"
sprint_capacity = 50
backlog_threshold = 30
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for threshold less than capacity")
	}

	if !strings.Contains(err.Error(), "backlog_threshold") || !strings.Contains(err.Error(), "should be at least as large as sprint_capacity") {
		t.Errorf("expected threshold/capacity validation error, got: %v", err)
	}
}

func TestLoadSprintPlanningConfigValidDays(t *testing.T) {
	validDays := []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

	for _, validDay := range validDays {
		t.Run("valid_day_"+validDay, func(t *testing.T) {
			cfg := validConfig + fmt.Sprintf(`

[projects.sprint-project]
enabled = true
beads_dir = "/tmp/sprint-test/.beads"
workspace = "/tmp/sprint-test"
sprint_planning_day = "%s"
`, validDay)
			path := writeTestConfig(t, cfg)
			config, err := Load(path)
			if err != nil {
				t.Fatalf("expected valid day %s to load: %v", validDay, err)
			}

			if config.Projects["sprint-project"].SprintPlanningDay != validDay {
				t.Errorf("expected day %s, got %s", validDay, config.Projects["sprint-project"].SprintPlanningDay)
			}
		})
	}
}

func TestLoadSprintPlanningConfigPartialConfiguration(t *testing.T) {
	// Test that partial sprint configuration is valid (users can configure just some fields)
	tests := []struct {
		name   string
		config string
	}{
		{
			name: "only_day",
			config: `
sprint_planning_day = "Monday"
`,
		},
		{
			name: "only_time",
			config: `
sprint_planning_time = "09:00"
`,
		},
		{
			name: "only_capacity",
			config: `
sprint_capacity = 30
`,
		},
		{
			name: "only_threshold",
			config: `
backlog_threshold = 45
`,
		},
		{
			name: "day_and_time",
			config: `
sprint_planning_day = "Wednesday"
sprint_planning_time = "14:30"
`,
		},
		{
			name: "capacity_and_threshold",
			config: `
sprint_capacity = 25
backlog_threshold = 40
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig + fmt.Sprintf(`

[projects.sprint-project]
enabled = true
beads_dir = "/tmp/sprint-test/.beads"
workspace = "/tmp/sprint-test"
%s
`, tt.config)
			path := writeTestConfig(t, cfg)
			_, err := Load(path)
			if err != nil {
				t.Fatalf("expected partial sprint config to be valid: %v", err)
			}
		})
	}
}

// DoD Configuration Tests

func TestLoadDoDConfigValid(t *testing.T) {
	cfg := validConfig + `

[projects.dod-project]
enabled = true
beads_dir = "/tmp/dod-test/.beads"
workspace = "/tmp/dod-test"
priority = 1

[projects.dod-project.dod]
checks = [
    "go test ./...",
    "go vet ./...",
    "golangci-lint run"
]
coverage_min = 80
require_estimate = true
require_acceptance = true
`
	path := writeTestConfig(t, cfg)
	config, err := Load(path)
	if err != nil {
		t.Fatalf("expected valid DoD config to load: %v", err)
	}

	project := config.Projects["dod-project"]
	dod := project.DoD

	if len(dod.Checks) != 3 {
		t.Errorf("expected 3 DoD checks, got %d", len(dod.Checks))
	}

	expectedChecks := []string{"go test ./...", "go vet ./...", "golangci-lint run"}
	for i, expected := range expectedChecks {
		if i >= len(dod.Checks) || dod.Checks[i] != expected {
			t.Errorf("expected check[%d] to be %q, got %q", i, expected, dod.Checks[i])
		}
	}

	if dod.CoverageMin != 80 {
		t.Errorf("expected coverage_min 80, got %d", dod.CoverageMin)
	}

	if !dod.RequireEstimate {
		t.Error("expected require_estimate to be true")
	}

	if !dod.RequireAcceptance {
		t.Error("expected require_acceptance to be true")
	}
}

func TestLoadDoDConfigBackwardCompatibility(t *testing.T) {
	// Test that projects without DoD config work normally (backward compatibility)
	path := writeTestConfig(t, validConfig)
	config, err := Load(path)
	if err != nil {
		t.Fatalf("expected config without DoD to load: %v", err)
	}

	project := config.Projects["test"]
	dod := project.DoD

	if len(dod.Checks) != 0 {
		t.Errorf("expected empty DoD checks, got %d", len(dod.Checks))
	}

	if dod.CoverageMin != 0 {
		t.Errorf("expected coverage_min 0, got %d", dod.CoverageMin)
	}

	if dod.RequireEstimate {
		t.Error("expected require_estimate to be false by default")
	}

	if dod.RequireAcceptance {
		t.Error("expected require_acceptance to be false by default")
	}
}

func TestLoadDoDConfigPartial(t *testing.T) {
	// Test that partial DoD configuration is valid
	tests := []struct {
		name   string
		config string
		verify func(t *testing.T, dod DoDConfig)
	}{
		{
			name: "only_checks",
			config: `
[projects.dod-project.dod]
checks = ["go test ./..."]
`,
			verify: func(t *testing.T, dod DoDConfig) {
				if len(dod.Checks) != 1 || dod.Checks[0] != "go test ./..." {
					t.Errorf("expected checks [\"go test ./...\"], got %v", dod.Checks)
				}
				if dod.CoverageMin != 0 {
					t.Errorf("expected default coverage_min 0, got %d", dod.CoverageMin)
				}
			},
		},
		{
			name: "only_coverage",
			config: `
[projects.dod-project.dod]
coverage_min = 90
`,
			verify: func(t *testing.T, dod DoDConfig) {
				if dod.CoverageMin != 90 {
					t.Errorf("expected coverage_min 90, got %d", dod.CoverageMin)
				}
				if len(dod.Checks) != 0 {
					t.Errorf("expected empty checks, got %v", dod.Checks)
				}
			},
		},
		{
			name: "only_flags",
			config: `
[projects.dod-project.dod]
require_estimate = true
require_acceptance = false
`,
			verify: func(t *testing.T, dod DoDConfig) {
				if !dod.RequireEstimate {
					t.Error("expected require_estimate to be true")
				}
				if dod.RequireAcceptance {
					t.Error("expected require_acceptance to be false")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig + fmt.Sprintf(`

[projects.dod-project]
enabled = true
beads_dir = "/tmp/dod-test/.beads"
workspace = "/tmp/dod-test"
priority = 1

%s
`, tt.config)
			path := writeTestConfig(t, cfg)
			config, err := Load(path)
			if err != nil {
				t.Fatalf("expected partial DoD config to be valid: %v", err)
			}

			project := config.Projects["dod-project"]
			tt.verify(t, project.DoD)
		})
	}
}

func TestLoadDoDConfigInvalidCoverageMin(t *testing.T) {
	cfg := validConfig + `

[projects.dod-project]
enabled = true
beads_dir = "/tmp/dod-test/.beads"
workspace = "/tmp/dod-test"

[projects.dod-project.dod]
coverage_min = -1
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for negative coverage_min")
	}

	if !strings.Contains(err.Error(), "coverage_min cannot be negative") {
		t.Errorf("expected negative coverage validation error, got: %v", err)
	}
}

func TestLoadDoDConfigExcessiveCoverageMin(t *testing.T) {
	cfg := validConfig + `

[projects.dod-project]
enabled = true
beads_dir = "/tmp/dod-test/.beads"
workspace = "/tmp/dod-test"

[projects.dod-project.dod]
coverage_min = 101
`
	path := writeTestConfig(t, cfg)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for coverage_min > 100")
	}

	if !strings.Contains(err.Error(), "coverage_min cannot exceed 100") {
		t.Errorf("expected excessive coverage validation error, got: %v", err)
	}
}

func TestLoadDoDConfigEmptyChecks(t *testing.T) {
	// Test that empty checks array is valid (DoD can be coverage-only or flags-only)
	cfg := validConfig + `

[projects.dod-project]
enabled = true
beads_dir = "/tmp/dod-test/.beads"
workspace = "/tmp/dod-test"

[projects.dod-project.dod]
checks = []
coverage_min = 80
require_estimate = true
`
	path := writeTestConfig(t, cfg)
	config, err := Load(path)
	if err != nil {
		t.Fatalf("expected empty checks to be valid: %v", err)
	}

	dod := config.Projects["dod-project"].DoD
	if len(dod.Checks) != 0 {
		t.Errorf("expected empty checks, got %v", dod.Checks)
	}
	if dod.CoverageMin != 80 {
		t.Errorf("expected coverage_min 80, got %d", dod.CoverageMin)
	}
}
