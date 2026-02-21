package matrix

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/dispatch"
	"github.com/antigravity-dev/chum/internal/graph"
	"github.com/antigravity-dev/chum/internal/store"
)

const (
	defaultPollInterval = 30 * time.Second
	defaultWorkDir      = "/tmp"
	defaultThinking     = "none"
	statusRecentWindow  = 24 * time.Hour
)

var createTaskPattern = regexp.MustCompile(`(?i)^create\s+task\s+"([^"]+)"\s+"([^"]+)"\s*$`)

type commandStore interface {
	GetRunningDispatches() ([]store.Dispatch, error)
	GetCompletedDispatchesSince(projectName, since string) ([]store.Dispatch, error)
}

type commandCanceler interface {
	CancelDispatch(id int64) error
}

type scrumCommandKind int

const (
	scrumCommandStatus scrumCommandKind = iota + 1
	scrumCommandPriority
	scrumCommandCancel
	scrumCommandCreate
)

type scrumCommand struct {
	kind        scrumCommandKind
	beadID      string
	priority    int
	dispatchID  int64
	title       string
	description string
}

// InboundMessage is a normalized inbound Matrix message.
type InboundMessage struct {
	ID        string
	Project   string
	Room      string
	Sender    string
	Body      string
	Timestamp time.Time
}

// Client reads inbound messages for a Matrix room.
type Client interface {
	ReadMessages(ctx context.Context, roomID string, after string) ([]InboundMessage, string, error)
}

// PollerConfig controls inbound polling and routing behavior.
type PollerConfig struct {
	Enabled       bool
	PollInterval  time.Duration
	BotUser       string
	RoomToProject map[string]string

	Projects       map[string]config.Project
	Sender         Sender
	Store          commandStore
	Canceler       commandCanceler
	CommandSenders []string
	DAG            *graph.DAG
}

// Poller polls Matrix rooms and routes inbound messages to project scrum agents.
type Poller struct {
	cfg        PollerConfig
	client     Client
	dispatcher dispatch.DispatcherInterface
	dag        *graph.DAG
	logger     *slog.Logger

	projects       map[string]config.Project
	sender         Sender
	store          commandStore
	canceler       commandCanceler
	commandSenders map[string]struct{}

	mu      sync.Mutex
	cursors map[string]string // room -> last cursor/message id
}

// NewPoller constructs a Matrix poller.
func NewPoller(cfg PollerConfig, client Client, dispatcher dispatch.DispatcherInterface, logger *slog.Logger) *Poller {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
	}
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.RoomToProject == nil {
		cfg.RoomToProject = make(map[string]string)
	}
	if cfg.Projects == nil {
		cfg.Projects = make(map[string]config.Project)
	}
	return &Poller{
		cfg:            cfg,
		client:         client,
		dispatcher:     dispatcher,
		dag:            cfg.DAG,
		logger:         logger,
		projects:       cloneProjects(cfg.Projects),
		sender:         cfg.Sender,
		store:          cfg.Store,
		canceler:       cfg.Canceler,
		commandSenders: normalizeCommandSenders(cfg.CommandSenders),
		cursors:        make(map[string]string),
	}
}

func cloneProjects(src map[string]config.Project) map[string]config.Project {
	if len(src) == 0 {
		return make(map[string]config.Project)
	}

	dst := make(map[string]config.Project, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func normalizeCommandSenders(raw []string) map[string]struct{} {
	if len(raw) == 0 {
		return nil
	}

	allowed := make(map[string]struct{}, len(raw))
	for _, rawSender := range raw {
		sender := strings.TrimSpace(strings.ToLower(rawSender))
		if sender == "" {
			continue
		}
		allowed[sender] = struct{}{}
	}

	if len(allowed) == 0 {
		return nil
	}
	return allowed
}

// BuildRoomProjectMap builds a room->project map from enabled projects and room config.
func BuildRoomProjectMap(cfg *config.Config) map[string]string {
	out := make(map[string]string)
	if cfg == nil {
		return out
	}

	names := make([]string, 0, len(cfg.Projects))
	for name, project := range cfg.Projects {
		if project.Enabled {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	for _, name := range names {
		room := strings.TrimSpace(cfg.ResolveRoom(name))
		if room == "" {
			continue
		}
		if _, exists := out[room]; exists {
			continue
		}
		out[room] = name
	}
	return out
}

// Run starts periodic polling until context cancellation.
func (p *Poller) Run(ctx context.Context) {
	if !p.cfg.Enabled {
		p.logger.Info("matrix poller disabled")
		return
	}
	if p.client == nil {
		p.logger.Error("matrix poller disabled: client is nil")
		return
	}
	if p.dispatcher == nil {
		p.logger.Error("matrix poller disabled: dispatcher is nil")
		return
	}

	p.logger.Info("matrix poller started",
		"poll_interval", p.cfg.PollInterval.String(),
		"rooms", len(p.cfg.RoomToProject))

	_ = p.PollOnce(ctx)
	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			p.logger.Info("matrix poller stopped")
			return
		case <-ticker.C:
			_ = p.PollOnce(ctx)
		}
	}
}

// PollOnce executes one polling cycle.
func (p *Poller) PollOnce(ctx context.Context) error {
	if !p.cfg.Enabled || p.client == nil || p.dispatcher == nil {
		return nil
	}

	rooms := make([]string, 0, len(p.cfg.RoomToProject))
	for room := range p.cfg.RoomToProject {
		rooms = append(rooms, room)
	}
	sort.Strings(rooms)

	for _, room := range rooms {
		project := strings.TrimSpace(p.cfg.RoomToProject[room])
		if project == "" {
			p.logger.Warn("matrix room has no project mapping", "room", room)
			continue
		}

		after := p.cursor(room)
		messages, nextCursor, err := p.client.ReadMessages(ctx, room, after)
		if err != nil {
			p.logger.Warn("matrix poll failed", "room", room, "project", project, "error", err)
			continue
		}
		if nextCursor != "" {
			p.setCursor(room, nextCursor)
		}

		for _, msg := range messages {
			if p.isOwnMessage(msg.Sender) {
				continue
			}
			if strings.TrimSpace(msg.Room) == "" {
				msg.Room = room
			}
			msg.Project = project
			if err := p.routeMessage(ctx, msg); err != nil {
				p.logger.Error("failed routing matrix message",
					"project", project,
					"room", msg.Room,
					"sender", msg.Sender,
					"message_id", msg.ID,
					"error", err)
			}
			if msg.ID != "" {
				p.setCursor(room, msg.ID)
			}
		}
	}
	return nil
}

func (p *Poller) routeMessage(ctx context.Context, msg InboundMessage) error {
	command, isCommand, parseErr := parseScrumCommand(msg.Body)
	if isCommand {
		if err := p.handleScrumCommand(ctx, msg, command, parseErr); err != nil {
			return err
		}
		return nil
	}

	agent := fmt.Sprintf("%s-scrum", strings.TrimSpace(msg.Project))
	if agent == "-scrum" {
		agent = "main"
	}

	prompt := fmt.Sprintf(`# Matrix Inbound Message

Project: %s
Room: %s
Sender: %s
Timestamp: %s
Message ID: %s

Message:
%s

You are the project scrum agent. Reply with a concise acknowledgement and the next action for this project.`,
		msg.Project,
		msg.Room,
		msg.Sender,
		msg.Timestamp.UTC().Format(time.RFC3339),
		msg.ID,
		msg.Body,
	)

	p.logger.Info("routing matrix message",
		"project", msg.Project,
		"room", msg.Room,
		"sender", msg.Sender,
		"message_id", msg.ID,
		"agent", agent)

	_, err := p.dispatcher.Dispatch(ctx, agent, prompt, "", defaultThinking, defaultWorkDir)
	if err != nil && agent != "main" {
		p.logger.Warn("matrix routing fallback to main agent", "project", msg.Project, "agent", agent, "error", err)
		_, fallbackErr := p.dispatcher.Dispatch(ctx, "main", prompt, "", defaultThinking, defaultWorkDir)
		if fallbackErr != nil {
			return fmt.Errorf("dispatch fallback failed: %w", fallbackErr)
		}
		return nil
	}
	return err
}

func (p *Poller) isOwnMessage(sender string) bool {
	bot := strings.TrimSpace(p.cfg.BotUser)
	if bot == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(sender), bot)
}

func (p *Poller) sendScrumResponse(ctx context.Context, msg InboundMessage, response string) error {
	response = strings.TrimSpace(response)
	if response == "" {
		response = "No response available."
	}
	if p.sender == nil {
		return errors.New("matrix sender is not configured for command responses")
	}
	if strings.TrimSpace(msg.Room) == "" {
		return fmt.Errorf("missing Matrix room for response")
	}
	return p.sender.SendMessage(ctx, msg.Room, response)
}

func (p *Poller) isAllowedCommandSender(sender string) bool {
	if len(p.commandSenders) == 0 {
		return true
	}
	_, ok := p.commandSenders[strings.ToLower(strings.TrimSpace(sender))]
	return ok
}

func (p *Poller) handleScrumCommand(ctx context.Context, msg InboundMessage, cmd scrumCommand, parseErr error) error {
	if !p.isAllowedCommandSender(msg.Sender) {
		return p.sendScrumResponse(ctx, msg, commandPermissionDeniedMessage())
	}

	if parseErr != nil {
		return p.sendScrumResponse(ctx, msg, formatCommandError(parseErr))
	}

	response, err := p.runScrumCommand(ctx, msg, cmd)
	if err != nil {
		return p.sendScrumResponse(ctx, msg, fmt.Sprintf("Command failed: %s", err.Error()))
	}

	return p.sendScrumResponse(ctx, msg, response)
}

func (p *Poller) runScrumCommand(ctx context.Context, msg InboundMessage, cmd scrumCommand) (string, error) {
	switch cmd.kind {
	case scrumCommandStatus:
		return p.handleStatusCommand(msg.Project)
	case scrumCommandPriority:
		return p.handlePriorityCommand(ctx, msg.Project, cmd.beadID, cmd.priority)
	case scrumCommandCancel:
		return p.handleCancelCommand(ctx, cmd.dispatchID)
	case scrumCommandCreate:
		return p.handleCreateCommand(msg.Project, cmd.title, cmd.description)
	default:
		return "", fmt.Errorf("unsupported command type")
	}
}

func (p *Poller) projectConfig(project string) (config.Project, bool) {
	project = strings.TrimSpace(project)
	if project == "" {
		return config.Project{}, false
	}
	cfg, ok := p.projects[project]
	return cfg, ok
}

func (p *Poller) handleStatusCommand(project string) (string, error) {
	if p.store == nil {
		return "", fmt.Errorf("status unavailable: command store is not configured")
	}

	running, err := p.store.GetRunningDispatches()
	if err != nil {
		return "", fmt.Errorf("retrieving running dispatches: %w", err)
	}
	recent, err := p.store.GetCompletedDispatchesSince(project, time.Now().UTC().Add(-statusRecentWindow).Format(time.DateTime))
	if err != nil {
		return "", fmt.Errorf("retrieving recent completions: %w", err)
	}

	var runningBeads []string
	for _, d := range running {
		if strings.TrimSpace(d.Project) != strings.TrimSpace(project) {
			continue
		}
		if strings.TrimSpace(d.BeadID) != "" {
			runningBeads = append(runningBeads, d.BeadID)
		}
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Project: %s", strings.TrimSpace(project)))
	lines = append(lines, fmt.Sprintf("Running beads: %d", len(runningBeads)))
	if len(runningBeads) > 0 {
		if len(runningBeads) > 5 {
			runningBeads = runningBeads[:5]
		}
		lines = append(lines, fmt.Sprintf("Running IDs: %s", strings.Join(runningBeads, ", ")))
	} else {
		lines = append(lines, "Running IDs: none")
	}

	lines = append(lines, fmt.Sprintf("Completed in last 24h: %d", len(recent)))
	if len(recent) == 0 {
		lines = append(lines, "Recent completions: none")
		return strings.Join(lines, "\n"), nil
	}

	limit := len(recent)
	if limit > 5 {
		limit = 5
	}
	for i := 0; i < limit; i++ {
		d := recent[i]
		timeStr := d.DispatchedAt.Format(time.RFC3339)
		if timeStr == "0001-01-01T00:00:00Z" {
			timeStr = "unknown"
		}
		lines = append(lines, fmt.Sprintf("- %s (%s)", d.BeadID, timeStr))
	}
	if len(recent) > limit {
		lines = append(lines, "- ...")
	}

	return strings.Join(lines, "\n"), nil
}

func (p *Poller) handlePriorityCommand(ctx context.Context, project, beadID string, priority int) (string, error) {
	if strings.TrimSpace(project) == "" {
		return "", fmt.Errorf("missing project")
	}
	if _, ok := p.projectConfig(project); !ok {
		return "", fmt.Errorf("unknown project %q", project)
	}
	if p.dag == nil {
		return "", fmt.Errorf("DAG not configured")
	}

	if err := p.dag.UpdateTask(ctx, beadID, map[string]any{"priority": priority}); err != nil {
		return "", err
	}
	return fmt.Sprintf("Updated %s priority to p%d", beadID, priority), nil
}

func (p *Poller) handleCancelCommand(_ context.Context, dispatchID int64) (string, error) {
	if p.canceler == nil {
		return "", fmt.Errorf("cancel unavailable: command dispatcher is not configured")
	}
	if err := p.canceler.CancelDispatch(dispatchID); err != nil {
		return "", err
	}
	return fmt.Sprintf("Cancelled dispatch %d", dispatchID), nil
}

func (p *Poller) handleCreateCommand(project, title, description string) (string, error) {
	if _, ok := p.projectConfig(project); !ok {
		return "", fmt.Errorf("unknown project %q", project)
	}
	if p.dag == nil {
		return "", fmt.Errorf("DAG not configured")
	}

	task := graph.Task{
		Title:       title,
		Type:        "task",
		Priority:    2,
		Description: description,
		Project:     project,
		Status:      "open",
	}
	id, err := p.dag.CreateTask(context.Background(), task)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("Created new task %s", id), nil
}

func parseScrumCommand(raw string) (scrumCommand, bool, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return scrumCommand{}, false, nil
	}

	parts := strings.Fields(text)
	if len(parts) == 0 {
		return scrumCommand{}, false, nil
	}

	keyword := strings.ToLower(parts[0])
	switch keyword {
	case "status":
		if len(parts) != 1 {
			return scrumCommand{}, true, errors.New("status takes no arguments. Usage: status")
		}
		return scrumCommand{kind: scrumCommandStatus}, true, nil
	case "priority":
		if len(parts) != 3 {
			return scrumCommand{}, true, errors.New("malformed priority command. Usage: priority <bead-id> <p0|p1|p2|p3|p4>")
		}
		beadID := strings.TrimSpace(parts[1])
		if beadID == "" {
			return scrumCommand{}, true, errors.New("priority command requires a bead id. Usage: priority <bead-id> <p0|p1|p2|p3|p4>")
		}
		priorityToken := strings.ToLower(strings.TrimSpace(parts[2]))
		if !strings.HasPrefix(priorityToken, "p") || len(priorityToken) != 2 {
			return scrumCommand{}, true, errors.New("priority must be p0, p1, p2, p3, or p4. Usage: priority <bead-id> <p0|p1|p2|p3|p4>")
		}
		priority, err := strconv.Atoi(priorityToken[1:])
		if err != nil || priority < 0 || priority > 4 {
			return scrumCommand{}, true, errors.New("priority must be p0, p1, p2, p3, or p4. Usage: priority <bead-id> <p0|p1|p2|p3|p4>")
		}
		return scrumCommand{kind: scrumCommandPriority, beadID: beadID, priority: priority}, true, nil
	case "cancel":
		if len(parts) != 2 {
			return scrumCommand{}, true, errors.New("malformed cancel command. Usage: cancel <dispatch-id>")
		}
		dispatchID, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil || dispatchID <= 0 {
			return scrumCommand{}, true, errors.New("cancel command requires a positive dispatch id. Usage: cancel <dispatch-id>")
		}
		return scrumCommand{kind: scrumCommandCancel, dispatchID: dispatchID}, true, nil
	case "create":
		matches := createTaskPattern.FindStringSubmatch(text)
		if len(matches) != 3 {
			return scrumCommand{}, true, errors.New(`create task command requires quoted title and description. Usage: create task "<title>" "<description>"`)
		}
		return scrumCommand{kind: scrumCommandCreate, title: matches[1], description: matches[2]}, true, nil
	default:
		return scrumCommand{}, false, nil
	}
}

func formatCommandError(err error) string {
	if err == nil {
		return commandUsageMessage()
	}
	return fmt.Sprintf("Malformed command: %s\n\n%s", err.Error(), commandUsageMessage())
}

func commandPermissionDeniedMessage() string {
	return "You do not have permission to run scrum commands from this Matrix user in this project.\n\n" + commandUsageMessage()
}

func commandUsageMessage() string {
	return `Supported commands:
- status
- priority <bead-id> <p0|p1|p2|p3|p4>
- cancel <dispatch-id>
- create task "<title>" "<description>"

Usage:
- status
  - Shows a brief project summary and recent completions.
- priority <bead-id> <p0|p1|p2|p3|p4>
  - Reprioritizes an existing bead.
- cancel <dispatch-id>
  - Cancels a running dispatch by id.
- create task "<title>" "<description>"
  - Creates a new task bead with the provided title and description.`
}

func (p *Poller) cursor(room string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cursors[room]
}

func (p *Poller) setCursor(room, cursor string) {
	if strings.TrimSpace(cursor) == "" {
		return
	}
	p.mu.Lock()
	p.cursors[room] = cursor
	p.mu.Unlock()
}
