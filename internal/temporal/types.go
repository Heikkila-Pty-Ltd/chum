package temporal

import "time"

// TaskRequest is submitted via the API to start a workflow.
type TaskRequest struct {
	TaskID            string           `json:"task_id"`
	Project           string           `json:"project"`
	Prompt            string           `json:"prompt"`
	TaskTitle         string           `json:"task_title"` // human-readable task title for workflow searchability and display
	Agent             string           `json:"agent"`      // primary coding agent (keyword): claude|codex|gemini
	Model             string           `json:"model"`      // model override for the CLI (e.g. "haiku", "gemini-2.5-flash")
	Reviewer          string           `json:"reviewer"`   // review agent — auto-assigned if empty
	WorkDir           string           `json:"work_dir"`
	Provider          string           `json:"provider"`
	Priority          int              `json:"priority"`            // scheduling priority expected as an int in range [0,4], where 0 is highest
	DoDChecks         []string         `json:"dod_checks"`          // e.g. ["go build ./cmd/chum", "go test ./..."]
	SlowStepThreshold time.Duration    `json:"slow_step_threshold"` // steps exceeding this are flagged slow
	EscalationChain   []EscalationTier `json:"escalation_chain"`    // ordered tiers for fail-upward retry
	PreviousErrors    []string         `json:"previous_errors,omitempty"`
}

// EscalationTier defines one level in the fail-upward chain.
type EscalationTier struct {
	ProviderKey string `json:"provider_key"` // e.g. "codex-spark", "gemini-flash"
	CLI         string `json:"cli"`          // CLI agent name: "codex", "gemini", "claude"
	Model       string `json:"model"`        // model to pass via --model flag (empty = default)
	Tier        string `json:"tier"`         // "fast", "balanced", "premium"
	Enabled     bool   `json:"enabled"`      // false = skip this tier (gated)
}

// DefaultReviewer returns the cross-model reviewer for a given primary agent.
// V0: claude ↔ codex. If unknown, defaults to codex.
func DefaultReviewer(agent string) string {
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
	SlowStepThreshold time.Duration `json:"slow_step_threshold"` // steps exceeding this are flagged slow
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

// SemgrepScanResult is the parsed output of a semgrep scan.
type SemgrepScanResult struct {
	Passed   bool     `json:"passed"`
	Findings int      `json:"findings"`
	Errors   []string `json:"errors,omitempty"`
	Output   string   `json:"output"`
}

// TacticalGroomRequest is passed to TacticalGroomWorkflow after a task completes.
type TacticalGroomRequest struct {
	TaskID  string `json:"task_id"`
	Project string `json:"project"`
	WorkDir string `json:"work_dir"`
	Tier    string `json:"tier"` // "fast" for tactical
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
	Markdown      string          `json:"markdown"` // full rendered markdown
}

// --- Dispatcher Types ---
// DispatcherWorkflow scans for ready morsels and starts ChumAgentWorkflow
// children. Runs on a Temporal Schedule every tick_interval.

// DispatchCandidate is a ready morsel with its project context, returned by
// ScanCandidatesActivity and dispatched as a child workflow.
type DispatchCandidate struct {
	TaskID            string        `json:"task_id"`
	Title             string        `json:"title"`
	TaskTitle         string        `json:"task_title"` // same as title; explicit for consistency with TaskRequest
	Project           string        `json:"project"`
	WorkDir           string        `json:"work_dir"`
	Prompt            string        `json:"prompt"`
	Priority          int           `json:"priority"` // 0..4 scheduling/search metadata
	Provider          string        `json:"provider"`
	DoDChecks         []string      `json:"dod_checks"`
	SlowStepThreshold time.Duration `json:"slow_step_threshold"`
	EstimateMinutes   int           `json:"estimate_minutes"`
}

// ScanCandidatesResult is returned by ScanCandidatesActivity.
type ScanCandidatesResult struct {
	Candidates      []DispatchCandidate `json:"candidates"`
	Running         int                 `json:"running"` // currently running workflow count
	MaxTotal        int                 `json:"max_total"`
	Throttled       bool                `json:"throttled"`        // true if dispatch was blocked by token budget
	ThrottleReason  string              `json:"throttle_reason"`  // human-readable reason for throttling
	AvailableAgents []string            `json:"available_agents"` // enabled CLI agent names from config
	EscalationTiers []EscalationTier    `json:"escalation_tiers"` // pre-computed escalation chain from config
}

// --- Crab Decomposition Types ---
// Crabs sit between Chief/human and Sharks. They take a high-level
// markdown plan and decompose it into whales (epic-level groupings)
// and morsels (bite-sized executable units) for shark consumption.

// CrabDecompositionRequest starts a crab decomposition workflow.
type CrabDecompositionRequest struct {
	PlanID             string `json:"plan_id"`
	Project            string `json:"project"`
	WorkDir            string `json:"work_dir"`
	PlanMarkdown       string `json:"plan_markdown"`
	Tier               string `json:"tier"`                      // LLM tier: "fast", "balanced", or "premium"
	ParentWhaleID      string `json:"parent_whale_id,omitempty"` // optional parent whale to nest under
	RequireHumanReview bool   `json:"require_human_review"`      // if true, block at Phase 6 for human signal; default: auto-approve
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
	ParentScopeItem    int               `json:"parent_scope_item"` // index into ParsedPlan.ScopeItems
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
