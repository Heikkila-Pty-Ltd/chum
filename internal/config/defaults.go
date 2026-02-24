package config

import (
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

func applyDefaults(cfg *Config, md toml.MetaData) {
	if cfg.General.TemporalHostPort == "" {
		cfg.General.TemporalHostPort = "127.0.0.1:7233"
	}
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

	// Higher-learning defaults: fewer retries, faster escalation
	if cfg.Dispatch.CostControl.HigherLearning.Enabled {
		if cfg.Dispatch.CostControl.HigherLearning.MaxRetries == 0 {
			cfg.Dispatch.CostControl.HigherLearning.MaxRetries = 1
		}
		if cfg.Dispatch.CostControl.HigherLearning.MaxHandoffs == 0 {
			cfg.Dispatch.CostControl.HigherLearning.MaxHandoffs = 1
		}
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

	// Crab defaults
	if cfg.Crab.Tier == "" {
		cfg.Crab.Tier = "fast"
	}
	if cfg.Crab.MaxMorselsPerPlan == 0 {
		cfg.Crab.MaxMorselsPerPlan = 20
	}
	if cfg.Crab.MaxScopeItems == 0 {
		cfg.Crab.MaxScopeItems = 10
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
