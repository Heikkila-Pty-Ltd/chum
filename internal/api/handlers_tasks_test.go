package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeTaskMirror struct {
	id      string
	err     error
	called  bool
	taskID  string
	lastReq CreateTaskRequest
}

func (f *fakeTaskMirror) MirrorTaskCreate(_ context.Context, taskID string, req CreateTaskRequest) (string, error) {
	f.called = true
	f.taskID = taskID
	f.lastReq = req
	return f.id, f.err
}

func TestHandleTaskCreateIncludesBeadsMirrorIDWhenEnabled(t *testing.T) {
	srv := setupTestServer(t)
	require.NoError(t, srv.dag.EnsureSchema(t.Context()))

	mirror := &fakeTaskMirror{id: "bd-abc123"}
	srv.taskMirror = mirror

	body := `{"title":"Mirror me","project":"test-proj","description":"hello","priority":2,"type":"task"}`
	req := httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(body))
	w := httptest.NewRecorder()

	srv.handleTaskCreate(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotEmpty(t, resp["id"])
	require.Equal(t, "bd-abc123", resp["beads_mirror_id"])

	require.True(t, mirror.called)
	require.Equal(t, "Mirror me", mirror.lastReq.Title)
	require.Equal(t, "test-proj", mirror.lastReq.Project)
}

func TestHandleTaskCreateContinuesWhenMirrorFails(t *testing.T) {
	srv := setupTestServer(t)
	require.NoError(t, srv.dag.EnsureSchema(t.Context()))

	mirror := &fakeTaskMirror{err: errors.New("mirror failed")}
	srv.taskMirror = mirror

	body := `{"title":"Mirror fail","project":"test-proj","description":"hello","priority":2,"type":"task"}`
	req := httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(body))
	w := httptest.NewRecorder()

	srv.handleTaskCreate(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotEmpty(t, resp["id"])
	_, hasMirrorID := resp["beads_mirror_id"]
	require.False(t, hasMirrorID)
}
