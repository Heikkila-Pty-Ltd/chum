package chief

import (
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/antigravity-dev/chum/internal/config"
)

func TestShouldRunCeremony(t *testing.T) {
	cfg := &config.Config{
		Chief: config.Chief{
			Enabled: true,
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	chief := New(cfg, nil, nil, nil, logger)
	if chief == nil {
		t.Fatal("Expected New to return a valid Chief instance")
	}

	// Inject clock: Monday 10:00 AM UTC
	fakeNow := time.Date(2024, 1, 8, 10, 0, 0, 0, time.UTC)
	chief.nowFunc = func() time.Time { return fakeNow }

	schedule := CeremonySchedule{
		Type:        CeremonyMultiTeamPlanning,
		DayOfWeek:   time.Monday,
		TimeOfDay:   time.Date(0, 1, 1, 9, 0, 0, 0, time.UTC),
		LastChecked: fakeNow.Add(-2 * time.Hour),
		LastRan:     time.Time{}, // never ran
	}

	if !chief.ShouldRunCeremony(t.Context(), schedule) {
		t.Error("Expected ceremony to run on Monday 10AM with no prior run")
	}
}

func TestShouldNotRunCeremony_WrongDay(t *testing.T) {
	cfg := &config.Config{
		Chief: config.Chief{
			Enabled: true,
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	chief := New(cfg, nil, nil, nil, logger)
	if chief == nil {
		t.Fatal("Expected New to return a valid Chief instance")
	}

	// Inject clock: Tuesday 10:00 AM UTC (wrong day for Monday ceremony)
	fakeNow := time.Date(2024, 1, 9, 10, 0, 0, 0, time.UTC)
	chief.nowFunc = func() time.Time { return fakeNow }

	schedule := CeremonySchedule{
		Type:        CeremonyMultiTeamPlanning,
		DayOfWeek:   time.Monday,
		TimeOfDay:   time.Date(0, 1, 1, 9, 0, 0, 0, time.UTC),
		LastChecked: fakeNow.Add(-2 * time.Hour),
	}

	if chief.ShouldRunCeremony(t.Context(), schedule) {
		t.Error("Expected ceremony NOT to run on Tuesday when scheduled for Monday")
	}
}

func TestShouldNotRunCeremony_TooEarly(t *testing.T) {
	cfg := &config.Config{
		Chief: config.Chief{
			Enabled: true,
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	chief := New(cfg, nil, nil, nil, logger)
	if chief == nil {
		t.Fatal("Expected New to return a valid Chief instance")
	}

	// Inject clock: Monday 8:00 AM UTC (before 9AM target)
	fakeNow := time.Date(2024, 1, 8, 8, 0, 0, 0, time.UTC)
	chief.nowFunc = func() time.Time { return fakeNow }

	schedule := CeremonySchedule{
		Type:        CeremonyMultiTeamPlanning,
		DayOfWeek:   time.Monday,
		TimeOfDay:   time.Date(0, 1, 1, 9, 0, 0, 0, time.UTC),
		LastChecked: fakeNow.Add(-2 * time.Hour),
	}

	if chief.ShouldRunCeremony(t.Context(), schedule) {
		t.Error("Expected ceremony NOT to run before target time")
	}
}

func TestShouldNotRunCeremony_AlreadyRanToday(t *testing.T) {
	cfg := &config.Config{
		Chief: config.Chief{
			Enabled: true,
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	chief := New(cfg, nil, nil, nil, logger)
	if chief == nil {
		t.Fatal("Expected New to return a valid Chief instance")
	}

	// Inject clock: Monday 10:00 AM UTC
	fakeNow := time.Date(2024, 1, 8, 10, 0, 0, 0, time.UTC)
	chief.nowFunc = func() time.Time { return fakeNow }

	schedule := CeremonySchedule{
		Type:        CeremonyMultiTeamPlanning,
		DayOfWeek:   time.Monday,
		TimeOfDay:   time.Date(0, 1, 1, 9, 0, 0, 0, time.UTC),
		LastChecked: fakeNow.Add(-2 * time.Hour),
		LastRan:     time.Date(2024, 1, 8, 9, 30, 0, 0, time.UTC), // already ran today
	}

	if chief.ShouldRunCeremony(t.Context(), schedule) {
		t.Error("Expected ceremony NOT to run when already ran today")
	}
}

func TestShouldNotRunCeremony_Disabled(t *testing.T) {
	cfg := &config.Config{
		Chief: config.Chief{
			Enabled: false,
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	chief := New(cfg, nil, nil, nil, logger)

	// Test that disabled config is respected
	if cfg.Chief.Enabled {
		t.Errorf("Expected Chief SM to be disabled")
	}

	// Test ceremony structure
	targetTime := time.Date(0, 1, 1, 9, 0, 0, 0, time.UTC) // 9:00 AM

	schedule := CeremonySchedule{
		Type:      CeremonyMultiTeamPlanning,
		DayOfWeek: time.Monday,
		TimeOfDay: targetTime,
	}

	// With disabled config, ceremony should not run
	shouldRun := chief.ShouldRunCeremony(t.Context(), schedule)
	if shouldRun {
		t.Errorf("Expected ceremony NOT to run when Chief SM is disabled")
	}
}

func TestBuildMultiTeamPlanningPrompt(t *testing.T) {
	cfg := &config.Config{
		Chief: config.Chief{
			Enabled: true,
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	chief := New(cfg, nil, nil, nil, logger)

	prompt := chief.buildMultiTeamPlanningPrompt(t.Context())

	// Check that prompt contains key elements
	expectedElements := []string{
		"Multi-Team Sprint Planning Ceremony",
		"Chief Scrum Master",
		"Portfolio Context",
		"Strategic Allocation",
		"Deliver Unified Plan",
	}

	for _, element := range expectedElements {
		if !containsSubstring(prompt, element) {
			t.Errorf("Expected prompt to contain '%s'", element)
		}
	}
}

func TestGetMultiTeamPlanningSchedule(t *testing.T) {
	cfg := &config.Config{
		Chief: config.Chief{
			Enabled: true,
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	chief := New(cfg, nil, nil, nil, logger)

	schedule := chief.GetMultiTeamPlanningSchedule()

	if schedule.Type != CeremonyMultiTeamPlanning {
		t.Errorf("Expected ceremony type %v, got %v", CeremonyMultiTeamPlanning, schedule.Type)
	}

	if schedule.DayOfWeek != time.Monday {
		t.Errorf("Expected Monday, got %v", schedule.DayOfWeek)
	}

	if schedule.TimeOfDay.Hour() != 9 || schedule.TimeOfDay.Minute() != 0 {
		t.Errorf("Expected 9:00 AM, got %02d:%02d", schedule.TimeOfDay.Hour(), schedule.TimeOfDay.Minute())
	}
}

func TestGetMultiTeamPlanningScheduleUsesCadenceOverrides(t *testing.T) {
	cfg := &config.Config{
		Cadence: config.Cadence{
			SprintStartDay:  "Wednesday",
			SprintStartTime: "14:30",
			Timezone:        "America/New_York",
		},
		Chief: config.Chief{
			Enabled: true,
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	chief := New(cfg, nil, nil, nil, logger)

	schedule := chief.GetMultiTeamPlanningSchedule()

	if schedule.DayOfWeek != time.Wednesday {
		t.Errorf("Expected Wednesday, got %v", schedule.DayOfWeek)
	}
	if schedule.TimeOfDay.Hour() != 14 || schedule.TimeOfDay.Minute() != 30 {
		t.Errorf("Expected 14:30, got %02d:%02d", schedule.TimeOfDay.Hour(), schedule.TimeOfDay.Minute())
	}
	if schedule.TimeOfDay.Location().String() != "America/New_York" {
		t.Errorf("Expected America/New_York timezone, got %s", schedule.TimeOfDay.Location().String())
	}
}

func TestWithMultiTeamPortfolioContext(t *testing.T) {
	ctx := WithMultiTeamPortfolioContext(t.Context(), `{"foo":"bar"}`)

	payload, ok := MultiTeamPortfolioContextFromContext(ctx)
	if !ok {
		t.Fatal("expected context payload to be present")
	}
	if payload != `{"foo":"bar"}` {
		t.Fatalf("unexpected payload: %q", payload)
	}
}

func TestBuildMultiTeamPlanningPromptUsesInjectedContext(t *testing.T) {
	cfg := &config.Config{
		Chief: config.Chief{
			Enabled: true,
		},
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	chief := New(cfg, nil, nil, nil, logger)

	ctx := WithMultiTeamPortfolioContext(t.Context(), `{"generated_at":"2026-02-18T00:00:00Z"}`)
	prompt := chief.buildMultiTeamPlanningPrompt(ctx)

	if !strings.Contains(prompt, "Authoritative, Scheduler-Prepared") {
		t.Fatalf("expected injected prompt section, got: %s", prompt)
	}
	if !strings.Contains(prompt, `"generated_at":"2026-02-18T00:00:00Z"`) {
		t.Fatalf("expected injected JSON in prompt, got: %s", prompt)
	}
}

func TestWithCrossProjectRetroContext(t *testing.T) {
	ctx := WithCrossProjectRetroContext(t.Context(), `{"period":"2026-02-01 to 2026-02-14"}`)

	payload, ok := CrossProjectRetroContextFromContext(ctx)
	if !ok {
		t.Fatal("expected retrospective context payload to be present")
	}
	if payload != `{"period":"2026-02-01 to 2026-02-14"}` {
		t.Fatalf("unexpected payload: %q", payload)
	}
}

func TestBuildOverallRetrospectivePromptUsesInjectedContext(t *testing.T) {
	cfg := &config.Config{
		Chief: config.Chief{
			Enabled: true,
		},
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	chief := New(cfg, nil, nil, nil, logger)

	ctx := WithCrossProjectRetroContext(t.Context(), `{"project_retro_reports":{"chum":{"period":"7d"}}}`)
	prompt := chief.buildOverallRetrospectivePrompt(ctx)

	if !strings.Contains(prompt, "Overall Sprint Retrospective Ceremony") {
		t.Fatalf("expected overall retrospective heading, got: %s", prompt)
	}
	if !strings.Contains(prompt, `"project_retro_reports":{"chum":{"period":"7d"}}`) {
		t.Fatalf("expected injected retrospective context, got: %s", prompt)
	}
	if !strings.Contains(prompt, "## Action Items") {
		t.Fatalf("expected action items contract in prompt, got: %s", prompt)
	}
}

func TestParseRetrospectiveActionItems(t *testing.T) {
	output := `
# Overall Sprint Retrospective

## Highlights
- shared wins

## Action Items
- [P1] Reduce retry churn on failing provider | project:chum | owner:ops | why:high retry waste
- [P3] Clarify cross-team handoff checklist | project = api | owner = scrum | reason = ownership gaps
`
	items := parseRetrospectiveActionItems(output)
	if len(items) != 2 {
		t.Fatalf("expected 2 action items, got %d", len(items))
	}
	if items[0].Priority != 1 || items[0].ProjectName != "chum" {
		t.Fatalf("unexpected first item parse: %+v", items[0])
	}
	if items[1].Priority != 3 || items[1].ProjectName != "api" {
		t.Fatalf("unexpected second item parse: %+v", items[1])
	}
}

func TestChiefPurposeMapping(t *testing.T) {
	if got := chiefPurpose("sprint_planning_multi"); got != "planning" {
		t.Fatalf("chiefPurpose(planning)=%q want planning", got)
	}
	if got := chiefPurpose("overall_retrospective"); got != "reporting" {
		t.Fatalf("chiefPurpose(overall_retrospective)=%q want reporting", got)
	}
	if got := chiefPurpose("anything-else"); got != "review" {
		t.Fatalf("chiefPurpose(default)=%q want review", got)
	}
}

// Helper function for substring checking
func containsSubstring(str, substr string) bool {
	return len(str) >= len(substr) && findSubstring(str, substr) >= 0
}

func findSubstring(str, substr string) int {
	for i := 0; i <= len(str)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			if str[i+j] != substr[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
