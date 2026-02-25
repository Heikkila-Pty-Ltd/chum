package config

import (
	"testing"
	"time"
)

func TestDispatchCostControlPlannerV2DefaultsFalse(t *testing.T) {
	path := writeTestConfig(t, validConfig)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Dispatch.CostControl.EnablePlannerV2 {
		t.Fatal("enable_planner_v2 = true, want false by default")
	}
}

func TestDispatchCostControlPlannerV2CanBeEnabled(t *testing.T) {
	cfgText := validConfig + `

[dispatch.cost_control]
enable_planner_v2 = true
`
	path := writeTestConfig(t, cfgText)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !cfg.Dispatch.CostControl.EnablePlannerV2 {
		t.Fatal("enable_planner_v2 = false, want true")
	}
}

func TestDispatchCostControlPlanningCandidateTopKDefaultsFive(t *testing.T) {
	path := writeTestConfig(t, validConfig)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Dispatch.CostControl.PlanningCandidateTopK != 5 {
		t.Fatalf("planning_candidate_top_k = %d, want 5 by default", cfg.Dispatch.CostControl.PlanningCandidateTopK)
	}
}

func TestDispatchCostControlPlanningCandidateTopKCanBeConfigured(t *testing.T) {
	cfgText := validConfig + `

[dispatch.cost_control]
planning_candidate_top_k = 8
`
	path := writeTestConfig(t, cfgText)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Dispatch.CostControl.PlanningCandidateTopK != 8 {
		t.Fatalf("planning_candidate_top_k = %d, want 8", cfg.Dispatch.CostControl.PlanningCandidateTopK)
	}
}

func TestDispatchCostControlPlanningCandidateTopKRejectsOversizedValue(t *testing.T) {
	cfgText := validConfig + `

[dispatch.cost_control]
planning_candidate_top_k = 21
`
	path := writeTestConfig(t, cfgText)
	if _, err := Load(path); err == nil {
		t.Fatal("expected planning_candidate_top_k > 20 to fail validation")
	}
}

func TestDispatchCostControlPlanningTimeoutDefaults(t *testing.T) {
	path := writeTestConfig(t, validConfig)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Dispatch.CostControl.PlanningSignalTimeout.Duration != 10*time.Minute {
		t.Fatalf("planning_signal_timeout = %v, want 10m", cfg.Dispatch.CostControl.PlanningSignalTimeout.Duration)
	}
	if cfg.Dispatch.CostControl.PlanningSessionTimeout.Duration != 30*time.Minute {
		t.Fatalf("planning_session_timeout = %v, want 30m", cfg.Dispatch.CostControl.PlanningSessionTimeout.Duration)
	}
	if cfg.Dispatch.CostControl.PlanningStaleBlockThreshold.Duration != 35*time.Minute {
		t.Fatalf("planning_stale_block_threshold = %v, want 35m", cfg.Dispatch.CostControl.PlanningStaleBlockThreshold.Duration)
	}
}

func TestDispatchCostControlPlanningStaleThresholdMustExceedSessionTimeout(t *testing.T) {
	cfgText := validConfig + `

[dispatch.cost_control]
planning_session_timeout = "30m"
planning_stale_block_threshold = "20m"
`
	path := writeTestConfig(t, cfgText)
	if _, err := Load(path); err == nil {
		t.Fatal("expected planning_stale_block_threshold <= planning_session_timeout to fail validation")
	}
}
