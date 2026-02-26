package matrix

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	out      []byte
	err      error
	lastName string
	lastArgs []string
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.lastName = name
	r.lastArgs = append([]string{}, args...)
	return r.out, r.err
}

func TestOpenClawClientReadMessagesParsesMessages(t *testing.T) {
	runner := &fakeRunner{
		out: []byte(`doctor warning line
{"messages":[{"event_id":"$evt-1","room_id":"!room:matrix.org","sender":"@alice:matrix.org","content":{"body":"hello matrix"},"timestamp":1700000000000}],"next":"$evt-1"}`),
	}
	client := NewOpenClawClient(runner, 50)

	msgs, next, err := client.ReadMessages(t.Context(), "!room:matrix.org", "$prev")
	if err != nil {
		t.Fatalf("ReadMessages returned error: %v", err)
	}
	if runner.lastName != "openclaw" {
		t.Fatalf("runner command = %q, want openclaw", runner.lastName)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].ID != "$evt-1" {
		t.Fatalf("message id = %q, want $evt-1", msgs[0].ID)
	}
	if msgs[0].Room != "!room:matrix.org" {
		t.Fatalf("message room = %q, want !room:matrix.org", msgs[0].Room)
	}
	if msgs[0].Sender != "@alice:matrix.org" {
		t.Fatalf("message sender = %q, want @alice:matrix.org", msgs[0].Sender)
	}
	if msgs[0].Body != "hello matrix" {
		t.Fatalf("message body = %q, want hello matrix", msgs[0].Body)
	}
	if next != "$evt-1" {
		t.Fatalf("next cursor = %q, want $evt-1", next)
	}
	if msgs[0].Timestamp.IsZero() {
		t.Fatal("expected non-zero timestamp")
	}
}

func TestOpenClawClientReadMessagesUsesAfterCursorArgument(t *testing.T) {
	runner := &fakeRunner{
		out: []byte(`{"messages":[]}`),
	}
	client := NewOpenClawClient(runner, 10)

	_, _, err := client.ReadMessages(t.Context(), "!room:matrix.org", "$cursor")
	if err != nil {
		t.Fatalf("ReadMessages returned error: %v", err)
	}
	args := strings.Join(runner.lastArgs, " ")
	if !strings.Contains(args, "--after $cursor") {
		t.Fatalf("expected --after argument, got args: %s", args)
	}
	if !strings.Contains(args, "--limit 10") {
		t.Fatalf("expected --limit argument, got args: %s", args)
	}
}

func TestOpenClawClientReadMessagesHandlesRunnerError(t *testing.T) {
	runner := &fakeRunner{
		out: []byte("gateway unavailable"),
		err: errors.New("exit status 1"),
	}
	client := NewOpenClawClient(runner, 20)

	_, _, err := client.ReadMessages(t.Context(), "!room:matrix.org", "")
	if err == nil {
		t.Fatal("expected runner error")
	}
	if !strings.Contains(err.Error(), "openclaw message read failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseReadOutputNestedMessagesAndCursorFallback(t *testing.T) {
	out := []byte(`{
  "payload": {
    "events": [
      {"id":"m1","sender":"@u:matrix.org","body":"first","time":"2026-02-18T00:00:00Z"},
      {"id":"m2","sender":"@u:matrix.org","body":"second","time":"2026-02-18T00:01:00Z"}
    ]
  }
}`)

	msgs, next, err := parseReadOutput(out, "!room:matrix.org")
	if err != nil {
		t.Fatalf("parseReadOutput returned error: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if next != "m2" {
		t.Fatalf("expected cursor fallback to last message id m2, got %q", next)
	}
	if msgs[0].Room != "!room:matrix.org" {
		t.Fatalf("default room not applied: %q", msgs[0].Room)
	}
	if msgs[0].Timestamp.Format(time.RFC3339) != "2026-02-18T00:00:00Z" {
		t.Fatalf("unexpected parsed timestamp: %s", msgs[0].Timestamp.Format(time.RFC3339))
	}
}
