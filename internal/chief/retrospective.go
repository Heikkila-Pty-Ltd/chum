package chief

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/dispatch"
	"github.com/antigravity-dev/chum/internal/graph"
	"github.com/antigravity-dev/chum/internal/store"
)

type retrospectiveActionItem struct {
	Title       string
	ProjectName string
	Owner       string
	Why         string
	Priority    int
}

// RetrospectiveRecorder handles post-processing for overall retrospective ceremonies.
type RetrospectiveRecorder struct {
	cfg        *config.Config
	store      *store.Store
	dag        *graph.DAG
	dispatcher dispatch.DispatcherInterface
	logger     *slog.Logger
}

// NewRetrospectiveRecorder creates a recorder for overall retrospective outcomes.
func NewRetrospectiveRecorder(cfg *config.Config, store *store.Store, dag *graph.DAG, dispatcher dispatch.DispatcherInterface, logger *slog.Logger) *RetrospectiveRecorder {
	return &RetrospectiveRecorder{
		cfg:        cfg,
		store:      store,
		dag:        dag,
		dispatcher: dispatcher,
		logger:     logger,
	}
}

// RecordRetrospectiveResults sends coordination summary and creates follow-up morsels from action items.
func (rr *RetrospectiveRecorder) RecordRetrospectiveResults(ctx context.Context, ceremonyID, output string) error {
	if rr == nil {
		return fmt.Errorf("retrospective recorder is nil")
	}
	if strings.TrimSpace(output) == "" {
		return fmt.Errorf("retrospective output is empty")
	}

	var errs []error

	if err := rr.sendRetrospectiveSummaryToMatrix(ctx, ceremonyID, output); err != nil {
		rr.logger.Warn("failed to send retrospective summary to matrix", "error", err, "ceremony_id", ceremonyID)
		errs = append(errs, err)
	}

	actionItems := parseRetrospectiveActionItems(output)
	created := 0
	for _, item := range actionItems {
		issueID, err := rr.createActionItemMorsel(ctx, ceremonyID, item)
		if err != nil {
			rr.logger.Warn("failed to create follow-up retrospective morsel",
				"ceremony_id", ceremonyID,
				"title", item.Title,
				"project", item.ProjectName,
				"error", err)
			errs = append(errs, err)
			continue
		}
		created++
		rr.logger.Info("created retrospective follow-up morsel",
			"ceremony_id", ceremonyID,
			"issue_id", issueID,
			"project", item.ProjectName,
			"title", item.Title)
	}

	if rr.store != nil {
		details := fmt.Sprintf("overall retrospective %s processed: action_items=%d followup_morsels_created=%d", ceremonyID, len(actionItems), created)
		if err := rr.store.RecordHealthEventWithDispatch("overall_retrospective_processed", details, 0, ceremonyID); err != nil {
			rr.logger.Warn("failed to record overall retrospective health event", "error", err, "ceremony_id", ceremonyID)
		}
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (rr *RetrospectiveRecorder) sendRetrospectiveSummaryToMatrix(ctx context.Context, ceremonyID, output string) error {
	if rr.cfg == nil {
		return fmt.Errorf("retrospective config is nil")
	}
	room := strings.TrimSpace(rr.cfg.Chief.MatrixRoom)
	if room == "" {
		return nil
	}
	if rr.dispatcher == nil {
		return fmt.Errorf("dispatcher is not configured")
	}

	agentID := strings.TrimSpace(rr.cfg.Chief.AgentID)
	if agentID == "" {
		agentID = "chum-chief"
	}

	provider := ""
	if len(rr.cfg.Tiers.Fast) > 0 {
		provider = rr.cfg.Tiers.Fast[0]
	} else if len(rr.cfg.Tiers.Balanced) > 0 {
		provider = rr.cfg.Tiers.Balanced[0]
	}
	if provider == "" {
		return fmt.Errorf("no provider configured for matrix summary dispatch")
	}

	workspace := "/tmp"
	if project, ok := rr.cfg.Projects["chum"]; ok {
		if resolved := strings.TrimSpace(config.ExpandHome(project.Workspace)); resolved != "" {
			workspace = resolved
		}
	}

	prompt := buildRetrospectiveMatrixPrompt(room, ceremonyID, output)
	if _, err := rr.dispatcher.Dispatch(ctx, agentID, prompt, provider, "none", workspace); err != nil {
		return fmt.Errorf("dispatch retrospective matrix summary: %w", err)
	}
	return nil
}

func (rr *RetrospectiveRecorder) createActionItemMorsel(ctx context.Context, ceremonyID string, item retrospectiveActionItem) (string, error) {
	if rr.cfg == nil {
		return "", fmt.Errorf("retrospective config is nil")
	}
	if rr.dag == nil {
		return "", fmt.Errorf("retrospective DAG is nil")
	}
	projectName := strings.TrimSpace(item.ProjectName)
	if projectName == "" {
		projectName = "chum"
	}
	if _, ok := rr.cfg.Projects[projectName]; !ok {
		projectName, _ = rr.defaultProject()
	}

	title := strings.TrimSpace(item.Title)
	if title == "" {
		return "", fmt.Errorf("empty retrospective action item title")
	}

	description := fmt.Sprintf(
		"Auto-created from Chief SM overall retrospective `%s` on %s.\n\nAction item: %s\nOwner: %s\nReason: %s",
		ceremonyID,
		time.Now().UTC().Format(time.RFC3339),
		title,
		emptyFallback(item.Owner, "unassigned"),
		emptyFallback(item.Why, "not specified"),
	)

	task := graph.Task{
		Title:       title,
		Type:        "task",
		Priority:    normalizePriority(item.Priority),
		Description: description,
		Project:     projectName,
		Status:      "open",
	}
	id, err := rr.dag.CreateTask(ctx, task)
	if err != nil {
		return "", fmt.Errorf("create retrospective action item: %w", err)
	}
	return id, nil
}

func (rr *RetrospectiveRecorder) defaultProject() (string, config.Project) {
	if project, ok := rr.cfg.Projects["chum"]; ok {
		return "chum", project
	}
	for name := range rr.cfg.Projects {
		if rr.cfg.Projects[name].Enabled {
			return name, rr.cfg.Projects[name]
		}
	}
	for name := range rr.cfg.Projects {
		return name, rr.cfg.Projects[name]
	}
	return "chum", config.Project{}
}

func parseRetrospectiveActionItems(output string) []retrospectiveActionItem {
	lines := strings.Split(output, "\n")
	items := make([]retrospectiveActionItem, 0)

	inActionSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "action items") {
			inActionSection = true
			continue
		}
		if inActionSection && strings.HasPrefix(trimmed, "#") {
			break
		}
		if !inActionSection {
			continue
		}
		item, ok := parseActionItemLine(trimmed)
		if ok {
			items = append(items, item)
		}
	}

	return items
}

func parseActionItemLine(line string) (retrospectiveActionItem, bool) {
	bulletRE := regexp.MustCompile(`^\s*[-*]\s*(?:\[[ xX]\]\s*)?(.*)$`)
	matches := bulletRE.FindStringSubmatch(line)
	if len(matches) != 2 {
		return retrospectiveActionItem{}, false
	}

	raw := strings.TrimSpace(matches[1])
	if raw == "" {
		return retrospectiveActionItem{}, false
	}

	item := retrospectiveActionItem{Priority: 2, ProjectName: "chum"}

	if pri, remainder, ok := consumePriorityPrefix(raw); ok {
		item.Priority = pri
		raw = remainder
	}

	parts := strings.Split(raw, "|")
	item.Title = strings.TrimSpace(parts[0])
	for _, part := range parts[1:] {
		field := strings.TrimSpace(part)
		if field == "" {
			continue
		}
		key, value, ok := splitKeyValue(field)
		if !ok {
			continue
		}
		switch strings.ToLower(key) {
		case "project":
			item.ProjectName = strings.TrimSpace(value)
		case "owner":
			item.Owner = strings.TrimSpace(value)
		case "why", "reason":
			item.Why = strings.TrimSpace(value)
		case "priority":
			if p, ok := parsePriorityValue(value); ok {
				item.Priority = p
			}
		}
	}

	if strings.TrimSpace(item.Title) == "" {
		return retrospectiveActionItem{}, false
	}
	return item, true
}

func consumePriorityPrefix(raw string) (priority int, remainder string, ok bool) {
	priorityRE := regexp.MustCompile(`^\[(P[0-4])\]\s*(.*)$`)
	matches := priorityRE.FindStringSubmatch(strings.TrimSpace(raw))
	if len(matches) != 3 {
		return 0, raw, false
	}
	p, ok := parsePriorityValue(matches[1])
	if !ok {
		return 0, raw, false
	}
	return p, strings.TrimSpace(matches[2]), true
}

func splitKeyValue(field string) (key, value string, ok bool) {
	if idx := strings.Index(field, ":"); idx > 0 {
		return strings.TrimSpace(field[:idx]), strings.TrimSpace(field[idx+1:]), true
	}
	if idx := strings.Index(field, "="); idx > 0 {
		return strings.TrimSpace(field[:idx]), strings.TrimSpace(field[idx+1:]), true
	}
	return "", "", false
}

func parsePriorityValue(raw string) (int, bool) {
	trimmed := strings.TrimSpace(strings.ToUpper(raw))
	if strings.HasPrefix(trimmed, "P") && len(trimmed) == 2 {
		switch trimmed[1] {
		case '0':
			return 0, true
		case '1':
			return 1, true
		case '2':
			return 2, true
		case '3':
			return 3, true
		case '4':
			return 4, true
		}
	}
	return 0, false
}

func normalizePriority(priority int) int {
	if priority < 0 {
		return 0
	}
	if priority > 4 {
		return 4
	}
	return priority
}

func buildRetrospectiveMatrixPrompt(room, ceremonyID, output string) string {
	trimmedOutput := strings.TrimSpace(output)
	if len(trimmedOutput) > 4000 {
		trimmedOutput = trimmedOutput[:4000] + "\n\n[truncated]"
	}

	return fmt.Sprintf(`# Matrix Room Coordination Message

Send a concise retrospective coordination update to Matrix room %s.

Include:
1. Key cross-project retrospective takeaways from ceremony %s.
2. Systemic risks and wins.
3. Action item highlights.

Source retrospective output:

---
%s
---
`, strings.TrimSpace(room), strings.TrimSpace(ceremonyID), trimmedOutput)
}

func emptyFallback(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
