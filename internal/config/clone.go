package config

import "strings"

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
