package matrix

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// PlanningStartRequest defines the minimal payload needed to start a planning session.
type PlanningStartRequest struct {
	Project       string
	WorkDir       string
	Agent         string
	Tier          string
	CandidateTopK int
	Source        string
}

// PlanningSession describes one started planning workflow.
type PlanningSession struct {
	SessionID string
	RunID     string
	Status    string
}

// PlanningPrompt is the next actionable prompt for a planning session.
type PlanningPrompt struct {
	SessionID         string
	Status            string
	Phase             string
	ExpectedSignal    string
	Prompt            string
	Options           []string
	Recommendation    string
	Context           string
	Cycle             int
	SelectedItemID    string
	SelectedItemTitle string
}

// PlanningStatus captures coarse workflow state for user-facing control loops.
type PlanningStatus struct {
	SessionID string
	RunID     string
	Status    string
	Note      string
}

// PlanningController defines planning lifecycle operations used by chat control bridges.
type PlanningController interface {
	StartPlanningSession(ctx context.Context, req PlanningStartRequest) (PlanningSession, error)
	SubmitPlanningSignal(ctx context.Context, sessionID, signal, value, source string) error
	GetPlanningPrompt(ctx context.Context, sessionID, source string) (PlanningPrompt, error)
	GetPlanningStatus(ctx context.Context, sessionID string) (PlanningStatus, error)
	StopPlanningSession(ctx context.Context, sessionID, reason, source string) error
}

type planningCommandKind int

const (
	planningCommandHelp planningCommandKind = iota + 1
	planningCommandStart
	planningCommandPrompt
	planningCommandStatus
	planningCommandSelect
	planningCommandAnswer
	planningCommandGo
	planningCommandRealign
	planningCommandStop
)

type planningCommand struct {
	kind          planningCommandKind
	project       string
	workDir       string
	agent         string
	tier          string
	candidateTopK int
	sessionID     string
	value         string
	reason        string
}

func (h *TurtleChatHandler) handlePlanningCommand(ctx context.Context, msg InboundMessage) (bool, error) {
	cmd, matched, parseErr := parsePlanningCommand(msg.Body)
	if !matched {
		return false, nil
	}
	if h.Planning == nil {
		return true, h.sendControlMessage(ctx, msg.Room, "Planning control is not configured for this CHUM runtime.")
	}
	if parseErr != nil {
		return true, h.sendControlMessage(ctx, msg.Room, fmt.Sprintf("Malformed planning command: %s\n\n%s", parseErr.Error(), planningCommandUsage()))
	}

	response, bridgeUpdate, execErr := h.executePlanningCommand(ctx, msg, cmd)
	if execErr != nil {
		response = "Planning command failed: " + execErr.Error()
	}
	if strings.TrimSpace(response) != "" {
		if err := h.sendControlMessage(ctx, msg.Room, response); err != nil {
			return true, err
		}
	}
	if strings.TrimSpace(bridgeUpdate) != "" {
		if err := h.sendBridgeMessage(ctx, msg.Room, bridgeUpdate); err != nil && h.Logger != nil {
			h.Logger.Warn(TurtleBotPrefix+" planning bridge update failed", "error", err)
		}
	}
	return true, nil
}

func (h *TurtleChatHandler) executePlanningCommand(ctx context.Context, msg InboundMessage, cmd planningCommand) (string, string, error) {
	switch cmd.kind {
	case planningCommandHelp:
		return planningCommandUsage(), "", nil
	case planningCommandStart:
		project := strings.TrimSpace(cmd.project)
		if project == "" {
			return "", "", fmt.Errorf("project is required. Usage: /plan start <project> [workdir] [agent=claude] [tier=fast] [topk=5]")
		}
		workDir := strings.TrimSpace(cmd.workDir)
		if workDir == "" {
			workDir = strings.TrimSpace(h.WorkDir)
		}
		if workDir == "" {
			return "", "", fmt.Errorf("workdir is required. Provide it in /plan start or configure turtle handler workdir")
		}

		session, err := h.Planning.StartPlanningSession(ctx, PlanningStartRequest{
			Project:       project,
			WorkDir:       workDir,
			Agent:         strings.TrimSpace(cmd.agent),
			Tier:          strings.TrimSpace(cmd.tier),
			CandidateTopK: cmd.candidateTopK,
			Source:        "matrix-control",
		})
		if err != nil {
			return "", "", err
		}
		h.setActivePlanningSession(msg.Room, session.SessionID)

		prompt, promptErr := h.Planning.GetPlanningPrompt(ctx, session.SessionID, "matrix-control")
		response := fmt.Sprintf("Started planning session `%s` (status: %s).", session.SessionID, strings.TrimSpace(session.Status))
		if promptErr == nil {
			response += "\n\n" + formatPlanningPromptForChat(prompt)
		}
		bridge := fmt.Sprintf("[planning-control] started session=%s project=%s room=%s", session.SessionID, project, msg.Room)
		return response, bridge, nil
	case planningCommandPrompt:
		sessionID, err := h.resolvePlanningSession(msg.Room, cmd.sessionID)
		if err != nil {
			return "", "", err
		}
		prompt, err := h.Planning.GetPlanningPrompt(ctx, sessionID, "matrix-control")
		if err != nil {
			return "", "", err
		}
		return formatPlanningPromptForChat(prompt), "", nil
	case planningCommandStatus:
		sessionID, err := h.resolvePlanningSession(msg.Room, cmd.sessionID)
		if err != nil {
			return "", "", err
		}
		status, err := h.Planning.GetPlanningStatus(ctx, sessionID)
		if err != nil {
			return "", "", err
		}
		resp := fmt.Sprintf("Session `%s` status: %s", status.SessionID, status.Status)
		if strings.TrimSpace(status.Note) != "" {
			resp += "\n" + strings.TrimSpace(status.Note)
		}
		return resp, "", nil
	case planningCommandSelect:
		sessionID, err := h.resolvePlanningSession(msg.Room, cmd.sessionID)
		if err != nil {
			return "", "", err
		}
		if strings.TrimSpace(cmd.value) == "" {
			return "", "", fmt.Errorf("item id is required. Usage: /plan select <item-id> [session]")
		}
		if err := h.Planning.SubmitPlanningSignal(ctx, sessionID, "item-selected", cmd.value, "matrix-control"); err != nil {
			return "", "", err
		}
		prompt, err := h.Planning.GetPlanningPrompt(ctx, sessionID, "matrix-control")
		if err != nil {
			return fmt.Sprintf("Submitted selection `%s` for `%s`.", cmd.value, sessionID), "", nil
		}
		return formatPlanningPromptForChat(prompt), "", nil
	case planningCommandAnswer:
		sessionID, err := h.resolvePlanningSession(msg.Room, cmd.sessionID)
		if err != nil {
			return "", "", err
		}
		answer := strings.TrimSpace(cmd.value)
		if answer == "" {
			return "", "", fmt.Errorf("answer text is required. Usage: /plan answer [session] <text>")
		}
		if err := h.Planning.SubmitPlanningSignal(ctx, sessionID, "answer", answer, "matrix-control"); err != nil {
			return "", "", err
		}
		prompt, err := h.Planning.GetPlanningPrompt(ctx, sessionID, "matrix-control")
		if err != nil {
			return "Answer submitted.", "", nil
		}
		return formatPlanningPromptForChat(prompt), "", nil
	case planningCommandGo:
		sessionID, err := h.resolvePlanningSession(msg.Room, cmd.sessionID)
		if err != nil {
			return "", "", err
		}
		if err := h.Planning.SubmitPlanningSignal(ctx, sessionID, "greenlight", "GO", "matrix-control"); err != nil {
			return "", "", err
		}
		status, err := h.Planning.GetPlanningStatus(ctx, sessionID)
		if err != nil {
			return fmt.Sprintf("Greenlight GO submitted for `%s`.", sessionID), "", nil
		}
		bridge := fmt.Sprintf("[planning-control] decision=GO session=%s status=%s", sessionID, status.Status)
		return fmt.Sprintf("Greenlight GO submitted for `%s` (status: %s).", sessionID, status.Status), bridge, nil
	case planningCommandRealign:
		sessionID, err := h.resolvePlanningSession(msg.Room, cmd.sessionID)
		if err != nil {
			return "", "", err
		}
		if err := h.Planning.SubmitPlanningSignal(ctx, sessionID, "greenlight", "REALIGN", "matrix-control"); err != nil {
			return "", "", err
		}
		prompt, err := h.Planning.GetPlanningPrompt(ctx, sessionID, "matrix-control")
		if err != nil {
			return fmt.Sprintf("Greenlight REALIGN submitted for `%s`.", sessionID), "", nil
		}
		bridge := fmt.Sprintf("[planning-control] decision=REALIGN session=%s", sessionID)
		return formatPlanningPromptForChat(prompt), bridge, nil
	case planningCommandStop:
		sessionID, err := h.resolvePlanningSession(msg.Room, cmd.sessionID)
		if err != nil {
			return "", "", err
		}
		reason := strings.TrimSpace(cmd.reason)
		if reason == "" {
			reason = "stopped by matrix control command"
		}
		if err := h.Planning.StopPlanningSession(ctx, sessionID, reason, "matrix-control"); err != nil {
			return "", "", err
		}
		h.clearActivePlanningSession(msg.Room, sessionID)
		bridge := fmt.Sprintf("[planning-control] stopped session=%s reason=%s", sessionID, reason)
		return fmt.Sprintf("Stopped planning session `%s`.", sessionID), bridge, nil
	default:
		return "", "", fmt.Errorf("unsupported planning command")
	}
}

func parsePlanningCommand(raw string) (planningCommand, bool, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return planningCommand{}, false, nil
	}
	lower := strings.ToLower(text)
	switch {
	case strings.HasPrefix(lower, "/plan"):
		text = strings.TrimSpace(text[len("/plan"):])
	case strings.HasPrefix(lower, "plan"):
		text = strings.TrimSpace(text[len("plan"):])
	default:
		return planningCommand{}, false, nil
	}

	if text == "" {
		return planningCommand{kind: planningCommandHelp}, true, nil
	}

	parts := strings.Fields(text)
	if len(parts) == 0 {
		return planningCommand{kind: planningCommandHelp}, true, nil
	}

	action := strings.ToLower(strings.TrimSpace(parts[0]))
	args := parts[1:]

	switch action {
	case "help":
		return planningCommand{kind: planningCommandHelp}, true, nil
	case "start":
		cmd := planningCommand{kind: planningCommandStart}
		if len(args) == 0 {
			return planningCommand{}, true, fmt.Errorf("missing project")
		}
		cursor := 0
		if !strings.Contains(args[cursor], "=") {
			cmd.project = args[cursor]
			cursor++
		}
		if cursor < len(args) && !strings.Contains(args[cursor], "=") {
			cmd.workDir = args[cursor]
			cursor++
		}
		for ; cursor < len(args); cursor++ {
			k, v, ok := strings.Cut(args[cursor], "=")
			if !ok {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(k)) {
			case "project":
				cmd.project = strings.TrimSpace(v)
			case "workdir", "work_dir":
				cmd.workDir = strings.TrimSpace(v)
			case "agent":
				cmd.agent = strings.TrimSpace(v)
			case "tier":
				cmd.tier = strings.TrimSpace(v)
			case "topk", "candidate_top_k":
				if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
					cmd.candidateTopK = n
				}
			}
		}
		if strings.TrimSpace(cmd.project) == "" {
			return planningCommand{}, true, fmt.Errorf("missing project")
		}
		return cmd, true, nil
	case "prompt":
		cmd := planningCommand{kind: planningCommandPrompt}
		if len(args) > 0 {
			cmd.sessionID, _ = parsePlanningSessionToken(args[0])
		}
		return cmd, true, nil
	case "status":
		cmd := planningCommand{kind: planningCommandStatus}
		if len(args) > 0 {
			cmd.sessionID, _ = parsePlanningSessionToken(args[0])
		}
		return cmd, true, nil
	case "select":
		if len(args) == 0 {
			return planningCommand{}, true, fmt.Errorf("missing item id")
		}
		cmd := planningCommand{kind: planningCommandSelect}
		if sid, ok := parsePlanningSessionToken(args[0]); ok && len(args) > 1 {
			cmd.sessionID = sid
			cmd.value = strings.TrimSpace(args[1])
			return cmd, true, nil
		}
		cmd.value = strings.TrimSpace(args[0])
		if len(args) > 1 {
			if sid, ok := parsePlanningSessionToken(args[1]); ok {
				cmd.sessionID = sid
			}
		}
		return cmd, true, nil
	case "answer":
		if len(args) == 0 {
			return planningCommand{}, true, fmt.Errorf("missing answer text")
		}
		cmd := planningCommand{kind: planningCommandAnswer}
		startIdx := 0
		if sid, ok := parsePlanningSessionToken(args[0]); ok {
			cmd.sessionID = sid
			startIdx = 1
		}
		cmd.value = strings.TrimSpace(strings.Join(args[startIdx:], " "))
		if cmd.value == "" {
			return planningCommand{}, true, fmt.Errorf("missing answer text")
		}
		return cmd, true, nil
	case "go", "approve":
		cmd := planningCommand{kind: planningCommandGo}
		if len(args) > 0 {
			cmd.sessionID, _ = parsePlanningSessionToken(args[0])
		}
		return cmd, true, nil
	case "realign", "reject", "no":
		cmd := planningCommand{kind: planningCommandRealign}
		if len(args) > 0 {
			cmd.sessionID, _ = parsePlanningSessionToken(args[0])
		}
		return cmd, true, nil
	case "stop", "cancel":
		cmd := planningCommand{kind: planningCommandStop}
		startIdx := 0
		if len(args) > 0 {
			if sid, ok := parsePlanningSessionToken(args[0]); ok {
				cmd.sessionID = sid
				startIdx = 1
			}
		}
		if startIdx < len(args) {
			cmd.reason = strings.TrimSpace(strings.Join(args[startIdx:], " "))
		}
		return cmd, true, nil
	default:
		return planningCommand{}, true, fmt.Errorf("unknown action %q", action)
	}
}

func parsePlanningSessionToken(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if strings.HasPrefix(strings.ToLower(raw), "session=") {
		value := strings.TrimSpace(raw[len("session="):])
		return value, value != ""
	}
	if strings.HasPrefix(raw, "planning-") {
		return raw, true
	}
	return "", false
}

func (h *TurtleChatHandler) resolvePlanningSession(room, explicit string) (string, error) {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		return explicit, nil
	}
	active := h.activePlanningSession(room)
	if active != "" {
		return active, nil
	}
	return "", fmt.Errorf("no active planning session for this room. Start one with /plan start <project> [workdir]")
}

func (h *TurtleChatHandler) activePlanningSession(room string) string {
	if h == nil {
		return ""
	}
	h.activeMu.Lock()
	defer h.activeMu.Unlock()
	if h.activeByRoom == nil {
		return ""
	}
	return strings.TrimSpace(h.activeByRoom[strings.TrimSpace(room)])
}

func (h *TurtleChatHandler) setActivePlanningSession(room, sessionID string) {
	if h == nil {
		return
	}
	room = strings.TrimSpace(room)
	sessionID = strings.TrimSpace(sessionID)
	if room == "" || sessionID == "" {
		return
	}
	h.activeMu.Lock()
	if h.activeByRoom == nil {
		h.activeByRoom = make(map[string]string)
	}
	h.activeByRoom[room] = sessionID
	h.activeMu.Unlock()
}

func (h *TurtleChatHandler) clearActivePlanningSession(room, sessionID string) {
	if h == nil {
		return
	}
	room = strings.TrimSpace(room)
	sessionID = strings.TrimSpace(sessionID)
	if room == "" {
		return
	}
	h.activeMu.Lock()
	defer h.activeMu.Unlock()
	if h.activeByRoom == nil {
		return
	}
	if sessionID == "" || h.activeByRoom[room] == sessionID {
		delete(h.activeByRoom, room)
	}
}

func (h *TurtleChatHandler) sendControlMessage(ctx context.Context, room, message string) error {
	room = strings.TrimSpace(room)
	message = strings.TrimSpace(message)
	if room == "" || message == "" {
		return nil
	}
	sender := NewHTTPSender(nil, h.controlBotAccount())
	return sender.SendMessage(ctx, room, message)
}

func (h *TurtleChatHandler) sendBridgeMessage(ctx context.Context, controlRoom, message string) error {
	room := strings.TrimSpace(h.BridgeRoom)
	if room == "" {
		return nil
	}
	if room == strings.TrimSpace(controlRoom) {
		return nil
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	sender := NewHTTPSender(nil, h.controlBotAccount())
	return sender.SendMessage(ctx, room, message)
}

func (h *TurtleChatHandler) controlBotAccount() string {
	if h == nil {
		return "spritzbot"
	}
	if account := strings.TrimSpace(h.ControlBot); account != "" {
		return account
	}
	return "spritzbot"
}

func formatPlanningPromptForChat(prompt PlanningPrompt) string {
	lines := []string{
		fmt.Sprintf("Session: %s", strings.TrimSpace(prompt.SessionID)),
		fmt.Sprintf("Phase: %s", strings.TrimSpace(prompt.Phase)),
	}
	if strings.TrimSpace(prompt.Status) != "" {
		lines = append(lines, fmt.Sprintf("Workflow Status: %s", strings.TrimSpace(prompt.Status)))
	}
	if strings.TrimSpace(prompt.Prompt) != "" {
		lines = append(lines, "", strings.TrimSpace(prompt.Prompt))
	}
	if len(prompt.Options) > 0 {
		lines = append(lines, "", "Options:")
		for i := range prompt.Options {
			lines = append(lines, fmt.Sprintf("%d. %s", i+1, prompt.Options[i]))
		}
	}
	if strings.TrimSpace(prompt.Recommendation) != "" {
		lines = append(lines, "", "Recommendation: "+strings.TrimSpace(prompt.Recommendation))
	}
	if strings.TrimSpace(prompt.ExpectedSignal) != "" {
		lines = append(lines, "", "Next Signal: "+prompt.ExpectedSignal)
	}
	return strings.Join(lines, "\n")
}

func planningCommandUsage() string {
	return `Planning control commands:
- /plan start <project> [workdir] [agent=claude] [tier=fast] [topk=5]
- /plan prompt [session]
- /plan status [session]
- /plan select <item-id> [session]
- /plan answer [session] <text>
- /plan go [session]
- /plan realign [session]
- /plan stop [session] [reason]

If session is omitted, the room's active planning session is used.`
}
