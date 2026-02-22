package main

import (
	"context"
	"time"

	"testing"

	"github.com/antigravity-dev/chum/internal/config"
	"github.com/stretchr/testify/require"
)

func TestValidateRuntimeConfigReloadAllowsLogLevelChange(t *testing.T) {
	oldCfg := &config.Config{
		General: config.General{
			StateDB:  "db1",
			LogLevel: "info",
		},
		API: config.API{Bind: "127.0.0.1:8900"},
	}
	newCfg := &config.Config{
		General: config.General{
			StateDB:  "db1",
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
			StateDB:      "db1",
			LogLevel:     "info",
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
			StateDB:      "db1",
			LogLevel:     "debug",
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

type fakeAdminBatchOps struct {
	drainQuery      string
	resumeQuery     string
	resetQuery      string
	terminateQuery  string
	drainCalled     bool
	resumeCalled    bool
	resetCalled     bool
	terminateCalled bool
}

func (f *fakeAdminBatchOps) Drain(_ context.Context, query string) (string, error) {
	f.drainCalled = true
	f.drainQuery = query
	return "drain-op", nil
}
func (f *fakeAdminBatchOps) Resume(_ context.Context, query string) (string, error) {
	f.resumeCalled = true
	f.resumeQuery = query
	return "resume-op", nil
}
func (f *fakeAdminBatchOps) Reset(_ context.Context, query string) (string, error) {
	f.resetCalled = true
	f.resetQuery = query
	return "reset-op", nil
}
func (f *fakeAdminBatchOps) Terminate(_ context.Context, query string) (string, error) {
	f.terminateCalled = true
	f.terminateQuery = query
	return "terminate-op", nil
}

func TestParseAdminSubcommand(t *testing.T) {
	t.Run("drain defaults query", func(t *testing.T) {
		cmd, query, err := parseAdminSubcommand([]string{"admin", "drain"}, "default-query")
		require.NoError(t, err)
		require.Equal(t, "drain", cmd)
		require.Equal(t, "default-query", query)
	})

	t.Run("resume accepts query override", func(t *testing.T) {
		cmd, query, err := parseAdminSubcommand([]string{"admin", "resume", "--query", "x = 1"}, "default-query")
		require.NoError(t, err)
		require.Equal(t, "resume", cmd)
		require.Equal(t, "x = 1", query)
	})

	t.Run("reset requires query", func(t *testing.T) {
		_, _, err := parseAdminSubcommand([]string{"admin", "reset"}, "default-query")
		require.ErrorContains(t, err, "--query is required")
	})

	t.Run("invalid subcommand", func(t *testing.T) {
		_, _, err := parseAdminSubcommand([]string{"admin", "boom"}, "default-query")
		require.ErrorContains(t, err, "unknown admin command")
	})

	t.Run("resume rejects unexpected args", func(t *testing.T) {
		_, _, err := parseAdminSubcommand([]string{"admin", "resume", "--query", "x = 1", "surplus"}, "default-query")
		require.ErrorContains(t, err, "unexpected arguments")
	})

	t.Run("subcommand requires query for reset", func(t *testing.T) {
		_, _, err := parseAdminSubcommand([]string{"admin", "reset", "--query", ""}, "default-query")
		require.ErrorContains(t, err, "--query is required")
	})
}

func TestRunAdminActionRoutesToCorrectRunner(t *testing.T) {
	cases := []struct {
		name            string
		command         string
		query           string
		expectDrain     bool
		expectResume    bool
		expectReset     bool
		expectTerminate bool
	}{
		{name: "drain", command: "drain", query: "q1", expectDrain: true},
		{name: "resume", command: "resume", query: "q2", expectResume: true},
		{name: "reset", command: "reset", query: "q3", expectReset: true},
		{name: "terminate", command: "terminate", query: "q4", expectTerminate: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ops := &fakeAdminBatchOps{}
			operationID, err := runAdminAction(context.Background(), tc.command, tc.query, ops)
			require.NoError(t, err)
			require.False(t, operationID == "")

			require.Equal(t, tc.expectDrain, ops.drainCalled)
			require.Equal(t, tc.expectResume, ops.resumeCalled)
			require.Equal(t, tc.expectReset, ops.resetCalled)
			require.Equal(t, tc.expectTerminate, ops.terminateCalled)
			require.Equal(t, tc.query, stringFromCalledQuery(ops, tc.command))
		})
	}

	t.Run("unknown command returns error", func(t *testing.T) {
		ops := &fakeAdminBatchOps{}
		_, err := runAdminAction(context.Background(), "invalid", "q", ops)
		require.ErrorContains(t, err, "unknown admin command")
	})
}

func stringFromCalledQuery(ops *fakeAdminBatchOps, command string) string {
	switch command {
	case "drain":
		return ops.drainQuery
	case "resume":
		return ops.resumeQuery
	case "reset":
		return ops.resetQuery
	case "terminate":
		return ops.terminateQuery
	default:
		return ""
	}
}
