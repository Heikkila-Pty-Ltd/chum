package temporal

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// stubActivities mocks all activities used by ChumAgentWorkflow for a clean
// success path: plan → approve → execute → review(approved) → semgrep(pass) → dod(pass) → record.
func stubActivities(env *testsuite.TestWorkflowEnvironment) {
	var a *Activities

	env.OnActivity(a.StructuredPlanActivity, mock.Anything, mock.Anything).Return(&StructuredPlan{
		Summary:            "Add widget endpoint",
		Steps:              []PlanStep{{Description: "Create handler", File: "handler.go", Rationale: "API needs it"}},
		FilesToModify:      []string{"handler.go"},
		AcceptanceCriteria: []string{"GET /widget returns 200"},
		TokenUsage:         TokenUsage{InputTokens: 75, OutputTokens: 25, CacheReadTokens: 5, CacheCreationTokens: 2, CostUSD: 0.001},
	}, nil)

	env.OnActivity(a.ExecuteActivity, mock.Anything, mock.Anything, mock.Anything).Return(&ExecutionResult{
		ExitCode: 0, Output: "implemented handler", Agent: "claude",
		Tokens: TokenUsage{InputTokens: 1500, OutputTokens: 800, CacheReadTokens: 100, CostUSD: 0.04},
	}, nil)

	env.OnActivity(a.CodeReviewActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&ReviewResult{
		Approved: true, ReviewerAgent: "codex",
		Tokens: TokenUsage{InputTokens: 500, OutputTokens: 300, CacheReadTokens: 50, CostUSD: 0.01},
	}, nil)

	env.OnActivity(a.RunSemgrepScanActivity, mock.Anything, mock.Anything).Return(&SemgrepScanResult{
		Passed: true,
	}, nil)

	env.OnActivity(a.DoDVerifyActivity, mock.Anything, mock.Anything).Return(&DoDResult{
		Passed: true,
	}, nil)
}

// TestCHUMChildWorkflowsSpawn verifies that ChumAgentWorkflow spawns
// ContinuousLearnerWorkflow and TacticalGroomWorkflow as abandoned children
// after a successful DoD pass. This was broken before the GetChildWorkflowExecution
// fix — children were killed before they started.
func TestCHUMChildWorkflowsSpawn(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	var a *Activities

	stubActivities(env)
	var outcome OutcomeRecord
	outcomeSet := false

	// Mock child workflows — OnWorkflow intercepts child spawning
	env.OnWorkflow(ContinuousLearnerWorkflow, mock.Anything, mock.Anything).Return(nil)
	env.OnWorkflow(TacticalGroomWorkflow, mock.Anything, mock.Anything).Return(nil)

	env.OnActivity(a.RecordOutcomeActivity, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		arg := args.Get(1)
		if o, ok := arg.(OutcomeRecord); ok {
			outcome = o
			outcomeSet = true
		}
	}).Return(nil)

	env.ExecuteWorkflow(ChumAgentWorkflow, TaskRequest{
		TaskID:  "test-morsel-chum",
		Project: "test-project",
		Prompt:  "add a widget endpoint",
		Agent:   "claude",
		WorkDir: "/tmp/test",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	// The critical assertions: both CHUM children must have been spawned
	env.AssertWorkflowCalled(t, "ContinuousLearnerWorkflow", mock.Anything, mock.Anything)
	env.AssertWorkflowCalled(t, "TacticalGroomWorkflow", mock.Anything, mock.Anything)
	require.True(t, outcomeSet)
	require.Equal(t, 2075, outcome.TotalTokens.InputTokens)
	require.Equal(t, 1125, outcome.TotalTokens.OutputTokens)
	require.Equal(t, 5+100+50, outcome.TotalTokens.CacheReadTokens)
	require.Equal(t, 2, outcome.TotalTokens.CacheCreationTokens)
	require.InDelta(t, 0.051, outcome.TotalTokens.CostUSD, 0.0001)
	require.Len(t, outcome.ActivityTokens, 3)
	require.Equal(t, "plan", outcome.ActivityTokens[0].ActivityName)
	require.Equal(t, "execute", outcome.ActivityTokens[1].ActivityName)
	require.Equal(t, "review", outcome.ActivityTokens[2].ActivityName)
}

// TestCHUMNotSpawnedOnFailure verifies that CHUM workflows are NOT spawned
// when DoD fails and the workflow escalates.
func TestCHUMNotSpawnedOnFailure(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var a *Activities

	env.OnActivity(a.StructuredPlanActivity, mock.Anything, mock.Anything).Return(&StructuredPlan{
		Summary:            "broken feature",
		Steps:              []PlanStep{{Description: "break things", File: "main.go", Rationale: "chaos"}},
		FilesToModify:      []string{"main.go"},
		AcceptanceCriteria: []string{"tests pass"},
	}, nil)

	env.OnActivity(a.ExecuteActivity, mock.Anything, mock.Anything, mock.Anything).Return(&ExecutionResult{
		ExitCode: 0, Output: "wrote code", Agent: "claude",
		Tokens: TokenUsage{InputTokens: 1000, OutputTokens: 500, CostUSD: 0.03},
	}, nil)

	env.OnActivity(a.CodeReviewActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&ReviewResult{
		Approved: true, ReviewerAgent: "codex",
		Tokens: TokenUsage{InputTokens: 400, OutputTokens: 200, CostUSD: 0.008},
	}, nil)

	env.OnActivity(a.RunSemgrepScanActivity, mock.Anything, mock.Anything).Return(&SemgrepScanResult{
		Passed: true,
	}, nil)

	// DoD always fails
	env.OnActivity(a.DoDVerifyActivity, mock.Anything, mock.Anything).Return(&DoDResult{
		Passed: false, Failures: []string{"go test failed"},
	}, nil)

	var outcome OutcomeRecord
	outcomeSet := false
	env.OnActivity(a.RecordOutcomeActivity, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		arg := args.Get(1)
		if o, ok := arg.(OutcomeRecord); ok {
			outcome = o
			outcomeSet = true
		}
	}).Return(nil)
	env.OnActivity(a.EscalateActivity, mock.Anything, mock.Anything).Return(nil)

	// Register the child workflows but they should NOT be called
	env.OnWorkflow(ContinuousLearnerWorkflow, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnWorkflow(TacticalGroomWorkflow, mock.Anything, mock.Anything).Return(nil).Maybe()

	env.ExecuteWorkflow(ChumAgentWorkflow, TaskRequest{
		TaskID:  "test-morsel-fail",
		Project: "test-project",
		Prompt:  "break everything",
		Agent:   "claude",
		WorkDir: "/tmp/test",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	require.True(t, outcomeSet)
	require.Equal(t, 1400, outcome.TotalTokens.InputTokens)
	require.Equal(t, 700, outcome.TotalTokens.OutputTokens)
	require.Equal(t, 0, outcome.TotalTokens.CacheReadTokens)
	require.Greater(t, outcome.TotalTokens.CostUSD, 0.0)
	require.Len(t, outcome.ActivityTokens, 2)
	require.Equal(t, "execute", outcome.ActivityTokens[0].ActivityName)
	require.Equal(t, "review", outcome.ActivityTokens[1].ActivityName)

	// CHUM should NOT have been spawned
	env.AssertWorkflowNotCalled(t, "ContinuousLearnerWorkflow", mock.Anything, mock.Anything)
	env.AssertWorkflowNotCalled(t, "TacticalGroomWorkflow", mock.Anything, mock.Anything)
}

func TestChumAgentWorkflowUpsertsSearchAttributesAtLifecycleStages(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	stubActivities(env)
	var captured []map[string]interface{}
	var a *Activities

	// Ensure optional CHUM child workflows are mocked to avoid environment
	// child-workflow registration issues when using testsuite with minimal mocks.
	env.OnWorkflow(ContinuousLearnerWorkflow, mock.Anything, mock.Anything).Return(nil)
	env.OnWorkflow(TacticalGroomWorkflow, mock.Anything, mock.Anything).Return(nil)

	original := upsertChumSearchAttributesFn
	t.Cleanup(func() {
		upsertChumSearchAttributesFn = original
	})
	upsertChumSearchAttributesFn = func(ctx workflow.Context, attrs map[string]interface{}) error {
		copyAttrs := make(map[string]interface{}, len(attrs))
		for k, v := range attrs {
			copyAttrs[k] = v
		}
		captured = append(captured, copyAttrs)
		return nil
	}

	env.ExecuteWorkflow(ChumAgentWorkflow, TaskRequest{
		TaskID:            "test-task-id",
		Project:           "my-project",
		TaskTitle:         "Priority hotfix",
		Prompt:            "fix auth bug",
		Agent:             "claude",
		Priority:          7,
		WorkDir:           "/tmp/test",
		SlowStepThreshold: defaultSlowStepThreshold,
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.GreaterOrEqual(t, len(captured), 5)

	stages := make(map[string]int)
	for _, attrs := range captured {
		stage := fmt.Sprintf("%v", attrs[SearchAttributeCurrentStage])
		stages[stage]++

		require.Equal(t, "my-project", attrs[SearchAttributeProject])
		require.Equal(t, "claude", attrs[SearchAttributeAgent])
		require.Equal(t, "Priority hotfix", attrs[SearchAttributeTaskTitle])
		require.Equal(t, 4, attrs[SearchAttributePriority])
	}

	require.Equal(t, 1, stages[chumWorkflowStatusPlan])
	require.Equal(t, 1, stages[chumWorkflowStatusGate])
	require.Equal(t, 1, stages[chumWorkflowStatusExecute])
	require.Equal(t, 1, stages[chumWorkflowStatusReview])
	require.Equal(t, 1, stages[chumWorkflowStatusDoD])
	require.Equal(t, 1, stages[chumWorkflowStatusCompleted])
}

func TestBuildOpenAgentWorkflowQueryFiltersByProject(t *testing.T) {
	q := buildOpenAgentWorkflowQuery("alpha-proj")
	require.Contains(t, q, fmt.Sprintf("%s = 'alpha-proj'", SearchAttributeProject))
	q = buildOpenAgentWorkflowQuery("acme's")
	require.Contains(t, q, fmt.Sprintf("%s = 'acme''s'", SearchAttributeProject))
}

func TestListOpenAgentWorkflowsUsesProjectFilter(t *testing.T) {
	fakeTC := &fakeWorkflowListClient{}
	_, err := listOpenAgentWorkflows(t.Context(), fakeTC, "alpha-proj")
	require.NoError(t, err)
	require.Len(t, fakeTC.queries, 1)
	require.Equal(t, 1, len(fakeTC.queries))
	require.Contains(t, fakeTC.queries[0], fmt.Sprintf("%s = 'alpha-proj'", SearchAttributeProject))
}

func TestChumAgentWorkflowPausesForDrainUntilResume(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()
	var a *Activities

	var executeCalls int32
	var resumeSignalSent int32
	var executeBeforeResume int32

	planCanContinue := make(chan struct{})
	executeCanContinue := make(chan struct{})

	env.OnActivity(a.StructuredPlanActivity, mock.Anything, mock.Anything).Run(func(_ mock.Arguments) {
		<-planCanContinue
	}).Return(&StructuredPlan{
		Summary:            "Add guarded drain boundary",
		Steps:              []PlanStep{{Description: "Write change", File: "guarded.go", Rationale: "test"}},
		FilesToModify:      []string{"guarded.go"},
		AcceptanceCriteria: []string{"compiles"},
		TokenUsage:         TokenUsage{InputTokens: 10, OutputTokens: 10, CostUSD: 0.01},
	}, nil)

	env.OnActivity(a.ExecuteActivity, mock.Anything, mock.Anything, mock.Anything).Run(func(_ mock.Arguments) {
		if atomic.LoadInt32(&resumeSignalSent) == 0 {
			atomic.StoreInt32(&executeBeforeResume, 1)
		}
		atomic.AddInt32(&executeCalls, 1)
		<-executeCanContinue
	}).Return(&ExecutionResult{
		ExitCode: 0,
		Output:   "implemented",
		Agent:    "claude",
		Tokens:   TokenUsage{InputTokens: 20, OutputTokens: 10, CostUSD: 0.01},
	}, nil)

	env.OnActivity(a.CodeReviewActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&ReviewResult{
		Approved:      true,
		ReviewerAgent: "codex",
		Tokens:        TokenUsage{InputTokens: 8, OutputTokens: 4, CostUSD: 0.002},
	}, nil)
	env.OnActivity(a.RunSemgrepScanActivity, mock.Anything, mock.Anything).Return(&SemgrepScanResult{
		Passed: true,
	}, nil)
	env.OnActivity(a.DoDVerifyActivity, mock.Anything, mock.Anything).Return(&DoDResult{
		Passed: true,
	}, nil)
	env.OnActivity(a.RecordOutcomeActivity, mock.Anything, mock.Anything).Return(nil)

	env.OnWorkflow(ContinuousLearnerWorkflow, mock.Anything, mock.Anything).Return(nil)
	env.OnWorkflow(TacticalGroomWorkflow, mock.Anything, mock.Anything).Return(nil)

	env.RegisterDelayedCallback(func() {
		close(planCanContinue)
		env.SignalWorkflow(ChumAgentDrainSignalName, nil)
	}, 1*time.Millisecond)

	env.RegisterDelayedCallback(func() {
		atomic.StoreInt32(&resumeSignalSent, 1)
		env.SignalWorkflow(ChumAgentResumeSignalName, nil)
		close(executeCanContinue)
	}, 10*time.Millisecond)

	env.ExecuteWorkflow(ChumAgentWorkflow, TaskRequest{
		TaskID:  "test-morsel-drain",
		Project: "test-project",
		Prompt:  "validate drain boundary",
		Agent:   "claude",
		WorkDir: "/tmp/test",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, int32(1), atomic.LoadInt32(&executeCalls))
	require.Equal(t, int32(0), atomic.LoadInt32(&executeBeforeResume))
}

// TestContinuousLearnerWorkflowPipeline verifies the learner extracts lessons,
// stores them, and generates semgrep rules.
func TestContinuousLearnerWorkflowPipeline(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var a *Activities

	lessons := []Lesson{
		{TaskID: "morsel-1", Category: "antipattern", Summary: "nil check after error"},
		{TaskID: "morsel-1", Category: "pattern", Summary: "table-driven tests"},
	}

	env.OnActivity(a.ExtractLessonsActivity, mock.Anything, mock.Anything).Return(lessons, nil)
	env.OnActivity(a.StoreLessonActivity, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(a.GenerateSemgrepRuleActivity, mock.Anything, mock.Anything, mock.Anything).Return([]SemgrepRule{
		{RuleID: "chum-nil-check", FileName: "chum-nil-check.yaml", Content: "rules: []"},
	}, nil)

	env.ExecuteWorkflow(ContinuousLearnerWorkflow, LearnerRequest{
		TaskID:  "morsel-1",
		Project: "test-project",
		Tier:    "fast",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestTacticalGroomWorkflow verifies tactical grooming runs the mutate activity.
func TestTacticalGroomWorkflow(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var a *Activities

	env.OnActivity(a.MutateTasksActivity, mock.Anything, mock.Anything).Return(&GroomResult{
		MutationsApplied: 3,
		MutationsFailed:  0,
		Details:          []string{"reprioritized morsel-1", "closed stale morsel-2", "added dep morsel-3->morsel-4"},
	}, nil)

	env.ExecuteWorkflow(TacticalGroomWorkflow, TacticalGroomRequest{
		TaskID:  "morsel-1",
		Project: "test-project",
		WorkDir: "/tmp/test",
		Tier:    "fast",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestStrategicGroomWorkflowPipeline verifies the full daily strategic pipeline:
// RepoMap -> MorselState -> Analysis -> Mutations -> Briefing
func TestStrategicGroomWorkflowPipeline(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var a *Activities

	env.OnActivity(a.GenerateRepoMapActivity, mock.Anything, mock.Anything).Return(&RepoMap{
		TotalFiles: 42,
		TotalLines: 5000,
		Packages: []PackageInfo{
			{ImportPath: "github.com/example/chum/internal/temporal", Name: "temporal"},
		},
	}, nil)

	env.OnActivity(a.GetMorselStateSummaryActivity, mock.Anything, mock.Anything).Return(
		"Open: 12, Closed: 45, Blocked: 3", nil)

	env.OnActivity(a.StrategicAnalysisActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&StrategicAnalysis{
		Priorities: []StrategicItem{
			{Title: "Fix flaky tests", Urgency: "high"},
		},
		Risks:     []string{"test coverage declining"},
		Mutations: []MorselMutation{{TaskID: "morsel-5", Action: "update_priority", Priority: intPtr(1)}},
	}, nil)

	env.OnActivity(a.ApplyStrategicMutationsActivity, mock.Anything, mock.Anything, mock.Anything).Return(&GroomResult{
		MutationsApplied: 1,
	}, nil)

	env.OnActivity(a.GenerateMorningBriefingActivity, mock.Anything, mock.Anything, mock.Anything).Return(&MorningBriefing{
		Date:     "2026-02-20",
		Project:  "test-project",
		Markdown: "# Morning Briefing\n## Top Priority: Fix flaky tests",
	}, nil)

	env.ExecuteWorkflow(StrategicGroomWorkflow, StrategicGroomRequest{
		Project: "test-project",
		WorkDir: "/tmp/test",
		Tier:    "premium",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestNormalizeStrategicMutationsAutoDecompositionWithoutActionableFieldsIsDeferred(t *testing.T) {
	priority := 1
	mutations := []MorselMutation{{
		Action:          "create",
		Title:           "Auto: break down authentication flow",
		Description:     "",
		Priority:        &priority,
		StrategicSource: "",
	}}

	got := normalizeStrategicMutations(mutations)
	require.Len(t, got, 1)
	require.True(t, got[0].Deferred)
	require.NotNil(t, got[0].Priority)
	require.Equal(t, 4, *got[0].Priority)
	require.Equal(t, StrategicMutationSource, got[0].StrategicSource)
	require.Equal(t, "break down authentication flow", got[0].Title)
	require.Equal(t, "Deferred strategic recommendation pending breakdown.", got[0].Description)
	require.Equal(t, "This is deferred strategy guidance. Review and expand before execution.", got[0].Acceptance)
	require.Equal(t, "Clarify design and acceptance criteria before creating executable subtasks.", got[0].Design)
	require.Equal(t, 30, got[0].EstimateMinutes)
}

func TestNormalizeStrategicMutationsActionableDecompositionRemainsExecutable(t *testing.T) {
	mutations := []MorselMutation{{
		Action:          "create",
		Title:           "Auto decomposition: split request validation into tasks",
		Description:     "Add one coded task for each phase of request validation rollout.",
		Acceptance:      "All validation paths are implemented and covered by tests.",
		Design:          "Implement helper modules and add targeted unit tests first.",
		EstimateMinutes: 120,
		StrategicSource: StrategicMutationSource,
	}}

	got := normalizeStrategicMutations(mutations)
	require.Len(t, got, 1)
	require.False(t, got[0].Deferred)
	require.Equal(t, StrategicMutationSource, got[0].StrategicSource)
	require.Nil(t, got[0].Priority)
	require.Equal(t, "Auto decomposition: split request validation into tasks", got[0].Title)
}

// TestStrategicGroomWorkflowActionableCreatePassesThroughToActivity verifies
// the end-to-end path: a fully actionable strategic create mutation flows
// from StrategicAnalysisActivity through normalizeStrategicMutations to
// ApplyStrategicMutationsActivity without being deferred.
func TestStrategicGroomWorkflowActionableCreatePassesThroughToActivity(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var a *Activities

	env.OnActivity(a.GenerateRepoMapActivity, mock.Anything, mock.Anything).Return(&RepoMap{
		TotalFiles: 10,
		Packages:   []PackageInfo{{ImportPath: "example.com/pkg", Name: "pkg"}},
	}, nil)

	env.OnActivity(a.GetMorselStateSummaryActivity, mock.Anything, mock.Anything).Return(
		"Open: 5, Closed: 10", nil)

	env.OnActivity(a.StrategicAnalysisActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&StrategicAnalysis{
		Priorities: []StrategicItem{{Title: "Add request validation", Urgency: "high"}},
		Mutations: []MorselMutation{{
			Action:          "create",
			Title:           "Add input validation for POST /users",
			Description:     "Validate request body fields before processing.",
			Acceptance:      "POST /users rejects invalid payloads with 400 and descriptive error.",
			Design:          "Add validation middleware using existing validator package.",
			EstimateMinutes: 45,
			StrategicSource: StrategicMutationSource,
			Deferred:        false,
		}},
	}, nil)

	var capturedMutations []MorselMutation
	env.OnActivity(a.ApplyStrategicMutationsActivity, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			if ms, ok := args.Get(2).([]MorselMutation); ok {
				capturedMutations = ms
			}
		}).Return(&GroomResult{MutationsApplied: 1}, nil)

	env.OnActivity(a.GenerateMorningBriefingActivity, mock.Anything, mock.Anything, mock.Anything).Return(&MorningBriefing{
		Date:     "2026-02-21",
		Project:  "test-project",
		Markdown: "# Briefing",
	}, nil)

	env.ExecuteWorkflow(StrategicGroomWorkflow, StrategicGroomRequest{
		Project: "test-project",
		WorkDir: "/tmp/test",
		Tier:    "premium",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.Len(t, capturedMutations, 1, "actionable create should reach ApplyStrategicMutationsActivity")
	require.False(t, capturedMutations[0].Deferred, "actionable create must not be deferred")
	require.Equal(t, "Add input validation for POST /users", capturedMutations[0].Title)
	require.Equal(t, 45, capturedMutations[0].EstimateMinutes)
}

// TestStrategicGroomWorkflowVagueCreateIsDeferredNotP1 verifies that a vague
// "break down" create from strategic analysis is deferred to P4 and never
// reaches the mutation activity as a high-priority executable task.
func TestStrategicGroomWorkflowVagueCreateIsDeferredNotP1(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var a *Activities

	env.OnActivity(a.GenerateRepoMapActivity, mock.Anything, mock.Anything).Return(&RepoMap{
		TotalFiles: 10,
		Packages:   []PackageInfo{{ImportPath: "example.com/pkg", Name: "pkg"}},
	}, nil)

	env.OnActivity(a.GetMorselStateSummaryActivity, mock.Anything, mock.Anything).Return(
		"Open: 5", nil)

	// Strategic analysis returns a vague decomposition suggestion without
	// required actionable fields — this is the production scenario we're guarding against.
	env.OnActivity(a.StrategicAnalysisActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&StrategicAnalysis{
		Priorities: []StrategicItem{{Title: "Break down auth", Urgency: "medium"}},
		Mutations: []MorselMutation{{
			Action:          "create",
			Title:           "Break down authentication flow into subtasks",
			Description:     "The auth system needs decomposition.",
			Priority:        intPtr(1),
			StrategicSource: StrategicMutationSource,
			// Missing: Acceptance, Design, EstimateMinutes — should trigger deferral.
		}},
	}, nil)

	var capturedMutations []MorselMutation
	env.OnActivity(a.ApplyStrategicMutationsActivity, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			if ms, ok := args.Get(2).([]MorselMutation); ok {
				capturedMutations = ms
			}
		}).Return(&GroomResult{MutationsApplied: 1}, nil)

	env.OnActivity(a.GenerateMorningBriefingActivity, mock.Anything, mock.Anything, mock.Anything).Return(&MorningBriefing{
		Date:     "2026-02-21",
		Project:  "test-project",
		Markdown: "# Briefing",
	}, nil)

	env.ExecuteWorkflow(StrategicGroomWorkflow, StrategicGroomRequest{
		Project: "test-project",
		WorkDir: "/tmp/test",
		Tier:    "premium",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.Len(t, capturedMutations, 1, "deferred create should still reach activity")
	require.True(t, capturedMutations[0].Deferred, "vague create must be deferred")
	require.NotNil(t, capturedMutations[0].Priority)
	require.Equal(t, 4, *capturedMutations[0].Priority, "deferred create must be downgraded to P4")
}

// TestNormalizeStrategicMutationsNonPrefixedVagueCreateIsDeferred verifies that
// a title like "Break down authentication flow" (without "Auto:" prefix) is still
// caught as deferred when it lacks actionable fields. This guards against the prompt
// telling the LLM not to use "Auto:" prefixes while the detection relies on title heuristics.
func TestNormalizeStrategicMutationsNonPrefixedVagueCreateIsDeferred(t *testing.T) {
	mutations := []MorselMutation{{
		Action:          "create",
		Title:           "Break down authentication flow",
		Description:     "The auth system needs decomposition.",
		Priority:        intPtr(1),
		StrategicSource: StrategicMutationSource,
		// Missing: Acceptance, Design, EstimateMinutes
	}}

	got := normalizeStrategicMutations(mutations)
	require.Len(t, got, 1)
	require.True(t, got[0].Deferred, "non-prefixed vague create must be deferred")
	require.Equal(t, 4, *got[0].Priority, "deferred must be P4")
	require.NotEmpty(t, got[0].Acceptance, "deferred must get safe defaults")
	require.NotEmpty(t, got[0].Design, "deferred must get safe defaults")
	require.Greater(t, got[0].EstimateMinutes, 0, "deferred must get safe defaults")
}

// TestNormalizeStrategicMutationsNonCreatePassesThrough verifies that non-create
// mutations (update_priority, close, etc.) pass through normalization unmodified.
func TestNormalizeStrategicMutationsNonCreatePassesThrough(t *testing.T) {
	mutations := []MorselMutation{
		{TaskID: "morsel-1", Action: "update_priority", Priority: intPtr(0)},
		{TaskID: "morsel-2", Action: "close", Reason: "stale"},
		{TaskID: "morsel-3", Action: "update_notes", Notes: "context from strategic review"},
	}

	got := normalizeStrategicMutations(mutations)
	require.Len(t, got, 3)
	for _, m := range got {
		require.Equal(t, StrategicMutationSource, m.StrategicSource, "all get source set")
		require.False(t, m.Deferred, "non-create mutations are never deferred")
	}
}

// TestStepDurationLogging verifies that every pipeline step records its name,
// duration, and status in the OutcomeRecord.StepMetrics field on a successful run.
func TestStepDurationLogging(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	stubActivities(env)

	// Mock child workflows
	env.OnWorkflow(ContinuousLearnerWorkflow, mock.Anything, mock.Anything).Return(nil)
	env.OnWorkflow(TacticalGroomWorkflow, mock.Anything, mock.Anything).Return(nil)

	var outcome OutcomeRecord
	env.OnActivity((*Activities)(nil).RecordOutcomeActivity, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		if o, ok := args.Get(1).(OutcomeRecord); ok {
			outcome = o
		}
	}).Return(nil)

	env.ExecuteWorkflow(ChumAgentWorkflow, TaskRequest{
		TaskID:  "test-morsel-steps",
		Project: "test-project",
		Prompt:  "add step metrics",
		Agent:   "claude",
		WorkDir: "/tmp/test",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	// Verify step metrics are populated
	require.NotEmpty(t, outcome.StepMetrics, "step metrics must be recorded")

	// Build a map of step names for lookup
	stepNames := make(map[string]StepMetric, len(outcome.StepMetrics))
	for _, m := range outcome.StepMetrics {
		stepNames[m.Name] = m
	}

	// All phases must be present: plan, execute[1], review[1], semgrep[1], dod[1]
	for _, expected := range []string{"plan", "execute[1]", "review[1]", "semgrep[1]", "dod[1]"} {
		m, ok := stepNames[expected]
		require.True(t, ok, "missing step metric for %q", expected)
		require.NotEmpty(t, m.Status, "step %q must have a status", expected)
		require.GreaterOrEqual(t, m.DurationS, 0.0, "step %q duration must be non-negative", expected)
	}

	// Verify each step has a valid status
	for _, m := range outcome.StepMetrics {
		require.Contains(t, []string{"ok", "failed", "skipped"}, m.Status,
			"step %q has invalid status %q", m.Name, m.Status)
	}
}

// TestStepDurationLoggingWhenReviewActivityFails verifies that review metric
// is still emitted as failed, even when review infrastructure is unavailable.
func TestStepDurationLoggingWhenReviewActivityFails(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	env.OnActivity((*Activities)(nil).StructuredPlanActivity, mock.Anything, mock.Anything).Return(&StructuredPlan{
		Summary:            "add fallback path",
		Steps:              []PlanStep{{Description: "Create fallback handler", File: "handler.go", Rationale: "resilience"}},
		FilesToModify:      []string{"handler.go"},
		AcceptanceCriteria: []string{"endpoint recovers"},
	}, nil)
	env.OnActivity((*Activities)(nil).ExecuteActivity, mock.Anything, mock.Anything, mock.Anything).Return(&ExecutionResult{
		ExitCode: 0, Output: "done", Agent: "claude",
	}, nil)
	env.OnActivity((*Activities)(nil).CodeReviewActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("review infra down"))
	env.OnActivity((*Activities)(nil).RunSemgrepScanActivity, mock.Anything, mock.Anything).Return(&SemgrepScanResult{
		Passed: true,
	}, nil)
	env.OnActivity((*Activities)(nil).DoDVerifyActivity, mock.Anything, mock.Anything).Return(&DoDResult{
		Passed: true,
	}, nil)

	env.OnWorkflow(ContinuousLearnerWorkflow, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnWorkflow(TacticalGroomWorkflow, mock.Anything, mock.Anything).Return(nil).Maybe()

	var outcome OutcomeRecord
	env.OnActivity((*Activities)(nil).RecordOutcomeActivity, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		if o, ok := args.Get(1).(OutcomeRecord); ok {
			outcome = o
		}
	}).Return(nil)

	env.ExecuteWorkflow(ChumAgentWorkflow, TaskRequest{
		TaskID:  "test-morsel-review-fail",
		Project: "test-project",
		Prompt:  "review infra failure path",
		Agent:   "claude",
		WorkDir: "/tmp/test",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	foundReview := false
	reviewSteps := 0
	for _, m := range outcome.StepMetrics {
		if m.Name == "review[1]" {
			reviewSteps++
			foundReview = true
			require.Equal(t, "failed", m.Status)
		}
	}
	require.True(t, foundReview, "review[1] should be recorded even when review activity fails")
	require.Equal(t, 1, reviewSteps, "review[1] should be recorded exactly once when review infrastructure fails")
}

// TestStepDurationLoggingEscalation verifies step metrics are recorded on escalation
// (all DoD retries fail).
func TestStepDurationLoggingEscalation(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var a *Activities

	env.OnActivity(a.StructuredPlanActivity, mock.Anything, mock.Anything).Return(&StructuredPlan{
		Summary:            "will fail dod",
		Steps:              []PlanStep{{Description: "break things", File: "main.go", Rationale: "test"}},
		FilesToModify:      []string{"main.go"},
		AcceptanceCriteria: []string{"tests pass"},
	}, nil)

	env.OnActivity(a.ExecuteActivity, mock.Anything, mock.Anything, mock.Anything).Return(&ExecutionResult{
		ExitCode: 0, Output: "code", Agent: "claude",
	}, nil)
	env.OnActivity(a.CodeReviewActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&ReviewResult{
		Approved: true, ReviewerAgent: "codex",
	}, nil)
	env.OnActivity(a.RunSemgrepScanActivity, mock.Anything, mock.Anything).Return(&SemgrepScanResult{
		Passed: true,
	}, nil)
	env.OnActivity(a.DoDVerifyActivity, mock.Anything, mock.Anything).Return(&DoDResult{
		Passed: false, Failures: []string{"tests failed"},
	}, nil)
	env.OnActivity(a.EscalateActivity, mock.Anything, mock.Anything).Return(nil)

	env.OnWorkflow(ContinuousLearnerWorkflow, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnWorkflow(TacticalGroomWorkflow, mock.Anything, mock.Anything).Return(nil).Maybe()

	var outcome OutcomeRecord
	env.OnActivity(a.RecordOutcomeActivity, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		if o, ok := args.Get(1).(OutcomeRecord); ok {
			outcome = o
		}
	}).Return(nil)

	env.ExecuteWorkflow(ChumAgentWorkflow, TaskRequest{
		TaskID:  "test-morsel-escalate",
		Project: "test-project",
		Prompt:  "will fail dod",
		Agent:   "claude",
		WorkDir: "/tmp/test",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	require.NotEmpty(t, outcome.StepMetrics)

	// Should have: plan, gate, 3x (execute, review, semgrep, dod), escalate
	// = 2 + 3*4 + 1 = 15 steps
	stepNames := make(map[string]int)
	for _, m := range outcome.StepMetrics {
		stepNames[m.Name]++
	}

	require.Equal(t, 1, stepNames["plan"])
	require.Equal(t, 1, stepNames["escalate"])

	// 3 DoD retries
	for i := 1; i <= 3; i++ {
		require.Equal(t, 1, stepNames[fmt.Sprintf("execute[%d]", i)], "execute[%d]", i)
		require.Equal(t, 1, stepNames[fmt.Sprintf("review[%d]", i)], "review[%d]", i)
		require.Equal(t, 1, stepNames[fmt.Sprintf("semgrep[%d]", i)], "semgrep[%d]", i)
		require.Equal(t, 1, stepNames[fmt.Sprintf("dod[%d]", i)], "dod[%d]", i)
	}

	// All dod steps should be "failed"
	for _, m := range outcome.StepMetrics {
		if m.Name == "dod[1]" || m.Name == "dod[2]" || m.Name == "dod[3]" {
			require.Equal(t, "failed", m.Status, "dod step should be failed")
		}
	}
}

// TestPlanningWorkflowPassesSlowStepThresholdToExecutionTask verifies that the
// planning ceremony forwards the workflow threshold into the execution request.
func TestPlanningWorkflowPassesSlowStepThresholdToExecutionTask(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var a *Activities
	var capturedReq TaskRequest
	var captured bool

	env.OnActivity(a.GroomBacklogActivity, mock.Anything, mock.Anything).Return(&BacklogPresentation{
		Items: []BacklogItem{{ID: "morsel-1", Title: "Plan this task"}},
	}, nil)
	env.OnActivity(a.GenerateQuestionsActivity, mock.Anything, mock.Anything, mock.Anything).Return([]PlanningQuestion{}, nil)
	env.OnActivity(a.SummarizePlanActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&PlanSummary{
		What:      "Plan this task",
		DoDChecks: []string{"go test ./..."},
	}, nil)

	env.OnWorkflow(ChumAgentWorkflow, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		if req, ok := args.Get(1).(TaskRequest); ok {
			capturedReq = req
			captured = true
		}
	}).Return(nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("item-selected", "morsel-1")
		env.SignalWorkflow("greenlight", "GO")
	}, 0)

	env.ExecuteWorkflow(PlanningCeremonyWorkflow, PlanningRequest{
		Project: "test-project",
		Agent:   "claude",
		WorkDir: "/tmp/test",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.True(t, captured, "planning workflow should dispatch ChumAgentWorkflow")
	require.Equal(t, defaultSlowStepThreshold, capturedReq.SlowStepThreshold)
	require.Equal(t, "Plan this task", capturedReq.TaskTitle)
	require.Equal(t, 2, capturedReq.Priority)
}

// TestDispatcherAppliesSlowStepThresholdFallback verifies that the dispatcher
// never passes a zero slow-step threshold into child execution requests.
func TestDispatcherAppliesSlowStepThresholdFallback(t *testing.T) {
	s := testsuite.WorkflowTestSuite{}
	env := s.NewTestWorkflowEnvironment()

	var da *DispatchActivities
	var capturedReq TaskRequest
	var captured bool

	env.OnActivity(da.ScanCandidatesActivity, mock.Anything).Return(&ScanCandidatesResult{
		Candidates: []DispatchCandidate{{
			TaskID:            "morsel-1",
			Title:             "Build dashboard",
			TaskTitle:         "Build dashboard",
			Project:           "project-1",
			WorkDir:           "/tmp/test",
			Prompt:            "Build dashboard",
			Provider:          "claude",
			DoDChecks:         []string{"go test ./..."},
			SlowStepThreshold: 0,
			Priority:          7,
			EstimateMinutes:   60,
		}},
		Running:  0,
		MaxTotal: 3,
	}, nil)

	env.OnWorkflow(ChumAgentWorkflow, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		if req, ok := args.Get(1).(TaskRequest); ok {
			capturedReq = req
			captured = true
		}
	}).Return(nil)

	env.ExecuteWorkflow(DispatcherWorkflow, struct{}{})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.True(t, captured, "dispatcher should dispatch ChumAgentWorkflow")
	require.Equal(t, defaultSlowStepThreshold, capturedReq.SlowStepThreshold)
	require.Equal(t, "Build dashboard", capturedReq.TaskTitle)
	require.Equal(t, 4, capturedReq.Priority)
}

func intPtr(i int) *int { return &i }

type fakeWorkflowListClient struct {
	queries []string
}

func (f *fakeWorkflowListClient) ListWorkflow(_ context.Context, req *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error) {
	if req != nil {
		f.queries = append(f.queries, req.GetQuery())
	}
	return &workflowservice.ListWorkflowExecutionsResponse{}, nil
}
