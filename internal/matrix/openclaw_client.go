package matrix

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const defaultReadLimit = 25

// Runner executes external commands.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner uses os/exec to run commands.
type ExecRunner struct{}

// Run executes a command and returns its combined output.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// OpenClawClient reads Matrix messages via `openclaw message read`.
type OpenClawClient struct {
	runner    Runner
	readLimit int
}

// NewOpenClawClient constructs a client with an optional custom runner.
func NewOpenClawClient(runner Runner, readLimit int) *OpenClawClient {
	if runner == nil {
		runner = ExecRunner{}
	}
	if readLimit <= 0 {
		readLimit = defaultReadLimit
	}
	return &OpenClawClient{
		runner:    runner,
		readLimit: readLimit,
	}
}

// ReadMessages fetches recent messages for a room and returns parsed messages + next cursor.
func (c *OpenClawClient) ReadMessages(ctx context.Context, roomID string, after string) ([]InboundMessage, string, error) {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return nil, "", fmt.Errorf("room id is required")
	}

	args := []string{
		"message", "read",
		"--channel", "matrix",
		"--target", roomID,
		"--limit", strconv.Itoa(c.readLimit),
		"--json",
	}
	if strings.TrimSpace(after) != "" {
		args = append(args, "--after", strings.TrimSpace(after))
	}

	out, err := c.runner.Run(ctx, "openclaw", args...)
	if err != nil {
		return nil, "", fmt.Errorf("openclaw message read failed: %w (%s)", err, compactOutput(out))
	}

	messages, next, parseErr := parseReadOutput(out, roomID)
	if parseErr != nil {
		return nil, "", parseErr
	}
	return messages, next, nil
}

func parseReadOutput(out []byte, defaultRoom string) ([]InboundMessage, string, error) {
	jsonPayload := extractJSONPayload(string(out))
	if jsonPayload == "" {
		return nil, "", nil
	}

	var decoded any
	if err := json.Unmarshal([]byte(jsonPayload), &decoded); err != nil {
		return nil, "", fmt.Errorf("parse openclaw read json: %w", err)
	}

	messages := decodeMessages(decoded, defaultRoom)
	next := decodeCursor(decoded, messages)
	return messages, next, nil
}

func decodeMessages(decoded any, defaultRoom string) []InboundMessage {
	items := findMessageArray(decoded)
	if len(items) == 0 {
		return nil
	}

	out := make([]InboundMessage, 0, len(items))
	for _, item := range items {
		msg := decodeMessageItem(item, defaultRoom)
		if strings.TrimSpace(msg.Body) == "" {
			continue
		}
		out = append(out, msg)
	}
	return out
}

func findMessageArray(node any) []any {
	switch v := node.(type) {
	case []any:
		return v
	case map[string]any:
		keys := []string{"messages", "events", "items", "results"}
		for _, key := range keys {
			if arr, ok := v[key].([]any); ok {
				return arr
			}
		}
		if nested, ok := v["data"]; ok {
			if arr := findMessageArray(nested); len(arr) > 0 {
				return arr
			}
		}
		if nested, ok := v["payload"]; ok {
			if arr := findMessageArray(nested); len(arr) > 0 {
				return arr
			}
		}
	}
	return nil
}

func decodeMessageItem(item any, defaultRoom string) InboundMessage {
	obj, ok := item.(map[string]any)
	if !ok {
		return InboundMessage{}
	}

	body := firstString(obj, "body", "text", "message")
	if body == "" {
		if content, ok := obj["content"].(map[string]any); ok {
			body = firstString(content, "body", "text", "message")
		}
	}

	msg := InboundMessage{
		ID:     firstString(obj, "id", "event_id", "message_id"),
		Room:   firstString(obj, "room", "room_id", "target"),
		Sender: decodeSender(obj),
		Body:   body,
	}
	if msg.Room == "" {
		msg.Room = defaultRoom
	}

	msg.Timestamp = decodeTimestamp(obj)
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now().UTC()
	}
	return msg
}

func decodeSender(obj map[string]any) string {
	sender := firstString(obj, "sender", "from", "user")
	if sender != "" {
		return sender
	}
	if author, ok := obj["author"].(map[string]any); ok {
		return firstString(author, "id", "user_id", "sender")
	}
	return ""
}

func decodeTimestamp(obj map[string]any) time.Time {
	for _, key := range []string{"timestamp", "ts", "created_at", "time"} {
		raw, ok := obj[key]
		if !ok {
			continue
		}
		if ts := decodeAnyTime(raw); !ts.IsZero() {
			return ts
		}
	}
	if content, ok := obj["content"].(map[string]any); ok {
		for _, key := range []string{"timestamp", "ts"} {
			if ts := decodeAnyTime(content[key]); !ts.IsZero() {
				return ts
			}
		}
	}
	return time.Time{}
}

func decodeAnyTime(value any) time.Time {
	switch v := value.(type) {
	case float64:
		sec := int64(v)
		if sec > 1_000_000_000_000 {
			sec /= 1000
		}
		return time.Unix(sec, 0).UTC()
	case int64:
		sec := v
		if sec > 1_000_000_000_000 {
			sec /= 1000
		}
		return time.Unix(sec, 0).UTC()
	case string:
		v = strings.TrimSpace(v)
		if v == "" {
			return time.Time{}
		}
		if unix, err := strconv.ParseInt(v, 10, 64); err == nil {
			if unix > 1_000_000_000_000 {
				unix /= 1000
			}
			return time.Unix(unix, 0).UTC()
		}
		layouts := []string{
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02 15:04:05",
		}
		for _, layout := range layouts {
			if parsed, err := time.Parse(layout, v); err == nil {
				return parsed.UTC()
			}
		}
	}
	return time.Time{}
}

func decodeCursor(decoded any, messages []InboundMessage) string {
	if m, ok := decoded.(map[string]any); ok {
		for _, key := range []string{"next", "next_cursor", "cursor", "since", "after"} {
			if value := firstString(m, key); value != "" {
				return value
			}
		}
		if data, ok := m["data"].(map[string]any); ok {
			for _, key := range []string{"next", "next_cursor", "cursor", "since", "after"} {
				if value := firstString(data, key); value != "" {
					return value
				}
			}
		}
		if payload, ok := m["payload"].(map[string]any); ok {
			for _, key := range []string{"next", "next_cursor", "cursor", "since", "after"} {
				if value := firstString(payload, key); value != "" {
					return value
				}
			}
		}
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.TrimSpace(messages[i].ID) != "" {
			return messages[i].ID
		}
	}
	return ""
}

func firstString(obj map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := obj[key]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case string:
			if trimmed := strings.TrimSpace(v); trimmed != "" {
				return trimmed
			}
		case json.Number:
			if trimmed := strings.TrimSpace(v.String()); trimmed != "" {
				return trimmed
			}
		case float64:
			return strconv.FormatInt(int64(v), 10)
		}
	}
	return ""
}

func extractJSONPayload(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	start := strings.IndexAny(trimmed, "{[")
	if start < 0 {
		return ""
	}
	return strings.TrimSpace(trimmed[start:])
}

func compactOutput(out []byte) string {
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return "no output"
	}
	const maxLen = 280
	if len(trimmed) <= maxLen {
		return trimmed
	}
	return trimmed[:maxLen] + "..."
}
