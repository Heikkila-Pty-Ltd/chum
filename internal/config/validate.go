package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func validate(cfg *Config) error {
	knownRoles := map[string]struct{}{
		"scrum":    {},
		"planner":  {},
		"coder":    {},
		"reviewer": {},
		"ops":      {},
	}

	allTierNames := make([]string, 0, len(cfg.Tiers.Fast)+len(cfg.Tiers.Balanced)+len(cfg.Tiers.Premium))
	allTierNames = append(allTierNames, cfg.Tiers.Fast...)
	allTierNames = append(allTierNames, cfg.Tiers.Balanced...)
	allTierNames = append(allTierNames, cfg.Tiers.Premium...)

	for _, name := range allTierNames {
		if _, ok := cfg.Providers[name]; !ok {
			return fmt.Errorf("tier references unknown provider %q", name)
		}
	}

	hasEnabled := false
	for projectName, p := range cfg.Projects {
		if p.Enabled {
			hasEnabled = true
		}

		// Validate sprint planning configuration when provided
		if err := validateSprintPlanningConfig(projectName, p); err != nil {
			return fmt.Errorf("project %q sprint planning config: %w", projectName, err)
		}

		// Validate DoD configuration when provided
		if err := validateDoDConfig(projectName, p.DoD); err != nil {
			return fmt.Errorf("project %q DoD config: %w", projectName, err)
		}
		if err := validateRetryPolicy(fmt.Sprintf("projects.%s.retry_policy", projectName), p.RetryPolicy); err != nil {
			return fmt.Errorf("project %q retry policy: %w", projectName, err)
		}
		if err := validateProjectMergeConfig(projectName, p); err != nil {
			return fmt.Errorf("project %q merge config: %w", projectName, err)
		}
	}
	if !hasEnabled {
		return fmt.Errorf("at least one project must be enabled")
	}

	if err := validateCadenceConfig(cfg.Cadence); err != nil {
		return fmt.Errorf("cadence config: %w", err)
	}

	if err := validateRetryPolicy("general.retry_policy", cfg.General.RetryPolicy); err != nil {
		return fmt.Errorf("general retry policy: %w", err)
	}
	for tier, policy := range cfg.General.RetryTiers {
		if _, ok := map[string]struct{}{"fast": {}, "balanced": {}, "premium": {}}[tier]; !ok {
			return fmt.Errorf("general.retry_tiers.%s: unknown tier %q", tier, tier)
		}
		if err := validateRetryPolicy(fmt.Sprintf("general.retry_tiers.%s", tier), policy); err != nil {
			return fmt.Errorf("general retry tier %q: %w", tier, err)
		}
	}

	if cfg.Workflows != nil {
		if len(cfg.Workflows) == 0 {
			return fmt.Errorf("workflows section exists but defines no workflows")
		}
		for workflowName, wf := range cfg.Workflows {
			if len(wf.Stages) == 0 {
				return fmt.Errorf("workflow %q must define at least one stage", workflowName)
			}
			seenStageNames := make(map[string]struct{}, len(wf.Stages))
			for i, stage := range wf.Stages {
				if stage.Name == "" {
					return fmt.Errorf("workflow %q stage %d missing name", workflowName, i)
				}
				if stage.Role == "" {
					return fmt.Errorf("workflow %q stage %q missing role", workflowName, stage.Name)
				}
				if _, ok := seenStageNames[stage.Name]; ok {
					return fmt.Errorf("workflow %q has duplicate stage name %q", workflowName, stage.Name)
				}
				seenStageNames[stage.Name] = struct{}{}
				if _, ok := knownRoles[stage.Role]; !ok {
					return fmt.Errorf("workflow %q stage %q references unknown role %q", workflowName, stage.Name, stage.Role)
				}
			}
		}
	}

	if cfg.General.StateDB != "" {
		dir := ExpandHome(filepath.Dir(cfg.General.StateDB))
		info, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("state_db directory %q does not exist: %w", dir, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("state_db parent path %q is not a directory", dir)
		}
	}

	// Validate rate limit budget configuration
	if cfg.RateLimits.Budget != nil && len(cfg.RateLimits.Budget) > 0 {
		total := 0
		for project, percentage := range cfg.RateLimits.Budget {
			if percentage < 0 {
				return fmt.Errorf("budget for project %q cannot be negative: %d", project, percentage)
			}
			if percentage > 100 {
				return fmt.Errorf("budget for project %q cannot exceed 100%%: %d", project, percentage)
			}
			total += percentage
		}
		if total != 100 {
			return fmt.Errorf("rate limit budget percentages must sum to 100, got %d", total)
		}
	}

	// Validate API security configuration
	if cfg.API.Security.Enabled {
		if len(cfg.API.Security.AllowedTokens) == 0 {
			return fmt.Errorf("api security enabled but no allowed_tokens configured")
		}
		for i, token := range cfg.API.Security.AllowedTokens {
			if len(token) < 16 {
				return fmt.Errorf("api security token %d is too short (minimum 16 characters)", i)
			}
		}
		if cfg.API.Security.AuditLog != "" {
			dir := ExpandHome(filepath.Dir(cfg.API.Security.AuditLog))
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("cannot create audit log directory %q: %w", dir, err)
			}
		}
	}

	// Validate Chief configuration
	if cfg.Chief.Enabled {
		if cfg.Chief.MatrixRoom == "" {
			return fmt.Errorf("chief scrum master enabled but no matrix_room configured")
		}
	}

	// Validate Crab configuration
	if cfg.Crab.Enabled {
		if cfg.Crab.MaxMorselsPerPlan < 0 {
			return fmt.Errorf("crab.max_morsels_per_plan cannot be negative: %d", cfg.Crab.MaxMorselsPerPlan)
		}
		if cfg.Crab.MaxMorselsPerPlan > 50 {
			return fmt.Errorf("crab.max_morsels_per_plan seems unreasonably large: %d (max 50)", cfg.Crab.MaxMorselsPerPlan)
		}
		if cfg.Crab.MaxScopeItems < 0 {
			return fmt.Errorf("crab.max_scope_items cannot be negative: %d", cfg.Crab.MaxScopeItems)
		}
		if cfg.Crab.MaxScopeItems > 25 {
			return fmt.Errorf("crab.max_scope_items seems unreasonably large: %d (max 25)", cfg.Crab.MaxScopeItems)
		}
		validTiers := map[string]bool{"fast": true, "balanced": true, "premium": true}
		if cfg.Crab.Tier != "" && !validTiers[cfg.Crab.Tier] {
			return fmt.Errorf("crab.tier must be one of: fast, balanced, premium")
		}
	}

	// Validate Matrix polling configuration
	if cfg.Matrix.Enabled {
		if cfg.Matrix.PollInterval.Duration <= 0 {
			return fmt.Errorf("matrix.poll_interval must be > 0")
		}
		if cfg.Matrix.ReadLimit <= 0 {
			return fmt.Errorf("matrix.read_limit must be > 0")
		}
	}

	// Validate dispatch CLI configuration
	if err := ValidateDispatchConfig(cfg); err != nil {
		return fmt.Errorf("dispatch configuration: %w", err)
	}
	if err := validateDispatchCostControlConfig(cfg.Dispatch.CostControl); err != nil {
		return fmt.Errorf("dispatch cost control configuration: %w", err)
	}

	return nil
}

// GetProjectBudget returns the budget percentage allocated to a project.
// If no budget is configured or the project is not in the budget, returns 0.
func (rl *RateLimits) GetProjectBudget(project string) int {
	if rl.Budget == nil {
		return 0
	}
	return rl.Budget[project]
}

// ResolveRoom returns the Matrix room for a project.
// Priority: projects.<name>.matrix_room -> reporter.default_room -> empty string.
func (cfg *Config) ResolveRoom(project string) string {
	if cfg == nil {
		return ""
	}
	project = strings.TrimSpace(project)
	if project != "" {
		if p, ok := cfg.Projects[project]; ok {
			if room := strings.TrimSpace(p.MatrixRoom); room != "" {
				return room
			}
		}
	}
	return strings.TrimSpace(cfg.Reporter.DefaultRoom)
}

// MissingProjectRoomRouting returns enabled projects that have neither a project room
// nor a reporter-level default room configured.
func (cfg *Config) MissingProjectRoomRouting() []string {
	if cfg == nil {
		return nil
	}
	if strings.TrimSpace(cfg.Reporter.DefaultRoom) != "" {
		return nil
	}

	missing := make([]string, 0)
	for name, project := range cfg.Projects {
		if !project.Enabled {
			continue
		}
		if strings.TrimSpace(project.MatrixRoom) != "" {
			continue
		}
		missing = append(missing, name)
	}
	sort.Strings(missing)
	return missing
}

// SprintLengthDuration parses cadence sprint_length (supports "1w", "2w", and time.ParseDuration formats).
func (c Cadence) SprintLengthDuration() (time.Duration, error) {
	return parseSprintLength(c.SprintLength)
}

// StartWeekday parses cadence sprint_start_day.
func (c Cadence) StartWeekday() (time.Weekday, error) {
	return parseWeekday(c.SprintStartDay)
}

// StartClock parses cadence sprint_start_time as HH:MM.
func (c Cadence) StartClock() (int, int, error) {
	return parseClock(c.SprintStartTime)
}

// LoadLocation parses cadence timezone.
func (c Cadence) LoadLocation() (*time.Location, error) {
	tz := strings.TrimSpace(c.Timezone)
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", tz, err)
	}
	return loc, nil
}

func validateCadenceConfig(c Cadence) error {
	length, err := c.SprintLengthDuration()
	if err != nil {
		return fmt.Errorf("invalid sprint_length: %w", err)
	}
	if length < 24*time.Hour {
		return fmt.Errorf("sprint_length must be at least 24h")
	}
	if length%(24*time.Hour) != 0 {
		return fmt.Errorf("sprint_length must be an exact multiple of 24h")
	}
	if _, err := c.StartWeekday(); err != nil {
		return fmt.Errorf("invalid sprint_start_day: %w", err)
	}
	if _, _, err := c.StartClock(); err != nil {
		return fmt.Errorf("invalid sprint_start_time: %w", err)
	}
	if _, err := c.LoadLocation(); err != nil {
		return err
	}
	return nil
}

func parseSprintLength(raw string) (time.Duration, error) {
	value := strings.TrimSpace(strings.ToLower(raw))
	if value == "" {
		return 0, fmt.Errorf("empty sprint length")
	}
	if strings.HasSuffix(value, "w") {
		weeksRaw := strings.TrimSpace(strings.TrimSuffix(value, "w"))
		weeks, err := strconv.Atoi(weeksRaw)
		if err != nil || weeks <= 0 {
			return 0, fmt.Errorf("invalid week length %q", raw)
		}
		return time.Duration(weeks) * 7 * 24 * time.Hour, nil
	}
	length, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	return length, nil
}

func parseWeekday(raw string) (time.Weekday, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "monday":
		return time.Monday, nil
	case "tuesday":
		return time.Tuesday, nil
	case "wednesday":
		return time.Wednesday, nil
	case "thursday":
		return time.Thursday, nil
	case "friday":
		return time.Friday, nil
	case "saturday":
		return time.Saturday, nil
	case "sunday":
		return time.Sunday, nil
	default:
		return time.Sunday, fmt.Errorf("must be one of Monday, Tuesday, Wednesday, Thursday, Friday, Saturday, Sunday")
	}
}

func parseClock(raw string) (int, int, error) {
	value := strings.TrimSpace(raw)
	if len(value) != 5 || value[2] != ':' {
		return 0, 0, fmt.Errorf("must be in HH:MM format")
	}
	hourRaw := value[:2]
	minuteRaw := value[3:]
	hour, err := strconv.Atoi(hourRaw)
	if err != nil {
		return 0, 0, fmt.Errorf("hour must be numeric")
	}
	minute, err := strconv.Atoi(minuteRaw)
	if err != nil {
		return 0, 0, fmt.Errorf("minute must be numeric")
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("hour must be 00-23 and minute must be 00-59")
	}
	return hour, minute, nil
}

// validateSprintPlanningConfig validates sprint planning configuration for a project.
func validateSprintPlanningConfig(projectName string, project Project) error {
	// Sprint planning day validation
	if project.SprintPlanningDay != "" {
		validDays := map[string]bool{
			"Monday":    true,
			"Tuesday":   true,
			"Wednesday": true,
			"Thursday":  true,
			"Friday":    true,
			"Saturday":  true,
			"Sunday":    true,
		}
		if !validDays[project.SprintPlanningDay] {
			return fmt.Errorf("invalid sprint_planning_day %q, must be one of: Monday, Tuesday, Wednesday, Thursday, Friday, Saturday, Sunday", project.SprintPlanningDay)
		}
	}

	// Sprint planning time validation (24-hour format HH:MM)
	if project.SprintPlanningTime != "" {
		// Basic time format validation - must be HH:MM
		if len(project.SprintPlanningTime) != 5 || project.SprintPlanningTime[2] != ':' {
			return fmt.Errorf("invalid sprint_planning_time %q, must be in HH:MM format (24-hour)", project.SprintPlanningTime)
		}

		// Parse hours and minutes
		hour := project.SprintPlanningTime[:2]
		minute := project.SprintPlanningTime[3:]

		// Simple validation without importing time package parsing
		for _, c := range hour {
			if c < '0' || c > '9' {
				return fmt.Errorf("invalid sprint_planning_time %q, hour must be numeric", project.SprintPlanningTime)
			}
		}
		for _, c := range minute {
			if c < '0' || c > '9' {
				return fmt.Errorf("invalid sprint_planning_time %q, minute must be numeric", project.SprintPlanningTime)
			}
		}

		// Check valid ranges
		if hour > "23" || minute > "59" {
			return fmt.Errorf("invalid sprint_planning_time %q, hour must be 00-23 and minute must be 00-59", project.SprintPlanningTime)
		}
	}

	// Sprint capacity validation
	if project.SprintCapacity < 0 {
		return fmt.Errorf("sprint_capacity cannot be negative: %d", project.SprintCapacity)
	}
	if project.SprintCapacity > 1000 {
		return fmt.Errorf("sprint_capacity seems unreasonably large: %d (max 1000)", project.SprintCapacity)
	}

	// Backlog threshold validation
	if project.BacklogThreshold < 0 {
		return fmt.Errorf("backlog_threshold cannot be negative: %d", project.BacklogThreshold)
	}
	if project.BacklogThreshold > 500 {
		return fmt.Errorf("backlog_threshold seems unreasonably large: %d (max 500)", project.BacklogThreshold)
	}

	// Cross-field validation
	if project.SprintCapacity > 0 && project.BacklogThreshold > 0 {
		if project.BacklogThreshold < project.SprintCapacity {
			return fmt.Errorf("backlog_threshold (%d) should be at least as large as sprint_capacity (%d)", project.BacklogThreshold, project.SprintCapacity)
		}
	}

	return nil
}

// validateDoDConfig validates Definition of Done configuration for a project.
func validateDoDConfig(projectName string, dod DoDConfig) error {
	// Validate coverage_min range
	if dod.CoverageMin < 0 {
		return fmt.Errorf("coverage_min cannot be negative: %d", dod.CoverageMin)
	}
	if dod.CoverageMin > 100 {
		return fmt.Errorf("coverage_min cannot exceed 100: %d", dod.CoverageMin)
	}

	// Note: Empty checks array is valid - DoD can be coverage-only or flags-only
	// Note: All string commands in checks are valid - we can't validate arbitrary commands

	return nil
}

func validateRetryPolicy(fieldPath string, policy RetryPolicy) error {
	if policy.MaxRetries < 0 {
		return fmt.Errorf("%s.max_retries cannot be negative: %d", fieldPath, policy.MaxRetries)
	}
	if policy.InitialDelay.Duration < 0 {
		return fmt.Errorf("%s.initial_delay cannot be negative: %s", fieldPath, policy.InitialDelay)
	}
	if policy.MaxDelay.Duration < 0 {
		return fmt.Errorf("%s.max_delay cannot be negative: %s", fieldPath, policy.MaxDelay)
	}
	if policy.BackoffFactor < 0 {
		return fmt.Errorf("%s.backoff_factor cannot be negative: %f", fieldPath, policy.BackoffFactor)
	}
	if policy.EscalateAfter < 0 {
		return fmt.Errorf("%s.escalate_after cannot be negative: %d", fieldPath, policy.EscalateAfter)
	}
	return nil
}

func validateProjectMergeConfig(projectName string, project Project) error {
	method := strings.ToLower(strings.TrimSpace(project.MergeMethod))
	switch method {
	case "squash", "merge", "rebase":
		return nil
	default:
		return fmt.Errorf("invalid merge_method %q for project %q: must be one of squash, merge, rebase", method, projectName)
	}
}

func validateDispatchCostControlConfig(cc DispatchCostControl) error {
	if cc.PauseOnChurn {
		if cc.ChurnPauseWindow.Duration <= 0 {
			return fmt.Errorf("churn_pause_window must be > 0 when pause_on_churn is enabled")
		}
		if cc.ChurnPauseFailure <= 0 {
			return fmt.Errorf("churn_pause_failure_threshold must be > 0 when pause_on_churn is enabled")
		}
		if cc.ChurnPauseTotal <= 0 {
			return fmt.Errorf("churn_pause_total_threshold must be > 0 when pause_on_churn is enabled")
		}
	}
	if cc.ChurnPauseFailure < 0 {
		return fmt.Errorf("churn_pause_failure_threshold cannot be negative")
	}
	if cc.ChurnPauseTotal < 0 {
		return fmt.Errorf("churn_pause_total_threshold cannot be negative")
	}
	if cc.ChurnPauseWindow.Duration < 0 {
		return fmt.Errorf("churn_pause_window cannot be negative")
	}
	if cc.RetryEscalationAttempt < 0 {
		return fmt.Errorf("retry_escalation_attempt cannot be negative")
	}
	if cc.ComplexityEscalationMinutes < 0 {
		return fmt.Errorf("complexity_escalation_minutes cannot be negative")
	}
	if cc.ForceSparkAtWeeklyUsagePct < 0 || cc.ForceSparkAtWeeklyUsagePct > 100 {
		return fmt.Errorf("force_spark_at_weekly_usage_pct must be between 0 and 100")
	}
	if cc.DailyCostCapUSD < 0 {
		return fmt.Errorf("daily_cost_cap_usd cannot be negative")
	}
	if cc.PerBeadCostCapUSD < 0 {
		return fmt.Errorf("per_bead_cost_cap_usd cannot be negative")
	}
	if cc.PerBeadStageAttemptLimit < 0 {
		return fmt.Errorf("per_bead_stage_attempt_limit cannot be negative")
	}
	if cc.PerBeadStageAttemptLimit > 0 && cc.StageAttemptWindow.Duration <= 0 {
		return fmt.Errorf("stage_attempt_window must be > 0 when per_bead_stage_attempt_limit is set")
	}
	if cc.PerBeadStageAttemptLimit > 0 && cc.StageCooldown.Duration < 0 {
		return fmt.Errorf("stage_cooldown cannot be negative")
	}
	if cc.TokenWasteWindow.Duration < 0 {
		return fmt.Errorf("token_waste_window cannot be negative")
	}
	if cc.PauseOnTokenWastage && cc.DailyCostCapUSD <= 0 {
		return fmt.Errorf("pause_on_token_waste requires daily_cost_cap_usd > 0")
	}
	if cc.PauseOnTokenWastage && cc.TokenWasteWindow.Duration == 0 {
		return fmt.Errorf("token_waste_window must be > 0 when pause_on_token_waste is enabled")
	}
	return nil
}

// DispatchValidationIssue is a structured dispatch config validation failure.
type DispatchValidationIssue struct {
	FieldPath  string
	Message    string
	Suggestion string
}

// DispatchValidationError aggregates dispatch config validation failures.
type DispatchValidationError struct {
	Issues []DispatchValidationIssue
}

// Error formats all validation issues into a multi-line string.
func (e *DispatchValidationError) Error() string {
	if e == nil || len(e.Issues) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("dispatch validation failed")
	for _, issue := range e.Issues {
		b.WriteString("\n  - ")
		if issue.FieldPath != "" {
			b.WriteString(issue.FieldPath)
			b.WriteString(": ")
		}
		b.WriteString(issue.Message)
		if strings.TrimSpace(issue.Suggestion) != "" {
			b.WriteString(" (suggestion: ")
			b.WriteString(issue.Suggestion)
			b.WriteString(")")
		}
	}
	return b.String()
}

func (e *DispatchValidationError) add(fieldPath, message, suggestion string) {
	e.Issues = append(e.Issues, DispatchValidationIssue{
		FieldPath:  fieldPath,
		Message:    message,
		Suggestion: suggestion,
	})
}

// ValidateDispatchConfig validates the dispatch configuration at startup.
// This prevents runtime command failures due to config/CLI drift.
func ValidateDispatchConfig(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	knownBackends := map[string]struct{}{
		"headless_cli": {},
		"openclaw":     {},
	}
	cliRequiredBackends := map[string]struct{}{
		"headless_cli": {},
	}

	routing := cfg.Dispatch.Routing
	backends := map[string]string{
		"fast":     routing.FastBackend,
		"balanced": routing.BalancedBackend,
		"premium":  routing.PremiumBackend,
		"comms":    routing.CommsBackend,
		"retry":    routing.RetryBackend,
	}

	validationErr := &DispatchValidationError{}
	dispatchConfigured := len(cfg.Dispatch.CLI) > 0
	for _, backend := range backends {
		if strings.TrimSpace(backend) != "" {
			dispatchConfigured = true
			break
		}
	}
	if !dispatchConfigured {
		for _, provider := range cfg.Providers {
			if strings.TrimSpace(provider.CLI) != "" {
				dispatchConfigured = true
				break
			}
		}
	}

	// Validate backend names.
	for tier, backend := range backends {
		trimmed := strings.TrimSpace(backend)
		if trimmed == "" {
			continue
		}
		if _, ok := knownBackends[trimmed]; !ok {
			validationErr.add(
				fmt.Sprintf("dispatch.routing.%s_backend", tier),
				fmt.Sprintf("invalid backend type %q (valid: headless_cli, openclaw)", backend),
				"choose one of: headless_cli, openclaw",
			)
		}
	}

	// Validate CLI config blocks.
	for cliName, cliConfig := range cfg.Dispatch.CLI {
		if err := validateCLIConfig(cliName, cliConfig); err != nil {
			validationErr.add(
				fmt.Sprintf("dispatch.cli.%s", cliName),
				err.Error(),
				"check dispatch CLI configuration fields",
			)
		}
	}

	// Validate provider -> backend -> CLI requirements for dispatch tiers.
	tierBackends := map[string]string{
		"fast":     strings.TrimSpace(routing.FastBackend),
		"balanced": strings.TrimSpace(routing.BalancedBackend),
		"premium":  strings.TrimSpace(routing.PremiumBackend),
	}
	for providerName, provider := range cfg.Providers {
		tier := strings.TrimSpace(strings.ToLower(provider.Tier))
		backend := tierBackends[tier]
		if dispatchConfigured && tier != "" && backend == "" {
			validationErr.add(
				fmt.Sprintf("providers.%s.tier", providerName),
				fmt.Sprintf("tier %q requires dispatch.routing.%s_backend to be configured", tier, tier),
				fmt.Sprintf("set dispatch.routing.%s_backend to headless_cli or openclaw", tier),
			)
			continue
		}
		if _, needsCLI := cliRequiredBackends[backend]; !needsCLI {
			continue
		}

		cliKey, source := resolveProviderCLIKey(provider.CLI, cfg.Dispatch.CLI)
		if cliKey == "" {
			validationErr.add(
				fmt.Sprintf("providers.%s.cli", providerName),
				fmt.Sprintf("no CLI binding resolved for provider %q using %s backend", providerName, backend),
				fmt.Sprintf("set providers.%s.cli or define dispatch.cli.codex", providerName),
			)
			continue
		}

		cliCfg, ok := cfg.Dispatch.CLI[cliKey]
		if !ok {
			field := fmt.Sprintf("providers.%s.cli", providerName)
			if source == "default_cli" {
				field = "dispatch.cli"
			}
			validationErr.add(
				field,
				fmt.Sprintf("provider %q references undefined CLI config %q", providerName, cliKey),
				fmt.Sprintf("add [dispatch.cli.%s] or update providers.%s.cli", cliKey, providerName),
			)
			continue
		}

		if strings.TrimSpace(provider.Model) != "" && strings.TrimSpace(cliCfg.ModelFlag) == "" {
			validationErr.add(
				fmt.Sprintf("dispatch.cli.%s.model_flag", cliKey),
				fmt.Sprintf("model_flag is required for provider %q (model=%q)", providerName, provider.Model),
				"set model_flag (for example --model or -m)",
			)
		}
	}

	if len(validationErr.Issues) > 0 {
		return validationErr
	}
	return nil
}

// validateCLIConfig validates an individual CLI configuration.
func validateCLIConfig(name string, config CLIConfig) error {
	// Validate command is specified
	if config.Cmd == "" {
		return fmt.Errorf("cmd is required")
	}

	// Validate prompt_mode
	validPromptModes := map[string]bool{
		"stdin": true,
		"file":  true,
		"arg":   true,
	}
	if config.PromptMode != "" && !validPromptModes[config.PromptMode] {
		return fmt.Errorf("invalid prompt_mode %q (valid: stdin, file, arg)", config.PromptMode)
	}

	// Validate model_flag format if specified
	if config.ModelFlag != "" {
		if !strings.HasPrefix(config.ModelFlag, "-") {
			return fmt.Errorf("model_flag %q must start with '-' (e.g., '--model', '-m')", config.ModelFlag)
		}
	}

	// Validate approval_flags format if specified
	for i, flag := range config.ApprovalFlags {
		if !strings.HasPrefix(flag, "-") {
			return fmt.Errorf("approval_flags[%d] %q must start with '-'", i, flag)
		}
	}

	return nil
}

// resolveProviderCLIKey resolves provider -> dispatch.cli key deterministically.
// Resolution order (matching runtime defaults):
// 1) providers.<name>.cli when set
// 2) dispatch.cli.codex when present
// 3) lexicographically first dispatch.cli key
func resolveProviderCLIKey(explicitCLI string, cliConfigs map[string]CLIConfig) (key string, source string) {
	if trimmed := strings.TrimSpace(explicitCLI); trimmed != "" {
		return trimmed, "provider.cli"
	}
	if _, ok := cliConfigs["codex"]; ok {
		return "codex", "default_cli"
	}
	keys := make([]string, 0, len(cliConfigs))
	for key := range cliConfigs {
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return "", "none"
	}
	sort.Strings(keys)
	return keys[0], "default_cli"
}
