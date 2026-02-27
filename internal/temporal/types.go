package temporal

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/antigravity-dev/chum/internal/config"
)

// TaskRequest is submitted via the API to start a workflow.
type TaskRequest struct {
	TaskID              string           `json:"task_id"`
	Project             string           `json:"project"`
	Prompt              string           `json:"prompt"`
	TaskTitle           string           `json:"task_title"` // human-readable task title for workflow searchability and display
	Agent               string           `json:"agent"`      // primary coding agent (keyword): claude|codex|gemini
	Model               string           `json:"model"`      // model override for the CLI (e.g. "haiku", "gemini-2.5-flash")
	Reviewer            string           `json:"reviewer"`   // review agent — auto-assigned if empty
	WorkDir             string           `json:"work_dir"`
	Provider            string           `json:"provider"`
	Priority            int              `json:"priority"`                        // scheduling priority expected as an int in range [0,4], where 0 is highest
	DoDChecks           []string         `json:"dod_checks"`                      // e.g. ["go build ./cmd/chum", "go test ./..."]
	SlowStepThreshold   time.Duration    `json:"slow_step_threshold"`             // steps exceeding this are flagged slow
	EscalationChain     []EscalationTier `json:"escalation_chain"`                // ordered tiers for fail-upward retry
	MaxRetriesOverride  int              `json:"max_retries_override,omitempty"`  // if >0, overrides retriesForTier for ALL tiers
	MaxHandoffsOverride int              `json:"max_handoffs_override,omitempty"` // if >0, overrides maxHandoffs constant
	PreviousErrors      []string         `json:"previous_errors,omitempty"`
	ExplosionID         string           `json:"explosion_id,omitempty"`     // If set, workflow runs in isolated sandbox mode (Cambrian Explosion)
	TraceSessionID      string           `json:"trace_session_id,omitempty"` // Graph-Brain trace session ID
}

// EscalationTier defines one level in the fail-upward chain.
type EscalationTier struct {
	ProviderKey string `json:"provider_key"` // e.g. "codex-spark", "gemini-flash"
	CLI         string `json:"cli"`          // CLI agent name: "codex", "gemini", "claude"
	Model       string `json:"model"`        // model to pass via --model flag (empty = default)
	Tier        string `json:"tier"`         // "fast", "balanced", "premium"
	Reviewer    string `json:"reviewer"`     // configured reviewer agent (empty = DefaultReviewer fallback)
	Enabled     bool   `json:"enabled"`      // false = skip this tier (gated)
}

// DefaultReviewer returns the cross-model reviewer for a given primary agent.
// If providers is non-nil and the agent has a configured Reviewer, that is used.
// Otherwise falls back to hardcoded cross-model routing.
func DefaultReviewer(agent string, providers ...map[string]config.Provider) string {
	// Check config-driven reviewer first
	if len(providers) > 0 && providers[0] != nil {
		if p, ok := providers[0][agent]; ok && p.Reviewer != "" {
			return p.Reviewer
		}
	}
	// Hardcoded fallback
	switch agent {
	case "claude":
		return "codex"
	case "codex":
		return "gemini"
	case "gemini":
		return "codex"
	default:
		return "codex"
	}
}

// AgentForDoD returns a cheap/free agent for running DoD test commands.
// Don't need a smart model to run tests and say if they passed.
const DoDAgent = "codex" // codex-mini when available

// StructuredPlan is the output of the planning activity.
// Tasks are gated — if the plan doesn't have acceptance criteria,
// it doesn't enter the coding engine.
type StructuredPlan struct {
	Summary             string     `json:"summary"`
	Steps               []PlanStep `json:"steps"`
	FilesToModify       []string   `json:"files_to_modify"`
	AcceptanceCriteria  []string   `json:"acceptance_criteria"`
	EstimatedComplexity string     `json:"estimated_complexity"` // low, medium, high
	RiskAssessment      string     `json:"risk_assessment"`
	PreviousErrors      []string   `json:"previous_errors,omitempty"`
	TokenUsage          TokenUsage `json:"token_usage,omitempty"`
}

// PlanStep is a single step in the structured plan.
type PlanStep struct {
	Description string `json:"description"`
	File        string `json:"file"`
	Rationale   string `json:"rationale"`
}

// Validate gates the plan — rejects it before entering the coding engine
// if it doesn't meet minimum quality standards.
func (p *StructuredPlan) Validate() []string {
	var issues []string
	if p.Summary == "" {
		issues = append(issues, "plan has no summary")
	}
	if len(p.Steps) == 0 {
		issues = append(issues, "plan has no steps")
	}
	if len(p.AcceptanceCriteria) == 0 {
		issues = append(issues, "plan has no acceptance criteria — nothing enters the coding engine without DoD")
	}
	if len(p.FilesToModify) == 0 {
		issues = append(issues, "plan doesn't specify which files will be modified")
	}
	return issues
}

// TokenUsage tracks LLM token consumption from a single CLI invocation.
// Populated by parsing --output-format json from the claude CLI.
type TokenUsage struct {
	InputTokens         int     `json:"input_tokens"`
	OutputTokens        int     `json:"output_tokens"`
	CacheReadTokens     int     `json:"cache_read_tokens"`
	CacheCreationTokens int     `json:"cache_creation_tokens"`
	CostUSD             float64 `json:"cost_usd"`
}

// Add accumulates another TokenUsage into this one.
func (t *TokenUsage) Add(other TokenUsage) {
	t.InputTokens += other.InputTokens
	t.OutputTokens += other.OutputTokens
	t.CacheReadTokens += other.CacheReadTokens
	t.CacheCreationTokens += other.CacheCreationTokens
	t.CostUSD += other.CostUSD
}

// ActivityTokenUsage carries per-activity token usage through the workflow
// so RecordOutcomeActivity can persist granular per-activity costs.
type ActivityTokenUsage struct {
	ActivityName string     `json:"activity_name"`
	Agent        string     `json:"agent"`
	Tokens       TokenUsage `json:"tokens"`
}

// ExecutionResult is returned by the execute activity.
type ExecutionResult struct {
	ExitCode int        `json:"exit_code"`
	Output   string     `json:"output"`
	Agent    string     `json:"agent"` // which agent executed
	Tokens   TokenUsage `json:"tokens"`
}

// ReviewResult is returned by the cross-model code review activity.
type ReviewResult struct {
	Approved      bool       `json:"approved"`
	Issues        []string   `json:"issues"`
	Suggestions   []string   `json:"suggestions"`
	ReviewerAgent string     `json:"reviewer_agent"`
	ReviewOutput  string     `json:"review_output"`
	Tokens        TokenUsage `json:"tokens"`
}

// DoDResult is returned by the DoD verification activity.
type DoDResult struct {
	Passed   bool          `json:"passed"`
	Checks   []CheckResult `json:"checks"`
	Failures []string      `json:"failures"`
}

// CheckResult is the result of a single DoD check command.
type CheckResult struct {
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	Output     string `json:"output"`
	Passed     bool   `json:"passed"`
	DurationMs int64  `json:"duration_ms"`
}

// StepMetric records the name, duration, and outcome of a single pipeline step.
type StepMetric struct {
	Name      string  `json:"name"`
	DurationS float64 `json:"duration_s"`
	Status    string  `json:"status"` // "ok", "failed", "skipped"
	Slow      bool    `json:"slow,omitempty"`
}

// OrganismLog captures a structured log entry for any non-shark organism
// (turtle, crab, learner, groomer, dispatcher, explosion, etc.).
type OrganismLog struct {
	OrganismType string  `json:"organism_type"` // turtle, crab, learner, groomer, dispatcher, explosion
	WorkflowID   string  `json:"workflow_id"`
	TaskID       string  `json:"task_id"`
	Project      string  `json:"project"`
	Status       string  `json:"status"` // completed, failed, escalated, throttled
	DurationS    float64 `json:"duration_s"`
	Details      string  `json:"details"` // free-text summary of what happened
	Steps        int     `json:"steps"`   // phase/step count
	Error        string  `json:"error,omitempty"`
}

// OutcomeRecord is passed to the store recording activity.
type OutcomeRecord struct {
	DispatchID     int64                `json:"dispatch_id"`
	TaskID         string               `json:"task_id"`
	Project        string               `json:"project"`
	Agent          string               `json:"agent"`
	Reviewer       string               `json:"reviewer"`
	Provider       string               `json:"provider"`
	Status         string               `json:"status"` // completed, failed, escalated
	ExitCode       int                  `json:"exit_code"`
	DurationS      float64              `json:"duration_s"`
	DoDPassed      bool                 `json:"dod_passed"`
	DoDFailures    string               `json:"dod_failures"`
	Handoffs       int                  `json:"handoffs"` // how many cross-model review cycles
	FilesChanged   int                  `json:"files_changed"`
	TotalTokens    TokenUsage           `json:"total_tokens"`
	ActivityTokens []ActivityTokenUsage `json:"activity_tokens,omitempty"`
	StepMetrics    []StepMetric         `json:"step_metrics,omitempty"`
}

// EscalationRequest is sent to the chief when DoD fails after retries.
type EscalationRequest struct {
	TaskID       string   `json:"task_id"`
	Project      string   `json:"project"`
	PlanSummary  string   `json:"plan_summary"`
	Failures     []string `json:"failures"`
	AttemptCount int      `json:"attempt_count"`
	HandoffCount int      `json:"handoff_count"`
}

// --- Planning Ceremony Types ---
// Planning happens BEFORE any code is written. Morsels are written, arranged,
// deps aligned, structure emerges. Nothing goes to the sharks until it's chum.

// PlanningRequest starts a planning session.
type PlanningRequest struct {
	Project           string        `json:"project"`
	Agent             string        `json:"agent"` // chief agent for execution (defaults to claude)
	Tier              string        `json:"tier"`  // LLM tier for planning activities: "fast" or "premium"
	WorkDir           string        `json:"work_dir"`
	CandidateTopK     int           `json:"candidate_top_k,omitempty"`  // shortlist size for ranked planning options (1..20, default 5)
	SignalTimeout     time.Duration `json:"signal_timeout,omitempty"`   // max wait per human signal in interactive mode
	SessionTimeout    time.Duration `json:"session_timeout,omitempty"`  // max lifecycle of one planning session
	SlowStepThreshold time.Duration `json:"slow_step_threshold"`        // steps exceeding this are flagged slow
	TraceSessionID    string        `json:"trace_session_id,omitempty"` // persistent planning trace session id
	TraceCycle        int           `json:"trace_cycle,omitempty"`      // 1-indexed planning cycle number
	SeedTaskID        string        `json:"seed_task_id,omitempty"`     // optional dispatcher/escalation seed task id
	SeedTaskTitle     string        `json:"seed_task_title,omitempty"`  // optional dispatcher/escalation seed task title
	SeedTaskPrompt    string        `json:"seed_task_prompt,omitempty"` // optional dispatcher/escalation seed prompt/context
	AutoMode          bool          `json:"auto_mode,omitempty"`        // auto-select/answer/greenlight without human signals
}

// BacklogItem is a single work item the chief has identified.
type BacklogItem struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Impact      string `json:"impact"`      // why this matters
	Effort      string `json:"effort"`      // rough estimate
	Recommended bool   `json:"recommended"` // chief's pick
	Rationale   string `json:"rationale"`   // why recommended (or not)
}

// BacklogPresentation is the chief's groomed view of the backlog.
type BacklogPresentation struct {
	Items     []BacklogItem `json:"items"`
	Rationale string        `json:"rationale"` // "here's what we think and why"
}

// PlanningQuestion is a single clarifying question, asked one at a time.
// Each question depends on answers to previous questions.
type PlanningQuestion struct {
	Number         int      `json:"number"`         // 1-indexed
	Total          int      `json:"total"`          // total questions
	Question       string   `json:"question"`       // the question
	Options        []string `json:"options"`        // A, B, C choices
	Recommendation string   `json:"recommendation"` // "We recommend A because..."
	Context        string   `json:"context"`        // what previous answer influenced this
}

// PlanSummary is the final summary before greenlight.
// What we're building, why, and how much effort.
type PlanSummary struct {
	What      string   `json:"what"`       // clear description of what we're building
	Why       string   `json:"why"`        // business justification
	Effort    string   `json:"effort"`     // time/complexity estimate
	Risks     []string `json:"risks"`      // what could go wrong
	DoDChecks []string `json:"dod_checks"` // how we'll verify success
}

// PlanningState tracks where we are in the planning ceremony.
// Exposed via GET /planning/{id} so the human always knows the current state.
type PlanningState struct {
	SessionID       string               `json:"session_id"`
	Phase           string               `json:"phase"` // backlog, selecting, questioning, summarizing, ready, executing
	Backlog         *BacklogPresentation `json:"backlog,omitempty"`
	SelectedItem    *BacklogItem         `json:"selected_item,omitempty"`
	CurrentQuestion *PlanningQuestion    `json:"current_question,omitempty"`
	Answers         map[string]string    `json:"answers,omitempty"` // question# → answer
	Summary         *PlanSummary         `json:"summary,omitempty"`
	TaskRequest     *TaskRequest         `json:"task_request,omitempty"` // produced after greenlight
}

// PlanningTraceRecord is one persisted event in the planning trajectory graph.
// Stores both summary and full-fidelity text for audit/replay.
type PlanningTraceRecord struct {
	SessionID      string  `json:"session_id"`
	RunID          string  `json:"run_id,omitempty"`
	Project        string  `json:"project"`
	TaskID         string  `json:"task_id,omitempty"`
	Cycle          int     `json:"cycle,omitempty"`
	Stage          string  `json:"stage"`
	NodeID         string  `json:"node_id,omitempty"`
	ParentNodeID   string  `json:"parent_node_id,omitempty"`
	BranchID       string  `json:"branch_id,omitempty"`
	OptionID       string  `json:"option_id,omitempty"`
	EventType      string  `json:"event_type"`
	Actor          string  `json:"actor,omitempty"`
	ToolName       string  `json:"tool_name,omitempty"`
	ToolInput      string  `json:"tool_input,omitempty"`
	ToolOutput     string  `json:"tool_output,omitempty"`
	PromptText     string  `json:"prompt_text,omitempty"`
	ResponseText   string  `json:"response_text,omitempty"`
	SummaryText    string  `json:"summary_text,omitempty"`
	FullText       string  `json:"full_text,omitempty"`
	SelectedOption string  `json:"selected_option,omitempty"`
	Reward         float64 `json:"reward,omitempty"`
	MetadataJSON   string  `json:"metadata_json,omitempty"`
}

// PlanningSnapshotRecord persists one checkpoint for deterministic rollback.
type PlanningSnapshotRecord struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id,omitempty"`
	Project   string `json:"project"`
	TaskID    string `json:"task_id,omitempty"`
	Cycle     int    `json:"cycle,omitempty"`
	Stage     string `json:"stage"`
	StateHash string `json:"state_hash"`
	StateJSON string `json:"state_json"`
	Stable    bool   `json:"stable"`
	Reason    string `json:"reason,omitempty"`
}

// PlanningSnapshotState is the deserialized planning state payload.
type PlanningSnapshotState struct {
	Cycle          int               `json:"cycle"`
	Stage          string            `json:"stage"`
	SelectedID     string            `json:"selected_id,omitempty"`
	SelectedTitle  string            `json:"selected_title,omitempty"`
	SelectedOption string            `json:"selected_option,omitempty"`
	Answers        map[string]string `json:"answers,omitempty"`
	SummaryWhat    string            `json:"summary_what,omitempty"`
	SummaryWhy     string            `json:"summary_why,omitempty"`
	SummaryEffort  string            `json:"summary_effort,omitempty"`
}

// PlanningBlacklistEntryRecord blocks a repeated failed state-action pair.
type PlanningBlacklistEntryRecord struct {
	SessionID  string `json:"session_id"`
	Project    string `json:"project"`
	TaskID     string `json:"task_id,omitempty"`
	Cycle      int    `json:"cycle,omitempty"`
	Stage      string `json:"stage"`
	StateHash  string `json:"state_hash"`
	ActionHash string `json:"action_hash"`
	Reason     string `json:"reason,omitempty"`
	Metadata   string `json:"metadata,omitempty"`
}

// PlanningBlacklistCheck requests blacklist status for a state-action pair.
type PlanningBlacklistCheck struct {
	SessionID  string `json:"session_id"`
	StateHash  string `json:"state_hash"`
	ActionHash string `json:"action_hash"`
}

// PlanningCandidateScoreRecord is one persisted per-project option score state.
type PlanningCandidateScoreRecord struct {
	Project         string    `json:"project"`
	OptionID        string    `json:"option_id"`
	ScoreAdjustment float64   `json:"score_adjustment"`
	Successes       int       `json:"successes"`
	Failures        int       `json:"failures"`
	LastReason      string    `json:"last_reason,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// PlanningCandidateScoreQuery loads score adjustments for a project and option set.
type PlanningCandidateScoreQuery struct {
	Project   string   `json:"project"`
	OptionIDs []string `json:"option_ids"`
}

// PlanningCandidateScoreDelta applies a score delta for one project option.
type PlanningCandidateScoreDelta struct {
	Project  string  `json:"project"`
	OptionID string  `json:"option_id"`
	Delta    float64 `json:"delta"`
	Outcome  string  `json:"outcome,omitempty"`
	Reason   string  `json:"reason,omitempty"`
}

// --- CHUM (Continuous Hyper-Kanban Utility Module) Types ---
// Event-driven learning + grooming triggered after every morsel completion,
// plus a daily strategic grooming cycle at 5 AM.

// LearnerRequest is passed to ContinuousLearnerWorkflow after a morsel completes.
type LearnerRequest struct {
	TaskID         string   `json:"task_id"`
	Project        string   `json:"project"`
	WorkDir        string   `json:"work_dir"`
	Agent          string   `json:"agent"` // which agent completed the morsel
	DoDPassed      bool     `json:"dod_passed"`
	DoDFailures    string   `json:"dod_failures"`
	FilesChanged   []string `json:"files_changed,omitempty"`
	DiffSummary    string   `json:"diff_summary,omitempty"`    // truncated unified diff
	PreviousErrors []string `json:"previous_errors,omitempty"` // review rejections + DoD failures from the loop
	Tier           string   `json:"tier"`                      // LLM tier: "fast" or "premium"
}

// Lesson is a single extracted lesson from a completed morsel.
type Lesson struct {
	ID            int64    `json:"id,omitempty"`
	TaskID        string   `json:"task_id"`
	Project       string   `json:"project"`
	Category      string   `json:"category"`                  // pattern, antipattern, rule, insight
	Summary       string   `json:"summary"`                   // one-line
	Detail        string   `json:"detail"`                    // full explanation
	FilePaths     []string `json:"file_paths"`                // affected files
	Labels        []string `json:"labels"`                    // searchable tags
	SemgrepRuleID string   `json:"semgrep_rule_id,omitempty"` // if a rule was generated
	CreatedAt     string   `json:"created_at,omitempty"`
}

// SemgrepRule is the output of Semgrep rule generation by the learner.
type SemgrepRule struct {
	RuleID   string `json:"rule_id"`
	FileName string `json:"file_name"` // e.g. "chum-nil-check-after-error.yaml"
	Content  string `json:"content"`   // full YAML content
	Category string `json:"category"`  // error-handling, security, performance, etc.
}

// FailureTriageRequest is passed to FailureTriageActivity after any pipeline failure.
// The triage reads the agent's actual output and decides: retry with guidance or rescope.
type FailureTriageRequest struct {
	TaskID      string   `json:"task_id"`
	Project     string   `json:"project"`
	WorkDir     string   `json:"work_dir"`
	Agent       string   `json:"agent"`
	FailureType string   `json:"failure_type"` // "execute", "review", "dod", "ubs"
	Failures    []string `json:"failures"`     // structured error strings
	AgentOutput string   `json:"agent_output"` // raw dispatch_output tail (truncated)
	Attempt     int      `json:"attempt"`
	MaxRetries  int      `json:"max_retries"`
	PlanSummary string   `json:"plan_summary"`
	Tier        string   `json:"tier"`
}

// FailureTriageResult is the LLM's triage decision after analyzing a failure.
type FailureTriageResult struct {
	Decision      string   `json:"decision"`       // "retry" or "rescope"
	Guidance      string   `json:"guidance"`       // if retry: specific instruction for next attempt
	RescopeReason string   `json:"rescope_reason"` // if rescope: why this needs turtle/crab intervention
	Antibodies    []string `json:"antibodies"`     // patterns to inject into genome
	Category      string   `json:"category"`       // "infrastructure", "logic", "scope", "complexity"
}

// TacticalGroomRequest is passed to TacticalGroomWorkflow after a task completes.
type TacticalGroomRequest struct {
	TaskID       string   `json:"task_id"`
	Project      string   `json:"project"`
	WorkDir      string   `json:"work_dir"`
	Tier         string   `json:"tier"`                    // "fast" for tactical
	FilesChanged []string `json:"files_changed,omitempty"` // files modified by the landed morsel
	DiffSummary  string   `json:"diff_summary,omitempty"`  // git diff --stat output
	TaskTitle    string   `json:"task_title,omitempty"`    // title of the completed morsel
}

// MorselMutation is a single mutation the groombot wants to apply to the backlog.
// The Action field determines which other fields are meaningful.
type MorselMutation struct {
	TaskID          string   `json:"task_id"`
	Action          string   `json:"action"` // update_priority, add_dependency, update_notes, create, close
	Priority        *int     `json:"priority,omitempty"`
	Notes           string   `json:"notes,omitempty"`
	DependsOnID     string   `json:"depends_on_id,omitempty"`
	Title           string   `json:"title,omitempty"`               // for create
	Description     string   `json:"description,omitempty"`         // for create
	Acceptance      string   `json:"acceptance_criteria,omitempty"` // for create
	Design          string   `json:"design,omitempty"`              // for create
	EstimateMinutes int      `json:"estimate_minutes,omitempty"`    // for create
	Labels          []string `json:"labels,omitempty"`
	Reason          string   `json:"reason,omitempty"` // for close
	StrategicSource string   `json:"strategic_source,omitempty"`
	Deferred        bool     `json:"deferred,omitempty"` // for strategic decomposition-only suggestions
}

const (
	// StrategicMutationSource identifies strategic-groomer-generated mutations.
	StrategicMutationSource = "strategic"
	// StrategicSourceLabel marks morsel metadata sourced from strategic grooming.
	StrategicSourceLabel = "source:strategic"
	// StrategicDeferredLabel marks deferred strategic suggestions.
	StrategicDeferredLabel = "strategy:deferred"
)

// GroomResult is the output of a grooming activity.
type GroomResult struct {
	MutationsApplied int      `json:"mutations_applied"`
	MutationsFailed  int      `json:"mutations_failed"`
	Details          []string `json:"details"`
}

// StrategicGroomRequest is passed to the daily StrategicGroomWorkflow.
type StrategicGroomRequest struct {
	Project string `json:"project"`
	WorkDir string `json:"work_dir"`
	Tier    string `json:"tier"` // "premium" for strategic
}

// RepoMap is a compressed representation of the codebase for LLM context.
// Generated by go list/go doc — keeps the full codebase under ~3k tokens.
type RepoMap struct {
	Packages    []PackageInfo `json:"packages"`
	TotalFiles  int           `json:"total_files"`
	TotalLines  int           `json:"total_lines"`
	GeneratedAt string        `json:"generated_at"`
}

// PackageInfo is a single Go package in the repo map.
type PackageInfo struct {
	ImportPath string   `json:"import_path"`
	Name       string   `json:"name"`
	GoFiles    []string `json:"go_files"`
	DocSummary string   `json:"doc_summary"` // first line of package doc
	Exports    []string `json:"exports"`     // exported function/type signatures
}

// StrategicAnalysis is the output of the premium LLM strategic analysis.
type StrategicAnalysis struct {
	Priorities   []StrategicItem  `json:"priorities"`
	Risks        []string         `json:"risks"`
	Observations []string         `json:"observations"`
	Mutations    []MorselMutation `json:"mutations"` // suggested morsel mutations
}

// StrategicItem is a single priority from strategic analysis.
type StrategicItem struct {
	TaskID    string `json:"task_id,omitempty"` // empty for new suggestions
	Title     string `json:"title"`
	Rationale string `json:"rationale"`
	Urgency   string `json:"urgency"` // critical, high, medium, low
}

// MorningBriefing is the daily briefing written to .morsels/morning_briefing.md.
type MorningBriefing struct {
	Date          string          `json:"date"`
	Project       string          `json:"project"`
	TopPriorities []StrategicItem `json:"top_priorities"`
	Risks         []string        `json:"risks"`
	RecentLessons []Lesson        `json:"recent_lessons"`
	HealthEvents  []HealthSummary `json:"health_events,omitempty"` // system health from last 24h
	Markdown      string          `json:"markdown"`                // full rendered markdown
}

// HealthSummary groups health events by type for the morning briefing.
type HealthSummary struct {
	EventType    string `json:"event_type"`
	Count        int    `json:"count"`
	LatestDetail string `json:"latest_detail"`
}

// PRReviewRequest asks ReviewPRActivity to fetch a PR diff and run a
// cross-model review, posting the result as a GitHub PR comment.
type PRReviewRequest struct {
	PRNumber  int    `json:"pr_number"`
	Workspace string `json:"workspace"`
	Reviewer  string `json:"reviewer"` // empty = auto-select via DefaultReviewer(Author)
	Author    string `json:"author"`   // agent that created the PR, for cross-model selection
}

// PRReviewResult is the structured output of a PR review.
type PRReviewResult struct {
	Approved      bool     `json:"approved"`
	Issues        []string `json:"issues"`
	Suggestions   []string `json:"suggestions"`
	ReviewerAgent string   `json:"reviewer_agent"`
}

// PRReviewPollerRequest drives the periodic scan for unreviewed PRs.
type PRReviewPollerRequest struct {
	Workspace string `json:"workspace"`
}

// UnreviewedPR is an open PR that hasn't been reviewed by CHUM yet.
type UnreviewedPR struct {
	Number int    `json:"number"`
	Author string `json:"author"` // mapped to CLI agent name for cross-model selection
}

// --- Post-Mortem Types ---

// FailedWorkflow is a workflow that failed and needs post-mortem investigation.
type FailedWorkflow struct {
	WorkflowID string `json:"workflow_id"`
	RunID      string `json:"run_id"`
	CloseTime  string `json:"close_time"`
	ErrorMsg   string `json:"error_msg"`
}

// FailureContext is the structured context fetched from a failed workflow's
// event history, ready for LLM investigation in td19b.
type FailureContext struct {
	WorkflowID     string            `json:"workflow_id"`
	RunID          string            `json:"run_id"`
	ErrorMessage   string            `json:"error_message"`
	FailedActivity string            `json:"failed_activity"`
	AttemptCount   int               `json:"attempt_count"`
	DurationS      float64           `json:"duration_s"`
	TaskID         string            `json:"task_id,omitempty"`
	RecentCommits  string            `json:"recent_commits,omitempty"`
	SearchAttrs    map[string]string `json:"search_attrs,omitempty"`
}

// PostMortemRequest drives the PostMortemWorkflow with failure context.
type PostMortemRequest struct {
	Failure FailureContext `json:"failure"`
	Project string         `json:"project"`
	WorkDir string         `json:"work_dir"`
	Tier    string         `json:"tier"`
}

// --- Dispatcher Types ---
// DispatcherWorkflow scans for ready morsels and starts ChumAgentWorkflow
// children. Runs on a Temporal Schedule every tick_interval.

// DispatchCandidate is a ready morsel with its project context, returned by
// ScanCandidatesActivity and dispatched as a child workflow.
type DispatchCandidate struct {
	TaskID            string            `json:"task_id"`
	Title             string            `json:"title"`
	TaskTitle         string            `json:"task_title"` // same as title; explicit for consistency with TaskRequest
	Project           string            `json:"project"`
	WorkDir           string            `json:"work_dir"`
	Prompt            string            `json:"prompt"`
	Species           string            `json:"species"`
	Labels            []string          `json:"labels,omitempty"`
	Priority          int               `json:"priority"` // 0..4 scheduling/search metadata
	Provider          string            `json:"provider"`
	DoDChecks         []string          `json:"dod_checks"`
	SlowStepThreshold time.Duration     `json:"slow_step_threshold"`
	EstimateMinutes   int               `json:"estimate_minutes"`
	PreviousErrors    []string          `json:"previous_errors,omitempty"`
	Generation        int               `json:"generation"`    // 0 = new species
	Complexity        int               `json:"complexity"`    // 0-100 score
	HasCrabSeal       bool              `json:"has_crab_seal"` // true if properly decomposed/sized for direct dispatch
	PlannerEdgeStats  []PlannerEdgeStat `json:"planner_edge_stats,omitempty"`
}

// ScanCandidatesResult is returned by ScanCandidatesActivity.
type ScanCandidatesResult struct {
	Candidates             []DispatchCandidate `json:"candidates"`
	Running                int                 `json:"running"` // currently running workflow count
	MaxTotal               int                 `json:"max_total"`
	PlanningRunning        int                 `json:"planning_running"`         // currently running planning workflow count
	PlanningSignalTimeout  time.Duration       `json:"planning_signal_timeout"`  // per-signal planning wait timeout
	PlanningSessionTimeout time.Duration       `json:"planning_session_timeout"` // max planning session runtime
	Throttled              bool                `json:"throttled"`                // true if dispatch was blocked by token budget
	ThrottleReason         string              `json:"throttle_reason"`          // human-readable reason for throttling
	AvailableAgents        []string            `json:"available_agents"`         // enabled CLI agent names from config
	EscalationTiers        []EscalationTier    `json:"escalation_tiers"`         // pre-computed escalation chain from config
	EnablePlannerV2        bool                `json:"enable_planner_v2"`
	MaxRetriesOverride     int                 `json:"max_retries_override,omitempty"`  // higher-learning: per-tier retry cap (0 = use default)
	MaxHandoffsOverride    int                 `json:"max_handoffs_override,omitempty"` // higher-learning: handoff cap (0 = use default)
}

// PlannerEdgeStat is edge-scoped MCTS state passed from scan activity to PlannerV2.
type PlannerEdgeStat struct {
	ActionKey   string    `json:"action_key"`
	Visits      int       `json:"visits"`
	Wins        int       `json:"wins"`
	TotalReward float64   `json:"total_reward"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// PlannerV2Request drives lane selection and dispatch for one candidate.
type PlannerV2Request struct {
	Candidate       DispatchCandidate `json:"candidate"`
	Task            TaskRequest       `json:"task"`
	EscalationTiers []EscalationTier  `json:"escalation_tiers"`
	ParentNodeKey   string            `json:"parent_node_key"`
}

// PlannerOutcomeRecord records planner decision telemetry for MCTS updates.
type PlannerOutcomeRecord struct {
	TaskID         string  `json:"task_id"`
	Project        string  `json:"project"`
	Species        string  `json:"species"`
	ParentNodeKey  string  `json:"parent_node_key"`
	SelectedAction string  `json:"selected_action"`
	Outcome        string  `json:"outcome"`
	Reward         float64 `json:"reward"`
	DurationS      float64 `json:"duration_s"`
	ChildWorkflow  string  `json:"child_workflow"`
	MetadataJSON   string  `json:"metadata_json,omitempty"`
}

// --- Crab Decomposition Types ---
// Crabs sit between Chief/human and Sharks. They take a high-level
// markdown plan and decompose it into whales (epic-level groupings)
// and morsels (bite-sized executable units) for shark consumption.

// CrabDecompositionRequest starts a crab decomposition workflow.
type CrabDecompositionRequest struct {
	PlanID                  string `json:"plan_id"`
	Project                 string `json:"project"`
	WorkDir                 string `json:"work_dir"`
	PlanMarkdown            string `json:"plan_markdown"`
	Tier                    string `json:"tier"`                      // LLM tier: "fast", "balanced", or "premium"
	ParentWhaleID           string `json:"parent_whale_id,omitempty"` // optional parent whale to nest under
	RequireHumanReview      bool   `json:"require_human_review"`      // if true, block at Phase 6 for human signal; default: auto-approve
	DisableTurtleEscalation bool   `json:"disable_turtle_escalation"` // if true, return failed instead of crab->planning escalation (prevents recursive rebound)
}

// ParsedPlan is the output of deterministic markdown parsing.
type ParsedPlan struct {
	Title              string      `json:"title"`
	Context            string      `json:"context"`
	ScopeItems         []ScopeItem `json:"scope_items"`
	AcceptanceCriteria []string    `json:"acceptance_criteria"`
	OutOfScope         []string    `json:"out_of_scope"`
	RawMarkdown        string      `json:"raw_markdown"`
}

// ScopeItem is a single deliverable from the plan's scope section.
type ScopeItem struct {
	Index       int    `json:"index"`
	Description string `json:"description"`
	Completed   bool   `json:"completed"` // true if [x] instead of [ ]
}

// ClarificationResult holds the results of the 3-tier clarification process.
type ClarificationResult struct {
	Resolved        []ClarificationEntry `json:"resolved"`
	NeedsHumanInput bool                 `json:"needs_human_input"`
	HumanQuestions  []string             `json:"human_questions,omitempty"`
	HumanAnswers    string               `json:"human_answers,omitempty"`
	Tokens          TokenUsage           `json:"tokens,omitempty"`
}

// ClarificationEntry is a single resolved question.
type ClarificationEntry struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
	Source   string `json:"source"` // "lessons_db", "existing_morsels", "chief_llm", "human"
}

// CandidateWhale is a proposed whale (epic-level grouping) from decomposition.
type CandidateWhale struct {
	Index              int               `json:"index"`
	Title              string            `json:"title"`
	Description        string            `json:"description"`
	AcceptanceCriteria string            `json:"acceptance_criteria"`
	Morsels            []CandidateMorsel `json:"morsels"`
	ParentScopeItem    FlexInt           `json:"parent_scope_item"` // index into ParsedPlan.ScopeItems; LLMs may return string or int
}

// FlexInt accepts both int and string JSON values, coercing strings to 0.
// This prevents LLM JSON output from crashing the pipeline when it returns
// a string (e.g. a task ID) instead of the expected integer index.
type FlexInt int

func (f *FlexInt) UnmarshalJSON(b []byte) error {
	// Try int first (the expected case).
	var n int
	if err := json.Unmarshal(b, &n); err == nil {
		*f = FlexInt(n)
		return nil
	}
	// LLM returned a string — attempt to parse as int, default to 0.
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(s)); parseErr == nil {
			*f = FlexInt(parsed)
		} else {
			*f = 0 // non-numeric string (e.g. task ID) → default
		}
		return nil
	}
	// Fallback: ignore unparseable values.
	*f = 0
	return nil
}

// CandidateMorsel is a proposed morsel (bite-sized executable unit) before sizing.
type CandidateMorsel struct {
	Index              int      `json:"index"`
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	AcceptanceCriteria string   `json:"acceptance_criteria"`
	DesignHints        string   `json:"design_hints"`
	FileHints          []string `json:"file_hints,omitempty"`
	DependsOnIndices   []int    `json:"depends_on_indices,omitempty"` // indices into flat morsel list
}

// SizedMorsel is a fully-qualified morsel ready for human review and DAG emission.
type SizedMorsel struct {
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	AcceptanceCriteria string   `json:"acceptance_criteria"`
	Design             string   `json:"design"`
	EstimateMinutes    int      `json:"estimate_minutes"`
	Priority           int      `json:"priority"`
	Labels             []string `json:"labels"`
	FileHints          []string `json:"file_hints,omitempty"`
	DependsOnIndices   []int    `json:"depends_on_indices,omitempty"`
	WhaleIndex         int      `json:"whale_index"`
	RiskLevel          string   `json:"risk_level"` // "low", "medium", "high"
	SizingRationale    string   `json:"sizing_rationale"`
}

// EmitResult is the output of the morsel emission activity.
type EmitResult struct {
	WhaleIDs    []string `json:"whale_ids"`
	MorselIDs   []string `json:"morsel_ids"`
	FailedCount int      `json:"failed_count"`
	Details     []string `json:"details"`
}

// CrabDecompositionResult is the final output of the crab workflow.
type CrabDecompositionResult struct {
	Status         string       `json:"status"` // "completed", "rejected"
	PlanID         string       `json:"plan_id"`
	WhalesEmitted  []string     `json:"whales_emitted"`
	MorselsEmitted []string     `json:"morsels_emitted"`
	StepMetrics    []StepMetric `json:"step_metrics,omitempty"`
	TotalTokens    TokenUsage   `json:"total_tokens,omitempty"`
}
