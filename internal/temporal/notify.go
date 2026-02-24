package temporal

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/antigravity-dev/chum/internal/matrix"
	"go.temporal.io/sdk/activity"
)

// agentToBotName maps CLI agent names to their Matrix bot account names.
// Each bot appears as a separate persona in Matrix deliberation channels.
var agentToBotName = map[string]string{
	"claude": "claudebot",
	"codex":  "codexbot",
	"gemini": "geminibot",
}

// TurtleSendAsRequest carries per-agent Matrix message data.
type TurtleSendAsRequest struct {
	Agent   string `json:"agent"`   // claude/codex/gemini
	Room    string `json:"room"`    // room ID (empty = default)
	Message string `json:"message"` // markdown message body
}

// TurtleSendAsActivity sends a Matrix message as a specific bot persona.
// This creates the visual effect of a multi-agent conversation in the channel.
func (a *Activities) TurtleSendAsActivity(ctx context.Context, req TurtleSendAsRequest) error {
	logger := activity.GetLogger(ctx)

	botName, ok := agentToBotName[req.Agent]
	if !ok {
		botName = "spritzbot" // fallback to default
	}

	room := req.Room
	if room == "" {
		room = a.TurtleRoom // dedicated deliberation channel
	}
	if room == "" {
		room = a.DefaultRoom // fallback
	}
	if room == "" {
		logger.Warn(TurtlePrefix+" No Matrix room configured, skipping per-agent send")
		return nil
	}

	sender := matrix.NewHTTPSender(nil, botName)
	if err := sender.SendMessage(ctx, room, req.Message); err != nil {
		logger.Warn(TurtlePrefix+" Per-agent Matrix send failed (non-fatal)",
			"Agent", req.Agent, "Bot", botName, "error", err)
	}
	return nil // never fail the workflow over a notification
}

// NotifyRequest carries the data for a Matrix notification.
type NotifyRequest struct {
	Event  string // event key: "plan", "execute", "review", "dod_pass", etc.
	TaskID string
	Extra  map[string]string // variable substitution data
}

// NotifyActivity sends a themed notification to the configured Matrix room.
// This is fire-and-forget — errors are logged but never returned.
func (a *Activities) NotifyActivity(ctx context.Context, req NotifyRequest) error {
	if a.Sender == nil || a.DefaultRoom == "" {
		return nil // notifications not configured
	}

	logger := activity.GetLogger(ctx)
	msg := themed(req.Event, req.TaskID, req.Extra)
	if msg == "" {
		return nil
	}

	if err := a.Sender.SendMessage(ctx, a.DefaultRoom, msg); err != nil {
		logger.Warn("Matrix notification failed (non-fatal)", "event", req.Event, "error", err)
	}
	return nil // never fail the workflow over a notification
}

// themed returns a fun, ocean-themed notification message for a given event.
func themed(event, taskID string, extra map[string]string) string {
	get := func(key string) string {
		if extra == nil {
			return ""
		}
		return extra[key]
	}

	switch event {
	// --- Dispatcher ---
	case "dispatch":
		tasks := get("tasks")
		count := get("count")
		if count == "" {
			count = "?"
		}
		return fmt.Sprintf("🦈 **chum fed to %s sharks** — %s", count, tasks)

	// --- Shark pipeline ---
	case "plan":
		title := get("title")
		agent := get("agent")
		if title != "" {
			return fmt.Sprintf("🗺️ **shark charting course** — `%s`: *%s* (planner: %s)", taskID, title, agent)
		}
		return fmt.Sprintf("🗺️ **shark charting course** — `%s` (planner: %s)", taskID, agent)

	case "execute":
		agent := get("agent")
		attempt := get("attempt")
		return fmt.Sprintf("🦈 **shark hunting** — %s executing `%s` (attempt %s/3)", agent, taskID, attempt)

	case "review":
		reviewer := get("reviewer")
		author := get("author")
		return fmt.Sprintf("🔍 **pilot fish inspecting** — %s reviewing %s's catch on `%s`", reviewer, author, taskID)

	case "review_approved":
		reviewer := get("reviewer")
		return fmt.Sprintf("✅ **pilot fish approved** — %s signed off on `%s`", reviewer, taskID)

	case "handoff":
		from := get("from")
		to := get("to")
		handoff := get("handoff")
		return fmt.Sprintf("🔄 **shark tag-team** — %s handing off to %s on `%s` (handoff %s)", from, to, taskID, handoff)

	case "dod_pass":
		duration := get("duration")
		cost := get("cost")
		return fmt.Sprintf("✅ **orca approved the kill** — `%s` passed DoD (%s, $%s)", taskID, duration, cost)

	case "dod_fail":
		failures := get("failures")
		attempt := get("attempt")
		detail := get("detail")
		msg := fmt.Sprintf("❌ **orca rejected** — `%s` DoD failed (attempt %s/3): %s", taskID, attempt, failures)
		if detail != "" {
			msg += fmt.Sprintf("\n```\n%s\n```", detail)
		}
		return msg

	case "escalate":
		attempts := get("attempts")
		return fmt.Sprintf("🚨 **shark beached** — `%s` failed %s attempts, needs human intervention", taskID, attempts)

	case "complete":
		duration := get("duration")
		cost := get("cost")
		return fmt.Sprintf("🎉 **catch landed** — `%s` completed in %s ($%s)", taskID, duration, cost)

	// --- Crab decomposition ---
	case "crab_start":
		planID := get("plan_id")
		return fmt.Sprintf("🦀 **crabs cutting the whale up** — decomposing plan `%s`", planID)

	case "crab_done":
		whales := get("whales")
		morsels := get("morsels")
		return fmt.Sprintf("🦀 **whale carved** — %s whales, %s morsels emitted", whales, morsels)

	// --- Learner ---
	case "learner":
		lessons := get("lessons")
		return fmt.Sprintf("🐙 **octopus updating the knowledge store** — %s lessons from `%s`", lessons, taskID)

	// --- Groom ---
	case "groom":
		applied := get("applied")
		return fmt.Sprintf("🪥 **remoras cleaning up** — %s mutations applied after `%s`", applied, taskID)

	// --- Turtle (Autonomous Planning Ceremony) ---
	case "turtle_start":
		desc := get("description")
		return fmt.Sprintf("🐢 **turtle planning ceremony** — 3 agents deliberating on `%s`: %s", taskID, desc)

	case "turtle_done":
		morsels := get("morsels")
		score := get("score")
		duration := get("duration")
		return fmt.Sprintf("🐢 **consensus reached** — `%s`: %s morsels emitted (%s%% confidence, %s)", taskID, morsels, score, duration)

	case "turtle_disagreement":
		score := get("score")
		disagreements := get("disagreements")
		return fmt.Sprintf("🐢 **need human tiebreak** — `%s` has unresolved disagreements (%s%% confidence)\n%s", taskID, score, disagreements)

	case "turtle_failed":
		phase := get("phase")
		errMsg := get("error")
		return fmt.Sprintf("🐢 **ceremony failed** — `%s` phase %s: %s", taskID, phase, errMsg)

	case "crab_escalate":
		reason := get("reason")
		return fmt.Sprintf("🦀→🐢 **crab couldn't slice** — `%s` escalated to turtle (%s)", taskID, reason)

	default:
		return ""
	}
}

// fmtDuration formats a duration into a human-readable string like "4m32s".
func fmtDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm%ds", m, s)
}

// fmtCost formats a USD cost as a short string.
func fmtCost(cost float64) string {
	if cost < 0.01 {
		return fmt.Sprintf("%.4f", cost)
	}
	return fmt.Sprintf("%.2f", cost)
}

// joinTasks joins task IDs for display.
func joinTasks(ids []string) string {
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = "`" + id + "`"
	}
	return strings.Join(quoted, ", ")
}
