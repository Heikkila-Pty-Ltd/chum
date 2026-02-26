package matrix

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	neturl "net/url"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// TurtleBotPrefix is the marker for turtle chat routing.
const TurtleBotPrefix = "🐢"

// turtleBotUser maps @mention localparts to CLI agent names.
var turtleBotUser = map[string]string{
	"claudebot": "claude",
	"codexbot":  "codex",
	"geminibot": "gemini",
}

// turtleBotSenders is the set of Matrix user IDs for the 3 turtle bots,
// used to filter out their own messages during polling.
var turtleBotSenders = map[string]struct{}{
	"@claudebot": {},
	"@codexbot":  {},
	"@geminibot": {},
}

// TurtleChatHandler handles interactive messages in the turtle deliberation room.
// When a human posts a message, it detects which agent(s) are @mentioned and runs
// them, posting responses as each agent's dedicated Matrix bot persona.
type TurtleChatHandler struct {
	Room    string // turtle room ID
	WorkDir string // project work directory
	Logger  *slog.Logger

	Planning     PlanningController // optional planning control bridge
	BridgeRoom   string             // optional throughput/log room
	ControlBot   string             // bot account used for control responses (default spritzbot)
	activeByRoom map[string]string
	activeMu     sync.Mutex
}

// IsTurtleRoom checks if a room is the dedicated turtle deliberation room.
func (h *TurtleChatHandler) IsTurtleRoom(room string) bool {
	return h != nil && strings.TrimSpace(h.Room) != "" &&
		strings.TrimSpace(room) == strings.TrimSpace(h.Room)
}

// IsTurtleBot checks if a sender is one of the 3 turtle bots.
func (h *TurtleChatHandler) IsTurtleBot(sender string) bool {
	localpart := matrixUserLocalpart(sender)
	_, ok := turtleBotUser[strings.ToLower(localpart)]
	return ok
}

// Handle processes an inbound message in the turtle room. If a specific bot is
// @mentioned, only that agent responds. Otherwise, all 3 respond for a roundtable.
func (h *TurtleChatHandler) Handle(ctx context.Context, msg InboundMessage) error {
	if h == nil || h.Logger == nil {
		return nil
	}
	if handled, err := h.handlePlanningCommand(ctx, msg); handled {
		return err
	}

	mentioned := h.detectMentions(msg.Body)
	if len(mentioned) == 0 {
		// No specific @mention — route to all 3 for a roundtable
		mentioned = []string{"claude", "codex", "gemini"}
	}

	h.Logger.Info(TurtleBotPrefix+" turtle chat message",
		"sender", msg.Sender,
		"agents", mentioned,
		"body_len", len(msg.Body))

	for _, agent := range mentioned {
		go func(agentName string) {
			if err := h.runAndRespond(ctx, agentName, msg); err != nil {
				h.Logger.Warn(TurtleBotPrefix+" turtle chat response failed",
					"agent", agentName,
					"error", err)
			}
		}(agent)
	}

	return nil
}

// detectMentions extracts which agents are @mentioned in the message body.
func (h *TurtleChatHandler) detectMentions(body string) []string {
	lower := strings.ToLower(body)
	var agents []string
	for localpart, agent := range turtleBotUser {
		// Check for @claudebot, claudebot, or claude mentions
		if strings.Contains(lower, "@"+localpart) ||
			strings.Contains(lower, localpart) ||
			strings.Contains(lower, agent) {
			agents = append(agents, agent)
		}
	}
	return agents
}

// runAndRespond runs an agent with the user's message as context and posts the
// response as that agent's dedicated Matrix bot persona.
func (h *TurtleChatHandler) runAndRespond(ctx context.Context, agent string, msg InboundMessage) error {
	prompt := fmt.Sprintf(`You are %s, participating in a multi-agent deliberation chat.

A human just sent a message in the turtle planning room.

Sender: %s
Message:
%s

Respond naturally and concisely. You are having a conversation — keep it brief and useful.
If asked about code or architecture, be specific. Reference files and functions.
If asked to plan, provide concrete steps.
Do NOT produce JSON. Just respond conversationally in plain text/markdown.`, agent, msg.Sender, msg.Body)

	workDir := h.WorkDir
	if workDir == "" {
		workDir = defaultWorkDir
	}

	// Run the agent CLI directly (same as runAgent in temporal package)
	output, err := runAgentCLI(ctx, agent, prompt, workDir)
	if err != nil {
		return fmt.Errorf("agent %s failed: %w", agent, err)
	}

	response := strings.TrimSpace(output)
	if response == "" {
		response = fmt.Sprintf("(%s had no response)", agent)
	}

	// Truncate very long responses
	if len(response) > 2000 {
		response = response[:2000] + "\n\n_(truncated)_"
	}

	// Send as this agent's bot persona
	botName := agentToBotName(agent)
	sender := NewHTTPSender(nil, botName)
	if err := sender.SendMessage(ctx, msg.Room, response); err != nil {
		return fmt.Errorf("send as %s failed: %w", botName, err)
	}

	h.Logger.Info(TurtleBotPrefix+" turtle chat response sent",
		"agent", agent,
		"bot", botName,
		"response_len", len(response))

	return nil
}

// agentToBotName maps agent CLI names to Matrix bot account names.
func agentToBotName(agent string) string {
	switch agent {
	case "claude":
		return "claudebot"
	case "codex":
		return "codexbot"
	case "gemini":
		return "geminibot"
	default:
		return "spritzbot"
	}
}

// runAgentCLI runs an agent CLI and returns its output.
// This is a lightweight version of temporal.runAgent for use outside workflows.
func runAgentCLI(ctx context.Context, agent, prompt, workDir string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	var cmd *exec.Cmd
	switch agent {
	case "claude":
		cmd = exec.CommandContext(ctx, "claude", "--print", "--dangerously-skip-permissions", "-p", prompt)
	case "codex":
		cmd = exec.CommandContext(ctx, "codex", "--full-auto", "--quiet", "-p", prompt)
	case "gemini":
		cmd = exec.CommandContext(ctx, "gemini", "-p", prompt)
	default:
		return "", fmt.Errorf("unknown agent: %s", agent)
	}
	cmd.Dir = workDir

	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("agent %s exited: %w", agent, err)
	}
	return string(out), nil
}

// RunPoller starts a self-contained polling loop for the turtle room.
// It uses spritzbot's credentials to read messages, skips bot messages,
// and dispatches human messages to the TurtleChatHandler.
func (h *TurtleChatHandler) RunPoller(ctx context.Context) {
	if h == nil || h.Room == "" || h.Logger == nil {
		return
	}

	h.Logger.Info(TurtleBotPrefix+" turtle room poller starting", "room", h.Room)

	// Use spritzbot to read messages (it's in the room)
	reader := NewHTTPSender(nil, "spritzbot")
	var sinceToken string

	// Do initial sync to skip existing messages
	_, token, err := readRoomMessages(ctx, reader, h.Room, sinceToken)
	if err != nil {
		h.Logger.Warn(TurtleBotPrefix+" initial turtle room sync failed", "error", err)
	} else {
		sinceToken = token
	}

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			h.Logger.Info(TurtleBotPrefix + " turtle room poller stopped")
			return
		case <-ticker.C:
			messages, nextToken, readErr := readRoomMessages(ctx, reader, h.Room, sinceToken)
			if readErr != nil {
				h.Logger.Warn(TurtleBotPrefix+" turtle room poll failed", "error", readErr)
				continue
			}
			if nextToken != "" {
				sinceToken = nextToken
			}
			for _, msg := range messages {
				// Skip our own bots
				if h.IsTurtleBot(msg.Sender) {
					continue
				}
				// Skip spritzbot
				localpart := matrixUserLocalpart(msg.Sender)
				if strings.EqualFold(localpart, "spritzbot") {
					continue
				}
				_ = h.Handle(ctx, msg)
			}
		}
	}
}

// readRoomMessages reads recent messages from a Matrix room.
func readRoomMessages(ctx context.Context, reader *HTTPSender, roomID, since string) ([]InboundMessage, string, error) {
	creds, err := reader.loadCredentials()
	if err != nil {
		return nil, "", err
	}

	endpoint := fmt.Sprintf("%s/_matrix/client/v3/rooms/%s/messages?dir=f&limit=20",
		creds.homeserver,
		neturl.PathEscape(roomID),
	)
	if since != "" {
		endpoint += "&from=" + neturl.QueryEscape(since)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+creds.accessToken)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("matrix messages: status %d", resp.StatusCode)
	}

	var result struct {
		Chunk []struct {
			EventID string `json:"event_id"`
			Sender  string `json:"sender"`
			Content struct {
				MsgType string `json:"msgtype"`
				Body    string `json:"body"`
			} `json:"content"`
			Type         string `json:"type"`
			OriginServer int64  `json:"origin_server_ts"`
		} `json:"chunk"`
		End string `json:"end"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, "", fmt.Errorf("parse messages: %w", err)
	}

	var messages []InboundMessage
	for _, evt := range result.Chunk {
		if evt.Type != "m.room.message" || evt.Content.MsgType != "m.text" {
			continue
		}
		messages = append(messages, InboundMessage{
			ID:        evt.EventID,
			Room:      roomID,
			Sender:    evt.Sender,
			Body:      evt.Content.Body,
			Timestamp: time.Unix(evt.OriginServer/1000, 0),
		})
	}

	return messages, result.End, nil
}
