package api

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/antigravity-dev/chum/internal/beadsfork"
)

type fakeBeadsForkClient struct {
	createCalls []createCall
	depCalls    []depCall
	createdIDs  []string
}

type createCall struct {
	title string
	req   beadsfork.CreateRequest
}

type depCall struct {
	issueID     string
	dependsOnID string
	depType     string
}

func (f *fakeBeadsForkClient) Create(_ context.Context, title string, req beadsfork.CreateRequest) (beadsfork.Issue, error) {
	f.createCalls = append(f.createCalls, createCall{title: title, req: req})
	if len(f.createdIDs) == 0 {
		return beadsfork.Issue{ID: "bd-auto"}, nil
	}
	id := f.createdIDs[0]
	f.createdIDs = f.createdIDs[1:]
	return beadsfork.Issue{ID: id}, nil
}

func (f *fakeBeadsForkClient) AddDependency(_ context.Context, issueID, dependsOnID, depType string) error {
	f.depCalls = append(f.depCalls, depCall{
		issueID:     issueID,
		dependsOnID: dependsOnID,
		depType:     depType,
	})
	return nil
}

func TestBeadsForkTaskMirrorMirrorsCreateAndKnownDependencies(t *testing.T) {
	fake := &fakeBeadsForkClient{
		createdIDs: []string{"bd-1", "bd-2"},
	}
	mirror := NewBeadsForkTaskMirrorWithClient(fake)

	firstReq := CreateTaskRequest{
		Title:       "First",
		Description: "first desc",
		Priority:    3,
		Type:        "feature",
		Labels:      []string{"backend"},
		Project:     "chum",
	}
	firstID, err := mirror.MirrorTaskCreate(t.Context(), "chum-111111", firstReq)
	require.NoError(t, err)
	require.Equal(t, "bd-1", firstID)

	secondReq := CreateTaskRequest{
		Title:       "Second",
		Description: "second desc",
		Priority:    1,
		Type:        "docs", // unsupported in bd; should normalize to task
		Labels:      []string{"docs"},
		Project:     "chum",
		DependsOn:   []string{"chum-111111"},
	}
	secondID, err := mirror.MirrorTaskCreate(t.Context(), "chum-222222", secondReq)
	require.NoError(t, err)
	require.Equal(t, "bd-2", secondID)

	require.Len(t, fake.createCalls, 2)
	require.Equal(t, "First", fake.createCalls[0].title)
	require.Equal(t, "feature", fake.createCalls[0].req.IssueType)
	require.Equal(t, 3, fake.createCalls[0].req.Priority)
	require.Contains(t, fake.createCalls[0].req.Labels, "source:chum")
	require.Contains(t, fake.createCalls[0].req.Labels, "mirror:beads")
	require.Contains(t, fake.createCalls[0].req.Labels, "chum-task:chum-111111")

	require.Equal(t, "Second", fake.createCalls[1].title)
	require.Equal(t, "task", fake.createCalls[1].req.IssueType)

	require.Len(t, fake.depCalls, 1)
	require.Equal(t, depCall{
		issueID:     "bd-2",
		dependsOnID: "bd-1",
		depType:     "blocks",
	}, fake.depCalls[0])
}
