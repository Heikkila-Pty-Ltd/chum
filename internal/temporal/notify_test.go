package temporal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func durFromSeconds(s int) time.Duration { return time.Duration(s) * time.Second }

func TestThemed_AllEvents(t *testing.T) {
	events := []struct {
		event string
		extra map[string]string
		want  string // substring that must appear
	}{
		{"dispatch", map[string]string{"count": "3", "tasks": "`a`, `b`, `c`"}, "chum fed to 3 sharks"},
		{"plan", map[string]string{"title": "Add auth", "agent": "claude"}, "shark charting course"},
		{"execute", map[string]string{"agent": "codex", "attempt": "2"}, "shark hunting"},
		{"review", map[string]string{"reviewer": "claude", "author": "codex"}, "pilot fish inspecting"},
		{"review_approved", map[string]string{"reviewer": "claude"}, "pilot fish approved"},
		{"handoff", map[string]string{"from": "codex", "to": "claude", "handoff": "1"}, "shark tag-team"},
		{"dod_pass", map[string]string{"duration": "4m32s", "cost": "0.05"}, "orca approved the kill"},
		{"dod_fail", map[string]string{"failures": "go test failed", "attempt": "2"}, "orca rejected"},
		{"escalate", map[string]string{"attempts": "3"}, "shark beached"},
		{"complete", map[string]string{"duration": "4m32s", "cost": "0.05"}, "catch landed"},
		{"crab_start", map[string]string{"plan_id": "plan-1"}, "crabs cutting the whale up"},
		{"crab_done", map[string]string{"whales": "4", "morsels": "12"}, "whale carved"},
		{"learner", map[string]string{"lessons": "3"}, "octopus updating the knowledge store"},
		{"groom", map[string]string{"applied": "2"}, "remoras cleaning up"},
	}

	for _, tt := range events {
		t.Run(tt.event, func(t *testing.T) {
			msg := themed(tt.event, "task-1", tt.extra)
			require.Contains(t, msg, tt.want, "event=%s", tt.event)
			require.NotEmpty(t, msg)
		})
	}
}

func TestThemed_UnknownEvent(t *testing.T) {
	msg := themed("nonexistent", "task-1", nil)
	require.Empty(t, msg)
}

func TestThemed_NilExtra(t *testing.T) {
	msg := themed("execute", "task-1", nil)
	require.Contains(t, msg, "shark hunting")
}

func TestThemed_TaskIDIncluded(t *testing.T) {
	msg := themed("execute", "chum-zdt-1", map[string]string{"agent": "codex", "attempt": "1"})
	require.Contains(t, msg, "chum-zdt-1")
}

func TestFmtDuration(t *testing.T) {
	tests := []struct {
		seconds int
		want    string
	}{
		{30, "30s"},
		{90, "1m30s"},
		{272, "4m32s"},
		{3600, "60m0s"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := fmtDuration(durFromSeconds(tt.seconds))
			require.Equal(t, tt.want, got)
		})
	}
}

func TestFmtCost(t *testing.T) {
	require.Equal(t, "0.05", fmtCost(0.05))
	require.Equal(t, "1.23", fmtCost(1.23))
	require.Equal(t, "0.0000", fmtCost(0.0))
	require.Equal(t, "0.0012", fmtCost(0.0012))
}

func TestJoinTasks(t *testing.T) {
	require.Equal(t, "`a`, `b`", joinTasks([]string{"a", "b"}))
	require.Equal(t, "`solo`", joinTasks([]string{"solo"}))
	require.Equal(t, "", joinTasks(nil))
}
