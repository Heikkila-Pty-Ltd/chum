package temporal

import (
	"context"
	"fmt"
	"strings"

	"go.temporal.io/sdk/activity"

	"github.com/antigravity-dev/chum/internal/store"
)

// RecordPlanningTraceActivity persists one planning trace record.
// This is best-effort from workflows: callers can ignore failures.
func (a *Activities) RecordPlanningTraceActivity(ctx context.Context, record PlanningTraceRecord) error {
	if a == nil || a.Store == nil {
		return nil
	}
	return a.recordPlanningTraceInternal(ctx, record)
}

func (a *Activities) recordPlanningTrace(ctx context.Context, record PlanningTraceRecord) {
	logger := activity.GetLogger(ctx)
	if a == nil || a.Store == nil {
		return
	}
	if err := a.recordPlanningTraceInternal(ctx, record); err != nil {
		logger.Warn("planning trace record failed (non-fatal)", "error", err)
	}
}

func (a *Activities) recordPlanningTraceInternal(ctx context.Context, record PlanningTraceRecord) error {
	info := activity.GetInfo(ctx)

	sessionID := strings.TrimSpace(record.SessionID)
	if sessionID == "" {
		sessionID = info.WorkflowExecution.ID
	}
	runID := strings.TrimSpace(record.RunID)
	if runID == "" {
		runID = info.WorkflowExecution.RunID
	}
	eventType := strings.TrimSpace(record.EventType)
	if eventType == "" {
		return fmt.Errorf("planning trace event_type is required")
	}

	return a.Store.RecordPlanningTraceEvent(store.PlanningTraceEvent{
		SessionID:      sessionID,
		RunID:          runID,
		Project:        strings.TrimSpace(record.Project),
		TaskID:         strings.TrimSpace(record.TaskID),
		Cycle:          record.Cycle,
		Stage:          strings.TrimSpace(record.Stage),
		NodeID:         strings.TrimSpace(record.NodeID),
		ParentNodeID:   strings.TrimSpace(record.ParentNodeID),
		BranchID:       strings.TrimSpace(record.BranchID),
		OptionID:       strings.TrimSpace(record.OptionID),
		EventType:      eventType,
		Actor:          strings.TrimSpace(record.Actor),
		ToolName:       strings.TrimSpace(record.ToolName),
		ToolInput:      record.ToolInput,
		ToolOutput:     record.ToolOutput,
		PromptText:     record.PromptText,
		ResponseText:   record.ResponseText,
		SummaryText:    record.SummaryText,
		FullText:       record.FullText,
		SelectedOption: strings.TrimSpace(record.SelectedOption),
		Reward:         record.Reward,
		MetadataJSON:   strings.TrimSpace(record.MetadataJSON),
	})
}

// RecordPlanningSnapshotActivity stores one planning checkpoint for rollback.
func (a *Activities) RecordPlanningSnapshotActivity(ctx context.Context, snapshot PlanningSnapshotRecord) error {
	if a == nil || a.Store == nil {
		return nil
	}
	info := activity.GetInfo(ctx)

	sessionID := strings.TrimSpace(snapshot.SessionID)
	if sessionID == "" {
		sessionID = info.WorkflowExecution.ID
	}
	runID := strings.TrimSpace(snapshot.RunID)
	if runID == "" {
		runID = info.WorkflowExecution.RunID
	}

	return a.Store.RecordPlanningStateSnapshot(store.PlanningStateSnapshot{
		SessionID: sessionID,
		RunID:     runID,
		Project:   strings.TrimSpace(snapshot.Project),
		TaskID:    strings.TrimSpace(snapshot.TaskID),
		Cycle:     snapshot.Cycle,
		Stage:     strings.TrimSpace(snapshot.Stage),
		StateHash: strings.TrimSpace(snapshot.StateHash),
		StateJSON: snapshot.StateJSON,
		Stable:    snapshot.Stable,
		Reason:    strings.TrimSpace(snapshot.Reason),
	})
}

// GetLatestStablePlanningSnapshotActivity fetches the latest stable checkpoint.
func (a *Activities) GetLatestStablePlanningSnapshotActivity(ctx context.Context, sessionID string) (*PlanningSnapshotRecord, error) {
	if a == nil || a.Store == nil {
		return nil, nil
	}
	snapshot, err := a.Store.GetLatestStablePlanningSnapshot(sessionID)
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, nil
	}
	return &PlanningSnapshotRecord{
		SessionID: snapshot.SessionID,
		RunID:     snapshot.RunID,
		Project:   snapshot.Project,
		TaskID:    snapshot.TaskID,
		Cycle:     snapshot.Cycle,
		Stage:     snapshot.Stage,
		StateHash: snapshot.StateHash,
		StateJSON: snapshot.StateJSON,
		Stable:    snapshot.Stable,
		Reason:    snapshot.Reason,
	}, nil
}

// AddPlanningBlacklistEntryActivity stores a blocked state-action pair.
func (a *Activities) AddPlanningBlacklistEntryActivity(ctx context.Context, entry PlanningBlacklistEntryRecord) error {
	if a == nil || a.Store == nil {
		return nil
	}
	return a.Store.AddPlanningBlacklistEntry(store.PlanningBlacklistEntry{
		SessionID:  strings.TrimSpace(entry.SessionID),
		Project:    strings.TrimSpace(entry.Project),
		TaskID:     strings.TrimSpace(entry.TaskID),
		Cycle:      entry.Cycle,
		Stage:      strings.TrimSpace(entry.Stage),
		StateHash:  strings.TrimSpace(entry.StateHash),
		ActionHash: strings.TrimSpace(entry.ActionHash),
		Reason:     strings.TrimSpace(entry.Reason),
		Metadata:   strings.TrimSpace(entry.Metadata),
	})
}

// IsPlanningActionBlacklistedActivity checks if the state-action pair is blocked.
func (a *Activities) IsPlanningActionBlacklistedActivity(ctx context.Context, check PlanningBlacklistCheck) (bool, error) {
	if a == nil || a.Store == nil {
		return false, nil
	}
	return a.Store.IsPlanningActionBlacklisted(
		strings.TrimSpace(check.SessionID),
		strings.TrimSpace(check.StateHash),
		strings.TrimSpace(check.ActionHash),
	)
}

// LoadPlanningCandidateScoresActivity fetches persisted option score adjustments.
func (a *Activities) LoadPlanningCandidateScoresActivity(ctx context.Context, query PlanningCandidateScoreQuery) ([]PlanningCandidateScoreRecord, error) {
	_ = ctx
	if a == nil || a.Store == nil {
		return []PlanningCandidateScoreRecord{}, nil
	}
	scores, err := a.Store.ListPlanningCandidateScores(strings.TrimSpace(query.Project), query.OptionIDs)
	if err != nil {
		return nil, err
	}
	records := make([]PlanningCandidateScoreRecord, 0, len(scores))
	for i := range scores {
		score := scores[i]
		records = append(records, PlanningCandidateScoreRecord{
			Project:         score.Project,
			OptionID:        score.OptionID,
			ScoreAdjustment: score.ScoreAdjustment,
			Successes:       score.Successes,
			Failures:        score.Failures,
			LastReason:      score.LastReason,
			UpdatedAt:       score.UpdatedAt,
		})
	}
	return records, nil
}

// AdjustPlanningCandidateScoreActivity persists one option score update.
func (a *Activities) AdjustPlanningCandidateScoreActivity(ctx context.Context, delta PlanningCandidateScoreDelta) error {
	_ = ctx
	if a == nil || a.Store == nil {
		return nil
	}
	return a.Store.AdjustPlanningCandidateScore(
		strings.TrimSpace(delta.Project),
		strings.TrimSpace(delta.OptionID),
		delta.Delta,
		strings.TrimSpace(delta.Outcome),
		strings.TrimSpace(delta.Reason),
	)
}

func (a *Activities) recordPlanningLLMCall(
	ctx context.Context,
	req PlanningRequest,
	stage string,
	taskID string,
	agent string,
	prompt string,
	cliResult CLIResult,
	callErr error,
	summary string,
) {
	var fullText strings.Builder
	if strings.TrimSpace(prompt) != "" {
		fullText.WriteString("PROMPT:\n")
		fullText.WriteString(prompt)
		fullText.WriteString("\n\n")
	}
	if strings.TrimSpace(cliResult.Output) != "" {
		fullText.WriteString("RESPONSE:\n")
		fullText.WriteString(cliResult.Output)
	}
	if callErr != nil {
		if fullText.Len() > 0 {
			fullText.WriteString("\n\n")
		}
		fullText.WriteString("ERROR:\n")
		fullText.WriteString(callErr.Error())
	}

	a.recordPlanningTrace(ctx, PlanningTraceRecord{
		SessionID:    req.TraceSessionID,
		Project:      req.Project,
		TaskID:       taskID,
		Cycle:        req.TraceCycle,
		Stage:        stage,
		EventType:    "llm_call",
		Actor:        agent,
		ToolName:     "runAgent",
		ToolInput:    prompt,
		ToolOutput:   cliResult.Output,
		PromptText:   prompt,
		ResponseText: cliResult.Output,
		SummaryText:  summary,
		FullText:     fullText.String(),
	})
}
