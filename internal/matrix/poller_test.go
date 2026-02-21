package matrix

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/antigravity-dev/chum/internal/config"
	"github.com/antigravity-dev/chum/internal/dispatch"
	"github.com/antigravity-dev/chum/internal/graph"
	"github.com/antigravity-dev/chum/internal/store"

	_ "modernc.org/sqlite"
)

type fakePollResponse struct {
	messages []InboundMessage
	next     string
}

type fakeClient struct {
	responses map[string]fakePollResponse
	errors    map[string]error
	calls     []string
}

func (c *fakeClient) ReadMessages(_ context.Context, roomID string, _ string) ([]InboundMessage, string, error) {
	c.calls = append(c.calls, roomID)
	if err := c.errors[roomID]; err != nil {
		return nil, "", err
	}
	resp := c.responses[roomID]
	return resp.messages, resp.next, nil
}

type pollDispatchCall struct {
	agent  string
	prompt string
}

type fakeDispatcher struct {
	calls      []pollDispatchCall
	failAgents map[string]bool
}

func (d *fakeDispatcher) Dispatch(_ context.Context, agent, prompt, _ string, _ string, _ string) (int, error) {
	d.calls = append(d.calls, pollDispatchCall{agent: agent, prompt: prompt})
	if d.failAgents != nil && d.failAgents[agent] {
		return 0, errors.New("simulated dispatch failure")
	}
	return len(d.calls), nil
}

func (d *fakeDispatcher) IsAlive(_ int) bool { return false }
func (d *fakeDispatcher) Kill(_ int) error   { return nil }
func (d *fakeDispatcher) GetHandleType() string {
	return "test"
}
func (d *fakeDispatcher) GetSessionName(_ int) string { return "" }
func (d *fakeDispatcher) GetProcessState(_ int) dispatch.ProcessState {
	return dispatch.ProcessState{}
}

type fakeSender struct {
	messages []string
	rooms    []string
	err      error
}

func (s *fakeSender) SendMessage(_ context.Context, roomID, message string) error {
	if s == nil {
		return nil
	}
	s.rooms = append(s.rooms, strings.TrimSpace(roomID))
	s.messages = append(s.messages, strings.TrimSpace(message))
	return s.err
}

type fakeStore struct {
	running   []store.Dispatch
	completed []store.Dispatch

	getRunningErr   error
	getCompletedErr error
}

func (s *fakeStore) GetRunningDispatches() ([]store.Dispatch, error) {
	if s == nil {
		return nil, nil
	}
	return s.running, s.getRunningErr
}

func (s *fakeStore) GetCompletedDispatchesSince(_ string, _ string) ([]store.Dispatch, error) {
	if s == nil {
		return nil, nil
	}
	return s.completed, s.getCompletedErr
}

type fakeCanceler struct {
	cancelledIDs []int64
	err          error
}

func (f *fakeCanceler) CancelDispatch(id int64) error {
	f.cancelledIDs = append(f.cancelledIDs, id)
	return f.err
}

func TestPollOnceRoutesMessagesAndSkipsBotSender(t *testing.T) {
	client := &fakeClient{
		responses: map[string]fakePollResponse{
			"!room-a:matrix.org": {
				messages: []InboundMessage{
					{ID: "1", Room: "!room-a:matrix.org", Sender: "@chum-bot:matrix.org", Body: "self-message"},
					{ID: "2", Room: "!room-a:matrix.org", Sender: "@alice:matrix.org", Body: "hello from a"},
				},
				next: "cursor-a",
			},
			"!room-b:matrix.org": {
				messages: []InboundMessage{
					{ID: "3", Room: "!room-b:matrix.org", Sender: "@bob:matrix.org", Body: "hello from b"},
				},
				next: "cursor-b",
			},
		},
	}
	dispatcher := &fakeDispatcher{}

	poller := NewPoller(PollerConfig{
		Enabled: true,
		BotUser: "@chum-bot:matrix.org",
		RoomToProject: map[string]string{
			"!room-a:matrix.org": "project-a",
			"!room-b:matrix.org": "project-b",
		},
	}, client, dispatcher, nil)

	if err := poller.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce returned error: %v", err)
	}

	if len(dispatcher.calls) != 2 {
		t.Fatalf("expected 2 routed dispatches, got %d", len(dispatcher.calls))
	}
	if dispatcher.calls[0].agent != "project-a-scrum" {
		t.Fatalf("first dispatch agent = %q, want project-a-scrum", dispatcher.calls[0].agent)
	}
	if dispatcher.calls[1].agent != "project-b-scrum" {
		t.Fatalf("second dispatch agent = %q, want project-b-scrum", dispatcher.calls[1].agent)
	}
	if !strings.Contains(dispatcher.calls[0].prompt, "hello from a") {
		t.Fatalf("first prompt missing message body: %q", dispatcher.calls[0].prompt)
	}
}

func TestPollOnceContinuesOnRoomReadError(t *testing.T) {
	client := &fakeClient{
		responses: map[string]fakePollResponse{
			"!ok:matrix.org": {
				messages: []InboundMessage{
					{ID: "7", Sender: "@person:matrix.org", Body: "ok room message"},
				},
			},
		},
		errors: map[string]error{
			"!fail:matrix.org": errors.New("matrix unavailable"),
		},
	}
	dispatcher := &fakeDispatcher{}

	poller := NewPoller(PollerConfig{
		Enabled: true,
		RoomToProject: map[string]string{
			"!fail:matrix.org": "failing-project",
			"!ok:matrix.org":   "ok-project",
		},
	}, client, dispatcher, nil)

	if err := poller.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce returned error: %v", err)
	}
	if len(dispatcher.calls) != 1 {
		t.Fatalf("expected 1 dispatch call from healthy room, got %d", len(dispatcher.calls))
	}
	if dispatcher.calls[0].agent != "ok-project-scrum" {
		t.Fatalf("routed agent = %q, want ok-project-scrum", dispatcher.calls[0].agent)
	}
}

func TestPollOnceDisabledDoesNothing(t *testing.T) {
	client := &fakeClient{}
	dispatcher := &fakeDispatcher{}

	poller := NewPoller(PollerConfig{
		Enabled: false,
		RoomToProject: map[string]string{
			"!room:matrix.org": "project",
		},
	}, client, dispatcher, nil)

	if err := poller.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce returned error: %v", err)
	}
	if len(client.calls) != 0 {
		t.Fatalf("expected no client calls while disabled, got %d", len(client.calls))
	}
	if len(dispatcher.calls) != 0 {
		t.Fatalf("expected no dispatches while disabled, got %d", len(dispatcher.calls))
	}
}

func TestPollOnceFallsBackToMainOnDispatchFailure(t *testing.T) {
	client := &fakeClient{
		responses: map[string]fakePollResponse{
			"!room:matrix.org": {
				messages: []InboundMessage{
					{ID: "10", Sender: "@alice:matrix.org", Body: "needs routing"},
				},
			},
		},
	}
	dispatcher := &fakeDispatcher{
		failAgents: map[string]bool{"project-a-scrum": true},
	}

	poller := NewPoller(PollerConfig{
		Enabled: true,
		RoomToProject: map[string]string{
			"!room:matrix.org": "project-a",
		},
	}, client, dispatcher, nil)

	if err := poller.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce returned error: %v", err)
	}
	if len(dispatcher.calls) != 2 {
		t.Fatalf("expected project + fallback dispatch calls, got %d", len(dispatcher.calls))
	}
	if dispatcher.calls[0].agent != "project-a-scrum" {
		t.Fatalf("first agent = %q, want project-a-scrum", dispatcher.calls[0].agent)
	}
	if dispatcher.calls[1].agent != "main" {
		t.Fatalf("fallback agent = %q, want main", dispatcher.calls[1].agent)
	}
}

func TestParseScrumCommandRecognizesSupportedCommands(t *testing.T) {
	priorityCmd, isCommand, err := parseScrumCommand("priority chum-1 P2")
	if !isCommand {
		t.Fatal("priority command not recognized")
	}
	if err != nil {
		t.Fatalf("priority command parse error: %v", err)
	}
	if priorityCmd.kind != scrumCommandPriority {
		t.Fatalf("priority kind = %d, want %d", priorityCmd.kind, scrumCommandPriority)
	}
	if priorityCmd.priority != 2 {
		t.Fatalf("priority = %d, want 2", priorityCmd.priority)
	}
	if priorityCmd.beadID != "chum-1" {
		t.Fatalf("beadID = %q, want chum-1", priorityCmd.beadID)
	}

	if _, isCommand, err = parseScrumCommand("create task \"Refine docs\" \"Add docs for matrix\""); !isCommand || err != nil {
		t.Fatalf("create command parse mismatch: isCommand=%v err=%v", isCommand, err)
	}

	if _, isCommand, err = parseScrumCommand("status"); !isCommand || err != nil {
		t.Fatalf("status command parse mismatch: isCommand=%v err=%v", isCommand, err)
	}

	if _, isCommand, err = parseScrumCommand("cancel 12"); !isCommand || err != nil {
		t.Fatalf("cancel command parse mismatch: isCommand=%v err=%v", isCommand, err)
	}
}

func TestParseScrumCommandReturnsSpecificGuidance(t *testing.T) {
	cases := []struct {
		command string
		want    string
	}{
		{"status now", "Usage: status"},
		{"priority chum-1", "Usage: priority <bead-id> <p0|p1|p2|p3|p4>"},
		{"cancel bad", "positive dispatch id"},
		{"create task \"Only title\"", "create task command requires quoted title and description"},
	}

	for _, tc := range cases {
		_, isCommand, err := parseScrumCommand(tc.command)
		if !isCommand {
			t.Fatalf("command %q not recognized", tc.command)
		}
		if err == nil {
			t.Fatalf("command %q should return parse error", tc.command)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("error for %q = %q, want substring %q", tc.command, err, tc.want)
		}
	}
}

func TestPollOnceRoutesScrumStatusCommandToMatrixSender(t *testing.T) {
	sender := &fakeSender{}
	store := &fakeStore{
		running: []store.Dispatch{
			{Project: "project-a", BeadID: "chum-1"},
		},
		completed: []store.Dispatch{
			{BeadID: "chum-2", DispatchedAt: time.Now().UTC().Add(-time.Minute)},
			{BeadID: "chum-3", DispatchedAt: time.Now().UTC().Add(-2 * time.Minute)},
		},
	}

	client := &fakeClient{
		responses: map[string]fakePollResponse{
			"!room-a:matrix.org": {
				messages: []InboundMessage{
					{ID: "1", Room: "!room-a:matrix.org", Sender: "@alice:matrix.org", Body: "status"},
				},
			},
		},
	}

	poller := NewPoller(PollerConfig{
		Enabled: true,
		BotUser: "@chum-bot:matrix.org",
		RoomToProject: map[string]string{
			"!room-a:matrix.org": "project-a",
		},
		Sender: sender,
		Store:  store,
	}, client, &fakeDispatcher{}, nil)

	if err := poller.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce returned error: %v", err)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("expected 1 Matrix response, got %d", len(sender.messages))
	}
	if !strings.Contains(sender.messages[0], "Project: project-a") {
		t.Fatalf("status response missing project summary: %q", sender.messages[0])
	}
}

func newTestPollerDAG(t *testing.T) *graph.DAG {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	dag := graph.NewDAG(db)
	if err := dag.EnsureSchema(t.Context()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return dag
}

func TestPollOnceRoutesScrumPriorityCommandToMatrixSender(t *testing.T) {
	dag := newTestPollerDAG(t)
	ctx := t.Context()

	// Pre-create a task in the DAG so priority update can find it.
	taskID, err := dag.CreateTask(ctx, graph.Task{
		Title:    "test task",
		Project:  "project-a",
		Priority: 0,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	sender := &fakeSender{}
	client := &fakeClient{
		responses: map[string]fakePollResponse{
			"!room-a:matrix.org": {
				messages: []InboundMessage{
					{ID: "1", Room: "!room-a:matrix.org", Sender: "@alice:matrix.org", Body: fmt.Sprintf("priority %s p2", taskID)},
				},
			},
		},
	}

	poller := NewPoller(PollerConfig{
		Enabled: true,
		BotUser: "@chum-bot:matrix.org",
		RoomToProject: map[string]string{
			"!room-a:matrix.org": "project-a",
		},
		Projects: map[string]config.Project{"project-a": {}},
		Sender:   sender,
		DAG:      dag,
	}, client, &fakeDispatcher{}, nil)

	if err := poller.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce returned error: %v", err)
	}

	if len(sender.messages) != 1 {
		t.Fatalf("expected 1 response, got %d", len(sender.messages))
	}
	if !strings.Contains(sender.messages[0], fmt.Sprintf("Updated %s priority to p2", taskID)) {
		t.Fatalf("unexpected response: %q", sender.messages[0])
	}

	// Verify priority was actually updated in the DAG.
	task, err := dag.GetTask(ctx, taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.Priority != 2 {
		t.Fatalf("expected priority 2, got %d", task.Priority)
	}
}

func TestPollOnceRoutesScrumCreateCommandToMatrixSender(t *testing.T) {
	dag := newTestPollerDAG(t)

	sender := &fakeSender{}
	client := &fakeClient{
		responses: map[string]fakePollResponse{
			"!room-a:matrix.org": {
				messages: []InboundMessage{
					{ID: "1", Room: "!room-a:matrix.org", Sender: "@alice:matrix.org", Body: "create task \"Create docs\" \"Add onboarding docs\""},
				},
			},
		},
	}

	poller := NewPoller(PollerConfig{
		Enabled: true,
		BotUser: "@chum-bot:matrix.org",
		RoomToProject: map[string]string{
			"!room-a:matrix.org": "project-a",
		},
		Projects: map[string]config.Project{"project-a": {}},
		Sender:   sender,
		DAG:      dag,
	}, client, &fakeDispatcher{}, nil)

	if err := poller.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce returned error: %v", err)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("expected 1 response, got %d", len(sender.messages))
	}
	if !strings.Contains(sender.messages[0], "Created new task") {
		t.Fatalf("unexpected response: %q", sender.messages[0])
	}

	// Verify the task was actually created in the DAG.
	tasks, err := dag.ListTasks(t.Context(), "project-a")
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Title != "Create docs" {
		t.Fatalf("expected title 'Create docs', got %q", tasks[0].Title)
	}
	if tasks[0].Description != "Add onboarding docs" {
		t.Fatalf("expected description 'Add onboarding docs', got %q", tasks[0].Description)
	}
}

func TestPollOnceRoutesScrumCancelCommandToMatrixSender(t *testing.T) {
	canceler := &fakeCanceler{}
	sender := &fakeSender{}
	client := &fakeClient{
		responses: map[string]fakePollResponse{
			"!room-a:matrix.org": {
				messages: []InboundMessage{
					{ID: "1", Room: "!room-a:matrix.org", Sender: "@alice:matrix.org", Body: "cancel 99"},
				},
			},
		},
	}

	poller := NewPoller(PollerConfig{
		Enabled: true,
		BotUser: "@chum-bot:matrix.org",
		RoomToProject: map[string]string{
			"!room-a:matrix.org": "project-a",
		},
		Canceler: canceler,
		Sender:   sender,
	}, client, &fakeDispatcher{}, nil)

	if err := poller.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce returned error: %v", err)
	}
	if len(canceler.cancelledIDs) != 1 || canceler.cancelledIDs[0] != 99 {
		t.Fatalf("canceler IDs = %v", canceler.cancelledIDs)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("expected 1 response, got %d", len(sender.messages))
	}
	if !strings.Contains(sender.messages[0], "Cancelled dispatch 99") {
		t.Fatalf("unexpected response: %q", sender.messages[0])
	}
}

func TestPollOnceRejectsScrumCommandWithoutPermission(t *testing.T) {
	sender := &fakeSender{}
	client := &fakeClient{
		responses: map[string]fakePollResponse{
			"!room-a:matrix.org": {
				messages: []InboundMessage{
					{ID: "1", Room: "!room-a:matrix.org", Sender: "@intruder:matrix.org", Body: "status"},
				},
			},
		},
	}

	poller := NewPoller(PollerConfig{
		Enabled: true,
		BotUser: "@chum-bot:matrix.org",
		RoomToProject: map[string]string{
			"!room-a:matrix.org": "project-a",
		},
		Sender:         sender,
		CommandSenders: []string{"@trusted:matrix.org"},
	}, client, &fakeDispatcher{}, nil)

	if err := poller.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce returned error: %v", err)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("expected 1 response, got %d", len(sender.messages))
	}
	if !strings.Contains(sender.messages[0], "You do not have permission to run scrum commands") {
		t.Fatalf("unexpected response: %q", sender.messages[0])
	}
}

func TestPollOnceRejectsMalformedScrumCommand(t *testing.T) {
	sender := &fakeSender{}
	client := &fakeClient{
		responses: map[string]fakePollResponse{
			"!room-a:matrix.org": {
				messages: []InboundMessage{
					{ID: "1", Room: "!room-a:matrix.org", Sender: "@alice:matrix.org", Body: "priority chum-1"},
				},
			},
		},
	}

	poller := NewPoller(PollerConfig{
		Enabled: true,
		BotUser: "@chum-bot:matrix.org",
		RoomToProject: map[string]string{
			"!room-a:matrix.org": "project-a",
		},
		Sender: sender,
	}, client, &fakeDispatcher{}, nil)

	if err := poller.PollOnce(context.Background()); err != nil {
		t.Fatalf("PollOnce returned error: %v", err)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("expected 1 response, got %d", len(sender.messages))
	}
	if !strings.Contains(sender.messages[0], "Malformed command") {
		t.Fatalf("unexpected response: %q", sender.messages[0])
	}
	if !strings.Contains(sender.messages[0], "Supported commands:") {
		t.Fatalf("unexpected usage response: %q", sender.messages[0])
	}
}

func TestBuildRoomProjectMapUsesResolvedRoom(t *testing.T) {
	cfg := &config.Config{
		Reporter: config.Reporter{
			DefaultRoom: "!fallback:matrix.org",
		},
		Projects: map[string]config.Project{
			"project-b": {Enabled: true},
			"project-a": {Enabled: true},
			"project-c": {Enabled: true, MatrixRoom: "!room-c:matrix.org"},
			"project-z": {Enabled: false, MatrixRoom: "!room-z:matrix.org"},
		},
	}

	got := BuildRoomProjectMap(cfg)
	if got["!room-c:matrix.org"] != "project-c" {
		t.Fatalf("room-c mapping = %q, want project-c", got["!room-c:matrix.org"])
	}
	// Duplicate fallback room is assigned deterministically to alphabetically first enabled project.
	if got["!fallback:matrix.org"] != "project-a" {
		t.Fatalf("fallback mapping = %q, want project-a", got["!fallback:matrix.org"])
	}
	if _, ok := got["!room-z:matrix.org"]; ok {
		t.Fatal("disabled project room should not be mapped")
	}
}

func TestPollerRunStopsOnContextCancel(t *testing.T) {
	client := &fakeClient{}
	dispatcher := &fakeDispatcher{}
	poller := NewPoller(PollerConfig{
		Enabled:      true,
		PollInterval: 10 * time.Millisecond,
		RoomToProject: map[string]string{
			"!room:matrix.org": "project-a",
		},
	}, client, dispatcher, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	poller.Run(ctx)
}
