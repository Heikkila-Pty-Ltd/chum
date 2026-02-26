package matrix

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestOpenClawSenderSendMessageIncludesAccount(t *testing.T) {
	runner := &fakeRunner{out: []byte(`{"ok":true}`)}
	sender := NewOpenClawSender(runner, "spritzbot")

	if err := sender.SendMessage(t.Context(), "!room:matrix.org", "hello"); err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}

	args := strings.Join(runner.lastArgs, " ")
	if !strings.Contains(args, "--channel matrix") {
		t.Fatalf("expected matrix channel args, got: %s", args)
	}
	if !strings.Contains(args, "--target !room:matrix.org") {
		t.Fatalf("expected target arg, got: %s", args)
	}
	if !strings.Contains(args, "--message hello") {
		t.Fatalf("expected message arg, got: %s", args)
	}
	if !strings.Contains(args, "--account spritzbot") {
		t.Fatalf("expected account arg, got: %s", args)
	}
}

func TestOpenClawSenderSendMessageOmitsEmptyAccount(t *testing.T) {
	runner := &fakeRunner{out: []byte(`{"ok":true}`)}
	sender := NewOpenClawSender(runner, "")

	if err := sender.SendMessage(t.Context(), "!room:matrix.org", "hello"); err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}

	args := strings.Join(runner.lastArgs, " ")
	if strings.Contains(args, "--account") {
		t.Fatalf("did not expect --account arg, got: %s", args)
	}
}

func TestOpenClawSenderSendMessageValidatesInputs(t *testing.T) {
	sender := NewOpenClawSender(&fakeRunner{}, "spritzbot")

	if err := sender.SendMessage(t.Context(), "", "hello"); err == nil {
		t.Fatal("expected error for empty room id")
	}
	if err := sender.SendMessage(t.Context(), "!room:matrix.org", ""); err == nil {
		t.Fatal("expected error for empty message")
	}
}

func TestOpenClawSenderSendMessageHandlesRunnerError(t *testing.T) {
	runner := &fakeRunner{
		out: []byte("matrix unavailable"),
		err: errors.New("exit status 1"),
	}
	sender := NewOpenClawSender(runner, "spritzbot")

	err := sender.SendMessage(t.Context(), "!room:matrix.org", "hello")
	if err == nil {
		t.Fatal("expected runner error")
	}
	if !strings.Contains(err.Error(), "openclaw message send failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

type fakeDirectSender struct {
	err   error
	calls int
}

func (s *fakeDirectSender) SendMessage(_ context.Context, _ string, _ string) error {
	s.calls++
	return s.err
}

func TestOpenClawSenderSendMessageUsesDirectSenderWhenAvailable(t *testing.T) {
	runner := &fakeRunner{err: errors.New("should not be called")}
	direct := &fakeDirectSender{}
	sender := &OpenClawSender{
		runner:  runner,
		account: "spritzbot",
		direct:  direct,
	}

	if err := sender.SendMessage(t.Context(), "!room:matrix.org", "hello"); err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if direct.calls != 1 {
		t.Fatalf("expected one direct sender call, got %d", direct.calls)
	}
	if runner.lastName != "" {
		t.Fatalf("expected runner not to be called, got command %q", runner.lastName)
	}
}

func TestOpenClawSenderSendMessageFallsBackToRunnerAfterDirectFailure(t *testing.T) {
	runner := &fakeRunner{out: []byte(`{"ok":true}`)}
	direct := &fakeDirectSender{err: errors.New("direct failed")}
	sender := &OpenClawSender{
		runner:  runner,
		account: "spritzbot",
		direct:  direct,
	}

	if err := sender.SendMessage(t.Context(), "!room:matrix.org", "hello"); err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if direct.calls != 1 {
		t.Fatalf("expected one direct sender call, got %d", direct.calls)
	}
	if runner.lastName != "openclaw" {
		t.Fatalf("expected runner command openclaw, got %q", runner.lastName)
	}
}

func TestOpenClawSenderSendMessageReturnsCombinedErrorWhenDirectAndRunnerFail(t *testing.T) {
	runner := &fakeRunner{
		out: []byte("runner failed"),
		err: errors.New("exit status 1"),
	}
	direct := &fakeDirectSender{err: errors.New("direct failed")}
	sender := &OpenClawSender{
		runner:  runner,
		account: "spritzbot",
		direct:  direct,
	}

	err := sender.SendMessage(t.Context(), "!room:matrix.org", "hello")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "direct matrix send failed") {
		t.Fatalf("expected combined direct failure context, got: %v", err)
	}
	if !strings.Contains(err.Error(), "openclaw message send failed") {
		t.Fatalf("expected openclaw failure context, got: %v", err)
	}
}
