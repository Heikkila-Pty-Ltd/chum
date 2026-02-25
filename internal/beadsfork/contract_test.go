package beadsfork

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBDContractScaffoldFlow(t *testing.T) {
	if os.Getenv("CHUM_BD_CONTRACT") != "1" {
		t.Skip("set CHUM_BD_CONTRACT=1 to run real bd contract tests")
	}
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd binary not found in PATH")
	}

	tmp := t.TempDir()
	mustRun(t, tmp, "git", "init", "-q")
	mustRun(t, tmp, "bd", "init")

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	pinned := strings.TrimSpace(os.Getenv("CHUM_BD_PINNED_VERSION"))
	if pinned == "" {
		detected := detectVersion(t, ctx, tmp)
		pinned = detected.Version
	}

	client, err := NewClient(Options{
		WorkDir:       tmp,
		PinnedVersion: pinned,
	})
	require.NoError(t, err)

	require.NoError(t, client.CheckPinnedVersion(ctx))

	b, err := client.Create(ctx, "B (blocker)", CreateRequest{
		Description: "blocking task",
		Priority:    1,
		IssueType:   "task",
	})
	require.NoError(t, err)
	require.NotEmpty(t, b.ID)

	a, err := client.Create(ctx, "A (blocked)", CreateRequest{
		Description: "blocked task",
		Priority:    1,
		IssueType:   "task",
	})
	require.NoError(t, err)
	require.NotEmpty(t, a.ID)

	require.NoError(t, client.AddDependency(ctx, a.ID, b.ID, "blocks"))

	listed, err := client.List(ctx, 20)
	require.NoError(t, err)
	require.True(t, containsIssue(listed, a.ID))
	require.True(t, containsIssue(listed, b.ID))

	ready, err := client.Ready(ctx, 20)
	require.NoError(t, err)
	require.True(t, containsIssue(ready, b.ID))
	require.False(t, containsIssue(ready, a.ID))

	blocked, err := client.Blocked(ctx)
	require.NoError(t, err)
	require.True(t, containsIssue(blocked, a.ID))

	newPriority := 0
	updated, err := client.Update(ctx, b.ID, UpdateRequest{
		Status:   "in_progress",
		Priority: &newPriority,
		Title:    "B (in progress)",
	})
	require.NoError(t, err)
	require.Equal(t, "in_progress", updated.Status)
	require.Equal(t, 0, updated.Priority)
	require.Equal(t, "B (in progress)", updated.Title)

	shown, err := client.Show(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, a.ID, shown.ID)

	require.NoError(t, client.SyncFlushOnly(ctx))

	jsonlPath := filepath.Join(tmp, ".beads", "issues.jsonl")
	data, readErr := os.ReadFile(jsonlPath)
	require.NoError(t, readErr)
	require.Contains(t, string(data), a.ID)
	require.Contains(t, string(data), b.ID)
}

func detectVersion(t *testing.T, ctx context.Context, dir string) VersionInfo {
	t.Helper()
	client, err := NewClient(Options{WorkDir: dir, PinnedVersion: "0.0.0"})
	require.NoError(t, err)
	info, err := client.Version(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, info.Version)
	return info
}

func mustRun(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "command failed: %s %s\n%s", name, strings.Join(args, " "), string(out))
}

func containsIssue(issues []Issue, id string) bool {
	for _, issue := range issues {
		if issue.ID == id {
			return true
		}
	}
	return false
}
