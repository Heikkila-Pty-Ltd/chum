package matrix

import (
	"context"
	"fmt"
	"strings"
)

// Sender sends outbound Matrix messages.
type Sender interface {
	SendMessage(ctx context.Context, roomID, message string) error
}

// OpenClawSender sends Matrix messages via `openclaw message send`.
type OpenClawSender struct {
	runner  Runner
	account string
	direct  Sender
}

// NewOpenClawSender constructs a sender with an optional account id.
func NewOpenClawSender(runner Runner, account string) *OpenClawSender {
	var direct Sender
	if runner == nil {
		runner = ExecRunner{}
		direct = NewHTTPSender(nil, account)
	}
	return &OpenClawSender{
		runner:  runner,
		account: strings.TrimSpace(account),
		direct:  direct,
	}
}

// SendMessage sends a message to a Matrix room.
func (s *OpenClawSender) SendMessage(ctx context.Context, roomID, message string) error {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return fmt.Errorf("room id is required")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return fmt.Errorf("message is required")
	}

	var directErr error
	if s.direct != nil {
		if err := s.direct.SendMessage(ctx, roomID, message); err == nil {
			return nil
		} else {
			directErr = err
		}
	}

	args := []string{
		"message", "send",
		"--channel", "matrix",
		"--target", roomID,
		"--message", message,
		"--json",
	}
	if s.account != "" {
		args = append(args, "--account", s.account)
	}

	out, err := s.runner.Run(ctx, "openclaw", args...)
	if err != nil {
		if directErr != nil {
			return fmt.Errorf("direct matrix send failed: %w; openclaw message send failed: %w (%s)", directErr, err, compactOutput(out))
		}
		return fmt.Errorf("openclaw message send failed: %w (%s)", err, compactOutput(out))
	}
	return nil
}
