// Package config loads and validates the CHUM TOML configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Duration is a time.Duration that unmarshals from TOML strings like "60s" or "2m".
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalText(text []byte) error {
	var err error
	d.Duration, err = time.ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", string(text), err)
	}
	return nil
}

func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d.Duration.String()), nil
}

type Config struct {
	General    General                   `toml:"general"`
	Projects   map[string]Project        `toml:"projects"`
	RateLimits RateLimits                `toml:"rate_limits"`
	Providers  map[string]Provider       `toml:"providers"`
	Tiers      Tiers                     `toml:"tiers"`
	Workflows  map[string]WorkflowConfig `toml:"workflows"`
	Cadence    Cadence                   `toml:"cadence"`
	Health     Health                    `toml:"health"`
	Reporter   Reporter                  `toml:"reporter"`
	Learner    Learner                   `toml:"learner"`
	Matrix     Matrix                    `toml:"matrix"`
	API        API                       `toml:"api"`
	Dispatch   Dispatch                  `toml:"dispatch"`
	Chief      Chief                     `toml:"chief"`
}

type General struct {
	TickInterval           Duration `toml:"tick_interval"`
	MaxPerTick             int      `toml:"max_per_tick"`
	StuckTimeout           Duration `toml:"stuck_timeout"`
	MaxRetries             int      `toml:"max_retries"`
	RetryBackoffBase       Duration `toml:"retry_backoff_base"`
	RetryMaxDelay          Duration `toml:"retry_max_delay"`
	RetryPolicy            RetryPolicy `toml:"retry_policy"`
	RetryTiers             map[string]RetryPolicy `toml:"retry_tiers"`
	DispatchCooldown       Duration `toml:"dispatch_cooldown"`
	LogLevel               string   `toml:"log_level"`
	StateDB                string   `toml:"state_db"`
	LockFile               string   `toml:"lock_file"`
	MaxConcurrentCoders    int      `toml:"max_concurrent_coders"`    // hard cap on concurrent coder agents
	MaxConcurrentReviewers int      `toml:"max_concurrent_reviewers"` // hard cap on concurrent reviewer agents
	MaxConcurrentTotal     int      `toml:"max_concurrent_total"`     // hard cap on total concurrent agents
	SlowStepThreshold      Duration `toml:"slow_step_threshold"`      // steps exceeding this are flagged slow (default 2m)
}

// Cadence defines shared sprint cadence across all projects.
type Cadence struct {
	SprintLength    string `toml:"sprint_length"`     // e.g. 1w, 2w
	SprintStartDay  string `toml:"sprint_start_day"`  // e.g. Monday
	SprintStartTime string `toml:"sprint_start_time"` // HH:MM 24h
	Timezone        string `toml:"timezone"`          // IANA timezone (e.g. UTC)
}

type Project struct {
	Enabled      bool   `toml:"enabled"`
	BeadsDir     string `toml:"beads_dir"`
	Workspace    string `toml:"workspace"`
	Priority     int    `toml:"priority"`
	MatrixRoom   string `toml:"matrix_room"`   // project-specific Matrix room (optional)
	BaseBranch   string `toml:"base_branch"`   // branch to create features from (default "main")
	BranchPrefix string `toml:"branch_prefix"` // prefix for feature branches (default "feat/")
	UseBranches  bool   `toml:"use_branches"`  // enable branch workflow (default false)
	MergeMethod  string `toml:"merge_method"`  // squash, merge, rebase (default squash)

	PostMergeChecks     []string `toml:"post_merge_checks"`      // checks run after PR merge
	AutoRevertOnFailure bool     `toml:"auto_revert_on_failure"` // auto-revert merge when post-merge checks fail (default true)

	// Sprint planning configuration (optional for backward compatibility)
	SprintPlanningDay  string `toml:"sprint_planning_day"`  // day of week for sprint planning (e.g., "Monday")
	SprintPlanningTime string `toml:"sprint_planning_time"` // time of day for sprint planning (e.g., "09:00")
	SprintCapacity     int    `toml:"sprint_capacity"`      // maximum points/tasks per sprint
	BacklogThreshold   int    `toml:"backlog_threshold"`    // minimum backlog size to maintain

	// Definition of Done configuration
	DoD DoDConfig `toml:"dod"`

	RetryPolicy RetryPolicy `toml:"retry_policy"`
}

type RetryPolicy struct {
	MaxRetries    int      `toml:"max_retries"`
	InitialDelay  Duration `toml:"initial_delay"`
	BackoffFactor float64  `toml:"backoff_factor"`
	MaxDelay      Duration `toml:"max_delay"`
	EscalateAfter int      `toml:"escalate_after"`
}

// DoDConfig defines the Definition of Done configuration for a project
type DoDConfig struct {
	Checks            []string `toml:"checks"`             // commands to run (e.g. "go test ./...", "go vet ./...")
	CoverageMin       int      `toml:"coverage_min"`       // optional: fail if coverage < N%
	RequireEstimate   bool     `toml:"require_estimate"`   // bead must have estimate before closing
	RequireAcceptance bool     `toml:"require_acceptance"` // bead must have acceptance criteria
}

type RateLimits struct {
	Window5hCap       int            `toml:"window_5h_cap"`
	WeeklyCap         int            `toml:"weekly_cap"`
	WeeklyHeadroomPct int            `toml:"weekly_headroom_pct"`
	Budget            map[string]int `toml:"budget"` // project -> percentage allocation
}

type Provider struct {
	Tier              string  `toml:"tier"`
	Authed            bool    `toml:"authed"`
	Model             string  `toml:"model"`
	CLI               string  `toml:"cli"`
	CostInputPerMtok  float64 `toml:"cost_input_per_mtok"`
	CostOutputPerMtok float64 `toml:"cost_output_per_mtok"`
}

type Tiers struct {
	Fast     []string `toml:"fast"`
	Balanced []string `toml:"balanced"`
	Premium  []string `toml:"premium"`
}

type WorkflowConfig struct {
	MatchLabels []string      `toml:"match_labels"`
	MatchTypes  []string      `toml:"match_types"`
	Stages      []StageConfig `toml:"stages"`
}

type StageConfig struct {
	Name string `toml:"name"`
	Role string `toml:"role"`
}

type Health struct {
	CheckInterval          Duration `toml:"check_interval"`
	GatewayUnit            string   `toml:"gateway_unit"`
	GatewayUserService     bool     `toml:"gateway_user_service"`     // use `systemctl --user` instead of system scope
	ConcurrencyWarningPct  float64  `toml:"concurrency_warning_pct"`  // alert threshold (default 0.80)
	ConcurrencyCriticalPct float64  `toml:"concurrency_critical_pct"` // critical threshold (default 0.95)
}

type Reporter struct {
	Channel          string `toml:"channel"`
	AgentID          string `toml:"agent_id"`
	MatrixBotAccount string `toml:"matrix_bot_account"` // optional OpenClaw matrix account id for direct reporting
	DefaultRoom      string `toml:"default_room"`       // fallback Matrix room when project has no explicit room
	DailyDigestTime  string `toml:"daily_digest_time"`
	WeeklyRetroDay   string `toml:"weekly_retro_day"`
}

type Learner struct {
	Enabled         bool     `toml:"enabled"`
	AnalysisWindow  Duration `toml:"analysis_window"`
	CycleInterval   Duration `toml:"cycle_interval"`
	IncludeInDigest bool     `toml:"include_in_digest"`
}

// Matrix configures inbound Matrix polling for scrum master routing.
type Matrix struct {
	Enabled      bool     `toml:"enabled"`
	PollInterval Duration `toml:"poll_interval"`
	BotUser      string   `toml:"bot_user"`
	ReadLimit    int      `toml:"read_limit"`
}

type API struct {
	Bind     string      `toml:"bind"`
	Security APISecurity `toml:"security"`
}

type APISecurity struct {
	Enabled          bool     `toml:"enabled"`            // Enable auth for control endpoints
	AllowedTokens    []string `toml:"allowed_tokens"`     // Valid API tokens for auth
	RequireLocalOnly bool     `toml:"require_local_only"` // Only allow local connections when auth disabled
	AuditLog         string   `toml:"audit_log"`          // Path to audit log file
}

type Dispatch struct {
	CLI              map[string]CLIConfig `toml:"cli"`
	Routing          DispatchRouting      `toml:"routing"`
	Timeouts         DispatchTimeouts     `toml:"timeouts"`
	Git              DispatchGit          `toml:"git"`
	Tmux             DispatchTmux         `toml:"tmux"`
	CostControl      DispatchCostControl  `toml:"cost_control"`
	LogDir           string               `toml:"log_dir"`
	LogRetentionDays int                  `toml:"log_retention_days"`
}

type CLIConfig struct {
	Cmd           string   `toml:"cmd"`
	PromptMode    string   `toml:"prompt_mode"` // "stdin", "file", "arg"
	Args          []string `toml:"args"`
	ModelFlag     string   `toml:"model_flag"`     // e.g. "--model"
	ApprovalFlags []string `toml:"approval_flags"` // e.g. ["--dangerously-skip-permissions"]
}

type DispatchRouting struct {
	FastBackend     string `toml:"fast_backend"` // "headless_cli", "tmux"
	BalancedBackend string `toml:"balanced_backend"`
	PremiumBackend  string `toml:"premium_backend"`
	CommsBackend    string `toml:"comms_backend"`
	RetryBackend    string `toml:"retry_backend"` // backend for retries
}

type DispatchTimeouts struct {
	Fast     Duration `toml:"fast"`     // default 15m
	Balanced Duration `toml:"balanced"` // default 45m
	Premium  Duration `toml:"premium"`  // default 120m
}

type DispatchGit struct {
	BranchPrefix            string `toml:"branch_prefix"`              // default "chum/"
	BranchCleanupDays       int    `toml:"branch_cleanup_days"`        // default 7
	MergeStrategy           string `toml:"merge_strategy"`             // "merge", "squash", "rebase"
	MaxConcurrentPerProject int    `toml:"max_concurrent_per_project"` // default 3
}

type DispatchTmux struct {
	HistoryLimit  int    `toml:"history_limit"`  // default 50000
	SessionPrefix string `toml:"session_prefix"` // default "chum-"
}

// DispatchCostControl defines configurable dispatch policies to reduce expensive usage/churn.
type DispatchCostControl struct {
	Enabled                     bool     `toml:"enabled"`
	SparkFirst                  bool     `toml:"spark_first"`
	RetryEscalationAttempt      int      `toml:"retry_escalation_attempt"`
	ComplexityEscalationMinutes int      `toml:"complexity_escalation_minutes"`
	RiskyReviewLabels           []string `toml:"risky_review_labels"`
	ForceSparkAtWeeklyUsagePct  float64  `toml:"force_spark_at_weekly_usage_pct"`
	DailyCostCapUSD             float64  `toml:"daily_cost_cap_usd"`
	PerBeadCostCapUSD           float64  `toml:"per_bead_cost_cap_usd"`
	PerBeadStageAttemptLimit    int      `toml:"per_bead_stage_attempt_limit"`
	StageAttemptWindow          Duration `toml:"stage_attempt_window"`
	StageCooldown               Duration `toml:"stage_cooldown"`

	// Escalation pause controls for system-level churn/token waste.
	PauseOnChurn      bool     `toml:"pause_on_churn"`
	ChurnPauseWindow  Duration `toml:"churn_pause_window"`
	ChurnPauseFailure int      `toml:"churn_pause_failure_threshold"`
	ChurnPauseTotal   int      `toml:"churn_pause_total_threshold"`

	PauseOnTokenWastage bool     `toml:"pause_on_token_waste"`
	TokenWasteWindow    Duration `toml:"token_waste_window"`
}

type Chief struct {
	Enabled             bool   `toml:"enabled"`               // Enable Chief Scrum Master
	MatrixRoom          string `toml:"matrix_room"`           // Matrix room for coordination
	Model               string `toml:"model"`                 // Model to use (defaults to premium)
	AgentID             string `toml:"agent_id"`              // Agent identifier (defaults to "chum-chief")
	RequireApprovedPlan bool   `toml:"require_approved_plan"` // Block implementation dispatch without active approved plan
}

// Clone returns a deep copy of cfg so callers can safely mutate the result.
func (cfg *Config) Clone() *Config {
	if cfg == nil {
		return nil
	}

	cloned := *cfg
	cloned.General.RetryPolicy = cloneRetryPolicy(cfg.General.RetryPolicy)
	cloned.General.RetryTiers = cloneRetryPolicyMap(cfg.General.RetryTiers)
	cloned.Projects = cloneProjects(cfg.Projects)
	cloned.RateLimits.Budget = cloneStringIntMap(cfg.RateLimits.Budget)
	cloned.Providers = cloneProviders(cfg.Providers)
	cloned.Tiers = Tiers{
		Fast:     cloneStringSlice(cfg.Tiers.Fast),
		Balanced: cloneStringSlice(cfg.Tiers.Balanced),
		Premium:  cloneStringSlice(cfg.Tiers.Premium),
	}
	cloned.Workflows = cloneWorkflows(cfg.Workflows)
	cloned.API.Security.AllowedTokens = cloneStringSlice(cfg.API.Security.AllowedTokens)
	cloned.Dispatch.CLI = cloneCLIConfigMap(cfg.Dispatch.CLI)
	cloned.Dispatch.CostControl.RiskyReviewLabels = cloneStringSlice(cfg.Dispatch.CostControl.RiskyReviewLabels)
	return &cloned
}

func cloneProjects(in map[string]Project) map[string]Project {
	if in == nil {
		return nil
	}
	out := make(map[string]Project, len(in))
	for key, project := range in {
		project.DoD.Checks = cloneStringSlice(project.DoD.Checks)
		project.PostMergeChecks = cloneStringSlice(project.PostMergeChecks)
		project.RetryPolicy = cloneRetryPolicy(project.RetryPolicy)
		out[key] = project
	}
	return out
}

func cloneRetryPolicyMap(in map[string]RetryPolicy) map[string]RetryPolicy {
	if in == nil {
		return nil
	}

	out := make(map[string]RetryPolicy, len(in))
	for key, policy := range in {
		out[strings.ToLower(strings.TrimSpace(key))] = cloneRetryPolicy(policy)
	}
	return out
}

func cloneRetryPolicy(in RetryPolicy) RetryPolicy {
	return RetryPolicy{
		MaxRetries:    in.MaxRetries,
		InitialDelay:  in.InitialDelay,
		BackoffFactor: in.BackoffFactor,
		MaxDelay:      in.MaxDelay,
		EscalateAfter: in.EscalateAfter,
	}
}

func cloneStringIntMap(in map[string]int) map[string]int {
	if in == nil {
		return nil
	}
	out := make(map[string]int, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneProviders(in map[string]Provider) map[string]Provider {
	if in == nil {
		return nil
	}
	out := make(map[string]Provider, len(in))
	for key, provider := range in {
		out[key] = provider
	}
	return out
}

func cloneWorkflows(in map[string]WorkflowConfig) map[string]WorkflowConfig {
	if in == nil {
		return nil
	}
	out := make(map[string]WorkflowConfig, len(in))
	for key, workflow := range in {
		stages := make([]StageConfig, len(workflow.Stages))
		copy(stages, workflow.Stages)
		out[key] = WorkflowConfig{
			MatchLabels: cloneStringSlice(workflow.MatchLabels),
			MatchTypes:  cloneStringSlice(workflow.MatchTypes),
			Stages:      stages,
		}
	}
	return out
}

func cloneCLIConfigMap(in map[string]CLIConfig) map[string]CLIConfig {
	if in == nil {
		return nil
	}
	out := make(map[string]CLIConfig, len(in))
	for key, cfg := range in {
		out[key] = CLIConfig{
			Cmd:           cfg.Cmd,
			PromptMode:    cfg.PromptMode,
			Args:          cloneStringSlice(cfg.Args),
			ModelFlag:     cfg.ModelFlag,
			ApprovalFlags: cloneStringSlice(cfg.ApprovalFlags),
		}
	}
	return out
}

func cloneStringSlice(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// Load reads and validates a CHUM TOML configuration file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	var cfg Config
	md, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	applyDefaults(&cfg, md)
	normalizePaths(&cfg)

	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return &cfg, nil
}

// Reload reads and validates a CHUM TOML configuration file.
//
// This mirrors Load but is intentionally named to reflect runtime refresh paths.
func Reload(path string) (*Config, error) {
	return Load(path)
}

// LoadManager reads config from path and returns an RWMutex-backed thread-safe manager.
func LoadManager(path string) (ConfigManager, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("config path is required")
	}

	cfg, err := Reload(path)
	if err != nil {
		return nil, err
	}
	return NewRWMutexManager(cfg), nil
}

func applyDefaults(cfg *Config, md toml.MetaData) {
	if cfg.General.TickInterval.Duration == 0 {
		cfg.General.TickInterval.Duration = 60 * time.Second
	}
	if cfg.General.StuckTimeout.Duration == 0 {
		cfg.General.StuckTimeout.Duration = 30 * time.Minute
	}
	if cfg.General.MaxPerTick == 0 {
		cfg.General.MaxPerTick = 3
	}
	if cfg.General.MaxRetries == 0 {
		cfg.General.MaxRetries = 3
	}
	if cfg.General.RetryBackoffBase.Duration == 0 {
		cfg.General.RetryBackoffBase.Duration = 5 * time.Minute
	}
	if cfg.General.RetryMaxDelay.Duration == 0 {
		cfg.General.RetryMaxDelay.Duration = 30 * time.Minute
	}
	if cfg.General.RetryPolicy.MaxRetries == 0 {
		cfg.General.RetryPolicy.MaxRetries = cfg.General.MaxRetries
	}
	if cfg.General.RetryPolicy.InitialDelay.Duration == 0 {
		cfg.General.RetryPolicy.InitialDelay = cfg.General.RetryBackoffBase
	}
	if cfg.General.RetryPolicy.MaxDelay.Duration == 0 {
		cfg.General.RetryPolicy.MaxDelay = cfg.General.RetryMaxDelay
	}
	if cfg.General.RetryPolicy.BackoffFactor == 0 {
		cfg.General.RetryPolicy.BackoffFactor = 2.0
	}
	if cfg.General.RetryPolicy.EscalateAfter == 0 {
		cfg.General.RetryPolicy.EscalateAfter = 2
	}
	cfg.General.RetryTiers = normalizeRetryPolicyMap(cfg.General.RetryTiers)
	if cfg.General.RetryTiers == nil {
		cfg.General.RetryTiers = map[string]RetryPolicy{}
	}
	if cfg.General.DispatchCooldown.Duration == 0 {
		cfg.General.DispatchCooldown.Duration = 5 * time.Minute
	}
	if cfg.General.LogLevel == "" {
		cfg.General.LogLevel = "info"
	}

	if cfg.General.SlowStepThreshold.Duration == 0 {
		cfg.General.SlowStepThreshold.Duration = 2 * time.Minute
	}

	// Concurrency limit defaults
	if cfg.General.MaxConcurrentCoders == 0 {
		cfg.General.MaxConcurrentCoders = 25
	}
	if cfg.General.MaxConcurrentReviewers == 0 {
		cfg.General.MaxConcurrentReviewers = 10
	}
	if cfg.General.MaxConcurrentTotal == 0 {
		cfg.General.MaxConcurrentTotal = 40
	}

	if cfg.RateLimits.Window5hCap == 0 {
		cfg.RateLimits.Window5hCap = 20
	}
	if cfg.RateLimits.WeeklyCap == 0 {
		cfg.RateLimits.WeeklyCap = 200
	}
	if cfg.RateLimits.WeeklyHeadroomPct == 0 {
		cfg.RateLimits.WeeklyHeadroomPct = 80
	}

	// Cadence defaults
	if cfg.Cadence.SprintLength == "" {
		cfg.Cadence.SprintLength = "1w"
	}
	if cfg.Cadence.SprintStartDay == "" {
		cfg.Cadence.SprintStartDay = "Monday"
	}
	if cfg.Cadence.SprintStartTime == "" {
		cfg.Cadence.SprintStartTime = "09:00"
	}
	if cfg.Cadence.Timezone == "" {
		cfg.Cadence.Timezone = "UTC"
	}

	// Dispatch timeouts
	if cfg.Dispatch.Timeouts.Fast.Duration == 0 {
		cfg.Dispatch.Timeouts.Fast.Duration = 15 * time.Minute
	}
	if cfg.Dispatch.Timeouts.Balanced.Duration == 0 {
		cfg.Dispatch.Timeouts.Balanced.Duration = 45 * time.Minute
	}
	if cfg.Dispatch.Timeouts.Premium.Duration == 0 {
		cfg.Dispatch.Timeouts.Premium.Duration = 120 * time.Minute
	}

	// Dispatch Git
	if cfg.Dispatch.Git.BranchPrefix == "" {
		cfg.Dispatch.Git.BranchPrefix = "chum/"
	}
	if cfg.Dispatch.Git.BranchCleanupDays == 0 {
		cfg.Dispatch.Git.BranchCleanupDays = 7
	}
	if cfg.Dispatch.Git.MergeStrategy == "" {
		cfg.Dispatch.Git.MergeStrategy = "squash"
	}
	if cfg.Dispatch.Git.MaxConcurrentPerProject == 0 {
		cfg.Dispatch.Git.MaxConcurrentPerProject = 3
	}

	// Dispatch Tmux
	if cfg.Dispatch.Tmux.HistoryLimit == 0 {
		cfg.Dispatch.Tmux.HistoryLimit = 50000
	}
	if cfg.Dispatch.Tmux.SessionPrefix == "" {
		cfg.Dispatch.Tmux.SessionPrefix = "chum-"
	}

	// Dispatch cost-control defaults
	if cfg.Dispatch.CostControl.RetryEscalationAttempt == 0 {
		cfg.Dispatch.CostControl.RetryEscalationAttempt = 2
	}
	if cfg.Dispatch.CostControl.ComplexityEscalationMinutes == 0 {
		cfg.Dispatch.CostControl.ComplexityEscalationMinutes = 120
	}
	if len(cfg.Dispatch.CostControl.RiskyReviewLabels) == 0 {
		cfg.Dispatch.CostControl.RiskyReviewLabels = []string{
			"risk:high",
			"security",
			"migration",
			"breaking-change",
			"database",
		}
	}
	if cfg.Dispatch.CostControl.StageAttemptWindow.Duration == 0 {
		cfg.Dispatch.CostControl.StageAttemptWindow.Duration = 6 * time.Hour
	}
	if cfg.Dispatch.CostControl.StageCooldown.Duration == 0 {
		cfg.Dispatch.CostControl.StageCooldown.Duration = 45 * time.Minute
	}
	if cfg.Dispatch.CostControl.ChurnPauseWindow.Duration == 0 {
		cfg.Dispatch.CostControl.ChurnPauseWindow.Duration = 60 * time.Minute
	}
	if cfg.Dispatch.CostControl.ChurnPauseFailure == 0 {
		cfg.Dispatch.CostControl.ChurnPauseFailure = 12
	}
	if cfg.Dispatch.CostControl.ChurnPauseTotal == 0 {
		cfg.Dispatch.CostControl.ChurnPauseTotal = 24
	}
	if cfg.Dispatch.CostControl.TokenWasteWindow.Duration == 0 {
		cfg.Dispatch.CostControl.TokenWasteWindow.Duration = 24 * time.Hour
	}

	// Dispatch log retention
	if cfg.Dispatch.LogRetentionDays == 0 {
		cfg.Dispatch.LogRetentionDays = 30
	}

	// Health defaults
	if cfg.Health.CheckInterval.Duration == 0 {
		cfg.Health.CheckInterval.Duration = 5 * time.Minute
	}
	if cfg.Health.GatewayUnit == "" {
		cfg.Health.GatewayUnit = "openclaw-gateway.service"
	}
	if cfg.Health.ConcurrencyWarningPct == 0 {
		cfg.Health.ConcurrencyWarningPct = 0.80
	}
	if cfg.Health.ConcurrencyCriticalPct == 0 {
		cfg.Health.ConcurrencyCriticalPct = 0.95
	}

	// Learner defaults
	if cfg.Learner.AnalysisWindow.Duration == 0 {
		cfg.Learner.AnalysisWindow.Duration = 48 * time.Hour
	}
	if cfg.Learner.CycleInterval.Duration == 0 {
		cfg.Learner.CycleInterval.Duration = 6 * time.Hour
	}
	// Enabled defaults to false - must be explicitly enabled
	// IncludeInDigest defaults to false

	// Matrix polling defaults
	if cfg.Matrix.PollInterval.Duration == 0 {
		cfg.Matrix.PollInterval.Duration = 30 * time.Second
	}
	if cfg.Matrix.ReadLimit == 0 {
		cfg.Matrix.ReadLimit = 25
	}

	// Project branch defaults
	for name, project := range cfg.Projects {
		if project.BaseBranch == "" {
			project.BaseBranch = "main"
		}
		if project.BranchPrefix == "" {
			project.BranchPrefix = "feat/"
		}
		if !md.IsDefined("projects", name, "merge_method") {
			project.MergeMethod = "squash"
		}
		project.MergeMethod = strings.ToLower(strings.TrimSpace(project.MergeMethod))

		if !md.IsDefined("projects", name, "auto_revert_on_failure") {
			project.AutoRevertOnFailure = true
		}

		// Sprint planning defaults (optional - no defaults applied to maintain backward compatibility)
		// Users must explicitly configure sprint planning to enable it

		cfg.Projects[name] = project
	}

	// API security defaults
	if !cfg.API.Security.Enabled && cfg.API.Bind != "" && !isLocalBind(cfg.API.Bind) {
		// Default to requiring local-only when security is disabled and binding to non-local
		cfg.API.Security.RequireLocalOnly = true
	}

	// Chief defaults
	if cfg.Chief.Model == "" {
		cfg.Chief.Model = "claude-opus-4-6" // Default to premium tier
	}
	if cfg.Chief.AgentID == "" {
		cfg.Chief.AgentID = "chum-chief"
	}
}

// RetryPolicyFor computes the effective retry policy for a project and tier.
func (cfg *Config) RetryPolicyFor(projectName, tier string) RetryPolicy {
	if cfg == nil {
		return RetryPolicy{
			MaxRetries:    3,
			InitialDelay:  Duration{Duration: 5 * time.Minute},
			BackoffFactor: 2.0,
			MaxDelay:      Duration{Duration: 30 * time.Minute},
			EscalateAfter: 2,
		}
	}

	policy := cfg.General.RetryPolicy
	if tierPolicy, ok := cfg.General.RetryTiers[strings.ToLower(strings.TrimSpace(tier))]; ok {
		policy = mergeRetryPolicy(policy, tierPolicy)
	}

	// If the project exists, merge its override.
	if _, ok := cfg.Projects[projectName]; ok {
		policy = mergeRetryPolicy(policy, cfg.Projects[projectName].RetryPolicy)
	}

	return ensureRetryPolicyDefaults(policy)
}

// ensureRetryPolicyDefaults applies final fallback values in case this config
// was constructed manually and defaults were not applied.
func ensureRetryPolicyDefaults(policy RetryPolicy) RetryPolicy {
	if policy.MaxRetries <= 0 {
		policy.MaxRetries = 3
	}
	if policy.InitialDelay.Duration <= 0 {
		policy.InitialDelay.Duration = 5 * time.Minute
	}
	if policy.BackoffFactor <= 0 {
		policy.BackoffFactor = 2.0
	}
	if policy.MaxDelay.Duration <= 0 {
		policy.MaxDelay.Duration = 30 * time.Minute
	}
	if policy.EscalateAfter <= 0 {
		policy.EscalateAfter = 2
	}
	return policy
}

func mergeRetryPolicy(base RetryPolicy, override RetryPolicy) RetryPolicy {
	if override.MaxRetries != 0 {
		base.MaxRetries = override.MaxRetries
	}
	if override.InitialDelay.Duration != 0 {
		base.InitialDelay = override.InitialDelay
	}
	if override.BackoffFactor != 0 {
		base.BackoffFactor = override.BackoffFactor
	}
	if override.MaxDelay.Duration != 0 {
		base.MaxDelay = override.MaxDelay
	}
	if override.EscalateAfter != 0 {
		base.EscalateAfter = override.EscalateAfter
	}
	return base
}

func normalizeRetryPolicyMap(in map[string]RetryPolicy) map[string]RetryPolicy {
	if len(in) == 0 {
		return map[string]RetryPolicy{}
	}
	out := make(map[string]RetryPolicy, len(in))
	for raw, policy := range in {
		key := strings.ToLower(strings.TrimSpace(raw))
		if key == "" {
			continue
		}
		out[key] = policy
	}
	return out
}

// normalizePaths expands "~" and trims whitespace for configured filesystem paths.
func normalizePaths(cfg *Config) {
	if cfg == nil {
		return
	}

	cfg.General.StateDB = ExpandHome(strings.TrimSpace(cfg.General.StateDB))
	cfg.Dispatch.LogDir = ExpandHome(strings.TrimSpace(cfg.Dispatch.LogDir))
	cfg.API.Security.AuditLog = ExpandHome(strings.TrimSpace(cfg.API.Security.AuditLog))

	for name, project := range cfg.Projects {
		project.BeadsDir = ExpandHome(strings.TrimSpace(project.BeadsDir))
		project.Workspace = ExpandHome(strings.TrimSpace(project.Workspace))
		cfg.Projects[name] = project
	}
}

// isLocalBind checks if a bind address is local (localhost, 127.0.0.1, or unix socket)
func isLocalBind(bind string) bool {
	if bind == "" {
		return true
	}
	if bind[0] == '/' || bind[0] == '@' {
		// Unix socket
		return true
	}
	if strings.HasPrefix(bind, "localhost:") || strings.HasPrefix(bind, "127.0.0.1:") || strings.HasPrefix(bind, ":") {
		return true
	}
	return false
}

func validate(cfg *Config) error {
	knownRoles := map[string]struct{}{
		"scrum":    {},
		"planner":  {},
		"coder":    {},
		"reviewer": {},
		"ops":      {},
	}

	allTierNames := make([]string, 0, len(cfg.Tiers.Fast)+len(cfg.Tiers.Balanced)+len(cfg.Tiers.Premium))
	allTierNames = append(allTierNames, cfg.Tiers.Fast...)
	allTierNames = append(allTierNames, cfg.Tiers.Balanced...)
	allTierNames = append(allTierNames, cfg.Tiers.Premium...)

	for _, name := range allTierNames {
		if _, ok := cfg.Providers[name]; !ok {
			return fmt.Errorf("tier references unknown provider %q", name)
		}
	}

	hasEnabled := false
	for projectName, p := range cfg.Projects {
		if p.Enabled {
			hasEnabled = true
		}

		// Validate sprint planning configuration when provided
		if err := validateSprintPlanningConfig(projectName, p); err != nil {
			return fmt.Errorf("project %q sprint planning config: %w", projectName, err)
		}

		// Validate DoD configuration when provided
		if err := validateDoDConfig(projectName, p.DoD); err != nil {
			return fmt.Errorf("project %q DoD config: %w", projectName, err)
		}
		if err := validateRetryPolicy(fmt.Sprintf("projects.%s.retry_policy", projectName), p.RetryPolicy); err != nil {
			return fmt.Errorf("project %q retry policy: %w", projectName, err)
		}
		if err := validateProjectMergeConfig(projectName, p); err != nil {
			return fmt.Errorf("project %q merge config: %w", projectName, err)
		}
	}
	if !hasEnabled {
		return fmt.Errorf("at least one project must be enabled")
	}

	if err := validateCadenceConfig(cfg.Cadence); err != nil {
		return fmt.Errorf("cadence config: %w", err)
	}

	if err := validateRetryPolicy("general.retry_policy", cfg.General.RetryPolicy); err != nil {
		return fmt.Errorf("general retry policy: %w", err)
	}
	for tier, policy := range cfg.General.RetryTiers {
		if _, ok := map[string]struct{}{"fast": {}, "balanced": {}, "premium": {}}[tier]; !ok {
			return fmt.Errorf("general.retry_tiers.%s: unknown tier %q", tier, tier)
		}
		if err := validateRetryPolicy(fmt.Sprintf("general.retry_tiers.%s", tier), policy); err != nil {
			return fmt.Errorf("general retry tier %q: %w", tier, err)
		}
	}

	if cfg.Workflows != nil {
		if len(cfg.Workflows) == 0 {
			return fmt.Errorf("workflows section exists but defines no workflows")
		}
		for workflowName, wf := range cfg.Workflows {
			if len(wf.Stages) == 0 {
				return fmt.Errorf("workflow %q must define at least one stage", workflowName)
			}
			seenStageNames := make(map[string]struct{}, len(wf.Stages))
			for i, stage := range wf.Stages {
				if stage.Name == "" {
					return fmt.Errorf("workflow %q stage %d missing name", workflowName, i)
				}
				if stage.Role == "" {
					return fmt.Errorf("workflow %q stage %q missing role", workflowName, stage.Name)
				}
				if _, ok := seenStageNames[stage.Name]; ok {
					return fmt.Errorf("workflow %q has duplicate stage name %q", workflowName, stage.Name)
				}
				seenStageNames[stage.Name] = struct{}{}
				if _, ok := knownRoles[stage.Role]; !ok {
					return fmt.Errorf("workflow %q stage %q references unknown role %q", workflowName, stage.Name, stage.Role)
				}
			}
		}
	}

	if cfg.General.StateDB != "" {
		dir := ExpandHome(filepath.Dir(cfg.General.StateDB))
		info, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("state_db directory %q does not exist: %w", dir, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("state_db parent path %q is not a directory", dir)
		}
	}

	// Validate rate limit budget configuration
	if cfg.RateLimits.Budget != nil && len(cfg.RateLimits.Budget) > 0 {
		total := 0
		for project, percentage := range cfg.RateLimits.Budget {
			if percentage < 0 {
				return fmt.Errorf("budget for project %q cannot be negative: %d", project, percentage)
			}
			if percentage > 100 {
				return fmt.Errorf("budget for project %q cannot exceed 100%%: %d", project, percentage)
			}
			total += percentage
		}
		if total != 100 {
			return fmt.Errorf("rate limit budget percentages must sum to 100, got %d", total)
		}
	}

	// Validate API security configuration
	if cfg.API.Security.Enabled {
		if len(cfg.API.Security.AllowedTokens) == 0 {
			return fmt.Errorf("api security enabled but no allowed_tokens configured")
		}
		for i, token := range cfg.API.Security.AllowedTokens {
			if len(token) < 16 {
				return fmt.Errorf("api security token %d is too short (minimum 16 characters)", i)
			}
		}
		if cfg.API.Security.AuditLog != "" {
			dir := ExpandHome(filepath.Dir(cfg.API.Security.AuditLog))
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("cannot create audit log directory %q: %w", dir, err)
			}
		}
	}

	// Validate Chief configuration
	if cfg.Chief.Enabled {
		if cfg.Chief.MatrixRoom == "" {
			return fmt.Errorf("chief scrum master enabled but no matrix_room configured")
		}
	}

	// Validate Matrix polling configuration
	if cfg.Matrix.Enabled {
		if cfg.Matrix.PollInterval.Duration <= 0 {
			return fmt.Errorf("matrix.poll_interval must be > 0")
		}
		if cfg.Matrix.ReadLimit <= 0 {
			return fmt.Errorf("matrix.read_limit must be > 0")
		}
	}

	// Validate dispatch CLI configuration
	if err := ValidateDispatchConfig(cfg); err != nil {
		return fmt.Errorf("dispatch configuration: %w", err)
	}
	if err := validateDispatchCostControlConfig(cfg.Dispatch.CostControl); err != nil {
		return fmt.Errorf("dispatch cost control configuration: %w", err)
	}

	return nil
}

// ExpandHome replaces a leading ~ with the user's home directory.
func ExpandHome(path string) string {
	if len(path) == 0 {
		return path
	}
	if path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[1:])
	}
	return path
}

// GetProjectBudget returns the budget percentage allocated to a project.
// If no budget is configured or the project is not in the budget, returns 0.
func (rl *RateLimits) GetProjectBudget(project string) int {
	if rl.Budget == nil {
		return 0
	}
	return rl.Budget[project]
}

// ResolveRoom returns the Matrix room for a project.
// Priority: projects.<name>.matrix_room -> reporter.default_room -> empty string.
func (cfg *Config) ResolveRoom(project string) string {
	if cfg == nil {
		return ""
	}
	project = strings.TrimSpace(project)
	if project != "" {
		if p, ok := cfg.Projects[project]; ok {
			if room := strings.TrimSpace(p.MatrixRoom); room != "" {
				return room
			}
		}
	}
	return strings.TrimSpace(cfg.Reporter.DefaultRoom)
}

// MissingProjectRoomRouting returns enabled projects that have neither a project room
// nor a reporter-level default room configured.
func (cfg *Config) MissingProjectRoomRouting() []string {
	if cfg == nil {
		return nil
	}
	if strings.TrimSpace(cfg.Reporter.DefaultRoom) != "" {
		return nil
	}

	missing := make([]string, 0)
	for name, project := range cfg.Projects {
		if !project.Enabled {
			continue
		}
		if strings.TrimSpace(project.MatrixRoom) != "" {
			continue
		}
		missing = append(missing, name)
	}
	sort.Strings(missing)
	return missing
}

// SprintLengthDuration parses cadence sprint_length (supports "1w", "2w", and time.ParseDuration formats).
func (c Cadence) SprintLengthDuration() (time.Duration, error) {
	return parseSprintLength(c.SprintLength)
}

// StartWeekday parses cadence sprint_start_day.
func (c Cadence) StartWeekday() (time.Weekday, error) {
	return parseWeekday(c.SprintStartDay)
}

// StartClock parses cadence sprint_start_time as HH:MM.
func (c Cadence) StartClock() (int, int, error) {
	return parseClock(c.SprintStartTime)
}

// LoadLocation parses cadence timezone.
func (c Cadence) LoadLocation() (*time.Location, error) {
	tz := strings.TrimSpace(c.Timezone)
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", tz, err)
	}
	return loc, nil
}

func validateCadenceConfig(c Cadence) error {
	length, err := c.SprintLengthDuration()
	if err != nil {
		return fmt.Errorf("invalid sprint_length: %w", err)
	}
	if length < 24*time.Hour {
		return fmt.Errorf("sprint_length must be at least 24h")
	}
	if length%(24*time.Hour) != 0 {
		return fmt.Errorf("sprint_length must be an exact multiple of 24h")
	}
	if _, err := c.StartWeekday(); err != nil {
		return fmt.Errorf("invalid sprint_start_day: %w", err)
	}
	if _, _, err := c.StartClock(); err != nil {
		return fmt.Errorf("invalid sprint_start_time: %w", err)
	}
	if _, err := c.LoadLocation(); err != nil {
		return err
	}
	return nil
}

func parseSprintLength(raw string) (time.Duration, error) {
	value := strings.TrimSpace(strings.ToLower(raw))
	if value == "" {
		return 0, fmt.Errorf("empty sprint length")
	}
	if strings.HasSuffix(value, "w") {
		weeksRaw := strings.TrimSpace(strings.TrimSuffix(value, "w"))
		weeks, err := strconv.Atoi(weeksRaw)
		if err != nil || weeks <= 0 {
			return 0, fmt.Errorf("invalid week length %q", raw)
		}
		return time.Duration(weeks) * 7 * 24 * time.Hour, nil
	}
	length, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	return length, nil
}

func parseWeekday(raw string) (time.Weekday, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "monday":
		return time.Monday, nil
	case "tuesday":
		return time.Tuesday, nil
	case "wednesday":
		return time.Wednesday, nil
	case "thursday":
		return time.Thursday, nil
	case "friday":
		return time.Friday, nil
	case "saturday":
		return time.Saturday, nil
	case "sunday":
		return time.Sunday, nil
	default:
		return time.Sunday, fmt.Errorf("must be one of Monday, Tuesday, Wednesday, Thursday, Friday, Saturday, Sunday")
	}
}

func parseClock(raw string) (int, int, error) {
	value := strings.TrimSpace(raw)
	if len(value) != 5 || value[2] != ':' {
		return 0, 0, fmt.Errorf("must be in HH:MM format")
	}
	hourRaw := value[:2]
	minuteRaw := value[3:]
	hour, err := strconv.Atoi(hourRaw)
	if err != nil {
		return 0, 0, fmt.Errorf("hour must be numeric")
	}
	minute, err := strconv.Atoi(minuteRaw)
	if err != nil {
		return 0, 0, fmt.Errorf("minute must be numeric")
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("hour must be 00-23 and minute must be 00-59")
	}
	return hour, minute, nil
}

// validateSprintPlanningConfig validates sprint planning configuration for a project.
func validateSprintPlanningConfig(projectName string, project Project) error {
	// Sprint planning day validation
	if project.SprintPlanningDay != "" {
		validDays := map[string]bool{
			"Monday":    true,
			"Tuesday":   true,
			"Wednesday": true,
			"Thursday":  true,
			"Friday":    true,
			"Saturday":  true,
			"Sunday":    true,
		}
		if !validDays[project.SprintPlanningDay] {
			return fmt.Errorf("invalid sprint_planning_day %q, must be one of: Monday, Tuesday, Wednesday, Thursday, Friday, Saturday, Sunday", project.SprintPlanningDay)
		}
	}

	// Sprint planning time validation (24-hour format HH:MM)
	if project.SprintPlanningTime != "" {
		// Basic time format validation - must be HH:MM
		if len(project.SprintPlanningTime) != 5 || project.SprintPlanningTime[2] != ':' {
			return fmt.Errorf("invalid sprint_planning_time %q, must be in HH:MM format (24-hour)", project.SprintPlanningTime)
		}

		// Parse hours and minutes
		hour := project.SprintPlanningTime[:2]
		minute := project.SprintPlanningTime[3:]

		// Simple validation without importing time package parsing
		for _, c := range hour {
			if c < '0' || c > '9' {
				return fmt.Errorf("invalid sprint_planning_time %q, hour must be numeric", project.SprintPlanningTime)
			}
		}
		for _, c := range minute {
			if c < '0' || c > '9' {
				return fmt.Errorf("invalid sprint_planning_time %q, minute must be numeric", project.SprintPlanningTime)
			}
		}

		// Check valid ranges
		if hour > "23" || minute > "59" {
			return fmt.Errorf("invalid sprint_planning_time %q, hour must be 00-23 and minute must be 00-59", project.SprintPlanningTime)
		}
	}

	// Sprint capacity validation
	if project.SprintCapacity < 0 {
		return fmt.Errorf("sprint_capacity cannot be negative: %d", project.SprintCapacity)
	}
	if project.SprintCapacity > 1000 {
		return fmt.Errorf("sprint_capacity seems unreasonably large: %d (max 1000)", project.SprintCapacity)
	}

	// Backlog threshold validation
	if project.BacklogThreshold < 0 {
		return fmt.Errorf("backlog_threshold cannot be negative: %d", project.BacklogThreshold)
	}
	if project.BacklogThreshold > 500 {
		return fmt.Errorf("backlog_threshold seems unreasonably large: %d (max 500)", project.BacklogThreshold)
	}

	// Cross-field validation
	if project.SprintCapacity > 0 && project.BacklogThreshold > 0 {
		if project.BacklogThreshold < project.SprintCapacity {
			return fmt.Errorf("backlog_threshold (%d) should be at least as large as sprint_capacity (%d)", project.BacklogThreshold, project.SprintCapacity)
		}
	}

	return nil
}

// validateDoDConfig validates Definition of Done configuration for a project.
func validateDoDConfig(projectName string, dod DoDConfig) error {
	// Validate coverage_min range
	if dod.CoverageMin < 0 {
		return fmt.Errorf("coverage_min cannot be negative: %d", dod.CoverageMin)
	}
	if dod.CoverageMin > 100 {
		return fmt.Errorf("coverage_min cannot exceed 100: %d", dod.CoverageMin)
	}

	// Note: Empty checks array is valid - DoD can be coverage-only or flags-only
	// Note: All string commands in checks are valid - we can't validate arbitrary commands

	return nil
}

func validateRetryPolicy(fieldPath string, policy RetryPolicy) error {
	if policy.MaxRetries < 0 {
		return fmt.Errorf("%s.max_retries cannot be negative: %d", fieldPath, policy.MaxRetries)
	}
	if policy.InitialDelay.Duration < 0 {
		return fmt.Errorf("%s.initial_delay cannot be negative: %s", fieldPath, policy.InitialDelay)
	}
	if policy.MaxDelay.Duration < 0 {
		return fmt.Errorf("%s.max_delay cannot be negative: %s", fieldPath, policy.MaxDelay)
	}
	if policy.BackoffFactor < 0 {
		return fmt.Errorf("%s.backoff_factor cannot be negative: %f", fieldPath, policy.BackoffFactor)
	}
	if policy.EscalateAfter < 0 {
		return fmt.Errorf("%s.escalate_after cannot be negative: %d", fieldPath, policy.EscalateAfter)
	}
	return nil
}

func validateProjectMergeConfig(projectName string, project Project) error {
	method := strings.ToLower(strings.TrimSpace(project.MergeMethod))
	switch method {
	case "squash", "merge", "rebase":
		return nil
	default:
		return fmt.Errorf("invalid merge_method %q for project %q: must be one of squash, merge, rebase", method, projectName)
	}
}

func validateDispatchCostControlConfig(cc DispatchCostControl) error {
	if cc.PauseOnChurn {
		if cc.ChurnPauseWindow.Duration <= 0 {
			return fmt.Errorf("churn_pause_window must be > 0 when pause_on_churn is enabled")
		}
		if cc.ChurnPauseFailure <= 0 {
			return fmt.Errorf("churn_pause_failure_threshold must be > 0 when pause_on_churn is enabled")
		}
		if cc.ChurnPauseTotal <= 0 {
			return fmt.Errorf("churn_pause_total_threshold must be > 0 when pause_on_churn is enabled")
		}
	}
	if cc.ChurnPauseFailure < 0 {
		return fmt.Errorf("churn_pause_failure_threshold cannot be negative")
	}
	if cc.ChurnPauseTotal < 0 {
		return fmt.Errorf("churn_pause_total_threshold cannot be negative")
	}
	if cc.ChurnPauseWindow.Duration < 0 {
		return fmt.Errorf("churn_pause_window cannot be negative")
	}
	if cc.RetryEscalationAttempt < 0 {
		return fmt.Errorf("retry_escalation_attempt cannot be negative")
	}
	if cc.ComplexityEscalationMinutes < 0 {
		return fmt.Errorf("complexity_escalation_minutes cannot be negative")
	}
	if cc.ForceSparkAtWeeklyUsagePct < 0 || cc.ForceSparkAtWeeklyUsagePct > 100 {
		return fmt.Errorf("force_spark_at_weekly_usage_pct must be between 0 and 100")
	}
	if cc.DailyCostCapUSD < 0 {
		return fmt.Errorf("daily_cost_cap_usd cannot be negative")
	}
	if cc.PerBeadCostCapUSD < 0 {
		return fmt.Errorf("per_bead_cost_cap_usd cannot be negative")
	}
	if cc.PerBeadStageAttemptLimit < 0 {
		return fmt.Errorf("per_bead_stage_attempt_limit cannot be negative")
	}
	if cc.PerBeadStageAttemptLimit > 0 && cc.StageAttemptWindow.Duration <= 0 {
		return fmt.Errorf("stage_attempt_window must be > 0 when per_bead_stage_attempt_limit is set")
	}
	if cc.PerBeadStageAttemptLimit > 0 && cc.StageCooldown.Duration < 0 {
		return fmt.Errorf("stage_cooldown cannot be negative")
	}
	if cc.TokenWasteWindow.Duration < 0 {
		return fmt.Errorf("token_waste_window cannot be negative")
	}
	if cc.PauseOnTokenWastage && cc.DailyCostCapUSD <= 0 {
		return fmt.Errorf("pause_on_token_waste requires daily_cost_cap_usd > 0")
	}
	if cc.PauseOnTokenWastage && cc.TokenWasteWindow.Duration == 0 {
		return fmt.Errorf("token_waste_window must be > 0 when pause_on_token_waste is enabled")
	}
	return nil
}

// DispatchValidationIssue is a structured dispatch config validation failure.
type DispatchValidationIssue struct {
	FieldPath  string
	Message    string
	Suggestion string
}

// DispatchValidationError aggregates dispatch config validation failures.
type DispatchValidationError struct {
	Issues []DispatchValidationIssue
}

func (e *DispatchValidationError) Error() string {
	if e == nil || len(e.Issues) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("dispatch validation failed")
	for _, issue := range e.Issues {
		b.WriteString("\n  - ")
		if issue.FieldPath != "" {
			b.WriteString(issue.FieldPath)
			b.WriteString(": ")
		}
		b.WriteString(issue.Message)
		if strings.TrimSpace(issue.Suggestion) != "" {
			b.WriteString(" (suggestion: ")
			b.WriteString(issue.Suggestion)
			b.WriteString(")")
		}
	}
	return b.String()
}

func (e *DispatchValidationError) add(fieldPath, message, suggestion string) {
	e.Issues = append(e.Issues, DispatchValidationIssue{
		FieldPath:  fieldPath,
		Message:    message,
		Suggestion: suggestion,
	})
}

// ValidateDispatchConfig validates the dispatch configuration at startup.
// This prevents runtime command failures due to config/CLI drift.
func ValidateDispatchConfig(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	knownBackends := map[string]struct{}{
		"tmux":         {},
		"headless_cli": {},
		"openclaw":     {},
	}
	cliRequiredBackends := map[string]struct{}{
		"tmux":         {},
		"headless_cli": {},
	}

	routing := cfg.Dispatch.Routing
	backends := map[string]string{
		"fast":     routing.FastBackend,
		"balanced": routing.BalancedBackend,
		"premium":  routing.PremiumBackend,
		"comms":    routing.CommsBackend,
		"retry":    routing.RetryBackend,
	}

	validationErr := &DispatchValidationError{}
	dispatchConfigured := len(cfg.Dispatch.CLI) > 0
	for _, backend := range backends {
		if strings.TrimSpace(backend) != "" {
			dispatchConfigured = true
			break
		}
	}
	if !dispatchConfigured {
		for _, provider := range cfg.Providers {
			if strings.TrimSpace(provider.CLI) != "" {
				dispatchConfigured = true
				break
			}
		}
	}

	// Validate backend names.
	for tier, backend := range backends {
		trimmed := strings.TrimSpace(backend)
		if trimmed == "" {
			continue
		}
		if _, ok := knownBackends[trimmed]; !ok {
			validationErr.add(
				fmt.Sprintf("dispatch.routing.%s_backend", tier),
				fmt.Sprintf("invalid backend type %q (valid: tmux, headless_cli, openclaw)", backend),
				"choose one of: tmux, headless_cli, openclaw",
			)
		}
	}

	// Validate CLI config blocks.
	for cliName, cliConfig := range cfg.Dispatch.CLI {
		if err := validateCLIConfig(cliName, cliConfig); err != nil {
			validationErr.add(
				fmt.Sprintf("dispatch.cli.%s", cliName),
				err.Error(),
				"check dispatch CLI configuration fields",
			)
		}
	}

	// Validate provider -> backend -> CLI requirements for dispatch tiers.
	tierBackends := map[string]string{
		"fast":     strings.TrimSpace(routing.FastBackend),
		"balanced": strings.TrimSpace(routing.BalancedBackend),
		"premium":  strings.TrimSpace(routing.PremiumBackend),
	}
	for providerName, provider := range cfg.Providers {
		tier := strings.TrimSpace(strings.ToLower(provider.Tier))
		backend := tierBackends[tier]
		if dispatchConfigured && tier != "" && backend == "" {
			validationErr.add(
				fmt.Sprintf("providers.%s.tier", providerName),
				fmt.Sprintf("tier %q requires dispatch.routing.%s_backend to be configured", tier, tier),
				fmt.Sprintf("set dispatch.routing.%s_backend to tmux, headless_cli, or openclaw", tier),
			)
			continue
		}
		if _, needsCLI := cliRequiredBackends[backend]; !needsCLI {
			continue
		}

		cliKey, source := resolveProviderCLIKey(provider.CLI, cfg.Dispatch.CLI)
		if cliKey == "" {
			validationErr.add(
				fmt.Sprintf("providers.%s.cli", providerName),
				fmt.Sprintf("no CLI binding resolved for provider %q using %s backend", providerName, backend),
				fmt.Sprintf("set providers.%s.cli or define dispatch.cli.codex", providerName),
			)
			continue
		}

		cliCfg, ok := cfg.Dispatch.CLI[cliKey]
		if !ok {
			field := fmt.Sprintf("providers.%s.cli", providerName)
			if source == "default_cli" {
				field = "dispatch.cli"
			}
			validationErr.add(
				field,
				fmt.Sprintf("provider %q references undefined CLI config %q", providerName, cliKey),
				fmt.Sprintf("add [dispatch.cli.%s] or update providers.%s.cli", cliKey, providerName),
			)
			continue
		}

		if strings.TrimSpace(provider.Model) != "" && strings.TrimSpace(cliCfg.ModelFlag) == "" {
			validationErr.add(
				fmt.Sprintf("dispatch.cli.%s.model_flag", cliKey),
				fmt.Sprintf("model_flag is required for provider %q (model=%q)", providerName, provider.Model),
				"set model_flag (for example --model or -m)",
			)
		}
	}

	if len(validationErr.Issues) > 0 {
		return validationErr
	}
	return nil
}

// validateCLIConfig validates an individual CLI configuration.
func validateCLIConfig(name string, config CLIConfig) error {
	// Validate command is specified
	if config.Cmd == "" {
		return fmt.Errorf("cmd is required")
	}

	// Validate prompt_mode
	validPromptModes := map[string]bool{
		"stdin": true,
		"file":  true,
		"arg":   true,
	}
	if config.PromptMode != "" && !validPromptModes[config.PromptMode] {
		return fmt.Errorf("invalid prompt_mode %q (valid: stdin, file, arg)", config.PromptMode)
	}

	// Validate model_flag format if specified
	if config.ModelFlag != "" {
		if !strings.HasPrefix(config.ModelFlag, "-") {
			return fmt.Errorf("model_flag %q must start with '-' (e.g., '--model', '-m')", config.ModelFlag)
		}
	}

	// Validate approval_flags format if specified
	for i, flag := range config.ApprovalFlags {
		if !strings.HasPrefix(flag, "-") {
			return fmt.Errorf("approval_flags[%d] %q must start with '-'", i, flag)
		}
	}

	return nil
}

// resolveProviderCLIKey resolves provider -> dispatch.cli key deterministically.
// Resolution order (matching runtime defaults):
// 1) providers.<name>.cli when set
// 2) dispatch.cli.codex when present
// 3) lexicographically first dispatch.cli key
func resolveProviderCLIKey(explicitCLI string, cliConfigs map[string]CLIConfig) (key string, source string) {
	if trimmed := strings.TrimSpace(explicitCLI); trimmed != "" {
		return trimmed, "provider.cli"
	}
	if _, ok := cliConfigs["codex"]; ok {
		return "codex", "default_cli"
	}
	keys := make([]string, 0, len(cliConfigs))
	for key := range cliConfigs {
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return "", "none"
	}
	sort.Strings(keys)
	return keys[0], "default_cli"
}
