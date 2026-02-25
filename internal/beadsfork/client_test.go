package beadsfork

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type runCall struct {
	dir  string
	name string
	args []string
}

type fakeRunner struct {
	calls     []runCall
	responses []fakeResponse
}

type fakeResponse struct {
	out []byte
	err error
}

func (f *fakeRunner) Run(_ context.Context, dir, name string, args ...string) ([]byte, error) {
	call := runCall{
		dir:  dir,
		name: name,
		args: append([]string(nil), args...),
	}
	f.calls = append(f.calls, call)
	if len(f.responses) == 0 {
		return nil, errors.New("unexpected run with no fake response")
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp.out, resp.err
}

func TestNewClientRequiresWorkDir(t *testing.T) {
	_, err := NewClient(Options{})
	require.ErrorContains(t, err, "workdir is required")
}

func TestCheckPinnedVersionMismatch(t *testing.T) {
	runner := &fakeRunner{
		responses: []fakeResponse{
			{out: []byte(`{"version":"0.49.3"}`)},
		},
	}
	client, err := NewClient(Options{
		WorkDir:       "/tmp/work",
		PinnedVersion: "0.56.1",
		Runner:        runner,
	})
	require.NoError(t, err)

	err = client.CheckPinnedVersion(t.Context())
	require.ErrorContains(t, err, "bd version mismatch")
	require.Len(t, runner.calls, 1)
	require.Equal(t, "/tmp/work", runner.calls[0].dir)
	require.Equal(t, "bd", runner.calls[0].name)
	require.Equal(t, []string{"--no-daemon", "--no-auto-import", "--no-auto-flush", "version", "--json"}, runner.calls[0].args)
}

func TestCreateAndReadCallsWithMixedOutput(t *testing.T) {
	runner := &fakeRunner{
		responses: []fakeResponse{
			{
				out: []byte("warning: beads.role not configured\n{\"id\":\"bd-a1\",\"title\":\"task a\",\"status\":\"open\",\"priority\":1,\"issue_type\":\"task\"}\n"),
			},
			{
				out: []byte("[{\"id\":\"bd-a1\",\"title\":\"task a\",\"status\":\"open\",\"priority\":1,\"issue_type\":\"task\"}]"),
			},
			{
				out: []byte("[{\"id\":\"bd-a1\",\"title\":\"task a\",\"status\":\"open\",\"priority\":1,\"issue_type\":\"task\"}]"),
			},
		},
	}
	client, err := NewClient(Options{
		WorkDir: "/tmp/work",
		Runner:  runner,
	})
	require.NoError(t, err)

	created, err := client.Create(t.Context(), "task a", CreateRequest{
		Description: "d",
		Priority:    1,
		IssueType:   "task",
		Labels:      []string{"alpha", "beta"},
	})
	require.NoError(t, err)
	require.Equal(t, "bd-a1", created.ID)

	listed, err := client.List(t.Context(), 10)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, "bd-a1", listed[0].ID)

	shown, err := client.Show(t.Context(), "bd-a1")
	require.NoError(t, err)
	require.Equal(t, "bd-a1", shown.ID)

	require.Len(t, runner.calls, 3)
	require.Equal(t, []string{
		"--no-daemon", "--no-auto-import", "--no-auto-flush",
		"create", "task a", "--json", "--description", "d", "--priority", "1", "--type", "task", "--labels", "alpha,beta",
	}, runner.calls[0].args)
	require.Equal(t, []string{
		"--no-daemon", "--no-auto-import", "--no-auto-flush",
		"list", "--json", "--limit", "10",
	}, runner.calls[1].args)
	require.Equal(t, []string{
		"--no-daemon", "--no-auto-import", "--no-auto-flush",
		"show", "--json", "bd-a1",
	}, runner.calls[2].args)
}

func TestSyncFlushOnly(t *testing.T) {
	runner := &fakeRunner{
		responses: []fakeResponse{
			{out: []byte("")},
		},
	}
	client, err := NewClient(Options{
		WorkDir: "/tmp/work",
		Runner:  runner,
	})
	require.NoError(t, err)

	err = client.SyncFlushOnly(t.Context())
	require.NoError(t, err)
	require.Equal(t, []string{
		"--no-daemon", "--no-auto-import", "--no-auto-flush",
		"sync", "--flush-only",
	}, runner.calls[0].args)
}

func TestVersionFallbackParsesPlainOutput(t *testing.T) {
	runner := &fakeRunner{
		responses: []fakeResponse{
			{out: []byte("bd version v0.56.1 (abcdef: HEAD@abcdef)\n")},
		},
	}
	client, err := NewClient(Options{
		WorkDir: "/tmp/work",
		Runner:  runner,
	})
	require.NoError(t, err)

	info, err := client.Version(t.Context())
	require.NoError(t, err)
	require.Equal(t, "0.56.1", info.Version)
}
