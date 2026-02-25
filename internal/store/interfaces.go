package store

import (
	"context"
	"time"

	"github.com/antigravity-dev/chum/internal/graph"
)

// CrystalCandidateStatus tracks whether a candidate trace is usable.
type CrystalCandidateStatus string

const (
	CrystalCandidateStatusPending    CrystalCandidateStatus = "pending"
	CrystalCandidateStatusActive     CrystalCandidateStatus = "active"
	CrystalCandidateStatusDeprecated CrystalCandidateStatus = "deprecated"
)

// ExecutionTrace is a durable, stage-spanning trace of workflow execution.
type ExecutionTrace struct {
	ID            int64
	TaskID        string
	Species       string
	GoalSignature string
	Status        string
	StartedAt     time.Time
	CompletedAt   time.Time
	Outcome       string
	SupportCount  int
	AttemptCount  int
	SuccessRate   float64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// TraceEvent stores one normalized event in a trace.
type TraceEvent struct {
	ID            int64
	TraceID       int64
	Stage         string
	Step          string
	Tool          string
	Command       string
	InputSummary  string
	OutputSummary string
	DurationMs    int64
	Success       bool
	ErrorContext  string
	CreatedAt     time.Time
}

// CrystalCandidate is a reusable successful deterministic flow extracted from traces.
type CrystalCandidate struct {
	ID                 int64
	Species            string
	GoalSignature      string
	Status             CrystalCandidateStatus
	TemplateJSON       string
	SupportCount       int
	AttemptCount       int
	SuccessCount       int
	SuccessRate        float64
	Preconditions      string
	OrderedSteps       string
	VerificationChecks string
	RequiredInputs     string
	LastSeenAt         time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// TraceStore tracks execution traces and trace events for workflow runs.
type TraceStore interface {
	StartExecutionTrace(taskID, species, goalSignature string) (int64, error)
	AppendTraceEvent(traceID int64, event TraceEvent) error
	CompleteExecutionTrace(traceID int64, status, outcome string, supportCount int, successCount int) error
	ListExecutionTraces(taskID string) ([]ExecutionTrace, error)
	GetTraceEvents(traceID int64) ([]TraceEvent, error)
}

// CrystalCandidateStore tracks reusable deterministic candidate flows.
type CrystalCandidateStore interface {
	UpsertCrystalCandidate(candidate CrystalCandidate) error
	GetCrystalCandidatesBySpeciesAndGoal(species, goalSignature string) ([]CrystalCandidate, error)
	GetCrystalCandidatesByStatus(status CrystalCandidateStatus) ([]CrystalCandidate, error)
}

// DispatchStore covers dispatch lifecycle: recording, querying, updating, and overflow queue management.
type DispatchStore interface {
	RecordDispatch(morselID, project, agent, provider, tier string, handle int, sessionName, prompt, logPath, branch, backend string) (int64, error)
	RecordSchedulerDispatch(morselID, project, agent, provider, tier string, handle int, sessionName, prompt, logPath, branch, backend string, labels []string) (int64, error)
	UpdateDispatchStatus(id int64, status string, exitCode int, durationS float64) error
	UpdateDispatchStage(id int64, stage string) error
	MarkDispatchPendingRetry(id int64, nextTier string, nextRetryAt time.Time) error
	UpdateDispatchPR(id int64, prURL string, prNumber int) error
	UpdateDispatchLabels(id int64, labels []string) error
	UpdateDispatchLabelsCSV(id int64, labelsCSV string) error
	UpdateFailureDiagnosis(id int64, category, summary string) error
	RecordDispatchCost(dispatchID int64, inputTokens, outputTokens int, costUSD float64) error
	RecordDoDResult(dispatchID int64, morselID, project string, passed bool, failures string, checkResults string) error
	RecordProviderUsage(provider, agentID, morselID string) (int64, error)
	DeleteProviderUsage(id int64) error
	SetDispatchTime(id int64, dispatchedAt time.Time) error
	SetDispatchPersistHookForTesting(hook func(point string) error)

	GetDispatchByID(id int64) (*Dispatch, error)
	GetLastDispatchIDForMorsel(morselID string) (int64, error)
	GetLatestDispatchForMorsel(morselID string) (*Dispatch, error)
	GetLatestDispatchBySession(sessionName string) (*Dispatch, error)
	GetLatestDispatchByPID(pid int) (*Dispatch, error)
	GetRunningDispatches() ([]Dispatch, error)
	GetStuckDispatches(timeout time.Duration) ([]Dispatch, error)
	GetDispatchesByMorsel(morselID string) ([]Dispatch, error)
	GetCompletedDispatchesSince(projectName, since string) ([]Dispatch, error)
	GetPendingRetryDispatches() ([]Dispatch, error)
	GetRunningDispatchStageCounts() (map[string]int, error)
	GetProjectDispatchStatusCounts(since time.Time) (map[string]ProjectDispatchStatusCounts, error)
	GetDispatchCost(dispatchID int64) (inputTokens, outputTokens int, costUSD float64, err error)
	GetTotalCost(project string) (float64, error)
	GetTotalCostSince(project string, since time.Time) (float64, error)

	CountRecentDispatchesByFailureCategory(category string, window time.Duration) (int, error)
	CountAuthedUsage5h() (int, error)
	CountAuthedUsageWeekly() (int, error)
	CountDispatchesSince(since time.Time, statuses []string) (int, error)

	WasMorselDispatchedRecently(morselID string, cooldownPeriod time.Duration) (bool, error)
	WasMorselAgentDispatchedRecently(morselID, agentID string, cooldownPeriod time.Duration) (bool, error)
	HasRecentConsecutiveFailures(morselID string, threshold int, window time.Duration) (bool, error)
	IsMorselDispatched(morselID string) (bool, error)
	IsAgentBusy(project, agent string) (bool, error)

	InterruptRunningDispatches() (int, error)

	EnqueueOverflowItem(morselID, project, role, agentID string, priority int, reason string) (int64, error)
	RemoveOverflowItem(morselID string) (int64, error)
	ListOverflowQueue() ([]OverflowQueueItem, error)
	CountOverflowQueue() (int, error)
}

// SafetyStore covers safety blocks and morsel validation state.
type SafetyStore interface {
	GetBlock(scope, blockType string) (*SafetyBlock, error)
	SetBlock(scope, blockType string, blockedUntil time.Time, reason string) error
	SetBlockWithMetadata(scope, blockType string, blockedUntil time.Time, reason string, metadata map[string]interface{}) error
	RemoveBlock(scope, blockType string) error
	GetActiveBlocks() ([]SafetyBlock, error)
	GetBlockCountsByType() (map[string]int, error)
	IsMorselValidating(morselID string) (bool, error)
	SetMorselValidating(morselID string, until time.Time) error
	ClearMorselValidating(morselID string) error
}

// MetricsStore covers health events, tick metrics, output capture, quality scores,
// provider stats, token usage, and step metrics.
type MetricsStore interface {
	RecordHealthEvent(eventType, details string) error
	RecordHealthEventWithDispatch(eventType, details string, dispatchID int64, morselID string) error
	RecordTickMetrics(project string, open, ready, dispatched, completed, failed, stuck int) error
	RecordSprintBoundary(sprintNumber int, sprintStart, sprintEnd time.Time) error
	GetCurrentSprintNumber() (int, error)
	GetRecentHealthEvents(hours int) ([]HealthEvent, error)

	CaptureOutput(dispatchID int64, output string) error
	GetOutput(dispatchID int64) (string, error)
	GetOutputTail(dispatchID int64) (string, error)

	UpsertQualityScore(score QualityScore) error
	GetProviderRoleQualityAverages(window time.Duration) (map[string]map[string]float64, error)
	GetProviderStats(window time.Duration) (map[string]ProviderStat, error)
	GetProviderLabelStats(window time.Duration) (map[string]map[string]ProviderLabelStat, error)

	StoreTokenUsage(dispatchID int64, morselID, project, activityName, agent string, usage TokenUsage) error
	GetTokenUsageByDispatch(dispatchID int64) ([]TokenUsageRecord, error)
	GetTokenUsageSummary(groupBy string, since time.Time) ([]TokenUsageSummary, error)

	StoreStepMetric(dispatchID int64, morselID, project, stepName string, durationS float64, status string, slow bool) error
	GetStepMetricsByDispatch(dispatchID int64) ([]StepMetricRecord, error)
}

// ClaimStore covers morsel claim lease lifecycle.
type ClaimStore interface {
	UpsertClaimLease(morselID, project, morselsDir, agentID string) error
	AttachDispatchToClaimLease(morselID string, dispatchID int64) error
	HeartbeatClaimLease(morselID string) error
	DeleteClaimLease(morselID string) error
	GetClaimLease(morselID string) (*ClaimLease, error)
	ListClaimLeases() ([]ClaimLease, error)
	GetExpiredClaimLeases(ttl time.Duration) ([]ClaimLease, error)
}

// StageStore covers morsel workflow stage tracking.
type StageStore interface {
	GetMorselStage(project, morselID string) (*MorselStage, error)
	UpsertMorselStage(stage *MorselStage) error
	UpdateMorselStageProgress(project, morselID, newStage string, stageIndex, totalStages int, dispatchID int64) error
	ListMorselStagesForProject(project string) ([]*MorselStage, error)
	DeleteMorselStage(project, morselID string) error
	GetMorselStagesByMorselIDOnly(morselID string) ([]*MorselStage, error)
}

// LessonStore covers lesson persistence and full-text search.
type LessonStore interface {
	StoreLesson(morselID, project, category, summary, detail string, filePaths []string, labels []string, semgrepRuleID string) (int64, error)
	SearchLessons(query string, limit int) ([]StoredLesson, error)
	SearchLessonsByFilePath(filePaths []string, limit int) ([]StoredLesson, error)
	GetRecentLessons(project string, limit int) ([]StoredLesson, error)
	GetLessonsByMorsel(morselID string) ([]StoredLesson, error)
	CountLessons(project string) (int, error)
}

// AllocationStore covers Chief SM capacity allocation decisions and history.
type AllocationStore interface {
	RecordAllocationDecision(decision *AllocationDecision) error
	GetAllocationDecision(id int64) (*AllocationDecision, error)
	GetAllocationDecisionByCeremony(ceremonyID string) (*AllocationDecision, error)
	UpdateAllocationStatus(id int64, status string) error
	GetActiveAllocation() (*AllocationDecision, error)
	ListAllocationDecisions(startDate, endDate time.Time) ([]*AllocationDecision, error)
	GetProjectCapacityHistory(project string, days int) ([]ProjectCapacityRecord, error)
}

// CeremonyStore covers sprint review data, failure/stuck dispatch details, and agent performance.
type CeremonyStore interface {
	GetSprintReviewData(startDate, endDate time.Time) (*SprintReviewData, error)
	GetFailedDispatchDetails(startDate, endDate time.Time) ([]FailedDispatchDetail, error)
	GetStuckDispatchDetails(timeout time.Duration) ([]StuckDispatchDetail, error)
	GetAgentPerformanceStats(startDate, endDate time.Time) (map[string]AgentPerformanceStats, error)
}

// SprintStore covers sprint planning context, backlog retrieval, and planning records.
type SprintStore interface {
	GetBacklogMorsels(ctx context.Context, dag *graph.DAG, project string) ([]*BacklogMorsel, error)
	GetSprintContext(ctx context.Context, dag *graph.DAG, project string, daysBack int) (*SprintContext, error)
	GetCurrentSprintBoundary() (*SprintBoundary, error)
	RecordSprintPlanning(project, trigger string, backlogSize, threshold int, result, details string) error
	GetLastSprintPlanning(project string) (*SprintPlanningRecord, error)
}

// PlanGateStore covers execution plan gate lifecycle.
type PlanGateStore interface {
	SetActiveApprovedPlan(planID, approvedBy string) error
	ClearActiveApprovedPlan() error
	GetActiveApprovedPlan() (*ExecutionPlanGate, error)
	HasActiveApprovedPlan() (bool, *ExecutionPlanGate, error)
}

// StingrayStore covers Stingray code health audit runs and findings.
type StingrayStore interface {
	RecordRun(project string, findingsTotal, findingsNew, findingsResolved int, metricsJSON string) (int64, error)
	RecordFinding(runID int64, project, category, severity, title, detail, filePath, evidence string) (int64, error)
	GetRecentFindings(project string, limit int) ([]StingrayFinding, error)
	GetTrendingFindings(project string, minOccurrences int) ([]StingrayFinding, error)
	UpdateFindingStatus(id int64, status string) error
	UpdateFindingMorselID(id int64, morselID string) error
	UpdateFindingLastSeen(id int64) error
	GetFindingByTitleAndFile(project, title, filePath string) (*StingrayFinding, error)
	GetLatestRun(project string) (*StingrayRun, error)
}

var _ StingrayStore = (*Store)(nil)
var _ TraceStore = (*Store)(nil)
var _ CrystalCandidateStore = (*Store)(nil)
