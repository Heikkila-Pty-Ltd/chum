package main

import (
	"time"

	"testing"

	"github.com/antigravity-dev/chum/internal/config"
)

func TestValidateRuntimeConfigReloadAllowsLogLevelChange(t *testing.T) {
	oldCfg := &config.Config{
		General: config.General{
			StateDB: "db1",
			LogLevel: "info",
		},
		API: config.API{Bind: "127.0.0.1:8900"},
	}
	newCfg := &config.Config{
		General: config.General{
			StateDB: "db1",
			LogLevel: "debug",
		},
		API: config.API{Bind: "127.0.0.1:8900"},
	}
	if err := validateRuntimeConfigReload(oldCfg, newCfg); err != nil {
		t.Fatalf("expected reload to be allowed, got %v", err)
	}
}

func TestValidateRuntimeConfigReloadAllowsReloadableFields(t *testing.T) {
	oldCfg := &config.Config{
		General: config.General{
			StateDB:     "db1",
			LogLevel:    "info",
			TickInterval: config.Duration{Duration: 60 * time.Second},
		},
		API: config.API{Bind: "127.0.0.1:8900"},
		RateLimits: config.RateLimits{
			Window5hCap: 20,
			WeeklyCap:   200,
			Budget:      map[string]int{"project-a": 100},
		},
		Providers: map[string]config.Provider{
			"p1": {Tier: "fast", Model: "m1", Authed: false},
		},
		Tiers: config.Tiers{
			Fast:     []string{"p1"},
			Balanced: []string{},
			Premium:  []string{},
		},
		Projects: map[string]config.Project{
			"project-a": {Enabled: true},
		},
	}
	newCfg := &config.Config{
		General: config.General{
			StateDB:     "db1",
			LogLevel:    "debug",
			TickInterval: config.Duration{Duration: 120 * time.Second},
		},
		API: config.API{Bind: "127.0.0.1:8900"},
		RateLimits: config.RateLimits{
			Window5hCap: 10,
			WeeklyCap:   100,
			Budget:      map[string]int{"project-a": 50, "project-b": 50},
		},
		Providers: map[string]config.Provider{
			"p1": {Tier: "fast", Model: "m1", Authed: false},
			"p2": {Tier: "balanced", Model: "m2", Authed: true},
		},
		Tiers: config.Tiers{
			Fast:     []string{"p1"},
			Balanced: []string{"p2"},
			Premium:  []string{},
		},
		Projects: map[string]config.Project{
			"project-a": {Enabled: false},
			"project-b": {Enabled: true},
		},
	}

	if err := validateRuntimeConfigReload(oldCfg, newCfg); err != nil {
		t.Fatalf("expected reload to allow reloadable changes, got %v", err)
	}
}

func TestValidateRuntimeConfigReloadRejectsStateDBChange(t *testing.T) {
	oldCfg := &config.Config{
		General: config.General{StateDB: "db1"},
		API:     config.API{Bind: "127.0.0.1:8900"},
	}
	newCfg := &config.Config{
		General: config.General{StateDB: "db2"},
		API:     config.API{Bind: "127.0.0.1:8900"},
	}
	if err := validateRuntimeConfigReload(oldCfg, newCfg); err == nil {
		t.Fatal("expected state_db reload validation error")
	}
}

func TestValidateRuntimeConfigReloadRejectsAPIBindChange(t *testing.T) {
	oldCfg := &config.Config{
		General: config.General{StateDB: "db1"},
		API:     config.API{Bind: "127.0.0.1:8900"},
	}
	newCfg := &config.Config{
		General: config.General{StateDB: "db1"},
		API:     config.API{Bind: "127.0.0.1:9000"},
	}
	if err := validateRuntimeConfigReload(oldCfg, newCfg); err == nil {
		t.Fatal("expected api.bind reload validation error")
	}
}

func TestValidateRuntimeConfigReloadAllowsWhitespaceNormalization(t *testing.T) {
	oldCfg := &config.Config{
		General: config.General{StateDB: "db1", LogLevel: "info"},
		API:     config.API{Bind: "127.0.0.1:8900"},
	}
	newCfg := &config.Config{
		General: config.General{StateDB: "  db1 ", LogLevel: "debug"},
		API:     config.API{Bind: " 127.0.0.1:8900 "},
	}

	if err := validateRuntimeConfigReload(oldCfg, newCfg); err != nil {
		t.Fatalf("expected whitespace-trimmed config reload to be allowed, got: %v", err)
	}
}

func TestValidateRuntimeConfigReloadRejectsNilConfig(t *testing.T) {
	if err := validateRuntimeConfigReload(nil, &config.Config{}); err == nil {
		t.Fatal("expected nil old config to be invalid")
	}
	if err := validateRuntimeConfigReload(&config.Config{}, nil); err == nil {
		t.Fatal("expected nil new config to be invalid")
	}
}
