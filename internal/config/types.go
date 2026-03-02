package config

import (
	"fmt"
	"time"
)

// Duration is a time.Duration that unmarshals from TOML strings like "60s" or "2m".
type Duration struct {
	time.Duration
}

// UnmarshalText parses a Go duration string (e.g. "30s", "2m").
func (d *Duration) UnmarshalText(text []byte) error {
	var err error
	d.Duration, err = time.ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", string(text), err)
	}
	return nil
}

// MarshalText formats the duration as a string.
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d.Duration.String()), nil
}

// Config is the top-level CHUM configuration loaded from chum.toml.
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
	Crab       Crab                      `toml:"crab"`
	Calcifier  Calcifier                 `toml:"calcifier"`
}

// General holds top-level scheduler settings (tick interval, retries, concurrency caps).
type General struct {
	TickInterval           Duration               `toml:"tick_interval"`
	MaxPerTick             int                    `toml:"max_per_tick"`
	StuckTimeout           Duration               `toml:"stuck_timeout"`
	MaxRetries             int                    `toml:"max_retries"`
	RetryBackoffBase       Duration               `toml:"retry_backoff_base"`
	RetryMaxDelay          Duration               `toml:"retry_max_delay"`
	RetryPolicy            RetryPolicy            `toml:"retry_policy"`
	RetryTiers             map[string]RetryPolicy `toml:"retry_tiers"`
	DispatchCooldown       Duration               `toml:"dispatch_cooldown"`
	LogLevel               string                 `toml:"log_level"`
	StateDB                string                 `toml:"state_db"`
	LockFile               string                 `toml:"lock_file"`
	MaxConcurrentCoders    int                    `toml:"max_concurrent_coders"`    // hard cap on concurrent coder agents
	MaxConcurrentReviewers int                    `toml:"max_concurrent_reviewers"` // hard cap on concurrent reviewer agents
	MaxConcurrentTotal     int                    `toml:"max_concurrent_total"`     // hard cap on total concurrent agents
	SlowStepThreshold      Duration               `toml:"slow_step_threshold"`      // steps exceeding this are flagged slow (default 2m)
	TemporalHostPort       string                 `toml:"temporal_host_port"`       // Temporal server address (default 127.0.0.1:7233)
	MaxAgentIterations     int                    `toml:"max_agent_iterations"`     // tool-call iteration budget per agent run (default 50)
}

// Cadence defines shared sprint cadence across all projects.
type Cadence struct {
	SprintLength    string `toml:"sprint_length"`     // e.g. 1w, 2w
	SprintStartDay  string `toml:"sprint_start_day"`  // e.g. Monday
	SprintStartTime string `toml:"sprint_start_time"` // HH:MM 24h
	Timezone        string `toml:"timezone"`          // IANA timezone (e.g. UTC)
}

// Project configures a single managed project (workspace, branching, DoD).
type Project struct {
	Enabled      bool   `toml:"enabled"`
	MorselsDir   string `toml:"morsels_dir"`
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

// RetryPolicy defines exponential backoff retry parameters.
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
	RequireEstimate   bool     `toml:"require_estimate"`   // morsel must have estimate before closing
	RequireAcceptance bool     `toml:"require_acceptance"` // morsel must have acceptance criteria
}

// RateLimits configures rolling-window and weekly dispatch caps.
type RateLimits struct {
	Window5hCap       int            `toml:"window_5h_cap"`
	WeeklyCap         int            `toml:"weekly_cap"`
	WeeklyHeadroomPct int            `toml:"weekly_headroom_pct"`
	Budget            map[string]int `toml:"budget"` // project -> percentage allocation
}

// Provider defines an LLM provider with tier, model, and cost info.
type Provider struct {
	Tier              string  `toml:"tier"`
	Authed            bool    `toml:"authed"`
	Enabled           *bool   `toml:"enabled"` // nil = enabled (default true for backward compat)
	Model             string  `toml:"model"`
	CLI               string  `toml:"cli"`
	Reviewer          string  `toml:"reviewer"`  // reviewer agent for cross-model review (empty = DefaultReviewer)
	TokenCap          int     `toml:"token_cap"` // max output tokens per day (0 = unlimited)
	CostInputPerMtok  float64 `toml:"cost_input_per_mtok"`
	CostOutputPerMtok float64 `toml:"cost_output_per_mtok"`
}

// IsEnabled returns true if the provider is enabled (defaults to true if not set).
func (p Provider) IsEnabled() bool {
	if p.Enabled == nil {
		return true
	}
	return *p.Enabled
}

// Tiers maps provider names into fast/balanced/premium groups.
type Tiers struct {
	Fast     []string `toml:"fast"`
	Balanced []string `toml:"balanced"`
	Premium  []string `toml:"premium"`
}

// WorkflowConfig defines a Temporal workflow template with label matching and stages.
type WorkflowConfig struct {
	MatchLabels []string      `toml:"match_labels"`
	MatchTypes  []string      `toml:"match_types"`
	Stages      []StageConfig `toml:"stages"`
}

// StageConfig names a workflow stage and the agent role that executes it.
type StageConfig struct {
	Name string `toml:"name"`
	Role string `toml:"role"`
}

// Health configures system health monitoring intervals and thresholds.
type Health struct {
	CheckInterval          Duration `toml:"check_interval"`
	GatewayUnit            string   `toml:"gateway_unit"`
	GatewayUserService     bool     `toml:"gateway_user_service"`     // use `systemctl --user` instead of system scope
	ConcurrencyWarningPct  float64  `toml:"concurrency_warning_pct"`  // alert threshold (default 0.80)
	ConcurrencyCriticalPct float64  `toml:"concurrency_critical_pct"` // critical threshold (default 0.95)
}

// Reporter configures Matrix-based status reporting and digests.
type Reporter struct {
	Channel          string `toml:"channel"`
	AgentID          string `toml:"agent_id"`
	MatrixBotAccount string `toml:"matrix_bot_account"` // optional OpenClaw matrix account id for direct reporting
	DefaultRoom      string `toml:"default_room"`       // fallback Matrix room when project has no explicit room
	AdminRoom        string `toml:"admin_room"`         // direct message room for critical escalations
	TurtleRoom       string `toml:"turtle_room"`        // 3-agent deliberation channel
	DailyDigestTime  string `toml:"daily_digest_time"`
	WeeklyRetroDay   string `toml:"weekly_retro_day"`
}

// Learner configures the post-dispatch learning analysis loop.
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

// API configures the HTTP status/control API server.
type API struct {
	Bind     string      `toml:"bind"`
	Security APISecurity `toml:"security"`
}

// APISecurity configures token-based auth and audit logging for the API.
type APISecurity struct {
	Enabled          bool     `toml:"enabled"`            // Enable auth for control endpoints
	AllowedTokens    []string `toml:"allowed_tokens"`     // Valid API tokens for auth
	RequireLocalOnly bool     `toml:"require_local_only"` // Only allow local connections when auth disabled
	AuditLog         string   `toml:"audit_log"`          // Path to audit log file
}

// Dispatch configures agent dispatch backends, routing, and cost control.
type Dispatch struct {
	CLI              map[string]CLIConfig `toml:"cli"`
	Routing          DispatchRouting      `toml:"routing"`
	Timeouts         DispatchTimeouts     `toml:"timeouts"`
	Git              DispatchGit          `toml:"git"`
	CostControl      DispatchCostControl  `toml:"cost_control"`
	LogDir           string               `toml:"log_dir"`
	LogRetentionDays int                  `toml:"log_retention_days"`
}

// CLIConfig defines a headless CLI backend (command, args, prompt delivery mode).
type CLIConfig struct {
	Cmd           string            `toml:"cmd"`
	PromptMode    string            `toml:"prompt_mode"` // "stdin", "file", "arg"
	Args          []string          `toml:"args"`
	ModelFlag     string            `toml:"model_flag"`     // e.g. "--model"
	ApprovalFlags []string          `toml:"approval_flags"` // e.g. ["--dangerously-skip-permissions"]
	Env           map[string]string `toml:"env"`            // environment variable overrides for child process
}

// DispatchRouting maps provider tiers to dispatch backends.
type DispatchRouting struct {
	FastBackend     string `toml:"fast_backend"` // "headless_cli", "openclaw"
	BalancedBackend string `toml:"balanced_backend"`
	PremiumBackend  string `toml:"premium_backend"`
	CommsBackend    string `toml:"comms_backend"`
	RetryBackend    string `toml:"retry_backend"` // backend for retries
}

// DispatchTimeouts sets per-tier maximum execution durations.
type DispatchTimeouts struct {
	Fast     Duration `toml:"fast"`     // default 15m
	Balanced Duration `toml:"balanced"` // default 45m
	Premium  Duration `toml:"premium"`  // default 120m
}

// DispatchGit configures branch naming and merge strategy for dispatches.
type DispatchGit struct {
	BranchPrefix            string `toml:"branch_prefix"`              // default "chum/"
	BranchCleanupDays       int    `toml:"branch_cleanup_days"`        // default 7
	MergeStrategy           string `toml:"merge_strategy"`             // "merge", "squash", "rebase"
	MaxConcurrentPerProject int    `toml:"max_concurrent_per_project"` // default 3
}

// DispatchCostControl defines configurable dispatch policies to reduce expensive usage/churn.
type DispatchCostControl struct {
	Enabled                     bool     `toml:"enabled"`
	SparkFirst                  bool     `toml:"spark_first"`
	EnablePlannerV2             bool     `toml:"enable_planner_v2"`
	PlanningCandidateTopK       int      `toml:"planning_candidate_top_k"`
	PlanningSignalTimeout       Duration `toml:"planning_signal_timeout"`
	PlanningSessionTimeout      Duration `toml:"planning_session_timeout"`
	PlanningStaleBlockThreshold Duration `toml:"planning_stale_block_threshold"`
	RetryEscalationAttempt      int      `toml:"retry_escalation_attempt"`
	ComplexityEscalationMinutes int      `toml:"complexity_escalation_minutes"`
	RiskyReviewLabels           []string `toml:"risky_review_labels"`
	ForceSparkAtWeeklyUsagePct  float64  `toml:"force_spark_at_weekly_usage_pct"`
	DailyCostCapUSD             float64  `toml:"daily_cost_cap_usd"`
	PerMorselCostCapUSD         float64  `toml:"per_morsel_cost_cap_usd"`
	PerMorselStageAttemptLimit  int      `toml:"per_morsel_stage_attempt_limit"`
	StageAttemptWindow          Duration `toml:"stage_attempt_window"`
	StageCooldown               Duration `toml:"stage_cooldown"`

	// Beached shark window: how long to exclude escalated tasks from re-dispatch.
	// Default 24h. Set to "0s" to disable (allow immediate re-dispatch of escalated tasks).
	BeachedSharkWindow Duration `toml:"beached_shark_window"`

	PauseOnTokenWastage bool     `toml:"pause_on_token_waste"`
	TokenWasteWindow    Duration `toml:"token_waste_window"`

	// Higher-learning mode: fewer retries, faster escalation, maximum learning signal.
	HigherLearning HigherLearning `toml:"higher_learning"`
}

// HigherLearning configures reduced-retry mode for overnight/unattended runs.
// Fewer retries per tier → faster escalation → more diverse failure data per token.
type HigherLearning struct {
	Enabled     bool `toml:"enabled"`
	MaxRetries  int  `toml:"max_retries"`  // per-tier retry cap (default 1 when enabled)
	MaxHandoffs int  `toml:"max_handoffs"` // cross-model review handoff cap (default 1 when enabled)
}

// Chief configures the Chief Scrum Master coordination agent.
type Chief struct {
	Enabled             bool   `toml:"enabled"`               // Enable Chief Scrum Master
	MatrixRoom          string `toml:"matrix_room"`           // Matrix room for coordination
	Model               string `toml:"model"`                 // Model to use (defaults to premium)
	AgentID             string `toml:"agent_id"`              // Agent identifier (defaults to "chum-chief")
	RequireApprovedPlan bool   `toml:"require_approved_plan"` // Block implementation dispatch without active approved plan
}

// Crab configures the Crab decomposition agent.
// Crabs sit between Chief/human and Sharks: they take a high-level markdown
// plan and decompose it into whales (epic-level groupings) and morsels
// (bite-sized executable units) for shark consumption.
type Crab struct {
	Enabled           bool   `toml:"enabled"`              // Enable Crab decomposition agent
	Tier              string `toml:"tier"`                 // Default LLM tier: "fast", "balanced", or "premium" (default "fast")
	MaxMorselsPerPlan int    `toml:"max_morsels_per_plan"` // Maximum morsels emitted per plan (default 20)
	MaxScopeItems     int    `toml:"max_scope_items"`      // Maximum scope items accepted in a plan (default 10)
	AutoApprove       bool   `toml:"auto_approve"`         // Phase 2: auto-approve high-confidence decompositions (default false)
}

// Calcifier configures the stochastic→deterministic calcification pipeline.
// When a morsel type is repeatedly solved by the LLM, the calcifier generates
// a deterministic script to replace the LLM call entirely.
type Calcifier struct {
	Enabled             bool   `toml:"enabled"`
	CalcifiedDir        string `toml:"calcified_dir"`         // directory for calcified scripts (default ".cortex/calcified")
	CompileThreshold    int    `toml:"compile_threshold"`     // consecutive successes before compilation (default 10)
	PromoteThreshold    int    `toml:"promote_threshold"`     // shadow matches before promotion (default 3)
	RiskMultiplier      int    `toml:"risk_multiplier"`       // multiplier for risky morsel types (default 3)
	QuarantineOnNonzero bool   `toml:"quarantine_on_nonzero"` // quarantine scripts on non-zero exit
	CompileModel        string `toml:"compile_model"`         // LLM model used to generate scripts (default "gemini-pro")
}

// EffectiveThreshold returns the compile threshold adjusted for risk.
// Morsels carrying risky labels (security, migration, etc.) require
// CompileThreshold × RiskMultiplier consecutive successes.
func (c Calcifier) EffectiveThreshold(labels, riskyLabels []string) int {
	threshold := c.CompileThreshold
	if threshold == 0 {
		threshold = 10
	}
	multiplier := c.RiskMultiplier
	if multiplier == 0 {
		multiplier = 3
	}
	for _, l := range labels {
		for _, r := range riskyLabels {
			if l == r {
				return threshold * multiplier
			}
		}
	}
	return threshold
}
